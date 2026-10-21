package qualify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wake/purdex/internal/wwd/labels"
	"github.com/wake/purdex/internal/wwd/queue"
	"github.com/wake/purdex/internal/wwd/store"
	"github.com/wake/purdex/internal/wwd/wire"
)

// This file is the coordinator of qualification (spec §7): it submits the two halves of a pair to this host's queue, settles them when both
// are done, and grants the label. cmd/wwd builds it (qualifysource.go gives it the host, the tasks of the prepared clones and the snapshot);
// a person starts a pair with `pdx wwd qualify` (POST /v1/qualify, WWD-5b-2c-1), and the automatic trigger calls Tick at the facts cadence
// (cmd/wwd qualifyTrigger, WWD-5b-2c-2).
//
// Locking: Tick and Request hold the coordinator's lock while they prepare a snapshot (git) and read and write the queue, so the caller
// runs them in a goroutine of their own, never inside another lock (not in the daemon's labels lock, which Granted takes). cmd/wwd wires it so.
//
// The pairs in flight are in memory: a restart forgets them (their two jobs go on and finish, unobserved). The rate rule's memory (the last
// unsuccessful attempt of each task) is in the store and is read back by New, so a restart does not bring a retry forward. A new pair for the
// same task supersedes the old jobs by their supersede keys, so a restart does not leave two pairs running.

// Queue is the part of the origin's queue the coordinator uses (*queue.Queue).
type Queue interface {
	Submit(ctx context.Context, s wire.JobSubmission) (wire.JobSubmissionResponse, error)
	Get(ctx context.Context, id string) (wire.Job, error)
	Cancel(ctx context.Context, id string) (wire.Job, error)
}

// Store is the earned-label table and the last unsuccessful attempt of each task (store.Store): the first is what the host holds, the second
// is what keeps the 24 h wait of spec §7 across a restart.
type Store interface {
	EarnedLabels() ([]wire.EarnedLabel, error)
	SaveEarnedLabel(wire.EarnedLabel) error
	QualifyAttempts() ([]store.QualifyAttempt, error)
	SaveQualifyAttempt(store.QualifyAttempt) error
	DeleteQualifyAttempt(label string) error
}

// Source is what the coordinator reads of this host.
type Source interface {
	// Host is the host now: its alias, whether its worker is on, the labels it advertises and its fingerprint.
	Host() Host
	// Tasks is the tasks of the prepared clones that have a qualify with a reference host. A clone whose manifest cannot be read or parsed
	// contributes nothing and is reported in the error (joined, one per clone) next to the tasks of the others.
	Tasks() ([]Task, error)
	// Prepare takes the snapshot of the task's clone and returns the submission of the task as the manifest declares it, with the qualify
	// args as its args (the caller turns it into the two halves). One call, so both halves share one snapshot.
	Prepare(ctx context.Context, t Task) (wire.JobSubmission, error)
}

// Config is what New needs.
type Config struct {
	Queue  Queue
	Store  Store
	Source Source
	// Now is the clock (default time.Now).
	Now func() time.Time
	// Granted is called after a label was earned, so the daemon advertises it without waiting for the next facts record (RefreshLabels).
	Granted func()
}

// Record is how a pair ended, for `pdx wwd qualify --status` and the log.
type Record struct {
	Label     string
	Outcome   Outcome
	Why       string
	At        time.Time
	Candidate string // job ids
	Reference string
}

// Started is a pair that was queued.
type Started struct {
	Label     string
	Candidate string
	Reference string
}

type openPair struct {
	task      Task
	identity  string // task.Identity() when the pair was queued
	cand, ref string
	at        time.Time
	fp        map[string]string
	tree      string
}

// Coordinator runs the pairs. Safe for concurrent use: Tick and Request take the same lock, so two triggers racing start one pair.
type Coordinator struct {
	cfg     Config
	mu      sync.Mutex
	pairs   map[string]*openPair
	last    map[string]Attempt
	history []Record
}

const maxHistory = 50

// SessionName is the submitter session of the jobs the coordinator queues.
const SessionName = labels.QualifySession

// New builds a coordinator. Nothing runs until Tick or Request is called.
func New(cfg Config) *Coordinator {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	c := &Coordinator{cfg: cfg, pairs: map[string]*openPair{}, last: map[string]Attempt{}}
	// the memory of the last unsuccessful attempts is the store's: a restart does not give a task its retry back before its time
	rows, err := cfg.Store.QualifyAttempts()
	var damaged *store.SkippedRowsError
	if err != nil && !errors.As(err, &damaged) {
		log.Printf("wwd: qualify: reading the last attempts (the retry waits start empty): %v", err)
	}
	for _, r := range rows {
		c.last[r.Label] = Attempt{At: r.At, Outcome: Outcome(r.Outcome), Fingerprint: r.Fingerprint}
	}
	return c
}

