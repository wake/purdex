package loop

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
)

// fakeGate is a host's pdx gate as the loop sees it: free heavy slots it hands out, and a record of every slot and what became of it.
type fakeGate struct {
	mu     sync.Mutex
	free   int
	noGate bool // a host without a gate: TakeHeavy answers ok with no slot
	// lateFail makes TakeLate fail (the gate cannot be read, or is paused)
	lateFail bool
	takes    int
	lates    int
	slots    []*fakeSlot
}

type fakeSlot struct {
	g        *fakeGate
	argv     []string
	session  string
	released int
}

func (g *fakeGate) TakeHeavy() (GateSlot, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.takes++
	if g.noGate {
		return nil, true
	}
	if g.free <= 0 {
		return nil, false
	}
	g.free--
	s := &fakeSlot{g: g}
	g.slots = append(g.slots, s)
	return s, true
}

// TakeLate is a slot for a command that is already running (it may go beyond the gate's size, so it is given while any is free, not only
// within the count; the fake treats it as a plain take that does not look at the count).
func (g *fakeGate) TakeLate() (GateSlot, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lates++
	if g.noGate {
		return nil, true
	}
	if g.lateFail {
		return nil, false
	}
	g.free--
	s := &fakeSlot{g: g}
	g.slots = append(g.slots, s)
	return s, true
}

func (s *fakeSlot) Describe(argv []string, session string) {
	s.g.mu.Lock()
	defer s.g.mu.Unlock()
	s.argv, s.session = argv, session
}

func (s *fakeSlot) Release() {
	s.g.mu.Lock()
	defer s.g.mu.Unlock()
	s.released++
	s.g.free++
}

func (g *fakeGate) state() (free, takes int, slots []fakeSlot) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, s := range g.slots {
		slots = append(slots, fakeSlot{argv: s.argv, session: s.session, released: s.released})
	}
	return g.free, g.takes, slots
}

func withGate(r *rig, g *fakeGate) *rig {
	r.loop.cfg.Gate = g
	return r
}

// A claimed job holds one of the gate's heavy slots, named as wwd's job so that the gate's status and the watcher can tell whose it is,
// and gives it back when the job is over.
func TestAClaimedJobHoldsAGateSlotUntilItIsOver(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	g := &fakeGate{free: 2}
	r := withGate(newRig(t, c), g)
	release := make(chan struct{})
	r.ex.run = func(ctx context.Context, _ wire.Job) run.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return run.Result{Outcome: run.Exited}
	}
	r.round()
	free, takes, slots := g.state()
	if takes != 1 || free != 1 || len(slots) != 1 || slots[0].released != 0 {
		t.Fatalf("while it runs: free=%d takes=%d slots=%+v", free, takes, slots)
	}
	if got := slots[0].argv; len(got) != 4 || got[0] != "wwd" || got[1] != "job" || got[2] != "j1" || got[3] != "test" || slots[0].session != "s-j1" {
		t.Fatalf("the slot says %v / %q, want wwd job j1 test / s-j1", got, slots[0].session)
	}
	close(release)
	waitFor(t, "the slot given back", func() bool { free, _, s := g.state(); return free == 2 && s[0].released == 1 })
}

// No free slot, no claim: the queue is not touched, and the next round tries again.
func TestNoGateSlotMeansNoClaim(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	g := &fakeGate{free: 0}
	r := withGate(newRig(t, c), g)
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v with the gate full", claims)
	}
	if _, takes, _ := g.state(); takes != 1 {
		t.Fatalf("takes %d, want one try", takes)
	}
	g.mu.Lock()
	g.free = 1
	g.mu.Unlock()
	time.Sleep(2 * unit)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	waitFor(t, "the slot given back", func() bool { free, _, _ := g.state(); return free == 1 })
}

// A host without a gate is not held back.
func TestAHostWithoutAGateClaimsAsBefore(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	g := &fakeGate{noGate: true}
	r := withGate(newRig(t, c), g)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	if _, _, slots := g.state(); len(slots) != 0 {
		t.Fatalf("slots %+v on a host without a gate", slots)
	}
}

// An empty queue costs the gate nothing: no slot is taken just to look.
func TestAnEmptyQueueTakesNoGateSlot(t *testing.T) {
	g := &fakeGate{free: 2}
	r := withGate(newRig(t, newConn("mlab")), g)
	r.round()
	if _, takes, _ := g.state(); takes != 0 {
		t.Fatalf("takes %d with nothing to claim", takes)
	}
}

