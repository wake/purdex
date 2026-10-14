package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/team"
)

// ---- RT-3c T38: pdx kill --handoff ----

// handoffMember is an adopted member (sid-2, _def456; killed by a signal to its process, which the tests count) whose mod says hello
// at protocol ver; it returns the member's spawn key.
func (f *fixture) handoffMember(ver string) string {
	f.t.Helper()
	key := f.adoptedMember(f.t)
	f.m.drainNotices()
	if ver != "" {
		if code, body := f.do(http.MethodPost, "/api/relay/hello", team.RelayHelloRequest{SessionID: "sid-2", ModVersion: ver, Agent: "cc"}); code != http.StatusOK {
			f.t.Fatalf("hello: %d %s", code, body)
		}
	}
	return key
}

func (f *fixture) killHandoff(target string) (int, team.RelayOp, team.APIError) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: "/tmp/10.sock", Target: target, Handoff: true})
	var resp team.RelayCreateResponse
	var ae team.APIError
	if code == http.StatusCreated || code == http.StatusOK {
		if err := json.Unmarshal(body, &resp); err != nil {
			f.t.Fatalf("decode: %v; %s", err, body)
		}
	} else {
		ae = decodeErr(f.t, body)
	}
	return code, resp.Op, ae
}

func (f *fixture) countKills() *atomic.Int64 {
	var n atomic.Int64
	f.m.killProcess = func(int) error { n.Add(1); return nil }
	return &n
}

func (f *fixture) handoffNotices(prefix string) []string {
	var out []string
	for _, c := range f.sender.calls() {
		if strings.HasPrefix(c.Text, prefix) {
			out = append(out, c.Text)
		}
	}
	return out
}

// walk takes the handoff op to `writing` the way the member's mod does: claim, then writing.
func (f *fixture) walkHandoff(op team.RelayOp) team.RelayClaimResponse {
	f.t.Helper()
	code, got, ae := f.claim(op.ID, "sid-2")
	if code != http.StatusOK {
		f.t.Fatalf("claim: %d %+v", code, ae)
	}
	if c, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWriting}); c != http.StatusOK {
		f.t.Fatalf("writing: %d %+v", c, ae)
	}
	return got
}

