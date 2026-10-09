package teammod

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"syscall"
	"testing"

	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/team"
)

// Release and the kill of an adopted member (adopt plan PL-1d2). sid-1 (/tmp/10.sock) leads, sid-2 (_def456, pid 20) is adopted.

func (f *fixture) release(target string) (int, team.Member, team.APIError, []byte) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/release", team.ReleaseRequest{OriginInbox: "/tmp/10.sock", Target: target})
	var m team.Member
	var e team.APIError
	if code == http.StatusOK {
		_ = json.Unmarshal(body, &m)
	} else {
		_ = json.Unmarshal(body, &e)
	}
	return code, m, e, body
}

func TestRelease_ReleasesAndOwesTheNotice(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.m.drainNotices() // the adopt notice goes first
	f.sender.mu.Lock()
	f.sender.sent = nil
	f.sender.mu.Unlock()
	code, m, _, body := f.release("_def456")
	if code != http.StatusOK || m.State != team.MemberReleased {
		t.Fatalf("release = %d %s", code, body)
	}
	row := memberBySpawn(t, f.m.store, key)
	if row.State != team.MemberReleased || row.EndedAt == 0 || row.NoticePending != team.NoticeReleased {
		t.Fatalf("row = %+v", row)
	}
	select {
	case <-f.m.noticeSig:
	default:
		t.Fatal("the release did not kick the notice drain")
	}
	f.m.drainNotices()
	calls := f.sender.calls()
	alias, _ := f.m.selfHost()
	if len(calls) != 1 || calls[0].Text != fmt.Sprintf(team.ReleaseNoticeFmt, alias+"/_abc123", uid(1)) || calls[0].To != alias+"/_def456" {
		t.Fatalf("sends = %+v, want the release notice", calls)
	}
}

// After a release the session is `none` again: its self relay follows the host switch, then its own pause (decision 14).
func TestRelease_HelloAndBeginMatrix(t *testing.T) {
	for _, c := range []struct {
		solo, paused     bool
		wantHello        string
		wantBeginCode    int
		wantBeginRefusal string
	}{
		{true, false, "on", http.StatusCreated, ""},
		{false, false, "off", http.StatusConflict, team.ErrSelfRelayOff},
		{true, true, "paused", http.StatusConflict, team.ErrSelfRelayPaused},
		{false, true, "off", http.StatusConflict, team.ErrSelfRelayOff},
	} {
		t.Run(fmt.Sprintf("solo=%v/paused=%v", c.solo, c.paused), func(t *testing.T) {
			f := newFixture(t)
			f.adoptedMember(t)
			if code, _, _, body := f.release("_def456"); code != http.StatusOK {
				t.Fatalf("release: %d %s", code, body)
			}
			f.switches.set(hostconfig.RelaySwitches{SelfSolo: true, SelfLead: true})
			if c.paused {
				if code, _, body := f.self("sid-2", "off"); code != http.StatusOK {
					t.Fatalf("pause: %d %s", code, body)
				}
			}
			f.switches.set(hostconfig.RelaySwitches{SelfSolo: c.solo, SelfLead: true})
			if code, h, body := f.hello("sid-2"); code != http.StatusOK || h.Role != "none" || h.SelfRelay != c.wantHello {
				t.Fatalf("hello = %d %s, want role none, self_relay %s", code, body, c.wantHello)
			}
			code, body := f.do(http.MethodPost, "/api/relay/begin", beginReq("sid-2"))
			if code != c.wantBeginCode || (c.wantBeginRefusal != "" && decodeErr(t, body).Error != c.wantBeginRefusal) {
				t.Fatalf("begin = %d %s, want %d %s", code, body, c.wantBeginCode, c.wantBeginRefusal)
			}
		})
	}
}

func TestRelease_RelayOpenBlocks(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	claimedOp(t, f.m.store, "relay-1", "sid-2", "_def456") // a lead's relay of the member: claimed
	code, _, e, body := f.release("_def456")
	if code != http.StatusConflict || e.Error != team.ErrRelayOpen || e.Op == nil {
		t.Fatalf("release = %d %s, want 409 relay_open with the op", code, body)
	}
	if row := memberBySpawn(t, f.m.store, key); row.State != team.MemberActive || row.NoticePending != team.NoticeAdopted {
		t.Fatalf("row = %+v, want untouched", row)
	}
}

func TestRelease_NotYourMemberAndNotLead(t *testing.T) {
	f := newFixture(t)
	if code, _, e, body := f.release("_def456"); code != http.StatusConflict || e.Error != team.ErrNotLead {
		t.Fatalf("not a lead = %d %s", code, body)
	}
	f.adoptedMember(t)
	if code, _, e, body := f.release("_zzzzzz"); code != http.StatusConflict || e.Error != team.ErrNotYourMember {
		t.Fatalf("no such member = %d %s", code, body)
	}
}

func TestRelease_AgainAnswersTheRowUnchanged(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.release("_def456")
	f.m.drainNotices()
	before := memberBySpawn(t, f.m.store, key)
	code, m, _, body := f.release("_def456")
	if code != http.StatusOK || m.State != team.MemberReleased {
		t.Fatalf("again = %d %s", code, body)
	}
	if after := memberBySpawn(t, f.m.store, key); after.EndedAt != before.EndedAt || after.NoticePending != before.NoticePending {
		t.Fatalf("row changed by the repeat: %+v -> %+v", before, after)
	}
}

