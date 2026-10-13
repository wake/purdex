package push

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/reads"
)

// BG-4b (badge spec §7.2 "Pushes"): reads ride on the push; the event cursor rides on every agent push.

func rd(i int) reads.Read {
	return reads.Read{Code: fmt.Sprintf("c%05d", i), Through: reads.Cursor{Epoch: "boot-1", Seq: uint64(i)}}
}

func readsOf(n int) []reads.Read {
	out := make([]reads.Read, n)
	for i := range out {
		out[i] = rd(i + 1)
	}
	return out
}

func TestPrefs_ReadSyncRoundTrips(t *testing.T) {
	r := good()
	r.Prefs.ReadSync = true
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if !r.Prefs.ReadSync {
		t.Fatal("read_sync was lost")
	}
}

// A visible push carries at most 16 reads, the oldest first, and says which it carried.
// Mutation gate: no cap / newest first → red.
func TestBuild_AVisiblePushCarriesTheSixteenOldestReads(t *testing.T) {
	c := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", Reads: readsOf(20)}
	raw, sent, err := c.Build("host-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 16 || sent[0] != rd(1) || sent[15] != rd(16) {
		t.Fatalf("sent = %v", sent)
	}
	_, purdex := parsePurdex(t, raw)
	got, _ := purdex["reads"].([]any)
	if len(got) != 16 {
		t.Fatalf("payload reads = %d", len(got))
	}
	first := got[0].(map[string]any)
	if first["code"] != "c00001" || first["through"].(map[string]any)["seq"] != float64(1) {
		t.Fatalf("first read = %v", first)
	}
}

