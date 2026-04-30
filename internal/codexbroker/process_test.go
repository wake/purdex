package codexbroker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "ps", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

// TestPsLister_ParsesMlabFormat parses the canned macOS-style fixture and
// verifies fields are extracted correctly.
func TestPsLister_ParsesMlabFormat(t *testing.T) {
	src := loadFixture(t, "one-broker.txt")
	rows, err := ParsePsOutput(src)
	if err != nil {
		t.Fatalf("ParsePsOutput: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	first := rows[0]
	if first.PID != 12345 {
		t.Errorf("PID = %d, want 12345", first.PID)
	}
	if first.PPID != 1 {
		t.Errorf("PPID = %d, want 1", first.PPID)
	}
	if first.RSSKB != 36000 {
		t.Errorf("RSSKB = %d, want 36000", first.RSSKB)
	}
	if !strings.Contains(first.Cmdline, "app-server-broker.mjs") {
		t.Errorf("Cmdline missing broker token: %q", first.Cmdline)
	}
	want := time.Date(2026, 5, 1, 13, 14, 15, 0, first.Lstart.Location())
	if !first.Lstart.Equal(want) {
		t.Errorf("Lstart = %v, want %v", first.Lstart, want)
	}
}

// TestPsLister_LstartFormats parses both macOS and Linux lstart formats.
func TestPsLister_LstartFormats(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		wantPID int
	}{
		{"macos", "one-broker.txt", 12345},
		{"linux", "linux-format.txt", 12345},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := loadFixture(t, tt.fixture)
			rows, err := ParsePsOutput(src)
			if err != nil {
				t.Fatalf("ParsePsOutput: %v", err)
			}
			if len(rows) == 0 || rows[0].PID != tt.wantPID {
				t.Fatalf("rows[0].PID = %v (rows=%v)", rows, rows)
			}
			if rows[0].Lstart.IsZero() {
				t.Errorf("Lstart was zero — parse failed silently")
			}
		})
	}
}

// TestPsLister_LstartMalformedReturnsError ensures a row with unrecognised
// lstart format produces a per-row LstartParseErr not a whole-scan failure.
func TestPsLister_LstartMalformedReturnsError(t *testing.T) {
	bad := "555 1 abc def ghi 1000 node /scripts/app-server-broker.mjs serve --cwd /tmp/x"
	rows, err := ParsePsOutput(bad)
	if err != nil {
		t.Fatalf("ParsePsOutput should not return whole-scan error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].LstartParseErr == "" {
		t.Errorf("expected LstartParseErr to be populated; row=%+v", rows[0])
	}
	if rows[0].PID != 555 {
		t.Errorf("PID = %d, want 555", rows[0].PID)
	}
}

// TestFakeLister_FromFixture provides the canned-text process lister used by
// other tests; verify it round-trips a fixture correctly.
func TestFakeLister_FromFixture(t *testing.T) {
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	rows, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("FakeLister returned %d rows, want 3", len(rows))
	}
}

// TestFakeLister_RespectsContext: ctx cancelled → caller observes ctx.Err.
func TestFakeLister_RespectsContext(t *testing.T) {
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := lister.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected ctx.Canceled, got %v", err)
	}
}

// TestFakeLister_InjectedDelay verifies the lister honours an injected delay
// and a deadline can interrupt it.
func TestFakeLister_InjectedDelay(t *testing.T) {
	src := loadFixture(t, "one-broker.txt")
	lister := NewFakeProcessListerFromText(src).WithDelay(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := lister.List(ctx)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Errorf("List blocked past deadline: %v", time.Since(start))
	}
}