// rememberAttempt keeps the last unsuccessful attempt of the task, in memory and in the store.
func (c *Coordinator) rememberAttempt(label string, a Attempt) {
	c.last[label] = a
	if err := c.cfg.Store.SaveQualifyAttempt(store.QualifyAttempt{Label: label, Outcome: string(a.Outcome), Fingerprint: a.Fingerprint, At: a.At}); err != nil {
		log.Printf("wwd: qualify %s: saving the attempt (a restart would forget its retry wait): %v", label, err)
	}
}

// forgetAttempt drops the memory of the task's last attempt (the label was earned, or the task is gone).
func (c *Coordinator) forgetAttempt(label string) {
	delete(c.last, label)
	if err := c.cfg.Store.DeleteQualifyAttempt(label); err != nil {
		log.Printf("wwd: qualify %s: deleting the attempt: %v", label, err)
	}
}

// Settle only settles the pairs that are done (a pair a person started by hand needs no tick to finish); it starts nothing.
func (c *Coordinator) Settle(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settle(ctx)
}

// RunSettle calls Settle every period until ctx ends.
func (c *Coordinator) RunSettle(ctx context.Context, period time.Duration) {
	t := time.NewTicker(period)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Settle(ctx)
		}
	}
}

// Tick settles the pairs that are done, then starts the pairs the automatic trigger asks for. It is meant to be called at the facts
// cadence; every call is cheap when nothing is due.
func (c *Coordinator) Tick(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settle(ctx)
	in, err := c.input()
	if err != nil {
		log.Printf("wwd: qualify: %v", err)
		return
	}
	if in.TasksErr != nil { // a manifest that could not be read hides its tasks: they are not gone, and their retry memory stays
		log.Printf("wwd: qualify: %v", in.TasksErr)
	} else {
		c.forgetGoneTasks(in.Tasks)
	}
	for _, t := range Decide(in) {
		if _, err := c.start(ctx, t); err != nil {
			log.Printf("wwd: qualify %s: %v", t.Label(), err)
		}
	}
}

// forgetGoneTasks drops the retry memory of tasks that no longer exist, so it cannot grow with the history of manifests.
func (c *Coordinator) forgetGoneTasks(tasks []Task) {
	have := map[string]bool{}
	for _, t := range tasks {
		have[t.Label()] = true
	}
	for l := range c.last {
		if !have[l] {
			c.forgetAttempt(l)
		}
	}
}

// Request starts a pair for <repo>/<task> on a person's word: the checks of the automatic trigger without its 24 h wait.
func (c *Coordinator) Request(ctx context.Context, repoTask string) (Started, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settle(ctx)
	in, err := c.input()
	if err != nil {
		return Started{}, err
	}
	t, err := Manual(in, repoTask)
	var unknown *UnknownTaskError
	if errors.As(err, &unknown) && in.TasksErr != nil { // not found, but a manifest could not be read: that is a fault of the host, not a wrong name
		log.Printf("wwd: qualify: %v", in.TasksErr)
		return Started{}, fmt.Errorf("task %q not found, and a manifest could not be read: %w", repoTask, in.TasksErr)
	}
	if err != nil {
		return Started{}, err
	}
	return c.start(ctx, t)
}

// Status is the pairs in flight (labels, oldest first by label) and the last records, newest first.
func (c *Coordinator) Status() (inFlight []Started, recent []Record) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.pairs {
		inFlight = append(inFlight, Started{Label: p.task.Label(), Candidate: p.cand, Reference: p.ref})
	}
	slices.SortFunc(inFlight, func(a, b Started) int { return strings.Compare(a.Label, b.Label) })
	for i := len(c.history) - 1; i >= 0; i-- {
		recent = append(recent, c.history[i])
	}
	return inFlight, recent
}

func (c *Coordinator) input() (Input, error) {
	rows, err := c.cfg.Store.EarnedLabels()
	var damaged *store.SkippedRowsError
	if err != nil && !errors.As(err, &damaged) { // damaged rows are left out and the rest is the answer; any other error is no answer
		return Input{}, fmt.Errorf("reading the earned labels: %w", err)
	}
	tasks, terr := c.cfg.Source.Tasks()
	in := Input{Now: c.cfg.Now(), Host: c.cfg.Source.Host(), Tasks: tasks, TasksErr: terr,
		Earned: map[string]string{}, InFlight: map[string]bool{}, Last: map[string]Attempt{}}
	for _, r := range rows {
		in.Earned[r.Label] = r.State
	}
	for l := range c.pairs {
		in.InFlight[l] = true
	}
	for l, a := range c.last {
		in.Last[l] = a
	}
	return in, nil
}

