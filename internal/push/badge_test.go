package push

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPayload_BadgeFields(t *testing.T) {
	for _, c := range []Content{
		{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: 3},
		{Title: "T", Body: "B", Kind: "agent", SessionCode: "c1", Event: "stop", CollapseID: "agent-c1"},
	} {
		raw, err := c.Payload("host-1")
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Aps    map[string]any `json:"aps"`
			Purdex map[string]any `json:"purdex"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if p.Aps["mutable-content"] != float64(1) {
			t.Fatalf("%s: mutable-content = %v in %s", c.Kind, p.Aps["mutable-content"], raw)
		}
		n, ok := p.Purdex["open_approvals"].(float64)
		if !ok || int(n) != c.OpenApprovals {
			t.Fatalf("%s: open_approvals = %#v, want integer %d in %s", c.Kind, p.Purdex["open_approvals"], c.OpenApprovals, raw)
		}
		if p.Purdex["host_id"] != "host-1" || p.Purdex["kind"] != c.Kind {
			t.Fatalf("string fields lost: %s", raw)
		}
		if _, has := p.Purdex["event"]; has != (c.Event != "") {
			t.Fatalf("empty string fields must stay out: %s", raw)
		}
		t.Logf("%s", raw)
	}
}

func TestPayload_BadgeFieldsKeepCut(t *testing.T) {
	c := Content{Title: "T", Body: strings.Repeat("字", 20000), Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: 12}
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 || !strings.Contains(string(raw), `"open_approvals":12`) {
		t.Fatalf("%d bytes: %.80s", len(raw), raw)
	}
}