func (f *fixture) writeHandoffFile(op team.RelayOp) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(op.HandoffPath), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(op.HandoffPath, []byte("# HANDOFF\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// The lead's `pdx kill --handoff` opens a member op of mode handoff, requested, and tells the member's mod by the control message; the
// member stays active and nothing is signalled. Mutation gates: no mode → red; the kill done at once → red; no control → red.
func TestHandoffKill_OpensAHandoffOpAndSendsTheControl(t *testing.T) {
	f := newFixture(t)
	f.handoffMember("8")
	kills := f.countKills()
	code, op, ae := f.killHandoff("_def456")
	if code != http.StatusCreated {
		t.Fatalf("kill --handoff = %d %+v, want 201", code, ae)
	}
	if op.Kind != team.RelayKindMember || op.Mode != team.RelayModeHandoff || op.State != team.RelayRequested || op.SessionID != "sid-2" {
		t.Fatalf("op = %+v, want a requested member op of mode handoff for sid-2", op)
	}
	waitFor(t, func() bool { return len(f.handoffNotices(team.RelayControlPrefix)) == 1 })
	if got := f.handoffNotices(team.RelayControlPrefix)[0]; got != team.RelayControlPrefix+op.ID {
		t.Fatalf("control = %q", got)
	}
	if kills.Load() != 0 {
		t.Fatal("the member was signalled before it wrote anything")
	}
}

// Refusals: a mod older than 8 and a session that is no member of the caller's team; a second request while the first is open.
// Mutation gates: the version check → red.
func TestHandoffKill_Refusals(t *testing.T) {
	f := newFixture(t)
	f.handoffMember("7")
	if code, _, ae := f.killHandoff("_def456"); code != http.StatusConflict || ae.Error != team.ErrRelayUnsupported {
		t.Fatalf("mod 7: %d %+v, want 409 relay_unsupported", code, ae)
	}
	if code, _, ae := f.killHandoff("_nobody"); code != http.StatusConflict || ae.Error != team.ErrNotYourMember {
		t.Fatalf("nobody: %d %+v, want 409 not_your_member", code, ae)
	}
	f2 := newFixture(t)
	f2.handoffMember("8")
	if code, _, _ := f2.killHandoff("_def456"); code != http.StatusCreated {
		t.Fatalf("first: %d", code)
	}
	if code, _, ae := f2.killHandoff("_def456"); code != http.StatusConflict || ae.Error != team.ErrRelayOpen {
		t.Fatalf("second while open: %d %+v, want 409 relay_open", code, ae)
	}
}

// A remote member is refused: its host runs its mod.
func TestHandoffKill_RemoteMemberIsRefused(t *testing.T) {
	f := newFixture(t)
	f.memberTeam("8")
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET host_id = 'host-elsewhere' WHERE session_id = 'sid-m1'`); err != nil {
		t.Fatal(err)
	}
	code, body := f.do(http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: "/tmp/10.sock", Target: "_mem001", Handoff: true})
	if code != http.StatusConflict || !strings.Contains(string(body), team.ErrRelayUnsupported) {
		t.Fatalf("remote member: %d %s, want 409 relay_unsupported", code, body)
	}
}

// `written` claims the member for the kill in the SAME transaction as written -> cancelled/half: the store call alone leaves the row
// killing, with no route and no goroutine to help. Mutation gates: claim after the commit (not in it) → active (red); no claim → red.
func TestHandoffKill_WrittenClaimsTheKillInTheSameTransaction(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	got, res, err := f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayWritten, At: f.m.now()})
	if err != nil || res != ReportApplied {
		t.Fatalf("written: %v %v", res, err)
	}
	if got.State != team.RelayCancelled || got.Reason != team.RelayReasonHalf {
		t.Fatalf("op = %+v, want cancelled / half", got)
	}
	if st := f.killRowState(key); st != team.MemberKilling {
		t.Fatalf("member = %s straight after the commit, want killing (claimed in the same transaction)", st)
	}
}

// Through the route: written kills the member (once), marks it killed, and the lead hears exactly one notice - the handoff one, no
// relay outcome notice. Mutation gates: no kill → red; a second notice → red; the cancel notice → red.
func TestHandoffKill_WrittenKillsTheMemberAndTellsTheLeadOnce(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	if c, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); c != http.StatusOK {
		t.Fatalf("written: %d %+v", c, ae)
	}
	waitFor(t, func() bool { return f.killRowState(key) == team.MemberKilled })
	waitFor(t, func() bool { return len(f.handoffNotices("[pdx team] member ")) >= 1 })
	if kills.Load() != 1 {
		t.Fatalf("signals = %d, want 1", kills.Load())
	}
	notices := f.handoffNotices("[pdx team] ")
	var handoff, other []string
	for _, n := range notices {
		if strings.Contains(n, "已交接") {
			handoff = append(handoff, n)
		} else if !strings.HasPrefix(n, team.RelayControlPrefix) && !strings.Contains(n, "你已成為") {
			other = append(other, n)
		}
	}
	if len(handoff) != 1 || !strings.Contains(handoff[0], op.HandoffPath) || !strings.Contains(handoff[0], "已關閉") {
		t.Fatalf("handoff notices = %v, want one naming %s", handoff, op.HandoffPath)
	}
	if len(other) != 0 {
		t.Fatalf("other notices to the lead: %v", other)
	}
}

// A kill that cannot be sent gives the claim back and the lead is told the file is written but the member not closed.
func TestHandoffKill_AFailedSignalGivesTheClaimBackAndTellsTheLead(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	f.m.killProcess = func(int) error { return os.ErrPermission }
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten})
	waitFor(t, func() bool {
		return len(f.handoffNotices("[pdx team] member ")) >= 1 && f.killRowState(key) == team.MemberActive
	})
	var told bool
	for _, n := range f.handoffNotices("[pdx team] member ") {
		told = told || strings.Contains(n, "關閉沒完成")
	}
	if !told {
		t.Fatalf("notices = %v, want the not-closed one", f.handoffNotices("[pdx team] member "))
	}
}

// A crash between the commit and the kill (the route's after-work never ran): the row stays killing; the sweeper, which never signals on
// the lead's behalf, gives it back once the process is seen alive - and the lead is told the file is written but the member was not closed.
// A second `pdx kill --handoff` then reuses that file (no fresh write): the op carries the same path and the claim says reuse.
// Mutation gates: no notice at the give-back → red; a new path on the second op → red; no reuse flag → red.
func TestHandoffKill_ACrashAfterTheCommitIsSettledToActiveAndTheLeadIsTold(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	if _, res, err := f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayWritten, At: f.m.now()}); err != nil || res != ReportApplied {
		t.Fatalf("written: %v %v", res, err)
	}
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000
	f.clock.Add(killClaimStuckAfter.Milliseconds() + 1)
	f.m.settleStuckKillingMembers()
	if st := f.killRowState(key); st != team.MemberActive {
		t.Fatalf("member = %s after the sweep, want active again", st)
	}
	if kills.Load() != 0 {
		t.Fatal("the sweeper signalled on the lead's behalf")
	}
	waitFor(t, func() bool { return len(f.handoffNotices("[pdx team] member ")) >= 1 })
	notice := f.handoffNotices("[pdx team] member ")[0]
	if !strings.Contains(notice, "交接檔已寫好") || !strings.Contains(notice, op.HandoffPath) || !strings.Contains(notice, "pdx kill") {
		t.Fatalf("notice = %q, want the not-closed one naming %s", notice, op.HandoffPath)
	}
	// the lead tries again: the same file, no new write
	code, op2, ae := f.killHandoff("_def456")
	if code != http.StatusCreated {
		t.Fatalf("second kill --handoff = %d %+v", code, ae)
	}
	if op2.HandoffPath != op.HandoffPath || op2.ID == op.ID {
		t.Fatalf("second op = %+v, want a new op on the first one's file %s", op2, op.HandoffPath)
	}
	c, claim, _ := f.claim(op2.ID, "sid-2")
	if c != http.StatusOK || !claim.Reuse {
		t.Fatalf("claim = %d %+v, want reuse", c, claim)
	}
}

// A first kill --handoff has nothing to reuse: the claim does not say reuse. A stale fresh row (the file gone) is not reused either.
func TestHandoffKill_NoFreshFileNoReuse(t *testing.T) {
	f := newFixture(t)
	f.handoffMember("8")
	_, op, _ := f.killHandoff("_def456")
	if c, claim, _ := f.claim(op.ID, "sid-2"); c != http.StatusOK || claim.Reuse {
		t.Fatalf("claim = %d %+v, want no reuse", c, claim)
	}
}

// An idle half the member wrote earlier is reused by the handoff: the op's file is the half's.
func TestHandoffKill_AFreshIdleHalfIsReused(t *testing.T) {
	f := newFixture(t)
	f.handoffMember("8")
	half := f.halfOnDisk("half-of-m2", "sid-2", f.m.now())
	_, op, _ := f.killHandoff("_def456")
	if op.HandoffPath != half.HandoffPath {
		t.Fatalf("handoff path = %s, want the half's %s", op.HandoffPath, half.HandoffPath)
	}
	if c, claim, _ := f.claim(op.ID, "sid-2"); c != http.StatusOK || !claim.Reuse {
		t.Fatalf("claim = %d %+v, want reuse", c, claim)
	}
}

// A member that never claims is not killed: the op fails as for a relay and the member stays active.
// Mutation gate: kill on failure → red.
func TestHandoffKill_AnUnresponsiveMemberIsNotKilled(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.m.bootAt = f.m.now() - (team.BootGraceS+1)*1000
	f.clock.Add(int64(team.RelayClaimTimeoutS)*1000 + 1)
	f.m.sweepRelayTimeouts()
	if got := f.op(op.ID); got.State != team.RelayFailed || got.Reason != team.RelayReasonMemberUnseen {
		t.Fatalf("op = %+v, want failed member_unseen", got)
	}
	if st := f.killRowState(key); st != team.MemberActive || kills.Load() != 0 {
		t.Fatalf("member = %s, signals = %d, want active and none", st, kills.Load())
	}
}

// A row that is killing under somebody else's claim (an ordinary `pdx kill` in flight) is not this handoff's to finish: no second signal.
// Mutation gate: finish whichever killing row the session has → a signal (red).
func TestHandoffKill_AnotherKillsClaimIsNotFinishedTwice(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, f.m.now()-5, key); err != nil { // an ordinary kill claimed it first
		t.Fatal(err)
	}
	if c, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); c != http.StatusOK {
		t.Fatalf("written: %d %+v", c, ae)
	}
	time.Sleep(200 * time.Millisecond)
	if kills.Load() != 0 {
		t.Fatalf("signals = %d: a kill claimed by another path was finished by the handoff", kills.Load())
	}
	if st := f.killRowState(key); st != team.MemberKilling {
		t.Fatalf("member = %s, want killing (the other kill's)", st)
	}
}

// The boot gives a claim back too (the daemon died between the commit and the signal, the session lives): the lead is told.
// Mutation gate: no notice at the boot's give-back → red.
func TestHandoffKill_TheBootGivesTheClaimBackAndTellsTheLead(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	if _, res, err := f.m.store.ReportRelay(op.ID, RelayReport{State: team.RelayWritten, At: f.m.now()}); err != nil || res != ReportApplied {
		t.Fatalf("written: %v %v", res, err)
	}
	f.forgetSent()
	f.m.recoverKillingMembers()
	if st := f.killRowState(key); st != team.MemberActive {
		t.Fatalf("member = %s after the boot, want active", st)
	}
	waitFor(t, func() bool { return len(f.handoffNotices("[pdx team] member ")) >= 1 })
	if n := f.handoffNotices("[pdx team] member ")[0]; !strings.Contains(n, "關閉沒完成") {
		t.Fatalf("notice = %q", n)
	}
}

// A claim that misses leaves an active member and the lead is told: the member is another host's row by the time `written` lands (the
// check at the request is made again in the transaction), so nothing claims it and nothing signals. Mutation gates: the host condition
// dropped → the kill happens (red); no notice for the active member → red.
func TestHandoffKill_AMissedClaimTellsTheLeadAndKillsNothing(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET host_id = 'host-elsewhere' WHERE spawn_op = ?`, key); err != nil {
		t.Fatal(err)
	}
	f.forgetSent()
	if c, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); c != http.StatusOK {
		t.Fatalf("written: %d %+v", c, ae)
	}
	waitFor(t, func() bool { return len(f.handoffNotices("[pdx team] member ")) >= 1 })
	if kills.Load() != 0 || f.killRowState(key) == team.MemberKilling {
		t.Fatalf("signals = %d, member = %s: a row of another host was claimed", kills.Load(), f.killRowState(key))
	}
	if n := f.handoffNotices("[pdx team] member ")[0]; !strings.Contains(n, "關閉沒完成") {
		t.Fatalf("notice = %q, want the not-closed one", n)
	}
}

