package push

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BG-1b (badge spec §3.1, background badge push): the body is `content-available` and the purdex block, nothing a person
// would see, and it stays inside 4096 bytes.

func bgContent(keys []string, ids []string) Content {
	return Content{Kind: "badge", Background: true, OpenApprovals: ip(len(keys)), OpenApprovalKeys: keys, ClosedApprovalIDs: ids}
}

func TestBackgroundPayload_Shape(t *testing.T) {
	raw, err := bgContent([]string{"a:x", "s:c1"}, []string{"ap1", "ap2"}).Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	aps, purdex := parsePurdex(t, raw)
	if len(aps) != 1 || aps["content-available"] != float64(1) {
		t.Fatalf("aps must be exactly content-available:1: %s", raw)
	}
	for _, banned := range []string{"alert", "sound", "mutable-content", "badge", "thread-id", "interruption-level"} {
		if _, has := aps[banned]; has {
			t.Fatalf("aps.%s on a background push: %s", banned, raw)
		}
	}
	if purdex["kind"] != "badge" || purdex["host_id"] != "host-1" || purdex["open_approvals"] != float64(2) {
		t.Fatalf("purdex block = %v", purdex)
	}
	if keys, _ := purdex["open_approval_keys"].([]any); len(keys) != 2 || keys[1] != "s:c1" {
		t.Fatalf("keys = %v", purdex["open_approval_keys"])
	}
	if ids, _ := purdex["closed_approval_ids"].([]any); len(ids) != 2 || ids[0] != "ap1" {
		t.Fatalf("closed ids = %v", purdex["closed_approval_ids"])
	}
	for _, banned := range []string{"approval_id", "session_code", "event", "member"} {
		if _, has := purdex[banned]; has {
			t.Fatalf("purdex.%s on a badge push: %s", banned, raw)
		}
	}
}

// The visible push is untouched by the new field: a Content that is not Background still has its alert and mutable-content.
func TestBackgroundPayload_AVisiblePushIsUnchanged(t *testing.T) {
	raw, err := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: ip(1)}.Payload("h")
	if err != nil {
		t.Fatal(err)
	}
	aps, purdex := parsePurdex(t, raw)
	if aps["mutable-content"] != float64(1) || aps["alert"] == nil || aps["sound"] != "default" {
		t.Fatalf("visible push changed: %s", raw)
	}
	if _, has := purdex["closed_approval_ids"]; has {
		t.Fatalf("closed_approval_ids on a visible push: %s", raw)
	}
}

// An empty open set is a real answer (0 and []), and ids may be empty.
func TestBackgroundPayload_ZeroOpenIsSentAsKnown(t *testing.T) {
	raw, err := bgContent([]string{}, nil).Payload("h")
	if err != nil {
		t.Fatal(err)
	}
	_, purdex := parsePurdex(t, raw)
	if v, ok := purdex["open_approvals"]; !ok || v != float64(0) {
		t.Fatalf("open_approvals = %v in %s", v, raw)
	}
	if v, ok := purdex["open_approval_keys"].([]any); !ok || len(v) != 0 {
		t.Fatalf("keys = %#v in %s", purdex["open_approval_keys"], raw)
	}
}

// A background push says nothing without the open set: a failed read is "unknown", which this push is only about.
func TestBackgroundPayload_UnknownOpenSetIsRefused(t *testing.T) {
	c := Content{Kind: "badge", Background: true, ClosedApprovalIDs: []string{"ap1"}}
	if _, err := c.Payload("h"); err == nil {
		t.Fatal("want an error when the open set is unknown")
	}
}

// 20 closes → at most 16 ids, the first 16. Mutation gate: no cap → red.
func TestBackgroundPayload_AtMost16ClosedIDs(t *testing.T) {
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	raw, err := bgContent([]string{"a:x"}, ids).Payload("h")
	if err != nil {
		t.Fatal(err)
	}
	_, purdex := parsePurdex(t, raw)
	got, _ := purdex["closed_approval_ids"].([]any)
	if len(got) != 16 || got[0] != ids[0] || got[15] != ids[15] {
		t.Fatalf("closed ids = %v", got)
	}
}

// 32 keys of maximum length (a c: key with a 64-byte id) plus 16 ids fit in 4096 bytes, everything kept.
func TestBackgroundPayload_32MaximalKeysAnd16IDsFit(t *testing.T) {
	var keys, ids []string
	for i := 0; i < 32; i++ {
		keys = append(keys, fmt.Sprintf("c:%s%02d", strings.Repeat("k", 62), i))
	}
	for i := 0; i < 16; i++ {
		ids = append(ids, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	raw, err := bgContent(keys, ids).Payload("host-with-a-long-enough-id-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 {
		t.Fatalf("%d bytes", len(raw))
	}
	_, purdex := parsePurdex(t, raw)
	if k, _ := purdex["open_approval_keys"].([]any); len(k) != 32 {
		t.Fatalf("keys cut to %d", len(k))
	}
	if i, _ := purdex["closed_approval_ids"].([]any); len(i) != 16 {
		t.Fatalf("ids cut to %d", len(i))
	}
}

// If it ever would not fit, ids go first and keys stay. Mutation gate: drop keys first → red.
func TestBackgroundPayload_OverBudgetDropsIDsBeforeKeys(t *testing.T) {
	var keys, ids []string
	for i := 0; i < 32; i++ {
		keys = append(keys, fmt.Sprintf("c:%s%02d", strings.Repeat("k", 110), i)) // 32 x 115 = 3680 bytes of keys
	}
	for i := 0; i < 16; i++ {
		ids = append(ids, fmt.Sprintf("00000000-0000-4000-8000-%012d", i)) // 16 x 39 = 624 bytes of ids
	}
	raw, err := bgContent(keys, ids).Payload("h")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 {
		t.Fatalf("%d bytes", len(raw))
	}
	_, purdex := parsePurdex(t, raw)
	if k, _ := purdex["open_approval_keys"].([]any); len(k) != 32 {
		t.Fatalf("keys were cut to %d before the ids were gone", len(k))
	}
	if i, _ := purdex["closed_approval_ids"].([]any); len(i) == 16 {
		t.Fatal("no id was dropped, yet the payload was over budget")
	}
}

// Keys alone over budget (cannot happen with real keys): the keys are cut last, and the result still fits.
func TestBackgroundPayload_KeysAreCutOnlyWhenNothingElseIsLeft(t *testing.T) {
	var keys []string
	for i := 0; i < 32; i++ {
		keys = append(keys, fmt.Sprintf("c:%s%02d", strings.Repeat("k", 200), i))
	}
	raw, err := bgContent(keys, []string{"ap1"}).Payload("h")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 {
		t.Fatalf("%d bytes", len(raw))
	}
	var p struct {
		Purdex struct {
			Open int      `json:"open_approvals"`
			Keys []string `json:"open_approval_keys"`
			IDs  []string `json:"closed_approval_ids"`
		} `json:"purdex"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Purdex.IDs) != 0 || len(p.Purdex.Keys) == 0 || len(p.Purdex.Keys) >= 32 || p.Purdex.Open != 32 {
		t.Fatalf("ids %d keys %d open %d", len(p.Purdex.IDs), len(p.Purdex.Keys), p.Purdex.Open)
	}
}
