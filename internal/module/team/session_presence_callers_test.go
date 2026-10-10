package teammod

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// #1857: the callers of LiveSession whose answer decides something the user sees or cannot undo take the three-state
// SessionPresence: gone acts; unknown (a registry that cannot be read, or a live pid's file that cannot be verified, which might
// be this very session) leaves it for the next round. Each caller: one test that unknown does nothing, one that gone still acts.
// Mutation gate for all: treat unknown as gone → the unknown test of that caller is red.

func (f *fixture) markUnknown(sid string) {
	f.origins.mu.Lock()
	defer f.origins.mu.Unlock()
	if f.origins.unknown == nil {
		f.origins.unknown = map[string]bool{}
	}
	f.origins.unknown[sid] = true
}

func (f *fixture) captureLogs() *logSink {
	s := &logSink{}
	f.m.logf = func(format string, args ...any) { s.add(fmt.Sprintf(format, args...)) }
	return s
}

type logSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *logSink) add(l string) { s.mu.Lock(); s.lines = append(s.lines, l); s.mu.Unlock() }
func (s *logSink) matching(sub string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, l := range s.lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// ---- 1: the sweeper abandons an open request whose origin is not there ----

func TestPresence_SweeperLeavesAnOpenRequestWhoseSessionIsUnknown(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // origin sid-1
	f.markUnknown("sid-1")
	logs := f.captureLogs()
	f.livenessTick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("an unknown session's request = %s, want open", a.State)
	}
	if n := logs.matching("presence unknown"); n != 1 {
		t.Fatalf("%d log lines about it, want 1", n)
	}
	// the next liveness ticks, inside 10 minutes: no second line for the same request
	f.livenessTick()
	f.livenessTick()
	if n := logs.matching("presence unknown"); n != 1 {
		t.Fatalf("%d log lines after more ticks, want still 1", n)
	}
}

// The line is rate-limited per subject: once per 10 minutes. Mutation gate: no limit → the second call logs (red).
func TestPresence_TheSkipIsLoggedOncePerSubjectPer10Minutes(t *testing.T) {
	f := newFixture(t)
	logs := f.captureLogs()
	f.m.logPresenceUnknown("approval a1", "sid-1")
	f.m.logPresenceUnknown("approval a1", "sid-1")
	f.m.logPresenceUnknown("approval a2", "sid-1") // another subject
	if n := logs.matching("presence unknown"); n != 2 {
		t.Fatalf("%d lines, want 2 (one per subject)", n)
	}
	f.clock.Add(10*minute - 1_000)
	f.m.logPresenceUnknown("approval a1", "sid-1")
	if n := logs.matching("presence unknown"); n != 2 {
		t.Fatalf("%d lines just inside 10 minutes, want 2", n)
	}
	f.clock.Add(1_000)
	f.m.logPresenceUnknown("approval a1", "sid-1")
	if n := logs.matching("presence unknown"); n != 3 {
		t.Fatalf("%d lines at 10 minutes, want 3", n)
	}
}

func TestPresence_SweeperStillAbandonsWhenTheSessionIsGone(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.origins.markDead("sid-1")
	f.livenessTick()
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateAbandoned {
		t.Fatalf("a gone session's request = %s, want abandoned", a.State)
	}
}

// ---- 2: boot / tick reconciliation of an awaiting self op ----

func TestPresence_ReconcileLeavesAnAwaitingSelfOpWhoseSessionIsUnknown(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.markUnknown("sid-1")
	f.m.reconcileRelays()
	if f.op(out.Op.ID).State != team.RelayAwaitingApproval || f.rowState(out.RequestID) != team.StateOpen {
		t.Fatalf("op %s, row %s: an unknown session's request was abandoned", f.op(out.Op.ID).State, f.rowState(out.RequestID))
	}
}

func TestPresence_ReconcileStillAbandonsAnAwaitingSelfOpWhoseSessionIsGone(t *testing.T) {
	f := newFixture(t)
	out := f.begin("sid-1")
	f.origins.markDead("sid-1")
	f.m.reconcileRelays()
	if f.rowState(out.RequestID) != team.StateAbandoned {
		t.Fatalf("row = %s, want abandoned", f.rowState(out.RequestID))
	}
}

