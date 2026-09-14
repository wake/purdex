package tmux

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// captureLog redirects the standard logger into a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

func TestParseListSessionsOutput(t *testing.T) {
	t.Run("two good lines, spaces preserved", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("$1\tfoo\t/Users/wake\n$2\tbar baz\t/tmp/x y\n")
		want := []TmuxSession{
			{ID: "$1", Name: "foo", Cwd: "/Users/wake"},
			{ID: "$2", Name: "bar baz", Cwd: "/tmp/x y"},
		}
		if len(got) != len(want) {
			t.Fatalf("got %d sessions %+v, want %d", len(got), got, len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("session[%d] = %+v, want %+v", i, got[i], want[i])
			}
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output for well-formed input, got %q", buf.String())
		}
	})

	t.Run("locale-mangled line is skipped and logged", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("$0_probe1_/Users/wake\n")
		if len(got) != 0 {
			t.Fatalf("got %d sessions %+v, want 0", len(got), got)
		}
		out := buf.String()
		if !strings.Contains(out, "malformed line") {
			t.Errorf("log %q does not mention 'malformed line'", out)
		}
		if !strings.Contains(out, "UTF-8 locale") {
			t.Errorf("log %q does not hint at 'UTF-8 locale'", out)
		}
		if !strings.Contains(out, `"$0_probe1_/Users/wake"`) {
			t.Errorf("log %q does not quote the offending line", out)
		}
	})

	t.Run("two-field line is skipped", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("$3\tonly-two-fields\n")
		if len(got) != 0 {
			t.Fatalf("got %d sessions %+v, want 0", len(got), got)
		}
		if !strings.Contains(buf.String(), "1 malformed") {
			t.Errorf("log %q does not report '1 malformed'", buf.String())
		}
	})

	t.Run("blank lines are ignored", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("\n\n$4\tq\t/\n\n")
		if len(got) != 1 {
			t.Fatalf("got %d sessions %+v, want 1", len(got), got)
		}
		if got[0] != (TmuxSession{ID: "$4", Name: "q", Cwd: "/"}) {
			t.Errorf("session = %+v", got[0])
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output, got %q", buf.String())
		}
	})

	t.Run("mixed input logs once with the count", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("$0_a_/x\n$1_b_/y\n$2\tc\t/z\n")
		if len(got) != 1 {
			t.Fatalf("got %d sessions %+v, want 1", len(got), got)
		}
		if got[0].ID != "$2" {
			t.Errorf("session ID = %q, want $2", got[0].ID)
		}
		out := buf.String()
		if n := strings.Count(out, "malformed"); n != 1 {
			t.Errorf("expected exactly one 'malformed' log line, got %d in %q", n, out)
		}
		if !strings.Contains(out, "2 malformed") {
			t.Errorf("log %q does not report '2 malformed'", out)
		}
		if !strings.Contains(out, `"$0_a_/x"`) {
			t.Errorf("log %q does not quote the first offending line", out)
		}
	})

	t.Run("empty output yields nil", func(t *testing.T) {
		buf := captureLog(t)
		if got := parseListSessionsOutput(""); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output, got %q", buf.String())
		}
	})
}