// The ordinary kill's claim carries exactly the handoff op's millisecond: still not the handoff's (the claim is proved by the commit that
// made it, not by a timestamp). Mutation gate: identify the claim by updated_at → a signal (red).
func TestHandoffKill_AnOrdinaryClaimOfTheSameMillisecondIsNotTheHandoffs(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	at := f.m.now()
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ? WHERE spawn_op = ?`, at, key); err != nil {
		t.Fatal(err)
	}
	f.clock.Store(at) // the handoff's written lands in the same millisecond
	if c, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); c != http.StatusOK {
		t.Fatalf("written: %d %+v", c, ae)
	}
	time.Sleep(200 * time.Millisecond)
	if kills.Load() != 0 {
		t.Fatalf("signals = %d: the handoff finished a kill another path had claimed", kills.Load())
	}
}

// A re-sent `written` (the mod's retry, a lost answer) repeats nothing: one signal, one notice - after a good kill, after a missed claim
// and after a failed signal. Mutation gate: finish on a Noop too → the missed-claim notice repeats (red).
func TestHandoffKill_AResentWrittenRepeatsNothing(t *testing.T) {
	f := newFixture(t)
	key := f.handoffMember("8")
	kills := f.countKills()
	_, op, _ := f.killHandoff("_def456")
	f.walkHandoff(op)
	f.writeHandoffFile(op)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET host_id = 'host-elsewhere' WHERE spawn_op = ?`, key); err != nil { // the claim will miss
		t.Fatal(err)
	}
	f.forgetSent()
	for i := 0; i < 3; i++ {
		if c, _, ae := f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten}); c != http.StatusOK {
			t.Fatalf("written #%d: %d %+v", i, c, ae)
		}
	}
	waitFor(t, func() bool { return len(f.handoffNotices("[pdx team] member ")) >= 1 })
	time.Sleep(300 * time.Millisecond)
	if n := len(f.handoffNotices("[pdx team] member ")); n != 1 || kills.Load() != 0 {
		t.Fatalf("notices = %d, signals = %d after three reports, want 1 and 0", n, kills.Load())
	}
}
