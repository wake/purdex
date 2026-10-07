package teammod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// touchLock writes the flag file the CLI (P2c) or the mod (P6) would write.
func touchLock(t *testing.T, f *fixture, agent, sid string) string {
	t.Helper()
	p := filepath.Join(f.m.dataDir, team.HookLocksDir, agent, sid)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func decideReq(agent, event, sid string) team.HookDecideRequest {
	return team.HookDecideRequest{Agent: agent, Event: event, SessionID: sid, ToolName: "Bash",
		ToolInput: json.RawMessage(`{"command":"ls"}`), ToolUseID: "toolu_1", Raw: json.RawMessage(`{"session_id":"` + sid + `"}`)}
}

func decodeDecide(t *testing.T, body []byte) team.HookDecideResponse {
	t.Helper()
	var d team.HookDecideResponse
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode HookDecideResponse: %v; body=%s", err, body)
	}
	return d
}

// Spec §6.6, §15 "flag + open lead request ⇒ PreToolUse deny with the
// reason, PermissionRequest {}": the lock answer comes from the session's
// open lead request, by origin_session_id; the request id is in the reason.
func TestHookDecide_OpenLeadRequestDeniesPreToolUseOnly(t *testing.T) {
	f := newFixture(t)
	ap := f.create(uid(1)) // origin sid-1
	code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusOK {
		t.Fatalf("PreToolUse: %d %s", code, body)
	}
	d := decodeDecide(t, body)
	want := team.HookDecideResponse{Decision: "deny", Reason: fmt.Sprintf(team.LeadLockReasonFmt, ap.ID), Lock: team.HookLockLeadRequest, ID: ap.ID}
	if d != want {
		t.Fatalf("PreToolUse = %+v, want %+v", d, want)
	}
	if d.Reason != "lead 申請等待核准中（"+ap.ID+"），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理" {
		t.Fatalf("reason = %q", d.Reason)
	}
	code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PermissionRequest", "sid-1"))
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("PermissionRequest: %d %q, want 200 {}", code, body)
	}
	// Another session on the same host is not locked by sid-1's request.
	code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-2"))
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("other session: %d %q, want 200 {}", code, body)
	}
	if n := f.countOps("closed"); n != 0 {
		t.Fatalf("a hook decision must not close anything; closed events = %d", n)
	}
}

// Once the request is closed (any state) the lock is gone and the answer
// is {}; the flag the CLI left behind (SIGKILL) is removed with that
// answer, so the next hook finds no flag and makes no call.
func TestHookDecide_ClosedRequestAnswersEmptyAndRemovesTheFlag(t *testing.T) {
	f := newFixture(t)
	ap := f.create(uid(1))
	flag := touchLock(t, f, "cc", "sid-1")
	code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusOK || decodeDecide(t, body).Decision != "deny" {
		t.Fatalf("while open: %d %s", code, body)
	}
	if !exists(flag) {
		t.Fatal("the flag must stay while the request is open")
	}
	f.do(http.MethodDelete, "/api/team/approvals/"+ap.ID, nil)
	code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("after close: %d %q, want 200 {}", code, body)
	}
	if exists(flag) {
		t.Fatal("a flag answered {} must be removed (stale flag costs one {} and then disappears)")
	}
	// No flag on disk is not an error either.
	if code, body = f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1")); code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("no flag: %d %q", code, body)
	}
}

func TestHookDecide_RejectsBadInputAndStopping(t *testing.T) {
	f := newFixture(t)
	for name, req := range map[string]team.HookDecideRequest{
		"agent":   decideReq("opencode", "PreToolUse", "sid-1"),
		"event":   decideReq("cc", " ", "sid-1"),
		"session": decideReq("cc", "PreToolUse", " "),
	} {
		code, body := f.do(http.MethodPost, "/api/hooks/decide", req)
		if code != http.StatusBadRequest || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s, want 400 bad_request", name, code, body)
		}
	}
	if code, body := f.do(http.MethodPost, "/api/hooks/decide", "{not json"); code != http.StatusBadRequest {
		t.Errorf("invalid JSON: %d %s", code, body)
	}
	// Any other known event is not a decision point for P2c: 200 {} even
	// while a lead request is open, and the flag stays (P8a-1d forwards
	// PostToolUse / Stop / UserPromptSubmit / SessionEnd here and must not
	// meet a 400).
	f.create(uid(1))
	flag := touchLock(t, f, "cc", "sid-1")
	for _, ev := range []string{"PostToolUse", "PostToolUseFailure", "Stop", "UserPromptSubmit", "SessionEnd", "Notification"} {
		if code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", ev, "sid-1")); code != http.StatusOK || string(body) != "{}\n" {
			t.Errorf("%s: %d %q, want 200 {}", ev, code, body)
		}
	}
	if !exists(flag) {
		t.Fatal("a non-decision event must not remove the lead lock flag")
	}
	// A session id that is not a single path element never touches the
	// disk: the answer is still {} and nothing outside locksDir is removed.
	outside := filepath.Join(f.m.dataDir, "keep.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "../keep.txt")); code != http.StatusOK || string(body) != "{}\n" {
		t.Fatalf("traversal id: %d %q", code, body)
	}
	if !exists(outside) {
		t.Fatal("a traversal session id must not delete files outside hooklocks")
	}
	_ = f.m.Stop(context.Background())
	code, body := f.do(http.MethodPost, "/api/hooks/decide", decideReq("cc", "PreToolUse", "sid-1"))
	if code != http.StatusServiceUnavailable || decodeErr(t, body).Error != team.ErrNotReady {
		t.Fatalf("stopping: %d %s, want 503 not_ready", code, body)
	}
}

// Spec §15 "stale flag ⇒ {} and the sweeper removes it": a flag whose
// session the registry no longer lists goes on the 10th tick (the liveness
// cadence), whether or not any request is open; a live session's flag and
// the codex directory are left alone.
func TestTick_PrunesStaleFlagsOnTheTenthTick(t *testing.T) {
	f := newFixture(t)
	dead := touchLock(t, f, "cc", "sid-dead")
	live := touchLock(t, f, "cc", "sid-1")
	codex := touchLock(t, f, "codex", "sid-dead")
	f.origins.markDead("sid-dead")
	for i := 1; i <= 9; i++ {
		f.m.tick()
		if !exists(dead) {
			t.Fatalf("tick %d: pruned before the 10th tick", i)
		}
	}
	f.m.tick()
	if exists(dead) {
		t.Fatal("10th tick: the dead session's flag must be removed")
	}
	if !exists(live) {
		t.Fatal("10th tick: the live session's flag must stay")
	}
	if !exists(codex) {
		t.Fatal("codex flags have no liveness oracle in P2c and must be left alone")
	}
}

// Start prunes too (the registry is read on boot; a flag left by a session
// that died while the daemon was down must not outlive the restart).
func TestStart_PrunesStaleFlags(t *testing.T) {
	f := newFixture(t)
	dead := touchLock(t, f, "cc", "sid-dead")
	live := touchLock(t, f, "cc", "sid-1")
	f.origins.markDead("sid-dead")
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if exists(dead) || !exists(live) {
		t.Fatalf("after Start: dead=%v live=%v, want false/true", exists(dead), exists(live))
	}
	if n := f.m.pruneHookLocks(); n != 0 {
		t.Fatalf("second prune removed %d, want 0", n)
	}
}
