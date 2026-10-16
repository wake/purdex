package fleet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var t0 = time.Unix(1_000_000, 0)

func TestPageWindowFirstReadingIsUnknown(t *testing.T) {
	var w PageWindow
	r, known := w.Observe(t0, 500)
	assert.False(t, known)
	assert.Zero(t, r)
}

func TestPageWindowRateIsOverTheWholeWindowNotOneFrame(t *testing.T) {
	var w PageWindow
	// ten quiet seconds, then 100 pages in the last one: 100 pages over a 10 s window is 10/s, not 100/s
	for i := 0; i <= 9; i++ {
		w.Observe(t0.Add(time.Duration(i)*time.Second), 1000)
	}
	r, known := w.Observe(t0.Add(10*time.Second), 1100)
	assert.True(t, known)
	assert.InDelta(t, 10.0, r, 0.001)
}

func TestPageWindowSteadyRate(t *testing.T) {
	var w PageWindow
	var r float64
	var known bool
	for i := 0; i < 30; i++ {
		r, known = w.Observe(t0.Add(time.Duration(i)*time.Second), uint64(60*i))
	}
	assert.True(t, known)
	assert.InDelta(t, 60.0, r, 0.001)
}

func TestPageWindowTwoReadingsASecondApartIsEnough(t *testing.T) {
	var w PageWindow
	w.Observe(t0, 0)
	r, known := w.Observe(t0.Add(time.Second), 80)
	assert.True(t, known)
	assert.InDelta(t, 80.0, r, 0.001)
}

func TestPageWindowDropsReadingsOlderThanTheWindow(t *testing.T) {
	var w PageWindow
	w.Observe(t0, 0)
	_, known := w.Observe(t0.Add(30*time.Second), 5000)
	assert.False(t, known, "the only earlier reading is outside the window: no baseline")
	assert.Len(t, w.samples, 1)
}

func TestPageWindowCounterGoingBackStartsAgain(t *testing.T) {
	var w PageWindow
	w.Observe(t0, 9000)
	w.Observe(t0.Add(time.Second), 9100)
	r, known := w.Observe(t0.Add(2*time.Second), 5)
	assert.False(t, known)
	assert.Zero(t, r)
	r, known = w.Observe(t0.Add(3*time.Second), 35)
	assert.True(t, known)
	assert.InDelta(t, 30.0, r, 0.001)
}

func TestPageWindowResetForgets(t *testing.T) {
	var w PageWindow
	w.Observe(t0, 0)
	w.Reset()
	_, known := w.Observe(t0.Add(time.Second), 100)
	assert.False(t, known)
}
