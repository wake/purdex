package team

import (
	"encoding/json"
	"testing"
)

func TestIsHookKind(t *testing.T) {
	if !IsHookKind(KindHookAsk) || !IsHookKind(KindHookPermission) || IsHookKind(KindLead) || IsHookKind(KindSelfRelay) || IsHookKind("") {
		t.Fatal("IsHookKind must be true for exactly hook_ask and hook_permission")
	}
	if NoExpiryAt >= 1<<53 {
		t.Fatal("NoExpiryAt must stay below 2^53 so the SPA reads it exactly")
	}
}

// A hook row's JSON carries `hook` in Grant's place and never `grant`; the
// wire names of the ask bodies are the contract the CLI and the mod read.
func TestHookWire_JSONNames(t *testing.T) {
	payload, _ := json.Marshal(HookAskPayload{ToolUseID: "toolu_1", Questions: json.RawMessage(`[{"question":"紅還是藍？"}]`)})
	a := Approval{ID: "x", Kind: KindHookAsk, HostID: "h", Payload: payload, State: StateAnsweredLocal, CreatedAt: 1, DeadlineAt: NoExpiryAt, LeaseUntil: 2,
		DecidedBy: &Client{Kind: ClientKindTerminal, Label: ClientKindTerminal}, DecidedAt: 3, Hook: &HookDecision{Answers: map[string]string{"紅還是藍？": "紅"}}}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["grant"]; has {
		t.Fatalf("a hook row must not carry grant: %s", raw)
	}
	if string(m["hook"]) != `{"answers":{"紅還是藍？":"紅"}}` || string(m["state"]) != `"answered_local"` || string(m["decided_by"]) != `{"kind":"terminal","label":"terminal"}` {
		t.Fatalf("hook row = %s", raw)
	}
	var back Approval
	if err := json.Unmarshal(raw, &back); err != nil || back.Hook == nil || back.Hook.Answers["紅還是藍？"] != "紅" || back.DeadlineAt != NoExpiryAt {
		t.Fatalf("round trip: %+v (%v)", back, err)
	}
	w, _ := json.Marshal(AskWaitResponse{State: AskAnsweredRemote, Hook: &HookDecision{Answers: map[string]string{"q": "a"}}})
	if string(w) != `{"state":"answered_remote","hook":{"answers":{"q":"a"}}}` {
		t.Fatalf("wait = %s", w)
	}
	w, _ = json.Marshal(AskWaitResponse{State: AskStillOpen})
	if string(w) != `{"state":"still_open"}` {
		t.Fatalf("wait = %s", w)
	}
	w, _ = json.Marshal(AskWaitResponse{State: AskClosed, Reason: string(StateDismissed)})
	if string(w) != `{"state":"closed","reason":"dismissed"}` {
		t.Fatalf("wait = %s", w)
	}
	b, _ := json.Marshal(AskBeginRequest{SessionID: "s", ToolUseID: "t", Kind: KindHookAsk, Payload: json.RawMessage(`{"questions":[]}`)})
	if string(b) != `{"session_id":"s","tool_use_id":"t","kind":"hook_ask","payload":{"questions":[]}}` {
		t.Fatalf("begin = %s", b)
	}
	d, _ := json.Marshal(DecideRequest{Decision: "approve", Hook: &HookDecision{Answers: map[string]string{"q": "a"}}, Client: Client{Kind: "app", Label: "L"}})
	if string(d) != `{"decision":"approve","hook":{"answers":{"q":"a"}},"client":{"kind":"app","label":"L"}}` {
		t.Fatalf("decide = %s", d)
	}
}
