package store

import (
	"errors"
	"testing"
	"time"
)

// A row that no longer decodes is reported, not silently dropped, and the rest
// of the minutes still come back.
func TestLoadMinutesReportsUndecodableRows(t *testing.T) {
	s, _ := open(t)
	if err := s.SaveMinute(minuteRec(t0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO host_minutes (at, rec) VALUES (?, 'not json')`, t0.Add(time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMinute(minuteRec(t0.Add(2*time.Minute), 3)); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadMinutes(t0)
	var sk *SkippedRowsError
	if !errors.As(err, &sk) || sk.N != 1 {
		t.Fatalf("want a skipped-rows error for 1 row, got %v", err)
	}
	if len(got) != 2 || got[1].CPU.Load1 != 3 {
		t.Fatalf("the good minutes still come back: %+v", got)
	}
}
