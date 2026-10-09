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
		got := parseListSessionsOutput("$1\tfoo\t1700000001\t/Users/wake\n$2\tbar baz\t1700000002\t/tmp/x y\n")
		want := []TmuxSession{
			{ID: "$1", Name: "foo", Created: 1700000001, Cwd: "/Users/wake"},
			{ID: "$2", Name: "bar baz", Created: 1700000002, Cwd: "/tmp/x y"},
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
		got := parseListSessionsOutput("$3\tonly-two-fields\t7\n")
		if len(got) != 0 {
			t.Fatalf("got %d sessions %+v, want 0", len(got), got)
		}
		if !strings.Contains(buf.String(), "1 malformed") {
			t.Errorf("log %q does not report '1 malformed'", buf.String())
		}
	})

	t.Run("blank lines are ignored", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("\n\n$4\tq\t5\t/\n\n")
		if len(got) != 1 {
			t.Fatalf("got %d sessions %+v, want 1", len(got), got)
		}
		if got[0] != (TmuxSession{ID: "$4", Name: "q", Created: 5, Cwd: "/"}) {
			t.Errorf("session = %+v", got[0])
		}
		if buf.Len() != 0 {
			t.Errorf("expected no log output, got %q", buf.String())
		}
	})

	t.Run("mixed input logs once with the count", func(t *testing.T) {
		buf := captureLog(t)
		got := parseListSessionsOutput("$0_a_/x\n$1_b_/y\n$2\tc\t9\t/z\n")
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

func TestParseListSessionsOutput_Created(t *testing.T) {
	t.Run("a path holding a TAB stays in the last field", func(t *testing.T) {
		got := parseListSessionsOutput("$1\tfoo\t42\t/tmp/a\tb\n")
		if len(got) != 1 || got[0].Created != 42 || got[0].Cwd != "/tmp/a\tb" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("a locale-mangled four-field line is skipped and logged, not half-parsed", func(t *testing.T) {
		// No UTF-8 locale: tmux sanitises every TAB of the -F output to "_", so the line is one field.
		buf := captureLog(t)
		got := parseListSessionsOutput("$0_probe1_1700000000_/Users/wake\n$1_name_with_underscores_1700000001_/tmp\n")
		if len(got) != 0 {
			t.Fatalf("got %d sessions %+v, want 0", len(got), got)
		}
		if !strings.Contains(buf.String(), "2 malformed") || !strings.Contains(buf.String(), "UTF-8 locale") {
			t.Errorf("log %q does not report the mangled lines with the locale hint", buf.String())
		}
	})
	t.Run("a session name with underscores and spaces is kept whole", func(t *testing.T) {
		got := parseListSessionsOutput("$5\tmy_sess name\t9\t/p\n")
		if len(got) != 1 || got[0].Name != "my_sess name" || got[0].Created != 9 || got[0].Cwd != "/p" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("an unreadable creation time is 0 (unknown), the session is kept", func(t *testing.T) {
		got := parseListSessionsOutput("$1\tfoo\tx\t/p\n")
		if len(got) != 1 || got[0].Created != 0 || got[0].Name != "foo" {
			t.Fatalf("got %+v", got)
		}
	})
}
