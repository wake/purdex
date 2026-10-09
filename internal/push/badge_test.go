package push

import (
	"encoding/json"
	"strings"
	"testing"
)

func ip(n int) *int { return &n }

func parsePurdex(t *testing.T, raw []byte) (aps, purdex map[string]any) {
	t.Helper()
	var p struct {
		Aps    map[string]any `json:"aps"`
		Purdex map[string]any `json:"purdex"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p.Aps, p.Purdex
}

func TestPayload_BadgeKnown(t *testing.T) {
	for _, n := range []int{0, 3} { // 0 is a real value when the read said so
		c := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: ip(n)}
		raw, err := c.Payload("host-1")
		if err != nil {
			t.Fatal(err)
		}
		aps, purdex := parsePurdex(t, raw)
		if aps["mutable-content"] != float64(1) {
			t.Fatalf("mutable-content: %s", raw)
		}
		if v, ok := purdex["open_approvals"].(float64); !ok || int(v) != n {
			t.Fatalf("open_approvals = %#v, want %d in %s", purdex["open_approvals"], n, raw)
		}
		t.Logf("known: %s", raw)
	}
}

func TestPayload_BadgeUnknownLeavesTheKeyOut(t *testing.T) {
	c := Content{Title: "T", Body: "B", Kind: "agent", SessionCode: "c1", Event: "stop", CollapseID: "agent-c1"}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	aps, purdex := parsePurdex(t, raw)
	if aps["mutable-content"] != float64(1) {
		t.Fatalf("mutable-content stays on every push: %s", raw)
	}
	if _, has := purdex["open_approvals"]; has {
		t.Fatalf("unknown must not be sent (not even as 0): %s", raw)
	}
	if purdex["host_id"] != "host-1" || purdex["kind"] != "agent" {
		t.Fatalf("string fields lost: %s", raw)
	}
	t.Logf("unknown: %s", raw)
}

func TestPayload_BadgeFieldsKeepCut(t *testing.T) {
	c := Content{Title: "T", Body: strings.Repeat("字", 20000), Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: ip(12)}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 || !strings.Contains(string(raw), `"open_approvals":12`) {
		t.Fatalf("%d bytes: %.80s", len(raw), raw)
	}
}
