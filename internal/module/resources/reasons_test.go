package resourcesmod

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

var rT0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func parseReasons(t *testing.T, s string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	require.NoError(t, json.Unmarshal([]byte(s), &out), s)
	return out
}

// The time between two passes belongs to the reason that was in force during
// it: 20 s of danger, then 10 s of rising, reads exactly that.
func TestReasons_ChargesThePreviousReason(t *testing.T) {
	var w waitReasons
	w.observe("a", resources.ReasonDanger, rT0)
	w.observe("a", resources.ReasonDanger, rT0.Add(10*time.Second))
	w.observe("a", resources.ReasonRising, rT0.Add(20*time.Second))
	got := parseReasons(t, w.peek("a", rT0.Add(30*time.Second)))
	assert.Equal(t, map[string]int64{"danger": 20000, "rising": 10000}, got)
}

// The reason that closes the wait is charged up to the moment of the close.
func TestReasons_PeekCountsTheOpenInterval(t *testing.T) {
	var w waitReasons
	w.observe("a", resources.ReasonGap, rT0)
	assert.Equal(t, map[string]int64{"gap": 4000}, parseReasons(t, w.peek("a", rT0.Add(4*time.Second))))
	// peek does not move the clock: a second peek later counts the same interval again from since.
	assert.Equal(t, map[string]int64{"gap": 9000}, parseReasons(t, w.peek("a", rT0.Add(9*time.Second))))
}

// A row nobody observed has nothing to write; one observed once and granted
// at once writes an empty map.
func TestReasons_UnknownAndEmpty(t *testing.T) {
	var w waitReasons
	assert.Equal(t, "", w.peek("nobody", rT0))
	w.observe("a", resources.ReasonBehind, rT0)
	assert.Equal(t, map[string]int64{}, parseReasons(t, w.peek("a", rT0)))
}

func TestReasons_CurrentAndDrop(t *testing.T) {
	var w waitReasons
	_, _, ok := w.current("a")
	assert.False(t, ok)
	w.observe("a", resources.ReasonGap, rT0)
	w.observe("a", resources.ReasonDanger, rT0.Add(5*time.Second))
	r, since, ok := w.current("a")
	assert.True(t, ok)
	assert.Equal(t, resources.ReasonDanger, r)
	assert.Equal(t, rT0.Add(5*time.Second), since)
	w.drop("a")
	_, _, ok = w.current("a")
	assert.False(t, ok)
	assert.Equal(t, "", w.peek("a", rT0))
}

// retain forgets the rows that are no longer waiting.
func TestReasons_Retain(t *testing.T) {
	var w waitReasons
	w.observe("a", resources.ReasonGap, rT0)
	w.observe("b", resources.ReasonBehind, rT0)
	w.retain(map[string]bool{"b": true})
	_, _, okA := w.current("a")
	_, _, okB := w.current("b")
	assert.False(t, okA)
	assert.True(t, okB)
}

// snapshot is a copy: changing it, or observing afterwards, does not change
// what the reader holds.
func TestReasons_SnapshotIsACopy(t *testing.T) {
	var w waitReasons
	w.observe("a", resources.ReasonGap, rT0)
	snap := w.snapshot()
	require.Contains(t, snap, "a")
	assert.Equal(t, resources.ReasonGap, snap["a"].Reason)
	w.observe("a", resources.ReasonDanger, rT0.Add(time.Second))
	assert.Equal(t, resources.ReasonGap, snap["a"].Reason)
	snap["a"] = reasonView{}
	r, _, _ := w.current("a")
	assert.Equal(t, resources.ReasonDanger, r)
}

// GET handlers and the pass share it from different goroutines.
func TestReasons_RaceFree(t *testing.T) {
	var w waitReasons
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := rT0.Add(time.Duration(i) * time.Second)
				w.observe("a", resources.ReasonGap, now)
				_ = w.peek("a", now)
				_ = w.snapshot()
				w.retain(map[string]bool{"a": true})
			}
		}()
	}
	wg.Wait()
}
