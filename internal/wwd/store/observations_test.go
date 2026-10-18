package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func obsAt(start time.Time, pid int, sig string) wire.Observation {
	end := start.Add(90 * time.Second)
	return wire.Observation{
		ID: "o_" + sig + "_" + start.Format("150405") + "_" + string(rune('a'+pid%26)), Host: "mlab", Session: "_abc123", Via: wire.ObsViaSession,
		Command: sig + " --run", Signature: sig, StartedAt: start, EndedAt: &end, CPUSeconds: 120, CPUPeak: 300, MemPeakMB: 1500, ProcsPeak: 8,
	}
}

func TestObservationsPersistInOrderAndSurviveReopen(t *testing.T) {
	s, p := open(t)
	late, early := obsAt(t0.Add(time.Hour), 1, "go test"), obsAt(t0, 2, "pnpm vitest")
	for _, o := range []wire.Observation{late, early} { // written out of order
		if err := s.SaveObservation(o); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveObservation(early); err != nil { // the same observation again changes nothing
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.LoadObservations(t0)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d err %v", len(got), err)
	}
	if got[0].ID != early.ID || got[1].ID != late.ID || got[0].Signature != "pnpm vitest" || got[0].EndedAt == nil {
		t.Fatalf("%+v", got)
	}
	// since filters on the start
	if got, _ := s2.LoadObservations(t0.Add(30 * time.Minute)); len(got) != 1 || got[0].ID != late.ID {
		t.Fatalf("since: %+v", got)
	}
}

func TestObservationsOlderThanRetentionArePruned(t *testing.T) {
	s, _ := open(t)
	old := obsAt(t0.Add(-ObservationRetention-time.Hour), 3, "make")
	if err := s.SaveObservation(old); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveObservation(obsAt(t0, 4, "go build")); err != nil { // a save prunes by the newest start
		t.Fatal(err)
	}
	got, err := s.LoadObservations(old.StartedAt.Add(-time.Hour))
	if err != nil || len(got) != 1 || got[0].Signature != "go build" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestObservationsReportAnUndecodableRow(t *testing.T) {
	s, _ := open(t)
	if err := s.SaveObservation(obsAt(t0, 5, "go test")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO observations (id, started_ms, rec) VALUES ('o_bad', ?, 'not json')`, t0.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadObservations(t0)
	var sk *SkippedRowsError
	if !errors.As(err, &sk) || sk.N != 1 || sk.Table != "observations" {
		t.Fatalf("want a skipped-rows error for the observations table, got %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the good row still comes back: %+v", got)
	}
}

// A refused observation is not stored: the table only holds what a reader can decode and validate.
func TestSaveObservationRefusesAnInvalidOne(t *testing.T) {
	s, _ := open(t)
	o := obsAt(t0, 6, "go test")
	o.Via = "docker"
	if err := s.SaveObservation(o); err == nil {
		t.Fatal("an invalid observation must not be stored")
	}
	if got, _ := s.LoadObservations(time.Time{}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// A wwd.db written by the v2 schema (host_minutes, no observations) gains the table on open, keeping what it had.
func TestOpenMigratesV2ToV3(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE host_schema (version INTEGER NOT NULL)`,
		`INSERT INTO host_schema (version) VALUES (2)`,
		`CREATE TABLE worker_settings (id INTEGER PRIMARY KEY CHECK (id = 1), enabled INTEGER NOT NULL, heavy INTEGER NOT NULL, light INTEGER NOT NULL, owner_rule TEXT NOT NULL)`,
		`INSERT INTO worker_settings VALUES (1, 1, 1, 2, 'r')`,
		`CREATE TABLE exceptions (name TEXT PRIMARY KEY, until_ms INTEGER, pid INTEGER, pid_ident TEXT NOT NULL DEFAULT '', holds TEXT NOT NULL, since_ms INTEGER NOT NULL)`,
		`CREATE TABLE host_minutes (at INTEGER PRIMARY KEY, rec TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveObservation(obsAt(t0, 7, "go test")); err != nil {
		t.Fatalf("observations after migration: %v", err)
	}
	if ws, ok, err := s.LoadWorker(); err != nil || !ok || ws.OwnerRule != "r" {
		t.Fatalf("old rows kept: %+v %v %v", ws, ok, err)
	}
	var v int
	if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != 3 {
		t.Fatalf("version %d err %v", v, err)
	}
}
