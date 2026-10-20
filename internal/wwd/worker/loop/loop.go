// Package loop is the worker's main loop (spec §5 Loop, Capacity, Owner reclaim): every couple of seconds, when the host may take a job,
// it reads the queued jobs of every origin, scores them (package pick), and claims the best one it can get (package runner). It also
// keeps the jobs it runs: when the owner takes the machine back it stops them all and gives them back.
//
// The loop decides whether to claim; it never decides what a job is (the snapshot's manifest does, package remotejob) or how it ended
// (package report). Everything it reads about the host comes in through Config as functions, so it is the same code on mlab, air26 and
// mserv and a test can drive it with a fake clock.
package loop

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/derived"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/locks"
	"github.com/wake/purdex/internal/wwd/worker/pick"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
	"github.com/wake/purdex/internal/wwd/worker/report"
	"github.com/wake/purdex/internal/wwd/worker/runner"
	"github.com/wake/purdex/internal/wwd/worker/yield"
)

// Conn is one origin as the loop uses it: claim, start and renew (runner), and the delivery of what came of it (report).
type Conn interface {
	runner.Origin
	report.Sink
}

// Origin is one queue the worker takes jobs from: this host's own and each peer's.
type Origin struct {
	Host   string      // the origin's host id (Candidate.Source, the key of its suspension and of its locks owner)
	Reader pick.Source // where its queued jobs are read from
	Conn   Conn
	Zone   func() wire.Zone // the origin's zone for origin_bonus (pool facts' Derived.PickZone()); nil: unknown
}

// UsageRecorder is told, after a job's result was taken by its origin and before its workspace goes, how the run went. It only records: it cannot
// change the outcome (a panic is logged, an error is not even possible).
type UsageRecorder interface {
	RecordRun(e runner.Ended)
}

// Sweeper cleans up what jobs leave outside the worker (a per-job database): given the keys ("source/id", worker/ident) of every job this worker still holds, it removes
// whatever belongs to any other. A failure is logged and tried again at the next sweep.
type Sweeper interface {
	Sweep(ctx context.Context, live []string) error
}

// Quarantine keeps the jobs whose command could not be proven stopped (package quarantine; *quarantine.Store is one). Their workspace and
// lock keys stay held until the sandbox unit is proven gone, also across a restart.
type Quarantine interface {
	Add(quarantine.Entry) error
	Restore() error
	Sweep(ctx context.Context) (int, error)
	Held() []quarantine.Entry
}

// GateSlot is one of this host's pdx gate heavy slots held for a job (gateheld.Held is one). Describe names the job in the slot's record
// (what `pdx gate status` shows) and Release gives the slot back; Release may be called more than once.
type GateSlot interface {
	Describe(argv []string, session string)
	Release()
}

// Gate is the host's pdx gate as the worker keeps its books with it (#3502, until WWD-6 moves the entry point): a job the worker runs
// holds one of the gate's heavy slots while it runs, so a heavy command arriving then waits for it instead of making one more heavy
// thing on the host. TakeHeavy never waits: ok is false when there is no slot to be had (the gate is full or cannot be read: the worker
// claims nothing); a nil slot with ok true is a host without a gate (nothing to hold).
type Gate interface {
	TakeHeavy() (slot GateSlot, ok bool)
	// TakeLate is a slot for a command that is already running (a quarantined job's, whose command could not be proven stopped): it
	// does not ask whether the gate admits more, and may go beyond the gate's size. ok is false when none could be had (the gate cannot
	// be read, or is paused): the caller tries again and claims nothing meanwhile. A nil slot with ok true is a host without a gate.
	TakeLate() (slot GateSlot, ok bool)
}

