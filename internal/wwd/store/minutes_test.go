package store

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func minuteRec(at time.Time, load float64) wire.Facts {
	return wire.Facts{V: wire.Proto, HostID: "h1", Alias: "mlab", At: at, CPU: wire.CPU{Load1: load}}
}

func TestMinutesPersistReadBackInOrderAndSurviveReopen(t *testing.T) {
	s, p := open(t)
	for i := 2; i >= 0; i-- { // written out of order, read back by time
		if err := s.SaveMinute(minuteRec(t0.Add(time.Duration(i)*time.Minute), float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveMinute(minuteRec(t0.Add(time.Minute), 9)); err != nil { // the same minute replaces
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.LoadMinutes(t0.Add(time.Minute))
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d err %v", len(got), err)
	}
	if !got[0].At.Equal(t0.Add(time.Minute)) || got[0].CPU.Load1 != 9 || got[1].CPU.Load1 != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestMinutesOlderThanRetentionArePruned(t *testing.T) {
	s, _ := open(t)
	old := t0.Add(-MinuteRetention - time.Hour)
	if err := s.SaveMinute(minuteRec(old, 1)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMinute(minuteRec(t0, 2)); err != nil { // a save prunes by the newest minute's time
		t.Fatal(err)
	}
	got, err := s.LoadMinutes(old.Add(-time.Hour))
	if err != nil || len(got) != 1 || got[0].CPU.Load1 != 2 {
		t.Fatalf("got %+v err %v", got, err)
	}
}