// ---- the kill of an adopted member ----

type killRec struct {
	mu   sync.Mutex
	pids []int
	err  error
}

func (k *killRec) kill(pid int) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.pids = append(k.pids, pid)
	return k.err
}

func (k *killRec) got() []int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]int(nil), k.pids...)
}

func (f *fixture) killTarget(target string) (int, team.Member, team.APIError, []byte) {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/kill", team.KillRequest{OriginInbox: "/tmp/10.sock", Target: target})
	var m team.Member
	var e team.APIError
	if code == http.StatusOK {
		_ = json.Unmarshal(body, &m)
	} else {
		_ = json.Unmarshal(body, &e)
	}
	return code, m, e, body
}

// Mutation gate: kill the adopted member's tmux session (the spawn path) → the nil tmux panics / refuses → red.
func TestKillAdopted_SignalsTheReverifiedProcessNotTheTmuxSession(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	f.m.tmux = nil // any tmux call by the adopted path would dereference it
	code, m, _, body := f.killTarget("_def456")
	if code != http.StatusOK || m.State != team.MemberKilled {
		t.Fatalf("kill = %d %s", code, body)
	}
	if pids := rec.got(); len(pids) != 1 || pids[0] != 20 {
		t.Fatalf("signalled %v, want the one live pid 20", pids)
	}
	if row := memberBySpawn(t, f.m.store, key); row.State != team.MemberKilled || row.EndedAt == 0 {
		t.Fatalf("row = %+v", row)
	}
}

// Mutation gate: signal before SameProcess → red.
func TestKillAdopted_ReusedPidIsNeverSignalled(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	f.origins.mu.Lock()
	f.origins.otherProc = map[int]bool{20: true}
	f.origins.mu.Unlock()
	code, m, _, body := f.killTarget("_def456")
	if code != http.StatusOK || m.State != team.MemberGone {
		t.Fatalf("kill = %d %s, want 200 gone", code, body)
	}
	if pids := rec.got(); len(pids) != 0 {
		t.Fatalf("signalled %v a pid whose process is not the registered one", pids)
	}
	if row := memberBySpawn(t, f.m.store, key); row.State != team.MemberGone {
		t.Fatalf("row = %+v", row)
	}
}

func TestKillAdopted_ESRCHMarksGone(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	rec := &killRec{err: syscall.ESRCH}
	f.m.killProcess = rec.kill
	code, m, _, body := f.killTarget("_def456")
	if code != http.StatusOK || m.State != team.MemberGone || len(rec.got()) != 1 {
		t.Fatalf("kill = %d %s signals=%v, want 200 gone after one signal", code, body, rec.got())
	}
}

// Mutation gate: mark killed on EPERM → red.
func TestKillAdopted_EPERMMarksNothing(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.m.killProcess = (&killRec{err: syscall.EPERM}).kill
	code, _, e, body := f.killTarget("_def456")
	if code != http.StatusInternalServerError || e.Error != team.ErrKillFailed {
		t.Fatalf("kill = %d %s, want 500 kill_failed", code, body)
	}
	if row := memberBySpawn(t, f.m.store, key); row.State != team.MemberActive {
		t.Fatalf("row = %+v, want still active", row)
	}
}

func TestKillAdopted_NotLiveMarksGoneWithoutSignal(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	f.origins.hide("sid-2")
	code, m, _, body := f.killTarget("_def456")
	if code != http.StatusOK || m.State != team.MemberGone || len(rec.got()) != 0 {
		t.Fatalf("kill = %d %s signals=%v, want 200 gone with no signal", code, body, rec.got())
	}
}

func TestKillAdopted_VerificationFailureIs503AndMarksNothing(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	f.origins.mu.Lock()
	f.origins.procErr = errors.New("start time cannot be read")
	f.origins.mu.Unlock()
	code, _, _, body := f.killTarget("_def456")
	if code != http.StatusServiceUnavailable || len(rec.got()) != 0 {
		t.Fatalf("kill = %d %s signals=%v, want 503 and no signal", code, body, rec.got())
	}
	if row := memberBySpawn(t, f.m.store, key); row.State != team.MemberActive {
		t.Fatalf("row = %+v", row)
	}
}

// pid 0 / 1 are never signalled, whatever the registry says.
func TestKillAdopted_RefusesAnImpossiblePid(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	rec := &killRec{}
	f.m.killProcess = rec.kill
	f.origins.show(team.Origin{SessionID: "sid-2", Ref: "_def456", PID: 1, ProcStart: "Sun Sep 13 15:22:36 2026"})
	if code, _, e, body := f.killTarget("_def456"); code != http.StatusInternalServerError || e.Error != team.ErrKillFailed || len(rec.got()) != 0 {
		t.Fatalf("kill = %d %s signals=%v", code, body, rec.got())
	}
}

// A released member is refused before any signal; a spawned member keeps today's path (its tmux session).
func TestKillAdopted_ReleasedIsRefusedWithoutASignal(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	f.release("_def456")
	rec := &killRec{}
	f.m.killProcess = rec.kill
	if code, _, e, body := f.killTarget("_def456"); code != http.StatusConflict || e.Error != team.ErrNotYourMember || len(rec.got()) != 0 {
		t.Fatalf("kill = %d %s signals=%v", code, body, rec.got())
	}
}
