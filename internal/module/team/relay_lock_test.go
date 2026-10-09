package teammod

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/team"
)

// claimedRelay: sid-1 has an approved self relay (op claimed) and the flag file the mod would raise.
func claimedRelay(t *testing.T) (*fixture, team.RelayOp, string) {
	t.Helper()
	f := newFixture(t)
	out := f.begin("sid-1")
	f.decide(out.RequestID, "approve")
	op := f.op(out.Op.ID)
	if op.State != team.RelayClaimed {
		t.Fatalf("op = %+v", op)
	}
	return f, op, touchLock(t, f, "cc", "sid-1")
}

func decideTool(f *fixture, event, tool, input string) team.HookDecideResponse {
	f.t.Helper()
	req := decideReq("cc", event, "sid-1")
	req.ToolName, req.ToolInput = tool, json.RawMessage(input)
	code, body := f.do(http.MethodPost, "/api/hooks/decide", req)
	if code != http.StatusOK {
		f.t.Fatalf("decide: %d %s", code, body)
	}
	return decodeDecide(f.t, body)
}

func writeInput(path string) string {
	b, _ := json.Marshal(map[string]string{"file_path": path})
	return string(b)
}

// Spec §6.6 row 2: only the handoff Write is allowed. Mutation gate: allow Edit too → the deny test red (below).
func TestHookDecide_RelayLockAllowsExactlyTheHandoffWrite(t *testing.T) {
	f, op, flag := claimedRelay(t)
	want := team.HookDecideResponse{Decision: "allow", Reason: team.RelayLockAllowReason, Lock: team.HookLockRelay, ID: op.ID}
	if d := decideTool(f, "PreToolUse", "Write", writeInput(op.HandoffPath)); d != want {
		t.Fatalf("handoff write = %+v", d)
	}
	// both sides cleaned: a path that cleans to the handoff matches
	dirty := filepath.Dir(op.HandoffPath) + "/../" + filepath.Base(filepath.Dir(op.HandoffPath)) + "/" + filepath.Base(op.HandoffPath)
	if d := decideTool(f, "PreToolUse", "Write", writeInput(dirty)); d != want {
		t.Fatalf("cleaned path = %+v", d)
	}
	if !exists(flag) {
		t.Fatal("the flag must stay while the relay holds the lock")
	}
}

func TestHookDecide_RelayLockDeniesEditReadBashAndOtherPaths(t *testing.T) {
	f, op, _ := claimedRelay(t)
	deny := team.HookDecideResponse{Decision: "deny", Reason: team.RelayLockDenyReason, Lock: team.HookLockRelay, ID: op.ID}
	for name, c := range map[string][2]string{
		"Edit of the handoff": {"Edit", writeInput(op.HandoffPath)},
		"Read":                {"Read", writeInput(op.HandoffPath)},
		"Bash":                {"Bash", `{"command":"git status"}`},
		"Write elsewhere":     {"Write", writeInput(op.HandoffPath + ".x")},
		"Write relative":      {"Write", writeInput("relay/" + filepath.Base(op.HandoffPath))},
		"Write, no path":      {"Write", `{}`},
		"Write, junk input":   {"Write", `"x"`},
	} {
		if d := decideTool(f, "PreToolUse", c[0], c[1]); d != deny {
			t.Errorf("%s: %+v", name, d)
		}
	}
}

func TestHookDecide_RelayLockPermissionRequestIsEmptyAndKeepsTheFlag(t *testing.T) {
	f, op, flag := claimedRelay(t)
	if d := decideTool(f, "PermissionRequest", "Write", writeInput(op.HandoffPath)); d != (team.HookDecideResponse{}) {
		t.Fatalf("PermissionRequest = %+v", d)
	}
	if !exists(flag) {
		t.Fatal("the flag was removed by a PermissionRequest")
	}
}

