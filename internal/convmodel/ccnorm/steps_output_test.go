package ccnorm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

const kib = 1024

// outputOfResult runs one tool of the given name that answered with text and
// returns the step's output (nil fails the test).
func outputOfResult(t testing.TB, tool string, content any) *convmodel.Output {
	t.Helper()
	s := oneStep(t, tool, obj{"command": "x", "file_path": "/f"}, resultRow("r1", 3, "toolu_1", content, false))
	if s.Output == nil {
		t.Fatalf("no output on %+v", s)
	}
	return s.Output
}

// numbered is n lines "line 0\nline 1\n…" with no trailing newline.
func numbered(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "line %d", i)
	}
	return b.String()
}

func TestOutput_ExecuteKeepsTail(t *testing.T) {
	text := numbered(20000)
	o := outputOfResult(t, "Bash", text)
	if !o.Truncated || o.Keep != convmodel.KeepTail {
		t.Fatalf("truncated=%v keep=%q", o.Truncated, o.Keep)
	}
	if len(o.Text) > convmodel.MaxOutput || len(o.Text) < convmodel.MaxOutput-100 {
		t.Errorf("kept %d bytes", len(o.Text))
	}
	if !strings.HasSuffix(text, o.Text) || !strings.HasPrefix(o.Text, "line ") {
		t.Errorf("kept text is not whole lines from the end: %q …", o.Text[:20])
	}
	lines := strings.Split(o.Text, "\n")
	if len(lines) < 10 || lines[len(lines)-1] != "line 19999" {
		t.Errorf("%d kept lines, last %q: want ≥ 10 whole last lines", len(lines), lines[len(lines)-1])
	}
	if e := outputOfResult(t, "Monitor", text); e.Keep != convmodel.KeepTail {
		t.Errorf("Monitor keeps %q", e.Keep)
	}
}

func TestOutput_ReadKeepsHead(t *testing.T) {
	text := numbered(20000)
	for _, tool := range []string{"Read", "Grep", "WebFetch", "Agent", "Edit", "Whatever"} {
		o := outputOfResult(t, tool, text)
		if !o.Truncated || o.Keep != convmodel.KeepHead {
			t.Errorf("%s: truncated=%v keep=%q", tool, o.Truncated, o.Keep)
			continue
		}
		if !strings.HasPrefix(text, o.Text) || !strings.HasPrefix(o.Text, "line 0\nline 1\n") {
			t.Errorf("%s: kept text is not the head", tool)
		}
		if text[len(o.Text)] != '\n' || len(o.Text) > convmodel.MaxOutput {
			t.Errorf("%s: head ends mid-line or over the cap (%d bytes)", tool, len(o.Text))
		}
	}
}

func TestOutput_TotalsCountWholeText(t *testing.T) {
	text := numbered(20000)
	for _, tool := range []string{"Bash", "Read"} {
		o := outputOfResult(t, tool, text)
		if o.TotalLines != 20000 || o.TotalBytes != len(text) {
			t.Errorf("%s: total_lines %d total_bytes %d, want 20000 and %d", tool, o.TotalLines, o.TotalBytes, len(text))
		}
		if strings.Count(o.Text, "\n")+1 >= 20000 {
			t.Errorf("%s: the text was not cut", tool)
		}
	}
}