// Config is what the loop needs from its host.
type Config struct {
	// Gate, when set, is asked for a heavy slot before a job is claimed. nil: no gate to keep books with.
	Gate Gate
	// Reserve, when set, books a heavy slot in the host's ledger before anything is claimed and gives it back when the round's claim is
	// settled (the claimed job is in the loop's running count by then): a fast-lane run is admitted against the same ledger, so the two
	// cannot take the last heavy slot between them. false: no room, nothing is claimed this round. nil: no ledger to keep books with.
	Reserve func() (release func(), ok bool)
	// Settings is the worker setting (/v1/worker): on or off, the slots, the owner rule.
	Settings func() wire.WorkerSettings
	// Facts is this host's latest facts record; nil until the first sample (the loop then takes nothing).
	Facts func() *wire.Facts
	// Origins is every queue to read: this host's own first, then the peers'. It is called once a round, so a peer that comes and goes
	// is followed.
	Origins func() []Origin
	// Labels are this host's labels (a job's must be a subset).
	Labels func() []string
	Locks  *locks.Table
	Ctl    *runner.Controller
	Report *report.Reporter
	// Warm says the repo clone and its dependency cache are here (warm_bonus); nil: nothing is warm.
	Warm func(wire.Job) bool
	// SpeedFactor and Estimate feed size_penalty (spec §4.6); the zero values mean none.
	SpeedFactor float64
	// ExplicitLabelsOnly is pick.Env.ExplicitLabelsOnly.
	ExplicitLabelsOnly bool
	Estimate           func(wire.Job) float64
	// Usage is told how each run went (see UsageRecorder); nil: nothing is recorded.
	Usage UsageRecorder
	// Sweepers are run every SweepEvery with the keys ("source/id") of the jobs the worker holds: the ones it runs, and Held() (quarantined ones, whose
	// leftovers must stay until they are proven gone).
	Sweepers []Sweeper
	Held     func() []string
	// Quarantine is told of every job that ended quarantined (before its delivery), puts the holds back when the worker starts (Run takes
	// nothing until that has worked) and is swept with the Sweepers; nil: none. Sandboxed says the host runs commands as systemd units
	// (an entry then names its unit, which is what proves the command gone).
	Quarantine Quarantine
	Sandboxed  bool
	// Guarded says the runner has the guard (NewGuard) over the same store: the marker of a run has then been on disk since before its
	// command started, and Finish does not write it again (a write after a sweep that had cleared the entry would bring it back without its
	// hold).
	Guarded bool
	// SweepEvery is the pause between two sweeps (a minute). MarkerRetry is the pause between two tries to write a quarantine marker (5 s).
	SweepEvery, MarkerRetry time.Duration

	// FactsMaxAge is how old this host's facts may be for the worker to go on taking jobs (30 s: six samples); a sampler that has stopped
	// must not leave the worker claiming against a headroom nobody has measured for a while.
	FactsMaxAge time.Duration
	// Interval is the pause between two rounds (2 s). Settle is the pause after a claim before the next one, so that the facts have
	// seen the job (the headroom they carry is only true once the job's processes are in a sample); two facts samples by default.
	Interval, Settle time.Duration
	// StopTimeout is how long one stop of the running jobs waits (an owner is waiting for the machine); a job that has not ended by
	// then is asked again at the next round. ShutdownGrace is the same for a worker that is ending.
	StopTimeout, ShutdownGrace time.Duration
	// MaxTries is how many candidates one round tries to claim before it waits for the next (a 409 goes to the next candidate).
	MaxTries int
	Now      func() time.Time
	Logf     func(format string, args ...any)
}

// Loop is the worker.
type Loop struct {
	cfg Config
	y   *yield.Yielder
	// ownerRule is whether y was made for a host with an owner rule
	ownerRule bool

	mu      sync.Mutex
	running map[string]*runner.Running
	// quarSlots are the gate slots of the quarantined jobs, by owner ("source/id"): their commands may still run, so the slot is kept until
	// the quarantine has proven them gone (releaseCleared, after each sweep).
	quarSlots map[string]GateSlot
	lastClaim time.Time
	lastSweep time.Time
}

// New returns a Loop; the zero values of the durations are the defaults.
func New(cfg Config) *Loop {
	if cfg.Interval <= 0 {
		cfg.Interval = 2 * time.Second
	}
	if cfg.FactsMaxAge <= 0 {
		cfg.FactsMaxAge = 6 * time.Duration(wire.SampleIntervalS) * time.Second
	}
	if cfg.Settle <= 0 {
		cfg.Settle = 2 * time.Duration(wire.SampleIntervalS) * time.Second
	}
	if cfg.MarkerRetry <= 0 {
		cfg.MarkerRetry = 5 * time.Second
	}
	if cfg.SweepEvery <= 0 {
		cfg.SweepEvery = time.Minute
	}
	if cfg.StopTimeout <= 0 {
		cfg.StopTimeout = 60 * time.Second
	}
	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = 30 * time.Second
	}
	if cfg.MaxTries <= 0 {
		cfg.MaxTries = 8
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	return &Loop{cfg: cfg, running: map[string]*runner.Running{}, quarSlots: map[string]GateSlot{}}
}

