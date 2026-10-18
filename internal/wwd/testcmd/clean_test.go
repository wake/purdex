package testcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":                    "plain text",
		"non-ascii 測試 ok é":             "non-ascii 測試 ok é",
		"line1\nline2\tcol":             "line1\nline2\tcol",
		"\x1b[2J\x1b[Hgone":             `\x1b[2J\x1b[Hgone`,
		"status ok\rFAILED hidden":      `status ok\x0dFAILED hidden`,
		"bell\x07 nul\x00 del\x7f":      `bell\x07 nul\x00 del\x7f`,
		"\x1b]0;title\x07":              `\x1b]0;title\x07`,
		"c1 \u009b31m":                  `c1 \x9b31m`,
		"bidi \u202eevil \u2066 end":    `bidi \u202eevil \u2066 end`,
		"zero\u200bwidth and bom\ufeff": `zero\u200bwidth and bom\ufeff`,
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CleanLine("a\nb\tc\x1b"); got != `a b c\x1b` {
		t.Errorf("CleanLine = %q", got)
	}
}

func TestPrintDoesNotSendJobTextToTheTerminal(t *testing.T) {
	r := Report{
		ID: "j_1\x1b[31m", Task: "lint\x1b[2J", State: wire.StateFailed, Host: "air26\r", Reason: "oom\x07",
		Summary: &wire.Summary{
			Failed:   1,
			Failures: []wire.Failure{{Name: "Test\x1b[0m", File: "f.go\x1b", Message: "boom\x1b]0;x\x07\nline2"}},
			Tail:     []string{"tail\x1b[2K"},
		},
		LogPath: "/tmp/x\x1b", Notes: []string{"note\x1b"},
	}
	var b bytes.Buffer
	Print(&b, r, false)
	if strings.ContainsRune(b.String(), '\x1b') || strings.ContainsRune(b.String(), '\r') || strings.ContainsRune(b.String(), '\x07') {
		t.Fatalf("control characters reached the terminal output: %q", b.String())
	}
	if !strings.Contains(b.String(), `\x1b`) || !strings.Contains(b.String(), "line2") {
		t.Fatalf("the text should be shown escaped, not dropped:\n%s", b.String())
	}
	if r.Summary.Failures[0].Name != "Test\x1b[0m" {
		t.Fatal("Print changed the report it was given")
	}
	// JSON keeps the data as it is (it is data for a program, and JSON escapes control characters itself)
	var j bytes.Buffer
	Print(&j, r, true)
	if strings.ContainsRune(j.String(), '\x1b') || !strings.Contains(j.String(), `\u001b`) {
		t.Fatalf("json = %q", j.String())
	}
}