func TestOutput_Boundaries(t *testing.T) {
	cases := []struct {
		name            string
		text            string
		lines, bytes    int
		truncated       bool
		tool            string
		checkKept       func(t *testing.T, kept string)
		wantKeptExactly *string
	}{
		{name: "empty", text: "", lines: 0, bytes: 0},
		{name: "one line, trailing newline", text: "a\n", lines: 1, bytes: 2},
		{name: "two lines", text: "a\nb", lines: 2, bytes: 3},
		{name: "blank lines count", text: "\n\n", lines: 2, bytes: 2},
		{name: "crlf: \\r stays, two lines", text: "a\r\nb", lines: 2, bytes: 4},
		{name: "6 lines in exactly 1,024 bytes", text: strings.Repeat(strings.Repeat("x", 169)+"\n", 5) + strings.Repeat("y", 1024-5*170), lines: 6, bytes: 1024},
		{name: "exactly 16 KiB is not truncated", text: strings.Repeat(strings.Repeat("x", 99)+"\n", 163) + strings.Repeat("y", 16*kib-163*100), lines: 164, bytes: 16 * kib},
		{name: "16 KiB + 1 is truncated", text: strings.Repeat(strings.Repeat("x", 99)+"\n", 163) + strings.Repeat("y", 16*kib-163*100+1), lines: 164, bytes: 16*kib + 1, truncated: true,
			checkKept: func(t *testing.T, kept string) {
				if strings.Contains(kept, "y") {
					t.Error("head should have stopped before the partial last line")
				}
			}},
		{name: "a single 20 KiB line is cut by bytes (head)", text: strings.Repeat("z", 20*kib), lines: 1, bytes: 20 * kib, truncated: true, tool: "Read",
			checkKept: func(t *testing.T, kept string) {
				if len(kept) != 16*kib {
					t.Errorf("kept %d bytes, want %d", len(kept), 16*kib)
				}
			}},
		{name: "a single 20 KiB line is cut by bytes (tail)", text: strings.Repeat("z", 20*kib), lines: 1, bytes: 20 * kib, truncated: true, tool: "Bash",
			checkKept: func(t *testing.T, kept string) {
				if len(kept) != 16*kib {
					t.Errorf("kept %d bytes, want %d", len(kept), 16*kib)
				}
			}},
		{name: "multi-line text is never joined", text: "one\ntwo\nthree", lines: 3, bytes: 13,
			wantKeptExactly: ptr("one\ntwo\nthree")},
	}
	for _, c := range cases {
		tool := c.tool
		if tool == "" {
			tool = "Read"
		}
		o := outputOfResult(t, tool, c.text)
		if o.TotalLines != c.lines || o.TotalBytes != c.bytes || o.Truncated != c.truncated {
			t.Errorf("%s: lines %d bytes %d truncated %v, want %d %d %v", c.name, o.TotalLines, o.TotalBytes, o.Truncated, c.lines, c.bytes, c.truncated)
		}
		if !c.truncated && (o.Text != c.text || o.Keep != "") {
			t.Errorf("%s: an uncut output was changed (keep %q)", c.name, o.Keep)
		}
		if c.truncated && o.Keep == "" {
			t.Errorf("%s: truncated without keep", c.name)
		}
		if c.checkKept != nil {
			c.checkKept(t, o.Text)
		}
		if c.wantKeptExactly != nil && o.Text != *c.wantKeptExactly {
			t.Errorf("%s: text %q", c.name, o.Text)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestOutput_CutOnLineBoundary(t *testing.T) {
	var lines []string
	for i := range 300 {
		lines = append(lines, fmt.Sprintf("L%04d %s", i, strings.Repeat("x", 93))) // 100 bytes with the newline
	}
	text := strings.Join(lines, "\n") + "\n"
	head := outputOfResult(t, "Read", text)
	tail := outputOfResult(t, "Bash", text)
	for _, line := range strings.Split(head.Text, "\n") {
		if len(line) != 99 && line != "" {
			t.Fatalf("head holds a partial line %q", line)
		}
	}
	for _, line := range strings.Split(tail.Text, "\n") {
		if len(line) != 99 && line != "" {
			t.Fatalf("tail holds a partial line %q", line)
		}
	}
	if !strings.HasPrefix(head.Text, "L0000 ") || !strings.HasPrefix(tail.Text, "L") || !strings.HasSuffix(tail.Text, "\n") {
		t.Errorf("head starts %q / tail starts %q ends %q", head.Text[:6], tail.Text[:6], tail.Text[len(tail.Text)-3:])
	}
	if !strings.HasSuffix(text, tail.Text) || !strings.HasPrefix(text, head.Text) {
		t.Error("kept text is not a head / tail of the whole")
	}
	if text[len(head.Text)] != '\n' {
		t.Error("head does not end before a line break")
	}
	if text[len(text)-len(tail.Text)-1] != '\n' {
		t.Error("tail does not start after a line break")
	}
	if head.TotalLines != 300 || tail.TotalLines != 300 {
		t.Errorf("total_lines %d / %d", head.TotalLines, tail.TotalLines)
	}
}

func TestOutput_LongLineAmongShortOnes(t *testing.T) {
	// a line longer than the cap cannot be kept whole: the cut is by bytes
	// there, after the whole lines before it (head) and before the whole
	// lines after it (tail)
	long := strings.Repeat("z", 20*kib)
	h := outputOfResult(t, "Read", "first\nsecond\n"+long+"\nlast")
	if !strings.HasPrefix(h.Text, "first\nsecond\nzzz") || len(h.Text) != 16*kib {
		t.Errorf("head: %d bytes, starts %q", len(h.Text), h.Text[:20])
	}
	e := outputOfResult(t, "Bash", "first\n"+long+"\nthird\nlast")
	if !strings.HasSuffix(e.Text, "zzz\nthird\nlast") || len(e.Text) != 16*kib {
		t.Errorf("tail: %d bytes, ends %q", len(e.Text), e.Text[len(e.Text)-20:])
	}
	// a long final line with a trailing newline keeps its end, not nothing
	e = outputOfResult(t, "Bash", "first\n"+long+"\n")
	if len(e.Text) != 16*kib || !strings.HasSuffix(e.Text, "zzz\n") {
		t.Errorf("tail of a long last line: %d bytes", len(e.Text))
	}
}

func TestOutput_NoSplitUTF8(t *testing.T) {
	for _, tool := range []string{"Read", "Bash"} {
		for _, shift := range []string{"", "a", "ab", "abc"} { // move the cut across all three offsets of a rune
			text := shift + strings.Repeat("世", 7000)
			o := outputOfResult(t, tool, text)
			if !o.Truncated || !utf8.ValidString(o.Text) || len(o.Text) > convmodel.MaxOutput || len(o.Text) < convmodel.MaxOutput-3 {
				t.Errorf("%s shift %q: %d bytes valid=%v truncated=%v", tool, shift, len(o.Text), utf8.ValidString(o.Text), o.Truncated)
			}
		}
	}
	// and across lines
	text := strings.Repeat("世界你好\n", 3000)
	for _, tool := range []string{"Read", "Bash"} {
		if o := outputOfResult(t, tool, text); !utf8.ValidString(o.Text) {
			t.Errorf("%s: line cut split a rune", tool)
		}
	}
}

func TestOutput_ImagePlaceholderCountedInTotals(t *testing.T) {
	o := outputOfResult(t, "Read", []obj{
		{"type": "text", "text": "hello"}, imgObj("image/png", "AAAA"),
		{"type": "text", "text": "world"}, imgObj("image/jpeg", "AAAAAAAA"),
	})
	const want = "hello\n[image]\nworld\n[image]"
	if o.Text != want || o.TotalLines != 4 || o.TotalBytes != len(want) {
		t.Errorf("text %q lines %d bytes %d", o.Text, o.TotalLines, o.TotalBytes)
	}
	if len(o.Images) != 2 || o.Images[0] != (convmodel.Image{MediaType: "image/png", Bytes: 3}) || o.Images[1] != (convmodel.Image{MediaType: "image/jpeg", Bytes: 6}) {
		t.Errorf("images = %+v", o.Images)
	}
	// an image-only result
	o = outputOfResult(t, "Read", []obj{imgObj("image/png", "AAAA")})
	if o.Text != "[image]" || o.TotalLines != 1 || o.TotalBytes != 7 || len(o.Images) != 1 {
		t.Errorf("image-only: %+v", o)
	}
	// text blocks only: joined by newlines, no images field
	o = outputOfResult(t, "Read", []obj{{"type": "text", "text": "a"}, {"type": "text", "text": "b"}})
	if o.Text != "a\nb" || o.Images != nil {
		t.Errorf("two text blocks: %+v", o)
	}
}

func TestOutput_PersistedOutputUnwrapped(t *testing.T) {
	inner := "Output too large (50KB). Full output saved to: /work/x/tool-results/b1.txt\n\nPreview (first 2KB):\nalpha\nbeta"
	o := outputOfResult(t, "Bash", "<persisted-output>\n"+inner+"\n</persisted-output>")
	if o.Text != inner || o.TotalBytes != len(inner) || o.TotalLines != 5 {
		t.Errorf("text %q lines %d bytes %d", o.Text, o.TotalLines, o.TotalBytes)
	}
	// one layer only, and only a whole block
	nested := "<persisted-output>\n<persisted-output>\nx\n</persisted-output>\n</persisted-output>"
	if o := outputOfResult(t, "Bash", nested); o.Text != "<persisted-output>\nx\n</persisted-output>" {
		t.Errorf("nested: %q", o.Text)
	}
	mid := "before <persisted-output>\nx\n</persisted-output>"
	if o := outputOfResult(t, "Bash", mid); o.Text != mid {
		t.Errorf("mid-text wrapper was touched: %q", o.Text)
	}
	// inside a list of blocks
	if o := outputOfResult(t, "Bash", []obj{{"type": "text", "text": "<persisted-output>\nx\n</persisted-output>"}}); o.Text != "x" {
		t.Errorf("block: %q", o.Text)
	}
}

func TestOutput_ResultWithoutContentHasEmptyOutput(t *testing.T) {
	s := oneStep(t, "Bash", obj{"command": "x"}, resultRow("r1", 3, "toolu_1", nil, false))
	if s.Output == nil || s.Output.Text != "" || s.Output.TotalLines != 0 || s.Output.TotalBytes != 0 {
		t.Errorf("output = %+v", s.Output)
	}
	if s := oneStep(t, "Bash", obj{"command": "x"}, nil); s.Output != nil {
		t.Errorf("output without a result: %+v", s.Output)
	}
}

func TestOutput_UnknownResultBlocksCountedBounded(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "ToolSearch", obj{"query": "x"}),
		resultRow("r1", 3, "toolu_1", []obj{{"type": "tool_reference", "tool_name": "Foo"}, {"type": "text", "text": "ok"}}, false))
	s := stepNamed(t, validated(t, n), "toolu_1")
	if s.Output.Text != "ok" || n.Stats().Skipped["result_block:tool_reference"] != 1 {
		t.Errorf("output %q skipped %v", s.Output.Text, n.Stats().Skipped)
	}
}

func TestSteps_DoesNotRetainImageBase64(t *testing.T) {
	// a 5 MB image in a tool result (M-U1-7: rows up to 2.2 MB are seen; the
	// line cap is 8 MiB) leaves only a placeholder and its decoded size
	b64 := strings.Repeat("QUJD", 5<<20/4)
	row := resultRow("r1", 3, "toolu_1", []obj{imgObj("image/png", b64)}, false,
		toolUseResult(obj{"type": "image", "file": obj{"type": "image/png", "originalSize": 3932160}}))
	c := conv(t, userRow("u1", 1, "go"), toolCall("a1", 2, "toolu_1", "Read", obj{"file_path": "/f.png"}), row)
	s := stepNamed(t, c, "toolu_1")
	if s.Output == nil || s.Output.Text != "[image]" || len(s.Output.Images) != 1 || s.Output.Images[0].Bytes != int64(len(b64)/4*3) {
		t.Fatalf("output = %+v", s.Output)
	}
	if got := jsonOf(t, c); len(got) > 4*kib || strings.Contains(got, "QUJD") {
		t.Errorf("the conversation is %d bytes and holds image data: %.60s", len(got), got)
	}
	if len(row) < 5<<20 {
		t.Fatalf("test row is only %d bytes", len(row))
	}
}

// resultBlocks is a tool_result content of n blocks as raw JSON (marshalling
// tens of thousands of maps would dominate the test); block(i) is the JSON of
// block i.
func resultBlocks(n int, block func(i int) string) json.RawMessage {
	var b strings.Builder
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(block(i))
	}
	b.WriteByte(']')
	return json.RawMessage(b.String())
}

