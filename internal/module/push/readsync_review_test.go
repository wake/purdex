package push

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/reads"
)

// Review findings of PR BG-4b (R1 P2 x2, attack high x2 / medium).

// A timer callback that was already running when Sent cancelled its timer and armed a new one must not touch the new timer's
// state: each arm has its own generation. Mutation gate: callbacks that only compare the registration → red.
func TestReadSync_AStaleCallbackCannotDisturbTheTimerSentArmed(t *testing.T) {
	e := newRSEnv()
	e.Add("d1", r("a", 1), e.clock.Now())
	stale := e.clock.timers[0].fn // the callback of the first arm, "already on its way"
	e.Sent("d1", []reads.Read{{Code: "zzz"}})
	e.Add("d1", r("b", 1), time.Time{})
	if e.clock.live() != 1 {
		t.Fatalf("%d live timers before the stale callback", e.clock.live())
	}
	stale() // arrives late, after Sent re-armed the timer
	if len(e.firedFor()) != 0 {
		t.Fatal("the stale callback flushed early")
	}
	if e.clock.live() != 1 {
		t.Fatalf("%d live timers after the stale callback, want the one Sent armed", e.clock.live())
	}
	e.clock.advance(15 * time.Minute)
	if got := e.firedFor(); len(got) != 1 {
		t.Fatalf("fired = %v, want exactly one flush", got)
	}
}

// onRead decides who gets the read and records it under the module's lock, the same lock every removal path holds: a
// registration that is removed or stops syncing at that moment is not given a pending list afterwards. Stress, so run with
// -race; the old snapshot-then-Add left an orphan timer behind.
func TestReadSyncPush_ARemovalRacingAReadLeavesNothingBehind(t *testing.T) {
	e := newRSPushEnv(t)
	for round := 0; round < 150; round++ {
		tok := fmt.Sprintf("%064x", 0x5eed0000+round)
		id := push.DeviceID(tok)
		e.device(tok, "en", "mlab", readSyncDevice("c1"))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 1; i <= 20; i++ {
				e.read(fmt.Sprintf("r%d", i), uint64(i))
			}
		}()
		go func() {
			defer wg.Done()
			e.do("DELETE", "/api/push/devices/"+id, "")
		}()
		wg.Wait()
		if n := e.mod.rs.Pending(id); n != 0 {
			t.Fatalf("round %d: a removed registration holds %d pending reads", round, n)
		}
	}
	if live := e.clock.live(); live != 0 {
		t.Fatalf("%d flush timers left for registrations that were removed", live)
	}
}

// A paired phone whose APNs token rotated (the same owner, bundle and environment under a new device id) has its older
// registration superseded: it neither keeps its pending reads nor is given new ones. A registration of the same owner in the
// other environment is a different thing and keeps working.
func TestReadSyncPush_ASupersededRegistrationTakesNoMoreReads(t *testing.T) {
	e := newRevokeEnv(t)
	must(t, e.push.Start(context.Background()))
	p := e.pair(t, pairA)
	reg := func(tok, env string) {
		body := reqBody(tok, func(r *push.DeviceRequest) { r.Env = env; r.Prefs = push.Prefs{ReadSync: true} })
		if rec := e.call(&p, "POST", "/api/push/devices", body); rec.Code != 200 {
			t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
		}
	}
	old, sandboxToo, fresh := tokA, tokB, "c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3"
	reg(old, "sandbox")
	reg(sandboxToo, "production") // another environment: not a replacement
	e.push.onRead(reads.Read{Code: "r1", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	if e.push.rs.Pending(push.DeviceID(old)) != 1 || e.push.rs.Pending(push.DeviceID(sandboxToo)) != 1 {
		t.Fatalf("pending: old %d, other env %d", e.push.rs.Pending(push.DeviceID(old)), e.push.rs.Pending(push.DeviceID(sandboxToo)))
	}
	reg(fresh, "sandbox") // the token rotated
	if e.push.rs.Pending(push.DeviceID(old)) != 0 {
		t.Fatal("the superseded registration kept its pending reads")
	}
	if e.push.rs.Pending(push.DeviceID(sandboxToo)) != 1 {
		t.Fatal("a registration of the other environment lost its pending reads")
	}
	e.push.onRead(reads.Read{Code: "r2", Through: reads.Cursor{Epoch: "e", Seq: 1}})
	if e.push.rs.Pending(push.DeviceID(old)) != 0 {
		t.Fatal("a superseded registration was given a new pending read: its flush would go to a dead token")
	}
	if e.push.rs.Pending(push.DeviceID(fresh)) != 1 || e.push.rs.Pending(push.DeviceID(sandboxToo)) != 2 {
		t.Fatalf("pending: fresh %d, other env %d", e.push.rs.Pending(push.DeviceID(fresh)), e.push.rs.Pending(push.DeviceID(sandboxToo)))
	}
}

// onRead records under m.mu (the lock every removal path holds), not after a snapshot taken and released: the stress test above
// is probabilistic, this one is not. Mutation gate: snapshot, then Add outside the lock → red.
func TestReadSyncPush_OnReadRecordsUnderTheModuleLock(t *testing.T) {
	e := newRSPushEnv(t)
	e.device(tokA, "en", "mlab", readSyncDevice("c1"))
	e.device(tokB, "en", "mlab", readSyncDevice("c1"))
	adds := 0
	e.mod.rs.beforeAdd = func() {
		adds++
		if e.mod.mu.TryLock() {
			e.mod.mu.Unlock()
			t.Error("Add ran without m.mu held: a removal could slip in between the choice and the record")
		}
	}
	e.read("r1", 1)
	if adds != 2 {
		t.Fatalf("Add ran %d times, want once per syncing registration", adds)
	}
}
