package ccnorm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
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

// manyImagesBlocks is a content array of one text block and n tiny image
// blocks, as JSON.
func manyImagesBlocks(n int) string {
	var b strings.Builder
	b.WriteString(`[{"type":"text","text":"look"}`)
	for i := 0; i < n; i++ {
		b.WriteString(`,{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA=="}}`)
	}
	b.WriteString(`]`)
	return b.String()
}

func assertOneBoundedUser(t *testing.T, n *Normalizer, total int) {
	t.Helper()
	items := itemsOf(t, validated(t, n), 0)
	if len(items) != 1 || items[0].User == nil {
		t.Fatalf("want one user item, got %d", len(items))
	}
	if got := len(items[0].User.Images); got == 0 || got > maxBlocksPerRow {
		t.Errorf("%d images from one row, want 1..%d", got, maxBlocksPerRow)
	}
	if got, want := n.Stats().Skipped["row:too_many_blocks"], total-maxBlocksPerRow; got != want {
		t.Errorf("Skipped = %v, want row:too_many_blocks = %d", n.Stats().Skipped, want)
	}
}

func TestUser_ManyImageBlocksRowIsBounded(t *testing.T) {
	row := fmt.Sprintf(`{"type":"user","uuid":"u1","timestamp":%q,"isSidechain":false,`+
		`"userType":"external","entrypoint":"cli","cwd":"/work/x","sessionId":%q,"version":"2.1.292",`+
		`"origin":{"kind":"human"},"promptSource":"typed","turnOrigin":"human",`+
		`"turnPosition":{"promptIndex":0,"turnIndex":0},`+
		`"message":{"role":"user","content":%s}}`, at(1), sidA, manyImagesBlocks(50000))
	n := New(Options{SessionID: sidA})
	if _, err := n.Feed(n.Next(), []byte(row)); err != nil {
		t.Fatal(err)
	}
	assertOneBoundedUser(t, n, 50001)
}

func TestAttachment_ManyBlocksRowIsBounded(t *testing.T) {
	o := common("attachment", "q1", 1)
	o["attachment"] = obj{"type": "queued_command", "commandMode": "prompt",
		"prompt": json.RawMessage(manyImagesBlocks(50000))}
	n := New(Options{SessionID: sidA})
	if _, err := n.Feed(n.Next(), line(o)); err != nil {
		t.Fatal(err)
	}
	assertOneBoundedUser(t, n, 50001)
}

func TestFeed_OversizeLineSkippedButOffsetAdvances(t *testing.T) {
	n := New(Options{SessionID: sidA})
	big := userRow("u0", 1, strings.Repeat("a", 9<<20))
	if len(big) <= 8<<20 {
		t.Fatalf("test line is only %d bytes", len(big))
	}
	ch, err := n.Feed(0, big)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 0 {
		t.Errorf("an oversize line produced changes: %v", ch)
	}
	if want := int64(len(big)) + 1; n.Next() != want {
		t.Fatalf("Next = %d, want offset+len+1 = %d", n.Next(), want)
	}
	if got := n.Stats().Skipped["line:oversize"]; got != 1 {
		t.Errorf("Skipped = %v, want line:oversize 1", n.Stats().Skipped)
	}
	if len(n.Conversation().Turns) != 0 {
		t.Error("an oversize line opened a turn")
	}

	// the next normal line at that offset is processed as usual
	off := n.Next()
	ch, err = n.Feed(off, userRow("u1", 2, "hi"))
	if err != nil || len(ch) == 0 {
		t.Fatalf("next line: changes %v err %v", ch, err)
	}
	c := validated(t, n)
	if len(c.Turns) != 1 || c.Turns[0].ID != "u1" || c.Turns[0].Offset != off {
		t.Errorf("turns after the oversize line: %s", dump(c))
	}

	// a line of exactly 8 MiB is still read
	m := New(Options{})
	pad := 8<<20 - len(userRow("u0", 1, ""))
	exact := userRow("u0", 1, strings.Repeat("a", pad))
	if len(exact) != 8<<20 {
		t.Fatalf("exact line is %d bytes", len(exact))
	}
	feed(t, m, exact)
	if len(m.Conversation().Turns) != 1 {
		t.Error("a line of exactly 8 MiB must still be parsed")
	}
}

// totalAlloc is the bytes allocated by f.
func totalAlloc(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

func imageBlock(t testing.TB, rawData string) block {
	t.Helper()
	raw := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + rawData + `"}}`
	o, ok := parseObject([]byte(raw))
	if !ok {
		t.Fatal("test image block does not parse")
	}
	return block{typ: "image", obj: o}
}

func TestUser_ImageSizeNoFullStringAllocation(t *testing.T) {
	// 4 MiB of JSON text with an escaped slash every 4 characters: measuring
	// it must not build the decoded string (nor any other copy of the data)
	unit := `AA\/A`
	rawData := strings.Repeat(unit, (4<<20)/len(unit)) + `\n`
	b := imageBlock(t, rawData)
	var mt string
	var size int64
	alloc := totalAlloc(func() { mt, size = imageSize(b) })
	chars := (4 << 20) / len(unit) * 4
	if mt != "image/png" || size != int64(chars/4*3) {
		t.Errorf("imageSize = %q, %d, want image/png, %d", mt, size, chars/4*3)
	}
	if limit := uint64(len(rawData) / 8); alloc > limit {
		t.Errorf("imageSize allocated %d bytes for %d bytes of data, want at most %d", alloc, len(rawData), limit)
	}
}

func TestUser_ImageSizeMatchesDecodedString(t *testing.T) {
	// the scan must agree with decode-then-trim on escapes, edge whitespace
	// and padding
	for _, data := range []string{
		``, `AAAA`, `AAAA=`, `AAA=`, `AA==`, `AA==`, `AA\/A`, `\nAAAA\n`, `\n \tAAA=\r\n`,
		`AA=\n=`, `AAAA\n==`, `AA\"A`, `A\\AA`, `ABAA`, `héllo`, `éAAA`, `   `,
	} {
		b := imageBlock(t, data)
		_, got := imageSize(b)
		var s string
		if err := json.Unmarshal([]byte(`"`+data+`"`), &s); err != nil {
			t.Fatalf("%q: %v", data, err)
		}
		inner := bytes.TrimSpace([]byte(s))
		want := int64(base64.StdEncoding.DecodedLen(len(inner)))
		for i := 0; i < 2 && i < len(inner) && inner[len(inner)-1-i] == '='; i++ {
			want--
		}
		if got != want {
			t.Errorf("data %q: size %d, want %d", data, got, want)
		}
	}
}