// ---- 3: a member op past claimed with no frame ----

func TestPresence_NoFrameAndSessionUnknownDoesNotFailMemberGone(t *testing.T) {
	f := newFixture(t)
	op := f.claimedMemberOp()
	f.setFrames()
	f.markUnknown("sid-m1")
	got, err := f.m.reconcileFromFrames(context.Background(), op)
	if err != nil || got.State != team.RelayClaimed {
		t.Fatalf("an unknown session was judged gone: %+v err=%v", got, err)
	}
}

// ---- 4: a stalled cleared op: done (new session there) or failed (not); unknown waits, but only up to twice the stall ----

func (f *fixture) stalledClearedOp() team.RelayOp {
	f.t.Helper()
	op := f.claimedMemberOp()
	f.origins.cleared = map[string]int{"sid-new": 42}
	for _, s := range []team.RelayReportRequest{{State: team.RelayWriting}, {State: team.RelayWritten}, {State: team.RelayCleared, NewSessionID: "sid-new"}} {
		f.report(op.ID, s)
	}
	f.setFrames()
	return op
}

func TestPresence_AStalledClearedOpWhoseNewSessionIsUnknownWaitsUpToTwiceTheStall(t *testing.T) {
	f := newFixture(t)
	op := f.stalledClearedOp()
	f.markUnknown("sid-new")
	logs := f.captureLogs()
	f.clock.Add(15 * minute) // the stall: today it would be judged now
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayCleared {
		t.Fatalf("unknown at the stall: %+v, want it left cleared", got)
	}
	if logs.matching("presence unknown") == 0 {
		t.Fatal("nothing logged about the skipped judgement")
	}
	f.clock.Add(14 * minute) // 29 min after it entered cleared
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayCleared {
		t.Fatalf("unknown at 29 min: %+v", got)
	}
	f.clock.Add(1 * minute) // 30 min: the bound — judged as today
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnresponsive {
		t.Fatalf("unknown past twice the stall: %+v, want failed as for a gone session", got)
	}
}

func TestPresence_AStalledClearedOpWhoseNewSessionIsGoneIsStillFailedAtTheStall(t *testing.T) {
	f := newFixture(t)
	op := f.stalledClearedOp()
	f.origins.markDead("sid-new")
	f.clock.Add(15 * minute)
	f.sweepTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed {
		t.Fatalf("gone at the stall: %+v", got)
	}
}

// ---- codex attack: the kill paths, the rows with no timer, the log map ----

// hideUnknown: the registry does not list sid and a live pid's file could not be verified — it may be sid.
func (f *fixture) hideUnknown(sid string) {
	f.origins.hide(sid)
	f.markUnknown(sid)
}

// An adopted member's kill marks it gone when its session is not there: irreversible, and the process is never signalled. A
// session that merely cannot be placed (a live pid's unreadable file) is a retryable error, the row untouched.
func TestPresence_KillOfAnAdoptedMemberWhoseSessionIsUnknownIsRetryableAndMarksNothing(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	f.hideUnknown("sid-2")
	code, _, e, body := f.killTarget("_def456")
	if code != http.StatusServiceUnavailable || e.Error != team.ErrNotReady {
		t.Fatalf("kill = %d %s, want 503 not_ready", code, body)
	}
	if row := memberBySpawn(t, f.m.store, key); row.State != team.MemberActive || len(rec.got()) != 0 {
		t.Fatalf("row = %s, signals %v: an unknown session was marked gone", row.State, rec.got())
	}
}

func TestPresence_KillOfAnAdoptedMemberWhoseSessionIsGoneIsMarkedGone(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	f.origins.hide("sid-2")
	f.origins.markDead("sid-2")
	if code, m, _, body := f.killTarget("_def456"); code != http.StatusOK || m.State != team.MemberGone {
		t.Fatalf("kill = %d %s, want 200 gone", code, body)
	}
}

func TestPresence_RemoteKillOfASessionThatIsUnknownIsRetryableAndMarksNothing(t *testing.T) {
	f, rec := killFixture(t, true)
	f.hideUnknown("sid-2")
	code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1"))
	if code != http.StatusServiceUnavailable {
		t.Fatalf("kill = %d %s, want a retryable 503", code, body)
	}
	if rowState(t, f, "mk-1") != remoteActive || len(rec.got()) != 0 {
		t.Fatalf("row = %s, signals %v", rowState(t, f, "mk-1"), rec.got())
	}
}