// pairClass is the class both halves of a pair run at: the task's own, capped at merge; dev when it is empty or not a class.
func pairClass(c wire.JobClass) wire.JobClass {
	switch c {
	case wire.ClassRelease, wire.ClassMerge:
		return wire.ClassMerge
	}
	return wire.ClassDev
}

// halves turns the task's own submission into the two halves. The candidate half asks for what a host that has not earned the label can
// offer: no os:/arch: (those are the task's reason to be Mac-only until a host is qualified) and not the task's own label, plus
// host:<candidate>. The reference half keeps every label the task has, plus host:<reference>. Both take the class of the task (base.Class, the
// manifest's) but at merge at most, and dev when it is empty or unknown: a dev class half starves behind a steady stream of merge work
// (the pick score is 1000 per class rank plus minutes waited). They are never cacheable, never interruptible, and carry supersede keys
// of their own so that a newer pair replaces an older one that is still around.
func halves(base wire.JobSubmission, t Task, candidate string) (cand, ref wire.JobSubmission) {
	own := t.Label()
	common := func(s wire.JobSubmission, role, host string) wire.JobSubmission {
		s.Class = pairClass(base.Class)
		s.Cacheable = false
		s.Interruptible = true // so that a newer pair (after a restart forgot this one) really stops an older one that is still running
		s.AdHoc = false
		s.SupersedeKey = labels.QualifyKey(candidate, own, role)
		s.Submitter = wire.Submitter{Session: SessionName, Host: candidate}
		s.Labels = labels.QualifyHalfLabels(base.Labels, own, role, host)
		return s
	}
	cand = common(base, labels.RoleCandidate, candidate)
	ref = common(base, labels.RoleReference, t.Reference)
	return cand, ref
}

func (c *Coordinator) start(ctx context.Context, t Task) (Started, error) {
	host := c.cfg.Source.Host()
	base, err := c.cfg.Source.Prepare(ctx, t)
	if err != nil {
		c.note(t, OutcomeVoid, "preparing the snapshot: "+err.Error(), "", "", host.Fingerprint)
		return Started{}, fmt.Errorf("preparing the snapshot: %w", err)
	}
	cand, ref := halves(base, t, host.Alias)
	cr, err := c.cfg.Queue.Submit(ctx, cand)
	if err != nil {
		c.note(t, OutcomeVoid, "queueing the candidate half: "+err.Error(), "", "", host.Fingerprint)
		return Started{}, fmt.Errorf("queueing the candidate half: %w", err)
	}
	rr, err := c.cfg.Queue.Submit(ctx, ref)
	if err != nil {
		_, _ = c.cfg.Queue.Cancel(ctx, cr.ID) // one half alone proves nothing and would use the host for nothing
		c.note(t, OutcomeVoid, "queueing the reference half: "+err.Error(), cr.ID, "", host.Fingerprint)
		return Started{}, fmt.Errorf("queueing the reference half: %w", err)
	}
	c.pairs[t.Label()] = &openPair{task: t, identity: t.Identity(), cand: cr.ID, ref: rr.ID, at: c.cfg.Now(), fp: host.Fingerprint, tree: base.Snapshot.Tree}
	return Started{Label: t.Label(), Candidate: cr.ID, Reference: rr.ID}, nil
}

// settle looks at every pair in flight and finishes those that are done.
func (c *Coordinator) settle(ctx context.Context) {
	for label, p := range c.pairs {
		cj, cerr := c.cfg.Queue.Get(ctx, p.cand)
		rj, rerr := c.cfg.Queue.Get(ctx, p.ref)
		switch {
		case errors.Is(cerr, queue.ErrNotFound) || errors.Is(rerr, queue.ErrNotFound):
			// the half that is still there is of no use now: stop it (a half we could not read, or that is already over, is left alone)
			c.cancelQuietly(ctx, p, cerr != nil || cj.State.Terminal(), rerr != nil || rj.State.Terminal())
			c.finish(label, p, Verdict{Outcome: OutcomeVoid, Why: "a half of the pair is gone from the queue"})
			continue
		case cerr != nil || rerr != nil:
			continue // the queue could not be read just now: look again on the next tick
		}
		cDone, rDone := cj.State.Terminal(), rj.State.Terminal()
		ranC, ranR := ranToAnExit(cj), ranToAnExit(rj)
		switch {
		case cDone && rDone:
			v := Compare(Half{Want: c.cfg.Source.Host().Alias, Result: cj.Result}, Half{Want: p.task.Reference, Result: rj.Result})
			if v.Outcome == OutcomeMatch && c.taskHiddenByUnreadableManifest(p) {
				continue // a match is not thrown away for a manifest that cannot be read just now: look again on the next tick
			}
			c.finish(label, p, v)
		case (cDone && !ranC) || (rDone && !ranR):
			// one half ended without running (cancelled, superseded, expired): the pair is void, and the other half is not worth waiting for
			c.cancelQuietly(ctx, p, cDone, rDone)
			c.finish(label, p, Verdict{Outcome: OutcomeVoid, Why: "a half ended without running to an exit"})
		}
	}
}

