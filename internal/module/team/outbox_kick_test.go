package teammod

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// The commands and facts pumps poll every outboxSafetyTick (5 s) instead of every second. What keeps a new entry from waiting for
// that tick is a pass at start, a kick after every commit that queued something, and a pass that is not lost while a drain runs.

func kicked(p *outboxPump) bool { return len(p.sig) > 0 }

func TestOutboxSafetyTick_IsLongerThanTheRemoteNoticeTickAndOwnedByThePump(t *testing.T) {
	if outboxSafetyTick <= pumpTick {
		t.Fatalf("outboxSafetyTick %s is not longer than pumpTick %s", outboxSafetyTick, pumpTick)
	}
	f, _, _ := pumpFixture(t)
	if f.m.cmdPump.tick != outboxSafetyTick {
		t.Fatalf("commands pump tick = %s", f.m.cmdPump.tick)
	}
	ff, _ := factsFixture(t)
	if ff.m.factPump.tick != outboxSafetyTick {
		t.Fatalf("facts pump tick = %s", ff.m.factPump.tick)
	}
}

// What was pending before the pump ran (a restart, or an expiry in Init that kicked a pump that did not exist yet) is sent by the
// pump's first pass, not by its first tick.
func TestPump_AnEntryPendingBeforeStartIsSentByTheFirstPass(t *testing.T) {
	f, fc, _ := pumpFixture(t)
	f.m.cmdPump.tick = time.Hour // only a pass at start can send it
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	f.m.sweepWG.Add(1)
	go f.m.cmdPump.run()
	waitFor(t, func() bool { return f.cmdState("c1").State == cmdDone })
	if len(fc.sent()) != 1 {
		t.Fatalf("calls = %d", len(fc.sent()))
	}
}

// The same for the facts pump.
func TestFactsPump_AFactPendingBeforeStartIsSentByTheFirstPass(t *testing.T) {
	f, fc := factsFixture(t)
	f.m.factPump.tick = time.Hour
	f.queueEnded("mk1", "sid-1")
	f.m.sweepWG.Add(1)
	go f.m.factPump.run()
	waitFor(t, func() bool { return len(fc.sent()) == 1 })
}

// hookStore runs afterEmptyHead once, the first time the queue answers "nothing pending" - the moment a drain is about to end
// but has not yet left `running`.
type hookStore struct {
	outboxStore
	afterEmptyHead func()
}

func (h *hookStore) Head(host string) (outboxEntry, int64, bool, error) {
	e, next, ok, err := h.outboxStore.Head(host)
	if !ok && h.afterEmptyHead != nil {
		fn := h.afterEmptyHead
		h.afterEmptyHead = nil
		fn()
	}
	return e, next, ok, err
}

// A commit and its kick that land after the drain looked at the queue and before it left `running` were skipped by the pass the
// kick caused; with a one-second tick that was a second late, with a five-second tick it would be five. The drain now asks for
// one more pass when it ends.
func TestPump_AKickWhileADrainIsEndingIsNotLost(t *testing.T) {
	f, fc, _ := pumpFixture(t)
	f.m.cmdPump.tick = time.Hour
	f.enqueue(f.cmd("c1", CmdRelease, "hostM", "mk1"))
	hs := &hookStore{outboxStore: f.m.cmdPump.store}
	hs.afterEmptyHead = func() {
		f.enqueue(f.cmd("c2", CmdRelease, "hostM", "mk2")) // committed after the drain's last look
		f.m.kickCommands()                                 // its pass finds the host running and skips it
		// keep the drain alive until the pump's goroutine has taken the kick and made that pass, which records the skip in `missed`
		// (bounded: without the record the pass never shows, and the test then fails on its own assertion below)
		p := f.m.cmdPump
		for i := 0; i < 400; i++ {
			p.mu.Lock()
			seen := p.missed
			p.mu.Unlock()
			if seen {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	f.m.cmdPump.store = hs
	f.m.sweepWG.Add(1)
	go f.m.cmdPump.run()
	waitFor(t, func() bool { return f.cmdState("c1").State == cmdDone && f.cmdState("c2").State == cmdDone })
	if len(fc.sent()) != 2 {
		t.Fatalf("calls = %d", len(fc.sent()))
	}
}

// Boot: a running spawn op whose stored row is corrupt is failed by the listing, and when it belongs to a lead on another host
// that queues a spawn_failed fact; the pump must hear of it.
func TestResumeSpawns_KicksTheFactsPumpForTheFactAFailedCorruptOpQueued(t *testing.T) {
	f, root := newSpawnFixture(t, 4)
	op := f.acceptOp(1, root, nil)
	if _, err := f.m.store.db.Exec(`UPDATE spawn_ops SET cwd = '', lead_host_id = 'host-L' WHERE id = ?`, op); err != nil {
		t.Fatal(err)
	}
	fc := &fakeHostCaller{}
	f.m.factPump = newOutboxPump("facts", fc, f.m.newFactOutbox(), f.m.now, f.m.logf, f.m.stopCtx, &f.m.sweepWG)
	f.m.resumeSpawns()
	if got := factsOf(t, f.m.store, "host-L"); len(got) != 1 || got[0].Kind != team.FactSpawnFailed {
		t.Fatalf("facts = %+v, want one spawn_failed", got)
	}
	if !kicked(f.m.factPump) {
		t.Fatal("the facts pump was not kicked for the fact the boot queued")
	}
}

// An approved member relay whose op cannot be read back still has its command committed; the kick comes before the read can fail.
func TestAfterMemberRelayApproved_KicksEvenWhenTheOpCannotBeRead(t *testing.T) {
	f, _, _ := pumpFixture(t)
	f.m.afterMemberRelayApproved(team.Approval{ID: "no-such-request"})
	if !kicked(f.m.cmdPump) {
		t.Fatal("the commands pump was not kicked")
	}
}
