package push

import (
	"encoding/json"
	"strings"
	"testing"
)

// Badge spec §3.1: the cards the badge counts are decided by CountsForBadge, not by what gets a visible push.
func TestCountsForBadge(t *testing.T) {
	for kind, want := range map[string]bool{
		"lead": true, "self_relay": true, "member_relay": true, "adopt": true, "hook_ask": true, "hook_permission": true,
		"": false, "agent": false, "badge": false, "Lead": false, "hook": false, "relay": false,
	} {
		if got := CountsForBadge(kind); got != want {
			t.Errorf("CountsForBadge(%q) = %v, want %v", kind, got, want)
		}
	}
}

// ApprovalContent keeps deciding only what gets a visible push: a terminal-only ask and an adopt count but are not pushed.
func TestApprovalContent_UnchangedByBadgeKinds(t *testing.T) {
	ask := Approval{ID: "a1", Kind: "hook_ask", Payload: json.RawMessage(`{"terminal_only":true}`)}
	if _, ok := ApprovalContent(ask, "", "en"); ok {
		t.Fatal("a terminal-only ask must stay without a visible push")
	}
	for _, kind := range []string{"adopt", "hook_permission"} {
		if _, ok := ApprovalContent(Approval{ID: "a2", Kind: kind}, "", "en"); ok {
			t.Fatalf("%s must stay without a visible push", kind)
		}
	}
}

func TestPrefs_MutedAndBadgePushValidation(t *testing.T) {
	for name, mut := range map[string]func(*DeviceRequest){
		"too many muted":   func(r *DeviceRequest) { r.Prefs.Muted = make([]string, 501) },
		"empty muted code": func(r *DeviceRequest) { r.Prefs.Muted = []string{""} },
		"long muted code":  func(r *DeviceRequest) { r.Prefs.Muted = []string{strings.Repeat("c", 65)} },
		"control in muted": func(r *DeviceRequest) { r.Prefs.Muted = []string{"a\nb"} },
	} {
		t.Run(name, func(t *testing.T) {
			r := good()
			mut(&r)
			if err := r.Validate(); err == nil {
				t.Fatal("want a validation error")
			}
		})
	}
	r := good()
	r.Prefs.Muted = make([]string, 500)
	for i := range r.Prefs.Muted {
		r.Prefs.Muted[i] = "c"
	}
	r.Prefs.BadgePush = true
	if err := r.Validate(); err != nil {
		t.Fatalf("the limits themselves are fine: %v", err)
	}
}

// An older App sends neither field: both are zero and omitted again when stored.
func TestPrefs_NewFieldsRoundTripJSON(t *testing.T) {
	in := Prefs{Tabs: []string{"a"}, Muted: []string{"m1", "m2"}, BadgePush: true}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Prefs
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Muted) != 2 || out.Muted[1] != "m2" || !out.BadgePush {
		t.Fatalf("round trip lost a field: %s -> %+v", raw, out)
	}
	raw, _ = json.Marshal(Prefs{Tabs: []string{"a"}})
	if strings.Contains(string(raw), "muted") || strings.Contains(string(raw), "badge_push") {
		t.Fatalf("zero values must be left out: %s", raw)
	}
}

func TestAgentPayload_MemberFlag(t *testing.T) {
	c, ok := AgentContent(AgentInput{SessionCode: "c1", SessionID: "s1", EventName: "Stop", Member: true}, "en")
	if !ok || !c.Member {
		t.Fatalf("Member must reach the content: ok=%v %+v", ok, c)
	}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	_, purdex := parsePurdex(t, raw)
	if purdex["member"] != true {
		t.Fatalf("purdex.member = %#v in %s", purdex["member"], raw)
	}
	c, _ = AgentContent(AgentInput{SessionCode: "c1", SessionID: "s1", EventName: "Stop"}, "en")
	raw, _ = c.Payload("host-1")
	_, purdex = parsePurdex(t, raw)
	if _, has := purdex["member"]; has {
		t.Fatalf("a non-member's payload leaves member out (never false): %s", raw)
	}
}

// A badge key list built from c: keys is still a plain []string in the payload.
func TestPayload_OpenApprovalKeysCarryCKeys(t *testing.T) {
	c := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: ip(2), OpenApprovalKeys: []string{"a:x", "c:sid-1"}}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"open_approval_keys":["a:x","c:sid-1"]`) {
		t.Fatalf("%s", raw)
	}
}
