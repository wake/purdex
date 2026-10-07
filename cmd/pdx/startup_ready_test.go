package main

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/execstat"
)

func TestStartupReadyLine(t *testing.T) {
	ex := execstat.Stats{TmuxN: 31, TmuxD: 1840 * time.Millisecond, PSN: 12, PSD: 960 * time.Millisecond}
	got := startupReadyLine(7123*time.Millisecond, 412*time.Millisecond, 6350*time.Millisecond, ex)
	want := "startup: ready in 7123ms (init=412ms start=6350ms, process start to ready) exec: tmux=31(1840ms) ps=12(960ms)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