// The relay check runs before the stale-flag removal. Mutation gate: remove first → the flag is gone and the answer {} (red).
func TestHookDecide_RelayLockIsCheckedBeforeFlagRemoval(t *testing.T) {
	f, op, flag := claimedRelay(t)
	if d := decideTool(f, "PreToolUse", "Bash", `{}`); d.Decision != "deny" || !exists(flag) {
		t.Fatalf("with the lock: %+v, flag exists %v", d, exists(flag))
	}
	// the relay ended: the leftover flag costs one {} and is removed
	f.m.store.db.Exec(`UPDATE relay_ops SET state = 'done' WHERE id = ?`, op.ID)
	if d := decideTool(f, "PreToolUse", "Bash", `{}`); d != (team.HookDecideResponse{}) || exists(flag) {
		t.Fatalf("after the relay: %+v, flag exists %v", d, exists(flag))
	}
}

// Mutation gate: check the relay lock first → the lead's deny turns into the relay's (red).
func TestHookDecide_LeadLockWinsOverRelay(t *testing.T) {
	f, op, _ := claimedRelay(t)
	ap := f.create(uid(1)) // sid-1 also has an open lead request
	d := decideTool(f, "PreToolUse", "Bash", `{}`)
	if d.Lock != team.HookLockLeadRequest || d.ID != ap.ID {
		t.Fatalf("answer = %+v (relay op %s)", d, op.ID)
	}
}

// The safety net: the daemon never raises the flag, but compare-and-removes it BY OP ID when the op reaches cleared or a
// terminal state. Mutation gate: a blind remove → the lead request's flag is lowered (red).
func TestSafetyNet_RemovesTheRelayFlagByOpIDAtClearedAndTerminal_KeepsALeadFlag(t *testing.T) {
	for _, c := range []struct {
		name  string
		state team.RelayState
		body  func(op team.RelayOp) team.RelayReportRequest
	}{
		{"cleared", team.RelayCleared, func(team.RelayOp) team.RelayReportRequest {
			return team.RelayReportRequest{State: team.RelayCleared, NewSessionID: "sid-1b"}
		}},
		{"failed", team.RelayFailed, func(team.RelayOp) team.RelayReportRequest {
			return team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete}
		}},
		{"cancelled", team.RelayCancelled, func(team.RelayOp) team.RelayReportRequest {
			return team.RelayReportRequest{State: team.RelayCancelled, Error: team.RelayReasonAbandoned}
		}},
	} {
		f, op, flag := claimedRelay(t)
		os.WriteFile(flag, []byte(op.ID), 0o600) // the mod's flag: the op id
		if c.state == team.RelayCleared {
			f.report(op.ID, team.RelayReportRequest{State: team.RelayWriting})
			f.report(op.ID, team.RelayReportRequest{State: team.RelayWritten})
		}
		if code, _, ae := f.report(op.ID, c.body(op)); code != http.StatusOK {
			t.Fatalf("%s: %d %+v", c.name, code, ae)
		}
		if exists(flag) {
			t.Errorf("%s: the relay's flag was not removed", c.name)
		}
		// a flag that holds somebody else's id stays
		f2, op2, flag2 := claimedRelay(t)
		os.WriteFile(flag2, []byte("11111111-2222-4333-8444-555555555555"), 0o600)
		f2.report(op2.ID, team.RelayReportRequest{State: team.RelayFailed, Error: team.RelayReasonHandoffIncomplete})
		if !exists(flag2) {
			t.Errorf("%s: a lead request's flag was removed by the relay's safety net", c.name)
		}
	}
}

// A flag whose session is gone is pruned, unless the session has an op holding the relay lock. Mutation gate: drop the
// guard → the flag is pruned (red).
func TestPrune_KeepsTheFlagOfAnActiveRelay(t *testing.T) {
	f, op, flag := claimedRelay(t)
	f.origins.markDead("sid-1")
	if n := f.m.pruneHookLocks(); n != 0 || !exists(flag) {
		t.Fatalf("pruned %d, flag exists %v", n, exists(flag))
	}
	f.m.store.db.Exec(`UPDATE relay_ops SET state = 'failed' WHERE id = ?`, op.ID)
	if n := f.m.pruneHookLocks(); n != 1 || exists(flag) {
		t.Fatalf("after the relay: pruned %d, flag exists %v", n, exists(flag))
	}
}
