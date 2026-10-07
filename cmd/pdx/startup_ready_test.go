package main

import (
	"testing"
	"time"
)

func TestStartupReadyLine(t *testing.T) {
	got := startupReadyLine(7123*time.Millisecond, 412*time.Millisecond, 6350*time.Millisecond)
	want := "startup: ready in 7123ms (init=412ms start=6350ms, process start to ready)"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
