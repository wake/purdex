package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// S3-3a: ActiveAlerts leaves out a row of a kind or level this build does not know (a later release wrote it and wwd was started back on
// this one), says so once, and gives the rest. Any other bad row is still an error.

func insertAlert(t *testing.T, s *Store, key, kind, level, detail string) {
	t.Helper()
	at := time.Date(2026, 10, 21, 4, 0, 0, 0, time.UTC)
	if _, err := s.DB().Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms, cleared_ms, detail) VALUES (?, ?, ?, ?, ?, NULL, ?)`,
		key, kind, level, at.UnixMilli(), at.Add(time.Minute).UnixMilli(), detail); err != nil {
		t.Fatal(err)
	}
}

func TestActiveAlertsLeavesOutAnUnknownKindAndLevelAndSaysSoOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mu sync.Mutex
	var lines []string
	s.SetLogf(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	insertAlert(t, s, "a", "thrash", "digest", "")
	insertAlert(t, s, "b", "meltdown", "push", "")       // a kind of a later release
	insertAlert(t, s, "c", "disk_low", "urgent", "")     // a level of a later release
	insertAlert(t, s, "d", "queue_stalled", "watch", "") // a kind of this release (S3-3b's)

	for round := 0; round < 3; round++ {
		got, err := s.ActiveAlerts()
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Key != "a" || got[1].Key != "d" {
			t.Fatalf("round %d: the rest of the list: %+v", round, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 2 || !strings.Contains(lines[0], `kind "meltdown"`) || !strings.Contains(lines[1], `level "urgent"`) {
		t.Fatalf("said once for each, however often the list is read: %q", lines)
	}
}

func TestActiveAlertsStillRefusesAnyOtherBadRow(t *testing.T) {
	for name, row := range map[string][4]string{
		"an empty key":         {"", "thrash", "push", ""},
		"a key too long":       {strings.Repeat("k", 201), "thrash", "push", ""},
		"unknown, bad key too": {"", "meltdown", "push", ""}, // the key is checked first
	} {
		s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
		if err != nil {
			t.Fatal(err)
		}
		s.SetLogf(func(string, ...any) {})
		insertAlert(t, s, row[0], row[1], row[2], row[3])
		insertAlert(t, s, "ok", "thrash", "push", "")
		if got, err := s.ActiveAlerts(); err == nil {
			t.Errorf("%s: no error, got %+v", name, got)
		}
		s.Close()
	}
}
