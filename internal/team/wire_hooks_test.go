package team

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The JSON names are the contract between pdx hook, the daemon and (P8a)
// the mod; the empty response must serialise as exactly {} so the hook's
// "no decision" is the spec's empty object, not a set of empty strings.
func TestHookDecide_JSONKeysAndEmptyResponse(t *testing.T) {
	req := HookDecideRequest{
		Agent: HookAgentCC, Event: HookEventPreToolUse, SessionID: "sid-1",
		ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"ls"}`), ToolUseID: "toolu_1",
		Raw: json.RawMessage(`{"session_id":"sid-1"}`),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"agent", "event", "raw", "session_id", "tool_input", "tool_name", "tool_use_id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request keys = %v, want %v", got, want)
	}
	var back HookDecideRequest
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Agent != req.Agent || back.Event != req.Event || back.SessionID != req.SessionID ||
		back.ToolName != req.ToolName || string(back.ToolInput) != string(req.ToolInput) ||
		back.ToolUseID != req.ToolUseID || string(back.Raw) != string(req.Raw) {
		t.Fatalf("round trip: %+v", back)
	}

	empty, err := json.Marshal(HookDecideResponse{})
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `{}` {
		t.Fatalf("empty response = %s, want {}", empty)
	}
	deny, err := json.Marshal(HookDecideResponse{Decision: "deny", Reason: "r", Lock: HookLockLeadRequest, ID: "id-1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(deny) != `{"decision":"deny","reason":"r","lock":"lead_request","id":"id-1"}` {
		t.Fatalf("deny = %s", deny)
	}
}

// The reason text is the spec's (§6.6), with the request id in the
// full-width parentheses.
func TestLeadLockReason_IsTheSpecText(t *testing.T) {
	got := fmt.Sprintf(LeadLockReasonFmt, "11111111-2222-4333-8444-555555555555")
	want := "lead 申請等待核准中（11111111-2222-4333-8444-555555555555），核准或拒絕前這個 session 不能執行工具；請在 Purdex 介面處理"
	if got != want {
		t.Fatalf("reason = %q", got)
	}
	if strings.Count(LeadLockReasonFmt, "%s") != 1 {
		t.Fatalf("LeadLockReasonFmt must take exactly one %%s: %q", LeadLockReasonFmt)
	}
	if HookLocksDir != "hooklocks" || HookAgentCC != "cc" || HookAgentCodex != "codex" ||
		HookEventPreToolUse != "PreToolUse" || HookEventPermissionRequest != "PermissionRequest" {
		t.Fatal("hook constants drifted from the contract")
	}
}
