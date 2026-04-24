package tmux

import "testing"

func TestSanitizeTmuxMetadata(t *testing.T) {
	input := "hello\tworld\nnext\rline\x00\x1b[31m\x7f\u0085done"
	got := sanitizeTmuxMetadata(input)
	want := "hello world next line [31m done"
	if got != want {
		t.Fatalf("sanitizeTmuxMetadata() = %q, want %q", got, want)
	}
}

func TestActivePaneTarget(t *testing.T) {
	got := activePaneTarget("dev")
	want := "=dev:"
	if got != want {
		t.Fatalf("activePaneTarget() = %q, want %q", got, want)
	}
}