// Running is how many jobs the worker is running now.
func (l *Loop) Running() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked()
	return len(l.running)
}

func (l *Loop) pruneLocked() {
	for k, r := range l.running {
		select {
		case <-r.Done():
			delete(l.running, k)
		default:
		}
	}
}

// Run does rounds until ctx ends, then stops what is running (each job is given back with the lease alive) and returns. It first puts the
// holds of the quarantined jobs back (nothing may take a lock before that); when that fails the worker does not start and Run says why.
func (l *Loop) Run(ctx context.Context) error {
	if l.cfg.Quarantine != nil {
		if err := l.cfg.Quarantine.Restore(); err != nil {
			return fmt.Errorf("worker: the quarantined jobs' holds could not all be put back, the worker does not start: %w", err)
		}
		l.adoptQuarantined() // before any claim: the commands of these jobs may be running, and the gate must count them
	}
	// The jobs do not run under ctx: when it ends they are stopped one by one and released, which needs their leases and their
	// connections to be alive.
	jobCtx, endJobs := context.WithCancel(context.WithoutCancel(ctx))
	defer endJobs()
	t := time.NewTicker(l.cfg.Interval)
	defer t.Stop()
	for {
		l.Round(ctx, jobCtx)
		l.sweepIfDue(ctx)
		select {
		case <-ctx.Done():
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.cfg.ShutdownGrace)
			l.stopAll(sctx, "the worker is ending")
			cancel()
			l.abandon(endJobs)
			return nil
		case <-t.C:
		}
	}
}

// Round is one pass: follow the owner, and take a job when there is room for one. ctx ends the round; jobCtx is what claimed jobs run under.
func (l *Loop) Round(ctx, jobCtx context.Context) {
	if ctx.Err() != nil {
		return
	}
	st := l.cfg.Settings()
	var facts *wire.Facts
	if l.cfg.Facts != nil {
		facts = l.cfg.Facts()
	}
	// owner_rule can be changed while the worker runs (PUT /v1/worker): a yielder made for the old setting would go on yielding to an
	// owner nobody watches any more, or take jobs without a first reading, so it is made again when the setting flips
	if rule := st.OwnerRule != ""; l.y == nil || rule != l.ownerRule {
		l.y, l.ownerRule = yield.New(l.cfg.Now, rule), rule
	}
	var owner *wire.OwnerState
	if facts != nil && l.ownerRule { // the rule in force is the only basis: with none, a reading (from the config's [owner], or a fault hook) is not watched
		owner = facts.Owner
	}
	d, changed := l.y.Observe(owner)
	if changed {
		l.cfg.Logf("worker: owner yield %v (%s)", d.Reclaim, d.Reason)
	}
	if d.Reclaim {
		sctx, cancel := context.WithTimeout(ctx, l.cfg.StopTimeout)
		l.stopAll(sctx, "the owner is using the machine ("+d.Reason+")")
		cancel()
		return
	}
	if !st.Enabled || !d.MayClaim || facts == nil {
		return
	}
	l.mu.Lock()
	l.pruneLocked()
	n, last := len(l.running), l.lastClaim
	l.mu.Unlock()
	// The headroom is 0 while any exception is on, a hold with `allow` included (so every outside reader waits). The worker knows what
	// `allow` means: when the selective holds are the only thing in the way it may still take a job they list, and only those (claimOne).
	if n >= st.Slots.Heavy || !heavyRoom(facts) {
		return
	}
	// a quarantined job whose command may still run must be counted by the gate before anything new is claimed (a restart takes these
	// slots at the start; one that could not be had is tried again here)
	if !l.adoptQuarantined() {
		return
	}
	// The headroom is only true for a sample that has seen the jobs already claimed: the sample must have been taken Settle after the last
	// claim, and it must not be old (a sampler that has stopped leaves the last "there is room" standing).
	// A sample "from the future" (the clock was set back after it was taken) is as little to be trusted as an old one.
	if age := l.cfg.Now().Sub(facts.At); age < 0 || age > l.cfg.FactsMaxAge || facts.At.Sub(last) < l.cfg.Settle {
		return
	}
	l.claimOne(ctx, jobCtx, facts.Exception)
}

