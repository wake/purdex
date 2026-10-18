package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var st0 = time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC)

func openStarts(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Spec §13.1/§13.3: "starts in the last hour" is what the crash-loop rule reads (>= 3 within an hour).
func TestStartsSinceCountsTheStartsInTheWindow(t *testing.T) {
	s := openStarts(t)
	for _, at := range []time.Duration{-3 * time.Hour, -50 * time.Minute, -20 * time.Minute, -time.Second, 0} {
		if err := s.RecordStartAt(st0.Add(at), "v1", ""); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.StartsSince(st0.Add(-time.Hour))
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v: the one 3 hours ago is outside the hour", n, err)
	}
	if n, _ := s.StartsSince(st0.Add(time.Minute)); n != 0 {
		t.Fatalf("n=%d", n)
	}
}

func TestRecordStartKeepsVersionAndPreviousExit(t *testing.T) {
	s := openStarts(t)
	if err := s.RecordStartAt(st0, "0.1.0-alpha.755", "signal: killed"); err != nil {
		t.Fatal(err)
	}
	var v, prev string
	if err := s.DB().QueryRow(`SELECT version, prev_exit FROM wwd_starts`).Scan(&v, &prev); err != nil || v != "0.1.0-alpha.755" || prev != "signal: killed" {
		t.Fatalf("%q %q %v", v, prev, err)
	}
	// both are one line of plain text: a text that cannot be one is cut, not refused (a start must always be recorded)
	if err := s.RecordStartAt(st0.Add(time.Second), "v\nx", strings.Repeat("e", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := s.DB().QueryRow(`SELECT version, prev_exit FROM wwd_starts WHERE started_ms = ?`, st0.Add(time.Second).UnixMilli()).Scan(&v, &prev); err != nil || strings.ContainsAny(v, "\n") || len(prev) > 200 {
		t.Fatalf("%q %d %v", v, len(prev), err)
	}
}

// A start is recorded once however many times its write is tried (the retry of a transient failure uses the same stamp): the second
// try changes nothing, and does not fail.
func TestRecordStartIsIdempotentForTheSameStart(t *testing.T) {
	s := openStarts(t)
	for i := 0; i < 3; i++ {
		if err := s.RecordStartAt(st0, "v1", ""); err != nil {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if n, _ := s.StartsSince(st0.Add(-time.Hour)); n != 1 {
		t.Fatalf("n=%d: a retried start is counted once", n)
	}
	if err := s.RecordStartAt(st0.Add(time.Millisecond), "v1", ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.StartsSince(st0.Add(-time.Hour)); n != 2 {
		t.Fatalf("n=%d: another instant is another start", n)
	}
}

// The table is a short history for the rule, not a log: rows older than a week go on the next start.
func TestRecordStartPrunesOldRows(t *testing.T) {
	s := openStarts(t)
	if err := s.RecordStartAt(st0.Add(-8*24*time.Hour), "old", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordStartAt(st0, "new", ""); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM wwd_starts`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