// A candidate that is not claimed (someone was first, the origin refused) gives the slot back at once, and one slot serves the whole
// round's tries: it is taken once.
func TestAnUnclaimedCandidateGivesTheSlotBack(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassDev, 0), qjob("b", wire.ClassDev, time.Second))
	c.claimErr["a"] = errors.New("409")
	c.claimErr["b"] = errors.New("409")
	g := &fakeGate{free: 1}
	r := withGate(newRig(t, c), g)
	r.round()
	free, takes, slots := g.state()
	if takes != 1 || free != 1 || len(slots) != 1 || slots[0].released != 1 {
		t.Fatalf("nothing claimed: free=%d takes=%d slots=%+v", free, takes, slots)
	}
}

// Second candidate claimed after the first was refused: the same slot goes to the job that ran, once.
func TestTheSlotGoesToTheCandidateThatWasClaimed(t *testing.T) {
	c := newConn("mlab", qjob("a", wire.ClassRelease, 0), qjob("b", wire.ClassDev, time.Second))
	c.claimErr["a"] = errors.New("409")
	g := &fakeGate{free: 1}
	r := withGate(newRig(t, c), g)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
	waitFor(t, "the slot given back", func() bool { free, _, _ := g.state(); return free == 1 })
	_, takes, slots := g.state()
	if takes != 1 || len(slots) != 1 || slots[0].released != 1 || len(slots[0].argv) != 4 || slots[0].argv[2] != "b" {
		t.Fatalf("takes=%d slots=%+v", takes, slots)
	}
}

// A job whose command could not be proven stopped may still be running: its slot stays taken (the gate must not hand it to another heavy
// command) until the quarantine proves the unit gone, and is given back by the sweep that clears it.
func TestAQuarantinedJobKeepsItsGateSlotUntilTheSweepClearsIt(t *testing.T) {
	c := newConn("mlab", qjob("stuck", wire.ClassDev, 0, "mysql"))
	g := &fakeGate{free: 1}
	q := newQRig(t, c)
	withGate(q.rig, g)
	q.ex.run = notProven
	q.round()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() == 1 })
	q.loop.stopAll(context.Background(), "test")
	waitFor(t, "the quarantine", func() bool { return len(q.store.Held()) == 1 })
	waitFor(t, "the job to be over", func() bool { return q.loop.Running() == 0 })
	time.Sleep(5 * unit)
	if free, _, s := g.state(); free != 0 || s[0].released != 0 {
		t.Fatalf("the command may still run, the slot must stay: free=%d %+v", free, s)
	}
	q.loop.Sweep(context.Background()) // the unit is not proven gone
	if free, _, _ := g.state(); free != 0 {
		t.Fatalf("not proven gone: free=%d", free)
	}
	q.units.mu.Lock()
	q.units.gone["wwd-stuck"] = true
	q.units.mu.Unlock()
	q.loop.Sweep(context.Background())
	if free, _, s := g.state(); free != 1 || s[0].released != 1 {
		t.Fatalf("proven gone and cleared: free=%d %+v", free, s)
	}
	q.loop.Sweep(context.Background()) // nothing left to give back twice
	if _, _, s := g.state(); s[0].released != 1 {
		t.Fatalf("released %d times", s[0].released)
	}
}

// The same job quarantined a second time (it was given back, claimed again and again not proven stopped) leaves one slot for its key: the
// first is given back, not leaked.
func TestAJobQuarantinedAgainReplacesItsSlot(t *testing.T) {
	g := &fakeGate{free: 2}
	r := newRig(t, newConn("mlab"))
	withGate(r, g)
	a, _ := g.TakeHeavy()
	b, _ := g.TakeHeavy()
	r.loop.keepForQuarantine("mlab/j", a)
	r.loop.keepForQuarantine("mlab/j", b)
	if free, _, s := g.state(); free != 1 || s[0].released != 1 || s[1].released != 0 {
		t.Fatalf("the first is given back, the second kept: free=%d %+v", free, s)
	}
	r.loop.keepForQuarantine("mlab/j", b) // the same slot again: nothing is given back
	if _, _, s := g.state(); s[1].released != 0 {
		t.Fatalf("%+v", s)
	}
}

