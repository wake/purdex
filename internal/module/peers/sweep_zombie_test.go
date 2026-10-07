package peers

// Zombie reaping in the startup sweep (#1767): Z1 — sweep behaviour over
// fake seams.

import (
	"os"
	"syscall"
	"testing"

	"github.com/wake/purdex/internal/peers/ccuds"
)

// zombieSweepSetup writes one record for sweepPID, marks the pid alive with
// our identity and returns the record.
func zombieSweepSetup(t *testing.T, tm *testManager) proxyRecord {
	t.Helper()
	rec := sweepRecord(t, tm, sweepPID, sweepPS)
	writeProxies(t, tm.proxiesPath, []proxyRecord{rec})
	tm.os.set(func() {
		tm.os.alive[sweepPID] = true
		tm.os.ps[sweepPID] = sweepPS
	})
	return rec
}

func TestSweepZombie_ReapedBeforeAnySignal(t *testing.T) {
	tm := newTestManager(t)
	rec := zombieSweepSetup(t, tm)
	tm.os.set(func() {
		tm.os.states[sweepPID] = fakeState{stat: "Z", ppid: testOwnPID}
		tm.os.reapOK = true
	})

	tm.sweepOK(t)

	if sent := tm.os.sent(); len(sent) != 0 {
		t.Fatalf("signals sent to a zombie: %v", sent)
	}
	if got := tm.os.reaped(); len(got) != 1 || got[0] != sweepPID {
		t.Fatalf("reaped = %v, want [%d]", got, sweepPID)
	}
	if !noneExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files not unlinked")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
	if !tm.logs.contains("peers: sweep: reaped zombie pid 4242 (identity same)") {
		t.Fatalf("no reap log line: %v", tm.logs.all())
	}
}

func TestSweepZombie_BecomesZombieAfterSIGTERM(t *testing.T) {
	tm := newTestManager(t)
	rec := zombieSweepSetup(t, tm)
	tm.os.set(func() { tm.os.reapOK = true })
	tm.os.onSignal = func(pid int, sig os.Signal) {
		if sig == syscall.SIGTERM {
			tm.os.set(func() { tm.os.states[pid] = fakeState{stat: "Z", ppid: testOwnPID} })
		}
	}

	tm.sweepOK(t)

	sent := tm.os.sent()
	if len(sent) != 1 || sent[0].sig != syscall.SIGTERM {
		t.Fatalf("signals = %v, want exactly one SIGTERM and no SIGKILL", sent)
	}
	if got := tm.os.reaped(); len(got) != 1 {
		t.Fatalf("reaped = %v, want one reap", got)
	}
	if !noneExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files not unlinked")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 0 {
		t.Fatalf("proxies.json = %+v, want empty", recs)
	}
}

// A zombie whose reap fails behaves exactly like today: TERM, KILL, record
// retained.
func TestSweepZombie_ReapFailureFallsBack(t *testing.T) {
	tm := newTestManager(t)
	rec := zombieSweepSetup(t, tm)
	tm.os.set(func() { tm.os.states[sweepPID] = fakeState{stat: "Z", ppid: testOwnPID} })

	tm.sweepOK(t)

	sent := tm.os.sent()
	if len(sent) != 2 || sent[0].sig != syscall.SIGTERM || sent[1].sig != syscall.SIGKILL {
		t.Fatalf("signals = %v, want TERM then KILL", sent)
	}
	if len(tm.os.reaped()) == 0 {
		t.Fatalf("reap was never attempted")
	}
	if !allExist(append([]string{rec.Sock}, rec.Files...)...) {
		t.Fatalf("files touched although the process is still alive")
	}
	if recs := readProxies(t, tm.proxiesPath); len(recs) != 1 {
		t.Fatalf("proxies.json = %+v, want retained", recs)
	}
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
}

// ProcDifferent and ProcUnknown never probe the state, let alone reap.
func TestSweepZombie_NotOursNeverProbed(t *testing.T) {
	cases := map[string]func(tm *testManager){
		"different": func(tm *testManager) { tm.os.ps[sweepPID] = "Mon Sep 14 09:30:00 2026" },
		"unknown":   func(tm *testManager) { tm.os.psErr[sweepPID] = os.ErrPermission },
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			tm := newTestManager(t)
			rec := zombieSweepSetup(t, tm)
			tm.os.set(func() {
				mod(tm)
				tm.os.states[sweepPID] = fakeState{stat: "Z", ppid: testOwnPID}
				tm.os.reapOK = true
			})

			tm.sweepOK(t)

			if n := tm.os.calls(sweepPID); n != 0 {
				t.Fatalf("procState called %d times", n)
			}
			if got := tm.os.reaped(); len(got) != 0 {
				t.Fatalf("reap called: %v", got)
			}
			t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
		})
	}
}

// Any one condition failing keeps reap away and leaves the TERM path.
func TestSweepZombie_ConditionsNotMetNoReap(t *testing.T) {
	zOwn := fakeState{stat: "Z", ppid: testOwnPID}
	cases := map[string][]fakeState{
		"not-zombie":        {{stat: "S", ppid: testOwnPID}},
		"other-parent":      {{stat: "Z", ppid: 1}},
		"state-error":       {{err: os.ErrNotExist}},
		"settled-not-z":     {zOwn, {stat: "S", ppid: testOwnPID}},
		"settled-ppid-diff": {zOwn, {stat: "Z", ppid: 1}},
		"settled-gone":      {zOwn, {err: os.ErrNotExist}},
	}
	for name, seq := range cases {
		t.Run(name, func(t *testing.T) {
			tm := newTestManager(t)
			rec := zombieSweepSetup(t, tm)
			tm.os.set(func() {
				tm.os.stateSeq[sweepPID] = seq
				tm.os.reapOK = true
			})

			tm.sweepOK(t)

			if got := tm.os.reaped(); len(got) != 0 {
				t.Fatalf("reap called: %v", got)
			}
			if sent := tm.os.sent(); len(sent) != 2 || sent[0].sig != syscall.SIGTERM {
				t.Fatalf("signals = %v, want the TERM/KILL path", sent)
			}
			t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
		})
	}
}

// codex R2: the identity is re-proved after the settle, right before wait4 — the pid may have been recycled
// into another child that also shows as Z under the same parent.
func TestSweepZombie_IdentityChangedDuringSettleNoReap(t *testing.T) {
	tm := newTestManager(t)
	rec := zombieSweepSetup(t, tm)
	tm.os.set(func() { tm.os.reapOK = true })
	calls := 0
	tm.m.procState = func(pid int) (string, int, error) {
		calls++
		if calls == 2 { // the settle has passed: a different process now owns the pid
			tm.os.set(func() { tm.os.ps[pid] = "Thu Jan  1 00:00:00 2099" })
		}
		return "Z", testOwnPID, nil
	}

	tm.sweepOK(t)

	if got := tm.os.reaped(); len(got) != 0 {
		t.Fatalf("reap called on a recycled pid: %v", got)
	}
	t.Cleanup(func() { ccuds.RemoveRegistry(rec.Files) })
}