func TestPresence_RemoteKillOfASessionThatIsGoneIsGone(t *testing.T) {
	f, _ := killFixture(t, true)
	f.origins.hide("sid-2")
	f.origins.markDead("sid-2")
	if code, body := f.postCmd(leadPrincipal(), killCmd(cmdUUID1, "mk-1")); code != http.StatusOK || outcomeState(t, body) != "gone" {
		t.Fatalf("kill = %d %s", code, body)
	}
}

// A terminal_only hook row has no lease and no deadline: the registry is its only clock here. Unknown holds it for as long as it
// stays unknown — abandoning an open row lifts the lock that makes the hook ask (the next hook decide removes the flag), which
// for a session that may be alive is worse than a read-only card on a phone; the settings hook's own backstop (the tool use's
// end, Stop) is what normally closes it. Mutation gate: abandon after any time → red.
func TestPresence_ATerminalOnlyRowWithAnUnknownSessionStaysOpen(t *testing.T) {
	f := newFixture(t)
	ro := f.openTerminalOnly("toolu_ro")
	f.markUnknown("sid-1")
	for range 3 {
		f.clock.Add(60 * minute)
		f.livenessTick()
	}
	if a, _, _ := f.m.store.Get(ro.ID); a.State != team.StateOpen {
		t.Fatalf("after 3 hours: %s, want still open", a.State)
	}
}

func TestPresence_ATerminalOnlyRowWithAGoneSessionIsAbandoned(t *testing.T) {
	f := newFixture(t)
	ro := f.openTerminalOnly("toolu_ro")
	f.origins.markDead("sid-1")
	f.livenessTick()
	if a, _, _ := f.m.store.Get(ro.ID); a.State != team.StateAbandoned {
		t.Fatalf("a gone session's terminal_only row = %s, want abandoned", a.State)
	}
}

// The rate-limit map does not grow with every subject ever skipped: entries past the window are dropped.
func TestPresence_TheLogMapIsPruned(t *testing.T) {
	f := newFixture(t)
	f.captureLogs()
	for i := range 600 {
		f.m.logPresenceUnknown(fmt.Sprintf("approval %d", i), "sid-1")
	}
	f.clock.Add(10 * minute)
	f.m.logPresenceUnknown("approval fresh", "sid-1")
	f.m.presenceLogMu.Lock()
	n := len(f.m.presenceLogged)
	f.m.presenceLogMu.Unlock()
	if n > 5 {
		t.Fatalf("%d subjects remembered after the window, want only the fresh one", n)
	}
}

// codex re-review: the hook flag is removed only for a session that is positively gone. (The flag is rewritten by the next
// create, so this is not irreversible, but it is user-visible: the hook stops asking.)
func TestPresence_AHookFlagOfAnUnknownSessionStaysAndOfAGoneOneIsPruned(t *testing.T) {
	f := newFixture(t)
	unknown := touchLock(t, f, "cc", "sid-unknown")
	gone := touchLock(t, f, "cc", "sid-gone")
	f.hideUnknown("sid-unknown")
	f.origins.hide("sid-gone")
	f.origins.markDead("sid-gone")
	f.m.pruneHookLocks()
	if !exists(unknown) {
		t.Fatal("the flag of a session that cannot be placed was removed")
	}
	if exists(gone) {
		t.Fatal("the flag of a gone session stayed")
	}
}

// The rate-limit map never holds more than presenceLogMax subjects, even when none has expired.
func TestPresence_TheLogMapNeverExceedsItsCapWithinTheWindow(t *testing.T) {
	f := newFixture(t)
	f.captureLogs()
	for i := range 600 {
		f.m.logPresenceUnknown(fmt.Sprintf("approval %d", i), "sid-1")
	}
	f.m.presenceLogMu.Lock()
	n := len(f.m.presenceLogged)
	f.m.presenceLogMu.Unlock()
	if n > presenceLogMax {
		t.Fatalf("%d subjects remembered at one instant, want at most %d", n, presenceLogMax)
	}
}
