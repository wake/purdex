package store

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func minuteRec(at time.Time, load float64) wire.Facts {
	return wire.Facts{V: wire.FactsProto, HostID: "h1", Alias: "mlab", At: at, CPU: wire.CPU{Load1: load}}
}

func TestMinutesPersistReadBackInOrderAndSurviveReopen(t *testing.T) {
	s, p := open(t)
	for i := 2; i >= 0; i-- { // written out of order, read back by time
		if err := s.SaveMinute(minuteRec(t0.Add(time.Duration(i)*time.Minute), float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	// the first write of a minute stays: a clock set back, then a restart, must not
	// replace a whole minute with a part of it
	if err := s.SaveMinute(minuteRec(t0.Add(time.Minute), 9)); err != nil {
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
	if !got[0].At.Equal(t0.Add(time.Minute)) || got[0].CPU.Load1 != 1 || got[1].CPU.Load1 != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestNewestMinute(t *testing.T) {
	s, _ := open(t)
	if _, ok, err := s.NewestMinute(); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	for _, i := range []int{0, 5, 2} {
		if err := s.SaveMinute(minuteRec(t0.Add(time.Duration(i)*time.Minute), 1)); err != nil {
			t.Fatal(err)
		}
	}
	at, ok, err := s.NewestMinute()
	if err != nil || !ok || !at.Equal(t0.Add(5*time.Minute)) {
		t.Fatalf("%v %v %v", at, ok, err)
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
