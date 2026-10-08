package ccnorm

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Bounds: whatever a transcript line holds, the work and the memory the
// normalizer spends on it stay bounded.

// manyBlocksRow is one assistant row with n text blocks, built as a string
// (marshalling 100,000 maps would dominate the test).
func manyBlocksRow(uuid string, n int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `{"type":"assistant","uuid":%q,"timestamp":%q,"isSidechain":false,`+
		`"message":{"model":"claude-opus-5-5","role":"assistant","content":[`, uuid, at(2))
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"type":"text","text":"x"}`)
	}
	b.WriteString(`]}}`)
	return []byte(b.String())
}

func TestFeed_ManyBlocksRowIsBounded(t *testing.T) {
	row := manyBlocksRow("a1", 100000)
	n := New(Options{SessionID: sidA})
	feed(t, n, userRow("u1", 1, "go"))

	start := time.Now()
	ch, err := n.Feed(n.Next(), row)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("a 100000-block row took %v", d)
	}
	items := itemsOf(t, validated(t, n), 0)
	if len(items) > 1+64 {
		t.Errorf("%d items from one row, want at most 64", len(items)-1)
	}
	if len(ch) > 3+64 {
		t.Errorf("%d changes from one row", len(ch))
	}
	if got := n.Stats().Skipped["row:too_many_blocks"]; got == 0 {
		t.Errorf("Skipped = %v, want row:too_many_blocks counted", n.Stats().Skipped)
	}
}

func TestFeed_ManyBlocksRowChangesDeduped(t *testing.T) {
	n := New(Options{SessionID: sidA})
	feed(t, n, userRow("u1", 1, "go"))
	ch, err := n.Feed(n.Next(), manyBlocksRow("a1", 1000))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[[2]string]bool{}
	for _, c := range ch {
		k := [2]string{c.TurnID, c.ItemID}
		if seen[k] {
			t.Errorf("change %v reported twice", k)
		}
		seen[k] = true
	}

	// the pending set itself is O(1) per change: many distinct changes must
	// not cost a quadratic scan, and repeats are still dropped
	m := New(Options{})
	start := time.Now()
	const distinct = 50000
	for i := 0; i < distinct; i++ {
		m.add(Change{"t", fmt.Sprintf("i%d", i), 0})
	}
	for i := 0; i < distinct; i += 1000 {
		m.add(Change{"t", fmt.Sprintf("i%d", i), 0})
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("%d pending changes took %v", distinct, d)
	}
	out := m.flush()
	if len(out) != distinct {
		t.Fatalf("flush = %d changes, want %d distinct", len(out), distinct)
	}
	if out[0].ItemID != "i0" || out[distinct-1].ItemID != fmt.Sprintf("i%d", distinct-1) {
		t.Error("flush lost the insertion order")
	}
	m.add(Change{"t", "i0", 0})
	if got := m.flush(); len(got) != 1 {
		t.Errorf("after flush the same change must be reportable again, got %d", len(got))
	}
}

func TestStats_SkippedKeyCardinalityBounded(t *testing.T) {
	n := New(Options{})
	const rows = 10000
	for i := 0; i < rows; i++ {
		feed(t, n, line(obj{"type": fmt.Sprintf("unknown-%d", i), "uuid": "x"}))
	}
	sk := n.Stats().Skipped
	if len(sk) > 65 {
		t.Errorf("%d distinct Skipped keys, want at most %d", len(sk), 65)
	}
	total := 0
	for _, v := range sk {
		total += v
	}
	if total != rows {
		t.Errorf("total skipped = %d, want %d (the count must survive the cap)", total, rows)
	}
	if sk["other"] != rows-64 {
		t.Errorf("other = %d, want %d", sk["other"], rows-64)
	}
	// an existing key keeps counting under its own name
	feed(t, n, line(obj{"type": "unknown-0", "uuid": "x"}))
	if got := n.Stats().Skipped["type:unknown-0"]; got != 2 {
		t.Errorf("type:unknown-0 = %d, want 2", got)
	}
}
