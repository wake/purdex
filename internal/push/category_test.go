package push

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/reads"
)

// P-1: the three yes / no kinds carry the PDX_APPROVAL category (the 同意 / 拒絕 buttons); hook_ask and agent pushes none.
// Mutation gate (on each kind): drop the kind from the switch / set the category on hook_ask -> red.
func TestApprovalContent_CategoryOnTheThreeYesNoKindsOnly(t *testing.T) {
	cases := map[string]bool{"lead": true, "self_relay": true, "member_relay": true, "hook_ask": false}
	for kind, want := range cases {
		c, ok := ApprovalContent(Approval{ID: "ap1", Kind: kind, Payload: json.RawMessage(`{"questions":[{"question":"q"}]}`)}, "mlab", "en")
		if !ok {
			t.Fatalf("%s: not pushed", kind)
		}
		raw, err := c.Payload("h")
		if err != nil {
			t.Fatal(err)
		}
		aps, _ := parsePurdex(t, raw)
		got, has := aps["category"]
		if want && got != "PDX_APPROVAL" {
			t.Fatalf("%s: category = %v", kind, got)
		}
		if !want && has {
			t.Fatalf("%s: category = %v, want none", kind, got)
		}
	}
}

// A push with no category is byte-identical to today's (the field is left out, never "").
func TestPayload_NoCategoryNoField(t *testing.T) {
	raw, _ := Content{Title: "T", Body: "B", Kind: "agent"}.Payload("h")
	if strings.Contains(string(raw), "category") {
		t.Fatalf("%s", raw)
	}
}

// The category is part of the 4 KiB accounting, and the reads the payload carries stay consistent with what Build reports.
func TestBuild_CategoryAtTheSizeBoundaryKeepsReadsConsistent(t *testing.T) {
	for n := 0; n < 400; n += 7 {
		c := Content{Title: strings.Repeat("t", 120), Body: strings.Repeat("b", 900+n*3), Kind: "lead", ApprovalID: "ap1",
			CollapseID: "ap1", Category: ApprovalCategory, Reads: readsOf(16)}
		raw, sent, err := c.Build("host-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > maxPayload {
			t.Fatalf("n=%d: %d bytes", n, len(raw))
		}
		aps, purdex := parsePurdex(t, raw)
		if aps["category"] != "PDX_APPROVAL" {
			t.Fatalf("n=%d: category lost: %s", n, raw)
		}
		got, _ := purdex["reads"].([]any)
		if len(got) != len(sent) {
			t.Fatalf("n=%d: payload carries %d reads, Build says %d", n, len(got), len(sent))
		}
		if len(sent) > 0 && sent[0] != (reads.Read{Code: "c00001", Through: reads.Cursor{Epoch: "boot-1", Seq: 1}}) {
			t.Fatalf("sent[0] = %v", sent[0])
		}
	}
}
