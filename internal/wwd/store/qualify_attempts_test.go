package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestQualifyAttemptsRoundTripReplaceAndDelete(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	a := QualifyAttempt{Label: "task:purdex/tsc", Outcome: "mismatch", Fingerprint: map[string]string{"node": "24"}, At: at}
	if err := s.SaveQualifyAttempt(a); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveQualifyAttempt(QualifyAttempt{Label: "task:purdex/lint", Outcome: "void", At: at}); err != nil {
		t.Fatal(err)
	}
	a.Outcome, a.At = "void", at.Add(time.Hour)
	if err := s.SaveQualifyAttempt(a); err != nil { // a second attempt of the same task replaces the first
		t.Fatal(err)
	}
	got, err := s.QualifyAttempts()
	if err != nil || len(got) != 2 || got[0].Label != "task:purdex/lint" || got[1].Label != "task:purdex/tsc" {
		t.Fatalf("%+v %v", got, err)
	}
	if !reflect.DeepEqual(got[1], a) {
		t.Fatalf("got %+v, want %+v", got[1], a)
	}
	if err := s.DeleteQualifyAttempt("task:purdex/tsc"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteQualifyAttempt("task:purdex/none"); err != nil {
		t.Fatalf("deleting a row that is not there: %v", err)
	}
	if got, _ = s.QualifyAttempts(); len(got) != 1 || got[0].Label != "task:purdex/lint" {
		t.Fatalf("%+v", got)
	}
}

func TestQualifyAttemptsSkipsADamagedRowAndTellsIt(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "wwd.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveQualifyAttempt(QualifyAttempt{Label: "task:a/good", Outcome: "void", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO qualify_attempts (label, outcome, fingerprint, at_ms) VALUES ('task:a/bad', 'void', 'not json', 0)`); err != nil {
		t.Fatal(err)
	}
	got, err := s.QualifyAttempts()
	var skipped *SkippedRowsError
	if !errors.As(err, &skipped) || skipped.N != 1 || len(got) != 1 || got[0].Label != "task:a/good" {
		t.Fatalf("%+v %v", got, err)
	}
}

// The v7 file (what 761 runs) gains the table on open, keeps the row a second open finds, and opening again changes nothing.
func TestOpenMigratesV7ToV8(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wwd.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE qualify_attempts`, `UPDATE host_schema SET version = 7`} {
		if _, err := s.DB().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	for round := 1; round <= 2; round++ {
		s, err := Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", round, err)
		}
		cols := tableCols(t, s, "qualify_attempts")
		for _, c := range []string{"label", "outcome", "fingerprint", "at_ms"} {
			if !cols[c] {
				t.Errorf("open %d: no column %s in %v", round, c, cols)
			}
		}
		if round == 1 {
			if err := s.SaveQualifyAttempt(QualifyAttempt{Label: "task:a/b", Outcome: "void", At: time.Now()}); err != nil {
				t.Fatal(err)
			}
		} else if got, err := s.QualifyAttempts(); err != nil || len(got) != 1 {
			t.Errorf("open 2 lost the row: %+v %v", got, err)
		}
		var v int
		if err := s.DB().QueryRow(`SELECT version FROM host_schema`).Scan(&v); err != nil || v != schemaVersion {
			t.Errorf("open %d: version %d %v", round, v, err)
		}
		s.Close()
	}
}