// heavyRoom says whether the facts leave room for a heavy job: the headroom, or (while only holds with `allow` are in the way) the room
// they leave, with the jobs they list the only ones to take.
func heavyRoom(f *wire.Facts) bool {
	return f.Derived.Headroom.Heavy >= 1 || derived.HeavyRoomForAllowed(*f) >= 1
}

// stopAll stops every running job at once and waits for them (Running.Stop: the command ends, Finish gives the claim back, the lease is
// kept until then). One that cannot be proven stopped is logged; it is not given back and stays tracked until it ends.
func (l *Loop) stopAll(ctx context.Context, why string) {
	l.mu.Lock()
	jobs := make([]*runner.Running, 0, len(l.running))
	for _, r := range l.running {
		jobs = append(jobs, r)
	}
	l.mu.Unlock()
	if len(jobs) == 0 {
		return
	}
	l.cfg.Logf("worker: stopping %d job(s): %s", len(jobs), why)
	var wg sync.WaitGroup
	for _, r := range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Stop(ctx); err != nil {
				l.cfg.Logf("worker: stop %s/%s: %v", r.Source(), r.ID(), err)
			}
		}()
	}
	wg.Wait()
}

func (l *Loop) claimOne(ctx, jobCtx context.Context, holds []wire.FactExc) {
	origins := l.cfg.Origins()
	byHost := make(map[string]Origin, len(origins))
	srcs := make([]pick.Source, 0, len(origins))
	for _, o := range origins {
		byHost[o.Host] = o
		srcs = append(srcs, o.Reader)
	}
	cands, errs := pick.Collect(ctx, srcs, func(host string) wire.Zone {
		if o := byHost[host]; o.Zone != nil {
			return o.Zone()
		}
		return ""
	})
	for host, err := range errs {
		l.cfg.Logf("worker: reading the queue of %s: %v", host, err)
	}
	env := pick.Env{
		Now: l.cfg.Now(), Labels: l.cfg.Labels(), ExplicitLabelsOnly: l.cfg.ExplicitLabelsOnly, SpeedFactor: l.cfg.SpeedFactor, Estimate: l.cfg.Estimate, Warm: l.cfg.Warm,
		Locked: func(keys []string) bool { return len(l.cfg.Locks.Conflicts(keys)) > 0 },
	}
	if l.cfg.Report != nil {
		env.Suspended = l.cfg.Report.Suspended
		env.HeldOff = l.cfg.Report.HeldOff
	}
	// the holds with `allow` (spec §13.4) let through only the jobs they list
	for _, h := range holds {
		if h.Allow != nil {
			env.Admit = func(j wire.Job) bool { return derived.AdmitsJob(holds, j.Class, j.Repo.Name) }
			break
		}
	}
	ranked := pick.Rank(cands, env)
	// The gate's heavy slot is taken only when there is something to claim (an empty queue does not make the gate's slots flicker), and
	// one slot serves all of this round's tries: it goes to the job that is claimed, or comes back when none is.
	if l.cfg.Reserve != nil && len(ranked) > 0 {
		release, ok := l.cfg.Reserve()
		if !ok {
			return
		}
		defer release() // after the claimed job is in l.running (below), so the ledger never shows the slot as free in between
	}
	var slot GateSlot
	if l.cfg.Gate != nil && len(ranked) > 0 {
		s, ok := l.cfg.Gate.TakeHeavy()
		if !ok {
			return
		}
		slot = s
	}
	defer func() {
		if slot != nil {
			slot.Release()
		}
	}()
	tries := 0
	for _, sc := range ranked {
		if tries >= l.cfg.MaxTries || ctx.Err() != nil {
			return
		}
		tries++
		// The facts are read again before each try: a sample taken while the queues were read (or since the last try) may carry a hold made
		// since the round began. A hold made after the newest sample is not seen until the next one (spec §13.4: it is in force for the
		// worker at the latest when the next sample shows it).
		if l.cfg.Facts != nil {
			f := l.cfg.Facts()
			if f == nil || !heavyRoom(f) {
				return
			}
			if !derived.AdmitsJob(f.Exception, sc.Job.Class, sc.Job.Repo.Name) {
				continue
			}
		}
		o, ok := byHost[sc.Source]
		if !ok {
			continue
		}
		r := l.cfg.Ctl.Start(jobCtx, o.Conn, sc, l.finish(byHost))
		if r.Claimed() {
			l.mu.Lock()
			l.running[sc.Source+"/"+sc.Job.ID] = r
			l.lastClaim = l.cfg.Now()
			l.mu.Unlock()
			if slot != nil {
				// the slot is the job's now: named in the gate's record, and given back when the job is over, however it ends (its
				// result, a stop, a lost claim). A worker that is killed loses the lock with the process.
				held := slot
				slot = nil
				held.Describe([]string{"wwd", "job", sc.Job.ID, sc.Job.Task}, sc.Job.Submitter.Session)
				owner := sc.Source + "/" + sc.Job.ID
				go func() {
					<-r.Done()
					if r.Ended().Quarantined {
						l.keepForQuarantine(owner, held) // its command may still run: the slot stays until the quarantine clears it
						return
					}
					held.Release()
				}()
			}
			return
		}
		// not taken (someone else was first, the job is held, its locks are busy, the origin did not answer): the next candidate
		if e := r.Ended(); e.Err != nil {
			l.cfg.Logf("worker: %s/%s not claimed: %v", sc.Source, sc.Job.ID, e.Err)
		}
	}
}

