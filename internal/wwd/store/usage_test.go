package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func usageEnding(id string, end time.Time) wire.JobUsage {
	no := false
	return wire.JobUsage{
		JobID: id, Host: "air26", Repo: "purdex", Task: "go-test", Slot: wire.SlotHeavy, Class: wire.ClassDev,
		Source: wire.UsageQueue, Result: wire.StateSucceeded,
		StartedAt: end.Add(-time.Minute), EndedAt: end, DurationS: 60,
		CPUSeconds: 120, CPUSource: wire.CPUFromRusage, AvgCores: 2, PeakCores: 3, PeakCoresMeasure: wire.MeasureSampled,
		MemPeakMB: 900, MemPeakMeasure: wire.MeasureSampled,
		JobsAtStart: 0, JobsPeak: 1, ZoneAtStart: wire.ZoneNormal, ZoneWorst: wire.ZoneNormal, OwnerAtStart: &no, OwnerSeen: &no,
	}
}

func saveUsageNow(s *Store, u wire.JobUsage) error { return s.SaveUsageAt(u, t0.Add(time.Hour)) }

func TestUsagePersistsInOrderAndSurvivesReopen(t *testing.T) {
	s, p := open(t)
	late, early := usageEnding("j_late", t0.Add(30*time.Minute)), usageEnding("j_early", t0)
	for _, u := range []wire.JobUsage{late, early} { // written out of order
		if err := saveUsageNow(s, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := saveUsageNow(s, early); err != nil { // the same job again changes nothing
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, more, err := s2.LoadUsage(t0, 10)
	if err != nil || more || len(got) != 2 || got[0].JobID != "j_early" || got[1].JobID != "j_late" || got[0].OwnerSeen == nil {
		t.Fatalf("%+v more %v err %v", got, more, err)
	}
	if got, _, _ := s2.LoadUsage(t0.Add(time.Minute), 10); len(got) != 1 || got[0].JobID != "j_late" {
		t.Fatalf("since: %+v", got)
	}
}

func TestUsageLimitSaysThereIsMore(t *testing.T) {
	s, _ := open(t)
	for i := 0; i < 5; i++ {
		if err := saveUsageNow(s, usageEnding(fmt.Sprintf("j_%d", i), t0.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	got, more, err := s.LoadUsage(t0, 3)
	if err != nil || !more || len(got) != 3 || got[0].JobID != "j_0" || got[2].JobID != "j_2" {
		t.Fatalf("%+v more %v err %v", got, more, err)
	}
	got, more, _ = s.LoadUsage(t0, 5)
	if more || len(got) != 5 {
		t.Fatalf("exactly the limit is not more: %d %v", len(got), more)
	}
}

// Two jobs ending in the same millisecond come in the same order every time (by job id).
func TestUsageTiesOrderByJobID(t *testing.T) {
	s, _ := open(t)
	for _, id := range []string{"j_b", "j_a", "j_c"} {
		if err := saveUsageNow(s, usageEnding(id, t0)); err != nil {
			t.Fatal(err)
		}
	}
	got, _, _ := s.LoadUsage(t0, 10)
	if len(got) != 3 || got[0].JobID != "j_a" || got[1].JobID != "j_b" || got[2].JobID != "j_c" {
		t.Fatalf("%+v", got)
	}
}

// The retention is 30 days, counted from the clock and not from the row: a wrong time on a row cannot erase the history or revive
// what has aged out.
func TestUsageRetentionFollowsTheClock(t *testing.T) {
	s, _ := open(t)
	now := t0.Add(31 * 24 * time.Hour)
	if err := s.SaveUsageAt(usageEnding("j_old", t0), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveUsageAt(usageEnding("j_new", now.Add(-time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.LoadUsage(t0.Add(-time.Hour), 10); len(got) != 1 || got[0].JobID != "j_new" {
		t.Fatalf("the old row is pruned by the clock: %+v", got)
	}
	// a row already past the retention is not stored; one ending in the far future is refused
	if err := s.SaveUsageAt(usageEnding("j_stale", now.Add(-UsageRetention-time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveUsageAt(usageEnding("j_future", now.Add(30*24*time.Hour)), now); err == nil {
		t.Fatal("a job that ends a month ahead was stored")
	}
	if got, _, _ := s.LoadUsage(t0.Add(-time.Hour), 10); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
}

func TestSaveUsageRefusesAnInvalidRow(t *testing.T) {
	s, _ := open(t)
	u := usageEnding("j_x", t0)
	u.CPUSource = "guess"
	if err := saveUsageNow(s, u); err == nil {
		t.Fatal("an invalid row was stored")
	}
	if got, _, _ := s.LoadUsage(t0.Add(-time.Hour), 10); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestUsageReportsAnUndecodableRow(t *testing.T) {
	s, _ := open(t)
	if err := saveUsageNow(s, usageEnding("j_ok", t0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO job_usage (job_id, ended_ms, rec) VALUES ('j_bad', ?, 'not json')`, t0.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.LoadUsage(t0.Add(-time.Hour), 10)
	var skipped *SkippedRowsError
	if !errors.As(err, &skipped) || skipped.N != 1 || skipped.Table != "job_usage" || len(got) != 1 || got[0].JobID != "j_ok" {
		t.Fatalf("%+v err %v", got, err)
	}
}

// A wwd.db made before this table (schema 3) gets it on open, and opening again changes nothing.
func TestUsageTableIsAddedToAnOlderDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP TABLE job_usage`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE host_schema SET version = 3`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for i := 0; i < 2; i++ { // the migration can be run again
		s, err = Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if err := saveUsageNow(s, usageEnding("j_1", t0)); err != nil && i == 0 {
			t.Fatal(err)
		}
		var v int
		if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != schemaVersion {
			t.Fatalf("version %d err %v", v, err)
		}
		s.Close()
	}
	if schemaVersion < 4 {
		t.Fatalf("the usage table is host schema 4 or later: %d", schemaVersion)
	}
}