// After a restart the quarantined jobs are still held (their commands may live on): the new process takes a slot for each before it claims
// anything, beyond the gate's size if it has to, and gives it back when the sweep clears the entry.
func TestARestartTakesASlotForEachQuarantinedJobBeforeAnyClaim(t *testing.T) {
	c := newConn("mlab", qjob("free", wire.ClassDev, time.Minute))
	g := &fakeGate{free: 2}
	q := newQRig(t, c)
	withGate(q.rig, g)
	if err := q.store.Add(quarantine.Entry{Owner: "mlab/old", JobID: "old", Origin: "mlab", Dir: filepath.Join(q.root, "old"), Unit: "wwd-old"}); err != nil {
		t.Fatal(err)
	}
	q.ex.run = blockUntilDone
	q.loop.cfg.SweepEvery = unit
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.loop.Run(ctx) }()
	waitFor(t, "a run", func() bool { return q.ex.ran.Load() >= 1 })
	free, takes, slots := g.state()
	g.mu.Lock()
	lates := g.lates
	g.mu.Unlock()
	if lates != 1 || len(slots) != 2 || free != 0 || takes != 1 {
		t.Fatalf("a slot for the quarantined job (late) and one for the new job: lates=%d free=%d takes=%d slots=%+v", lates, free, takes, slots)
	}
	if len(slots[0].argv) < 3 || slots[0].argv[0] != "wwd" || slots[0].argv[2] != "old" {
		t.Fatalf("the late slot says %v", slots[0].argv)
	}
	q.units.mu.Lock()
	q.units.gone["wwd-old"] = true
	q.units.mu.Unlock()
	waitFor(t, "the quarantined slot given back", func() bool { _, _, s := g.state(); return s[0].released == 1 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A quarantined job the new process could not count (the gate could not be read or was paused when it started) is counted as soon as the
// gate can, and nothing new is claimed before that: its command may be running, and a full slot count would let a heavy job on top of it.
func TestNothingIsClaimedWhileAQuarantinedJobIsNotCounted(t *testing.T) {
	c := newConn("mlab", qjob("free", wire.ClassDev, time.Minute))
	g := &fakeGate{free: 2, lateFail: true}
	q := newQRig(t, c)
	withGate(q.rig, g)
	if err := q.store.Add(quarantine.Entry{Owner: "mlab/old", JobID: "old", Origin: "mlab", Dir: filepath.Join(q.root, "old"), Unit: "wwd-old"}); err != nil {
		t.Fatal(err)
	}
	q.ex.run = blockUntilDone
	q.round()
	q.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v while the quarantined job is not counted", claims)
	}
	if _, takes, _ := g.state(); takes != 0 {
		t.Fatalf("takes %d: the gate was asked for a job's slot before the old one was counted", takes)
	}
	g.mu.Lock()
	g.lateFail = false
	g.mu.Unlock()
	time.Sleep(2 * unit)
	q.round()
	waitFor(t, "the claim", func() bool { claims, _, _ := c.seen(); return len(claims) == 1 })
	free, _, slots := g.state()
	if free != 0 || len(slots) != 2 || len(slots[0].argv) < 3 || slots[0].argv[2] != "old" {
		t.Fatalf("the old job counted first, then the new one: free=%d %+v", free, slots)
	}
	q.loop.stopAll(context.Background(), "test")
}

// A host without a gate has nothing to count, so a quarantined job there never holds back a claim.
func TestAQuarantinedJobOnAHostWithoutAGateHoldsNothingBack(t *testing.T) {
	c := newConn("mlab", qjob("free", wire.ClassDev, time.Minute))
	g := &fakeGate{noGate: true}
	q := newQRig(t, c)
	withGate(q.rig, g)
	if err := q.store.Add(quarantine.Entry{Owner: "mlab/old", JobID: "old", Origin: "mlab", Dir: filepath.Join(q.root, "old"), Unit: "wwd-old"}); err != nil {
		t.Fatal(err)
	}
	q.round()
	waitFor(t, "the claim", func() bool { claims, _, _ := c.seen(); return len(claims) == 1 })
}

// Every way a job ends gives the slot back: stopped for the owner, and a command that fails.
func TestEveryEndOfAJobGivesTheSlotBack(t *testing.T) {
	t.Run("stopped for the owner", func(t *testing.T) {
		c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
		g := &fakeGate{free: 1}
		r := withGate(newRig(t, c), g)
		r.ex.run = blockUntilDone
		r.round()
		if free, _, _ := g.state(); free != 0 {
			t.Fatalf("free %d while the job runs", free)
		}
		r.loop.stopAll(context.Background(), "test")
		waitFor(t, "the slot given back", func() bool { free, _, _ := g.state(); return free == 1 })
	})
	t.Run("the command fails", func(t *testing.T) {
		c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
		g := &fakeGate{free: 1}
		r := withGate(newRig(t, c), g)
		r.ex.run = func(context.Context, wire.Job) run.Result { return run.Result{Outcome: run.Exited, ExitCode: 1} }
		r.round()
		waitFor(t, "the slot given back", func() bool { free, _, s := g.state(); return free == 1 && len(s) == 1 && s[0].released == 1 })
	})
	t.Run("the worker ends", func(t *testing.T) {
		c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
		g := &fakeGate{free: 1}
		r := withGate(newRig(t, c), g)
		r.ex.run = blockUntilDone
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = r.loop.Run(ctx); close(done) }()
		waitFor(t, "the slot taken", func() bool { free, _, _ := g.state(); return free == 0 })
		cancel()
		<-done
		waitFor(t, "the slot given back", func() bool { free, _, _ := g.state(); return free == 1 })
	})
}
