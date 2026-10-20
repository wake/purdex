package loop

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/run"
	"github.com/wake/purdex/internal/wwd/wire"
	"github.com/wake/purdex/internal/wwd/worker/quarantine"
)

// No room in the ledger, no claim: the queue is not touched, and nothing is left booked.
func TestNoReservationMeansNoClaim(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	r := newRig(t, c)
	asked := 0
	r.loop.cfg.Reserve = func() (func(), bool) { asked++; return nil, false }
	r.round()
	if claims, _, _ := c.seen(); len(claims) != 0 || asked != 1 {
		t.Fatalf("claims %v, asked %d", claims, asked)
	}
}

// An empty queue books nothing (the ledger does not flicker for a round with nothing to claim).
func TestAnEmptyQueueReservesNothing(t *testing.T) {
	r := newRig(t, newConn("mlab"))
	r.loop.cfg.Reserve = func() (func(), bool) { t.Error("reserved for an empty queue"); return func() {}, true }
	r.round()
}

// The reservation is given back only after the claimed job is in the loop's running count, so a fast-lane run that is admitted the
// moment it is released already sees the job.
func TestTheReservationIsReleasedAfterTheJobIsRunning(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	r := newRig(t, c)
	release := make(chan struct{})
	r.ex.run = func(ctx context.Context, _ wire.Job) run.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return run.Result{Outcome: run.Exited}
	}
	released, runningAtRelease := 0, -1
	r.loop.cfg.Reserve = func() (func(), bool) {
		return func() { released++; runningAtRelease = r.loop.Running() }, true
	}
	r.round()
	if released != 1 || runningAtRelease != 1 {
		t.Fatalf("released %d times, with %d running at the release; want once, with the job running", released, runningAtRelease)
	}
	close(release)
}

// A round that claims nothing (every candidate refused) also gives the reservation back.
func TestTheReservationIsReleasedWhenNothingIsClaimed(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	c.claimErr["j1"] = errors.New("409") // the only candidate is refused: nothing is claimed
	r := newRig(t, c)
	released := 0
	r.loop.cfg.Reserve = func() (func(), bool) { return func() { released++ }, true }
	r.round()
	if released != 1 {
		t.Fatalf("released %d times, want once", released)
	}
}

// WWD-9 W3: the worker no longer asks the pdx gate for anything. With the host's ledger and the facts saying there is room, a job is
// claimed and run whatever the state of a gate on the host (full, paused or absent: the loop cannot tell, there is nothing to ask).
func TestAJobIsClaimedWithoutAnyGateToAsk(t *testing.T) {
	c := newConn("mlab", qjob("j1", wire.ClassDev, 0))
	r := newRig(t, c)
	r.round()
	waitFor(t, "the result", func() bool { _, _, n := c.seen(); return n == 1 })
}

// WWD-9 W3: a quarantined job's command may still run, and the pdx gate no longer counts it. The worker does: it holds one heavy slot
// until the sweep proves the unit gone.
func TestAQuarantinedJobStillTakesItsHeavySlot(t *testing.T) {
	c := newConn("mlab", qjob("free", wire.ClassDev, time.Minute))
	q := newQRig(t, c)
	q.st.Slots.Heavy = 1
	if err := q.store.Add(quarantine.Entry{Owner: "mlab/old", JobID: "old", Origin: "mlab", Dir: filepath.Join(q.root, "old"), Unit: "wwd-old"}); err != nil {
		t.Fatal(err)
	}
	if n := q.loop.Running(); n != 1 {
		t.Fatalf("Running %d, want the quarantined job counted", n)
	}
	q.round()
	if claims, _, _ := c.seen(); len(claims) != 0 {
		t.Fatalf("claims %v with the only heavy slot taken by a quarantined job", claims)
	}
	q.units.mu.Lock()
	q.units.gone["wwd-old"] = true
	q.units.mu.Unlock()
	q.loop.Sweep(context.Background())
	if n := q.loop.Running(); n != 0 {
		t.Fatalf("Running %d after the quarantine cleared", n)
	}
	time.Sleep(2 * unit)
	q.round()
	waitFor(t, "the claim", func() bool { claims, _, _ := c.seen(); return len(claims) == 1 })
}