// finish is the runner.Finish of one job: the delivery (report), then the usage record.
func (l *Loop) finish(byHost map[string]Origin) runner.Finish {
	deliver := func(context.Context, runner.Ended) bool { return false }
	if l.cfg.Report != nil {
		deliver = l.cfg.Report.Delivery(func(source string) report.Sink {
			if o, ok := byHost[source]; ok {
				return o.Conn
			}
			return nil
		})
	}
	return func(ctx context.Context, e runner.Ended) {
		// The marker first: it must exist before anything slow (a release that is retried for minutes) happens, and without it the job is
		// not given back at all (see quarantine).
		if !l.quarantine(ctx, e) {
			return
		}
		// What is recorded is the run whose result the origin took: a result it refused or never got means the job is run again, and that
		// run is the job's (a record per job id, first one wins, would otherwise keep the wrong one).
		if deliver(ctx, e) {
			l.record(e)
		}
	}
}

func (l *Loop) record(e runner.Ended) {
	if l.cfg.Usage == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			l.cfg.Logf("worker: recording the run of %s: %v", e.Job.ID, fmt.Sprint(p))
		}
	}()
	l.cfg.Usage.RecordRun(e)
}

func (l *Loop) sweepIfDue(ctx context.Context) {
	if (len(l.cfg.Sweepers) == 0 && l.cfg.Quarantine == nil) || ctx.Err() != nil {
		return
	}
	l.mu.Lock()
	due := l.cfg.Now().Sub(l.lastSweep) >= l.cfg.SweepEvery
	if due {
		l.lastSweep = l.cfg.Now()
	}
	l.mu.Unlock()
	if due {
		l.Sweep(ctx)
	}
}

// keepForQuarantine keeps a quarantined job's gate slot under its owner key until releaseCleared finds the entry gone.
func (l *Loop) keepForQuarantine(owner string, slot GateSlot) {
	l.mu.Lock()
	old := l.quarSlots[owner]
	l.quarSlots[owner] = slot
	l.mu.Unlock()
	if old != nil && old != slot {
		old.Release()
	}
}

// adoptQuarantined takes a gate slot for each quarantined job that has none (a restart found them: their commands may live on, outside
// this process, and the slots of the process that ended went with it). Late slots: they count for commands that are running already,
// whatever the gate says. It says whether every one is counted (or the host has no gate): when a slot could not be had (the gate cannot
// be read, or is paused) it is tried again at the next round, and nothing new is claimed until all are counted.
func (l *Loop) adoptQuarantined() bool {
	if l.cfg.Gate == nil || l.cfg.Quarantine == nil {
		return true
	}
	all := true
	for _, e := range l.cfg.Quarantine.Held() {
		l.mu.Lock()
		have := l.quarSlots[e.Owner] != nil
		l.mu.Unlock()
		if have {
			continue
		}
		slot, ok := l.cfg.Gate.TakeLate()
		if !ok {
			all = false
			continue
		}
		if slot == nil {
			continue // a host without a gate
		}
		slot.Describe([]string{"wwd", "job", e.JobID, "(quarantined)"}, "")
		l.keepForQuarantine(e.Owner, slot)
	}
	return all
}

