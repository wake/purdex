package store

import (
	"testing"
	"time"
)

// #3843: the crash-loop rule counts the starts that follow a run that did not end on request, not every start.

func TestUncleanStartsSinceCountsOnlyTheStartsAfterARunThatDidNotEndCleanly(t *testing.T) {
	s := openStarts(t)
	for i, prev := range []string{PrevExitFirst, PrevExitClean, PrevExitUnclean, PrevExitClean, PrevExitUnclean, PrevExitUnknown, ""} {
		if err := s.RecordStartAt(st0.Add(time.Duration(i-10)*time.Minute), "v1", prev); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.UncleanStartsSince(st0.Add(-time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v: two starts followed a run that did not end cleanly", n, err)
	}
	got, err := s.UncleanStartTimes(st0.Add(-time.Hour))
	if err != nil || len(got) != 2 || !got[0].Equal(st0.Add(-8*time.Minute)) || !got[1].Equal(st0.Add(-6*time.Minute)) {
		t.Fatalf("%v %v", got, err)
	}
	if n, _ := s.UncleanStartsSince(st0.Add(-7 * time.Minute)); n != 1 {
		t.Fatalf("n=%d: the window cuts", n)
	}
	// the plain count of starts is unchanged
	if all, _ := s.StartsSince(st0.Add(-time.Hour)); all != 7 {
		t.Fatalf("all=%d", all)
	}
}

// LastStart is the start recorded last (by insertion, not by the time it carries: a clock that went back must not make an older run the last one).
func TestLastStartIsTheLastRecordedNotTheLatestStamp(t *testing.T) {
	s := openStarts(t)
	if _, ok, err := s.LastStart(); ok || err != nil {
		t.Fatalf("ok=%v err=%v: nothing recorded yet", ok, err)
	}
	if err := s.RecordStartAt(st0, "v1", PrevExitFirst); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordStartAt(st0.Add(-time.Hour), "v1", PrevExitClean); err != nil { // the clock went back
		t.Fatal(err)
	}
	last, ok, err := s.LastStart()
	if err != nil || !ok || !last.At.Equal(st0.Add(-time.Hour)) || last.PrevExit != PrevExitClean {
		t.Fatalf("%+v ok=%v err=%v", last, ok, err)
	}
}
