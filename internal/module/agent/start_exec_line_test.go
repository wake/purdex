package agent

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

func TestStartExecLineIsDifferenceSinceBase(t *testing.T) {
	execstat.Tmux.Reset()
	execstat.PS.Reset()
	defer execstat.Tmux.Reset()
	defer execstat.PS.Reset()

	// Forks before Start must not appear in Start's line.
	execstat.Tmux.Observe(100 * time.Millisecond)
	execstat.PS.Observe(50 * time.Millisecond)
	base := execstat.Take()

	execstat.Tmux.Observe(30 * time.Millisecond)
	execstat.Tmux.Observe(12 * time.Millisecond)
	execstat.PS.Observe(7 * time.Millisecond)

	got := startExecLine(base)
	want := "[agent] start exec: tmux=2(42ms) ps=1(7ms)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStartExecLineZeroWhenNothingForked(t *testing.T) {
	execstat.Tmux.Reset()
	execstat.PS.Reset()
	defer execstat.Tmux.Reset()
	if got := startExecLine(execstat.Take()); got != "[agent] start exec: tmux=0(0ms) ps=0(0ms)" {
		t.Fatalf("got %q", got)
	}
}
