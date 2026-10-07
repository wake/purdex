package daemonclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

// WithGrace shortens the hard bound: against a port nobody listens on, Do
// gives up at exactly the given grace (the last sleep cut to the
// remainder), not at the 30 s default. `pdx hook` depends on this (spec
// §6.6: a 5 s grace, "a session must not stall on a daemon restart").
func TestDo_WithGraceShortensTheHardBound(t *testing.T) {
	addr := freeAddr(t) // nothing listens
	clock := newFakeClock()
	start := clock.now()
	c := newTestClient("http://"+addr, clock, io.Discard, WithGrace(5*time.Second))

	_, err := c.Do(context.Background(), http.MethodPost, "/api/hooks/decide", map[string]string{"x": "y"}, nil, Idempotent())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := clock.elapsed(start); got != 5*time.Second {
		t.Fatalf("elapsed = %v, want exactly 5s", got)
	}
	// 0.25 + 0.5 + 1 + 1 + 1 + 1 = 4.75 s, then the remainder 0.25 s.
	if n := len(clock.sleeps); n != 7 || clock.sleeps[n-1] != 250*time.Millisecond {
		t.Fatalf("sleeps = %v, want 7 ending in 250ms", clock.sleeps)
	}
}

// Zero or negative is ignored: the default stays.
func TestWithGrace_IgnoresNonPositive(t *testing.T) {
	c := New("http://127.0.0.1:1", "t", WithGrace(0))
	if c.grace != Grace {
		t.Fatalf("grace = %v, want %v", c.grace, Grace)
	}
	c = New("http://127.0.0.1:1", "t", WithGrace(-time.Second))
	if c.grace != Grace {
		t.Fatalf("grace = %v, want %v", c.grace, Grace)
	}
}
