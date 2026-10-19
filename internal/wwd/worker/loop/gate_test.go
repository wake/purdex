package loop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
)

// fakeGate is a host's pdx gate as the loop sees it: free heavy slots it hands out, and a record of every slot and what became of it.
type fakeGate struct {
	mu     sync.Mutex
	free   int
	noGate bool // a host without a gate: TakeHeavy answers ok with no slot
	takes  int
	slots  []*fakeSlot
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
