package resources

import (
	"testing"
	"time"
)

func TestUpdateEWMA_FirstSampleIsTheSample(t *testing.T) {
	if got := UpdateEWMA(99, 12, time.Second, 15*time.Second, true); got != 12 {
		t.Fatalf("first = %v, want 12", got)
	}
}

func TestUpdateEWMA_HalfLife(t *testing.T) {
	// After one half-life a step from 0 to 40 has covered half the way.
	if got := UpdateEWMA(0, 40, 15*time.Second, 15*time.Second, false); !near(got, 20) {
		t.Fatalf("after one half-life = %v, want 20", got)
	}
	if got := UpdateEWMA(0, 40, 30*time.Second, 15*time.Second, false); !near(got, 30) {
		t.Fatalf("after two half-lives = %v, want 30", got)
	}
}

func TestUpdateEWMA_NoTimeNoMove(t *testing.T) {
	if got := UpdateEWMA(10, 40, 0, 15*time.Second, false); got != 10 {
		t.Fatalf("dt 0 = %v, want 10", got)
	}
	if got := UpdateEWMA(10, 40, -time.Second, 15*time.Second, false); got != 10 {
		t.Fatalf("dt < 0 = %v, want 10 (a clock that went back does not move it)", got)
	}
}

func TestUpdateEWMA_NoHalfLifeTakesTheSample(t *testing.T) {
	if got := UpdateEWMA(10, 40, time.Second, 0, false); got != 40 {
		t.Fatalf("half-life 0 = %v, want the sample", got)
	}
}