// releaseCleared gives back the slots of the quarantined jobs whose entries are gone (proven stopped and cleared).
func (l *Loop) releaseCleared() {
	if l.cfg.Quarantine == nil {
		return
	}
	still := map[string]bool{}
	for _, e := range l.cfg.Quarantine.Held() {
		still[e.Owner] = true
	}
	var gone []GateSlot
	l.mu.Lock()
	for owner, s := range l.quarSlots {
		if !still[owner] {
			gone = append(gone, s)
			delete(l.quarSlots, owner)
		}
	}
	l.mu.Unlock()
	for _, s := range gone {
		s.Release()
	}
}

// Sweep runs every Sweeper once with the ids of the jobs this worker holds.
func (l *Loop) Sweep(ctx context.Context) {
	// the quarantine first: a unit proven gone frees its hold and workspace, and the other sweepers then see the job as not held
	if q := l.cfg.Quarantine; q != nil {
		if n, err := q.Sweep(ctx); err != nil {
			l.cfg.Logf("worker: quarantine sweep: %v", err)
		} else if n > 0 {
			l.cfg.Logf("worker: quarantine: %d job(s) proven stopped and cleared", n)
		}
		l.releaseCleared()
	}
	l.mu.Lock()
	l.pruneLocked()
	// keys, "source/id": a job id is only unique per origin (the map's own keys are these)
	live := make([]string, 0, len(l.running))
	for k := range l.running {
		live = append(live, k)
	}
	l.mu.Unlock()
	if l.cfg.Held != nil {
		live = append(live, l.cfg.Held()...)
	}
	if q := l.cfg.Quarantine; q != nil {
		for _, e := range q.Held() {
			live = append(live, e.Owner)
		}
	}
	for _, sw := range l.cfg.Sweepers {
		if err := sw.Sweep(ctx, live); err != nil {
			l.cfg.Logf("worker: sweep: %v", err)
		}
	}
}

// abandon is what is left of a worker that is ending when the grace has passed: the jobs that did not stop in time lose their leases'
// context (their commands are ended, they are not given back, their leases lapse at the origins) and are named in the log. A worker
// must not wait for ever, but it must not leave a command running behind it either.
func (l *Loop) abandon(endJobs context.CancelFunc) {
	l.mu.Lock()
	l.pruneLocked()
	left := make([]*runner.Running, 0, len(l.running))
	for _, r := range l.running {
		left = append(left, r)
	}
	l.mu.Unlock()
	if len(left) == 0 {
		return
	}
	for _, r := range left {
		l.cfg.Logf("worker: %s/%s did not stop within the shutdown grace; its commands are ended and the origin gets the job back when its lease lapses", r.Source(), r.ID())
	}
	endJobs()
	deadline := time.NewTimer(killWait)
	defer deadline.Stop()
	for _, r := range left {
		select {
		case <-r.Done():
		case <-deadline.C:
			l.cfg.Logf("worker: %s/%s still had a command running when the worker ended", r.Source(), r.ID())
			return
		}
	}
}

// killWait is how long a worker that is ending waits for the commands it ended to be gone (TERM, then KILL after 10 s).
const killWait = 15 * time.Second

// quarantine writes the marker of a job that ended quarantined and says whether the job may now be given back. The marker is what keeps
// the job's lock keys and workspace across a restart; the delivery of a quarantined job is a release, which lets another worker run the
// job while its unit may still be going. So a marker that cannot be written (disk full, read-only) is retried, every MarkerRetry, while
// the lease is kept alive, and the job is not released until it is written: if ctx ends first (the lease was lost, or the worker ends),
// nothing is delivered and the origin gets the job back only when the lease lapses, with the hold still standing in memory.
func (l *Loop) quarantine(ctx context.Context, e runner.Ended) bool {
	if !e.Quarantined || l.cfg.Quarantine == nil || l.cfg.Guarded {
		return true
	}
	ent := entryOf(e, l.cfg.Sandboxed, l.cfg.Now())
	for {
		err := l.cfg.Quarantine.Add(ent)
		if err == nil {
			return true
		}
		l.cfg.Logf("worker: QUARANTINE MARKER NOT WRITTEN for %s, the job is not given back until it is: %v", ent.Owner, err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(l.cfg.MarkerRetry):
		}
	}
}