// No reads, no field: a push of a device that does not sync reads is byte-identical to today's.
func TestBuild_NoReadsLeavesThePayloadAsItWas(t *testing.T) {
	c := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1", OpenApprovals: ip(1)}
	plain, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"reads"`) || strings.Contains(string(plain), "event_cursor") {
		t.Fatalf("%s", plain)
	}
	c.Reads = []reads.Read{}
	again, _ := c.Payload("host-1")
	if string(again) != string(plain) {
		t.Fatalf("an empty reads list changed the payload: %s", again)
	}
}

// The reads are fitted in the 4 KiB budget BEFORE the body is trimmed (the body gives way); reads are dropped only when the
// body is already empty. Swept over the room the fixed fields leave: whenever a read was dropped, the body must be gone.
// Mutation gate: trim reads before the body → red.
func TestBuild_ReadsAreFittedBeforeTheBodyIsTrimmed(t *testing.T) {
	trimmed, dropped := 0, 0
	for keyLen := 20; keyLen <= 140; keyLen += 2 {
		var keys []string
		for i := 0; i < 32; i++ {
			keys = append(keys, fmt.Sprintf("c:%s%02d", strings.Repeat("k", keyLen), i))
		}
		c := Content{Title: "T", Body: strings.Repeat("字", 240), Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1",
			OpenApprovals: ip(32), OpenApprovalKeys: keys, Reads: readsOf(16)}
		raw, sent, err := c.Build("host-1")
		if err != nil {
			continue // beyond what any trimming can fit
		}
		if len(raw) > maxPayload {
			t.Fatalf("keyLen %d: %d bytes", keyLen, len(raw))
		}
		_, ok := parseAlertBody(t, raw)
		if ok != "" && len([]rune(ok)) < 240 {
			trimmed++
		}
		if len(sent) < 16 {
			dropped++
			if ok != "" {
				t.Fatalf("keyLen %d: %d reads dropped while the body (%d runes) was still there", keyLen, 16-len(sent), len([]rune(ok)))
			}
		}
	}
	if trimmed == 0 || dropped == 0 {
		t.Fatalf("the sweep did not exercise both branches (body trimmed %d times, reads dropped %d times)", trimmed, dropped)
	}
}

func parseAlertBody(t *testing.T, raw []byte) (map[string]any, string) {
	t.Helper()
	aps, _ := parsePurdex(t, raw)
	alert, _ := aps["alert"].(map[string]any)
	body, _ := alert["body"].(string)
	return aps, body
}

func TestBuild_WhenEvenAnEmptyBodyDoesNotFitReadsAreDroppedFromTheNewestEnd(t *testing.T) {
	// huge fixed fields: open_approval_keys of 32 maximal keys leave room for only a few reads
	var keys []string
	for i := 0; i < 32; i++ {
		keys = append(keys, fmt.Sprintf("c:%s%02d", strings.Repeat("k", 62), i))
	}
	long := make([]reads.Read, 16)
	for i := range long {
		long[i] = reads.Read{Code: strings.Repeat("x", 60) + fmt.Sprintf("%02d", i), Through: reads.Cursor{Epoch: strings.Repeat("e", 100), Seq: uint64(i)}}
	}
	c := Content{Title: strings.Repeat("t", 120), Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1",
		OpenApprovals: ip(32), OpenApprovalKeys: keys, Reads: long}
	raw, sent, err := c.Build("host-with-a-rather-long-label-0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxPayload {
		t.Fatalf("%d bytes", len(raw))
	}
	if len(sent) == 0 || len(sent) >= 16 {
		t.Fatalf("%d reads sent, want some but not all", len(sent))
	}
	for i, r := range sent {
		if r != long[i] {
			t.Fatalf("sent[%d] = %v: the oldest must be the ones kept", i, r)
		}
	}
}

// A push that cannot be built carries nothing: no reads are reported as sent.
func TestBuild_AFailedBuildReportsNoReads(t *testing.T) {
	c := Content{Title: strings.Repeat("t", 120), Body: "B", Kind: "lead", CollapseID: "x", Reads: readsOf(3),
		OpenApprovals: ip(1), OpenApprovalKeys: []string{strings.Repeat("k", 5000)}}
	raw, sent, err := c.Build("h")
	if err == nil || raw != nil || sent != nil {
		t.Fatalf("raw %d sent %v err %v: want a failure carrying nothing", len(raw), sent, err)
	}
}

// Every agent push carries the hook cursor of its event; with no epoch there is nothing to say and the field is left out.
func TestAgentPayload_EventCursor(t *testing.T) {
	c, _ := AgentContent(AgentInput{SessionCode: "c1", SessionID: "s1", EventName: "Stop", EventCursor: reads.Cursor{Epoch: "boot-1", Seq: 77}}, "en")
	raw, err := c.Payload("host-1")
	if err != nil {
		t.Fatal(err)
	}
	_, purdex := parsePurdex(t, raw)
	cur, _ := purdex["event_cursor"].(map[string]any)
	if cur["epoch"] != "boot-1" || cur["seq"] != float64(77) {
		t.Fatalf("event_cursor = %v in %s", purdex["event_cursor"], raw)
	}
	c, _ = AgentContent(AgentInput{SessionCode: "c1", SessionID: "s1", EventName: "Stop"}, "en")
	raw, _ = c.Payload("host-1")
	if _, has := parsePurdex2(t, raw)["event_cursor"]; has {
		t.Fatalf("an empty epoch must leave event_cursor out: %s", raw)
	}
}

func parsePurdex2(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	_, p := parsePurdex(t, raw)
	return p
}

// An approval push is not an agent event: it has no event cursor.
func TestApprovalPayload_HasNoEventCursor(t *testing.T) {
	raw, _ := Content{Title: "T", Body: "B", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1"}.Payload("h")
	if _, has := parsePurdex2(t, raw)["event_cursor"]; has {
		t.Fatalf("%s", raw)
	}
}

// The background badge push carries reads too. When the budget is tight, reads (which stay pending, so nothing is lost) go
// before ids (only cleaned up at the next foreground), and ids before keys. Swept over the room the keys leave: whenever an
// id was dropped, every read is already gone; whenever a key was dropped, every id is. Mutation gate: ids before reads → red.
func TestBackgroundBadge_CarriesReadsAndTrimsThemFirst(t *testing.T) {
	c := bgContent([]string{"a:x"}, []string{"ap1", "ap2"})
	c.Reads = readsOf(5)
	raw, sent, err := c.Build("host-1")
	if err != nil {
		t.Fatal(err)
	}
	_, purdex := parsePurdex(t, raw)
	if got, _ := purdex["reads"].([]any); len(got) != 5 || len(sent) != 5 {
		t.Fatalf("reads in payload %d, sent %d", len(got), len(sent))
	}
	if ids, _ := purdex["closed_approval_ids"].([]any); len(ids) != 2 {
		t.Fatalf("ids = %v", ids)
	}
	var ids []string
	for i := 0; i < 16; i++ {
		ids = append(ids, fmt.Sprintf("00000000-0000-4000-8000-%012d", i))
	}
	sawReadsDropped, sawIDsDropped := false, false
	for keyLen := 60; keyLen <= 135; keyLen++ {
		var keys []string
		for i := 0; i < 32; i++ {
			keys = append(keys, fmt.Sprintf("c:%s%02d", strings.Repeat("k", keyLen), i))
		}
		tight := bgContent(keys, ids)
		tight.Reads = readsOf(16)
		raw, sent, err := tight.Build("h")
		if err != nil {
			continue
		}
		if len(raw) > maxPayload {
			t.Fatalf("keyLen %d: %d bytes", keyLen, len(raw))
		}
		_, purdex := parsePurdex(t, raw)
		gotIDs, _ := purdex["closed_approval_ids"].([]any)
		gotKeys, _ := purdex["open_approval_keys"].([]any)
		if len(sent) < 16 {
			sawReadsDropped = true
		}
		if len(gotIDs) < 16 {
			sawIDsDropped = true
			if len(sent) != 0 {
				t.Fatalf("keyLen %d: %d ids dropped while %d reads were still carried", keyLen, 16-len(gotIDs), len(sent))
			}
		}
		if len(gotKeys) < 32 && len(gotIDs) != 0 {
			t.Fatalf("keyLen %d: keys were cut with %d ids left", keyLen, len(gotIDs))
		}
	}
	if !sawReadsDropped || !sawIDsDropped {
		t.Fatalf("the sweep did not exercise both branches (reads dropped %v, ids dropped %v)", sawReadsDropped, sawIDsDropped)
	}
}

// The flush push: kind "reads", background, no open set, only the reads.
func TestReadsFlush_Payload(t *testing.T) {
	c := Content{Kind: "reads", Background: true, Reads: readsOf(20)}
	raw, sent, err := c.Build("host-1")
	if err != nil {
		t.Fatal(err)
	}
	aps, purdex := parsePurdex(t, raw)
	if len(aps) != 1 || aps["content-available"] != float64(1) {
		t.Fatalf("aps = %v", aps)
	}
	if purdex["kind"] != "reads" || purdex["host_id"] != "host-1" {
		t.Fatalf("purdex = %v", purdex)
	}
	for _, banned := range []string{"open_approvals", "open_approval_keys", "closed_approval_ids", "approval_id"} {
		if _, has := purdex[banned]; has {
			t.Fatalf("%s on a flush push: %s", banned, raw)
		}
	}
	if got, _ := purdex["reads"].([]any); len(got) != 16 || len(sent) != 16 {
		t.Fatalf("reads %d sent %d, want the 16 oldest", len(got), len(sent))
	}
	if _, _, err := (Content{Kind: "reads", Background: true}).Build("h"); err == nil {
		t.Fatal("a flush with nothing to carry must not build")
	}
}