const (
	tinyText  = `{"type":"text","text":"x"}`
	tinyImage = `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}`
)

func TestOutput_ManyResultBlocksTotalsCoverWholeResult(t *testing.T) {
	// totals describe the whole result, not its first 64 blocks
	o := outputOfResult(t, "Read", resultBlocks(65, func(int) string { return tinyText }))
	if want := strings.TrimSuffix(strings.Repeat("x\n", 65), "\n"); o.Text != want || o.TotalLines != 65 || o.TotalBytes != 129 {
		t.Errorf("65 blocks: lines %d bytes %d text %d bytes", o.TotalLines, o.TotalBytes, len(o.Text))
	}

	// 50,000 tiny blocks, every other one an image: bounded work and storage,
	// exact totals (25,000 "x" + 25,000 "[image]" + 49,999 newlines)
	start := time.Now()
	o = outputOfResult(t, "Read", resultBlocks(50000, func(i int) string {
		if i%2 == 1 {
			return tinyImage
		}
		return tinyText
	}))
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v", d)
	}
	if o.TotalLines != 50000 || o.TotalBytes != 25000+25000*len(imagePlaceholder)+49999 {
		t.Errorf("lines %d bytes %d", o.TotalLines, o.TotalBytes)
	}
	if len(o.Text) > convmodel.MaxOutput || !o.Truncated || o.Keep != convmodel.KeepHead {
		t.Errorf("text %d bytes truncated=%v keep=%q", len(o.Text), o.Truncated, o.Keep)
	}
	if len(o.Images) != 64 {
		t.Errorf("%d images stored, want 64", len(o.Images))
	}
	if len(o.Images) > 0 && o.Images[0] != (convmodel.Image{MediaType: "image/png", Bytes: 3}) {
		t.Errorf("image 0 = %+v", o.Images[0])
	}

	// the same, text only
	o = outputOfResult(t, "Read", resultBlocks(50000, func(int) string { return tinyText }))
	if o.TotalLines != 50000 || o.TotalBytes != 99999 || len(o.Text) > convmodel.MaxOutput || o.Images != nil {
		t.Errorf("text only: lines %d bytes %d text %d images %d", o.TotalLines, o.TotalBytes, len(o.Text), len(o.Images))
	}
}

func TestOutput_ImageBeyond64BlocksStillCounted(t *testing.T) {
	o := outputOfResult(t, "Read", resultBlocks(65, func(i int) string {
		if i == 64 {
			return tinyImage
		}
		return tinyText
	}))
	if want := strings.Repeat("x\n", 64) + imagePlaceholder; o.Text != want || o.TotalLines != 65 || o.TotalBytes != len(want) {
		t.Errorf("lines %d bytes %d text %q", o.TotalLines, o.TotalBytes, o.Text)
	}
	if len(o.Images) != 1 || o.Images[0] != (convmodel.Image{MediaType: "image/png", Bytes: 3}) {
		t.Errorf("images = %+v", o.Images)
	}
}