func ranToAnExit(j wire.Job) bool { return j.Result != nil && j.Result.Exit != nil }

func (c *Coordinator) cancelQuietly(ctx context.Context, p *openPair, candDone, refDone bool) {
	if !candDone {
		_, _ = c.cfg.Queue.Cancel(ctx, p.cand)
	}
	if !refDone {
		_, _ = c.cfg.Queue.Cancel(ctx, p.ref)
	}
}

// finish records the end of a pair; a match earns the label, unless the host's toolchain changed while the pair ran (what both halves
// measured is then not what the host is).
func (c *Coordinator) finish(label string, p *openPair, v Verdict) {
	delete(c.pairs, label)
	now := c.cfg.Now()
	if v.Outcome == OutcomeMatch && !sameFingerprint(p.fp, c.cfg.Source.Host().Fingerprint) {
		v = Verdict{Outcome: OutcomeVoid, Why: "the host's toolchain changed while the pair ran"}
	}
	if v.Outcome == OutcomeMatch && !c.stillTheSameTask(p) {
		v = Verdict{Outcome: OutcomeVoid, Why: "the task's definition changed (or the task is gone) while the pair ran"}
	}
	if v.Outcome == OutcomeMatch {
		err := c.cfg.Store.SaveEarnedLabel(wire.EarnedLabel{Label: label, State: wire.EarnedLabelEarned, Fingerprint: p.fp, Snapshot: p.tree, GrantedAt: now})
		if err != nil {
			log.Printf("wwd: qualify %s: saving the label: %v", label, err)
			v = Verdict{Outcome: OutcomeVoid, Why: "saving the label failed: " + err.Error()}
		} else {
			c.forgetAttempt(label)
			if c.cfg.Granted != nil {
				c.cfg.Granted()
			}
		}
	}
	if v.Outcome != OutcomeMatch {
		c.rememberAttempt(label, Attempt{At: now, Outcome: v.Outcome, Fingerprint: p.fp})
	}
	c.remember(Record{Label: label, Outcome: v.Outcome, Why: v.Why, At: now, Candidate: p.cand, Reference: p.ref})
}

// taskHiddenByUnreadableManifest says whether the task cannot be seen now only because a manifest could not be read (so whether it is
// still the same task is not known yet, as opposed to known to be gone or changed).
func (c *Coordinator) taskHiddenByUnreadableManifest(p *openPair) bool {
	tasks, err := c.cfg.Source.Tasks()
	if err == nil {
		return false
	}
	for _, t := range tasks {
		if t.Label() == p.task.Label() {
			return false
		}
	}
	return true
}

// stillTheSameTask says whether the task is, now, the one the pair was queued for: the same repo, name, reference, qualify args, needs and
// definition. A result of an older definition proves nothing about the current one.
func (c *Coordinator) stillTheSameTask(p *openPair) bool {
	tasks, _ := c.cfg.Source.Tasks() // a manifest that cannot be read hides the task: the pair is not known to be of the current definition
	for _, t := range tasks {
		if t.Label() == p.task.Label() {
			return t.Identity() == p.identity
		}
	}
	return false
}

// note records a pair that never got going.
func (c *Coordinator) note(t Task, o Outcome, why, cand, ref string, fp map[string]string) {
	now := c.cfg.Now()
	c.rememberAttempt(t.Label(), Attempt{At: now, Outcome: o, Fingerprint: fp})
	c.remember(Record{Label: t.Label(), Outcome: o, Why: why, At: now, Candidate: cand, Reference: ref})
}

func (c *Coordinator) remember(r Record) {
	log.Printf("wwd: qualify %s: %s (%s)", r.Label, r.Outcome, r.Why)
	c.history = append(c.history, r)
	if len(c.history) > maxHistory {
		c.history = c.history[len(c.history)-maxHistory:]
	}
}
