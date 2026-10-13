package resourcesmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/resources"
)

// zoneRig drives the sampler by hand: a clock the test moves 5 s per tick and
// a reading it sets before each one.
type zoneRig struct {
	m   *Module
	cur time.Time
	raw resources.HostRaw
	set *fakeSettings
}

func newZoneRig(t *testing.T) *zoneRig {
	t.Helper()
	r := &zoneRig{cur: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC), set: &fakeSettings{}}
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return r.raw, nil, nil
	}}
	r.m = newTestModule(s, nil)
	r.m.now = func() time.Time { return r.cur }
	r.m.settingsSrc = r.set
	r.raw = zoneRaw(40, 1, 1)
	return r
}

// zoneRaw is a reading of a 10-core, 16 GiB host with memPct in use.
func zoneRaw(memPct, load1 float64, pressure int) resources.HostRaw {
	const pages = (16 << 30) / 16384
	return resources.HostRaw{
		Load1: load1, NCPU: 10, MemBytes: 16 << 30, PageSize: 16384,
		Free: uint64(pages * (100 - memPct) / 100), Pressure: pressure, MemorystatusLevel: 80, PcpuSum: 50,
	}
}

// tick moves the clock 5 s, applies the reading and runs one sampler tick.
func (r *zoneRig) tick(t *testing.T, raw resources.HostRaw) resources.HostUse {
	t.Helper()
	r.cur = r.cur.Add(5 * time.Second)
	r.raw = raw
	require.False(t, r.m.tick(context.Background()))
	return r.m.latest.Load().Host
}

// The snapshot carries the zone, the trend and when the zone last changed,
// in measure mode too (the default of a module with no settings reader).
func TestSampler_PublishesZoneAndTrend(t *testing.T) {
	r := newZoneRig(t)
	var h resources.HostUse
	for i := 0; i < 5; i++ {
		h = r.tick(t, zoneRaw(40, 1, 1))
	}
	assert.Equal(t, "normal", h.Zone)
	assert.Equal(t, "unknown", h.Trend, "5 samples are too few")
	since := h.ZoneSince
	assert.Equal(t, r.m.latest.Load().SampledAt.UnixMilli()-20_000, since, "the zone began at the first tick")

	// Climb 0.5 points per tick = 6 per minute: rising once the window has data.
	for i := 0; i < 12; i++ {
		h = r.tick(t, zoneRaw(41+0.5*float64(i), 1, 1))
	}
	assert.Equal(t, "normal", h.Zone)
	assert.Equal(t, "rising", h.Trend)
	assert.Equal(t, since, h.ZoneSince, "a zone that did not change keeps its start")

	// Into warning: the zone changes and ZoneSince moves to that tick.
	h = r.tick(t, zoneRaw(85, 1, 1))
	assert.Equal(t, "warning", h.Zone)
	assert.Equal(t, r.cur.UnixMilli(), h.ZoneSince)

	// Flat for a full 120 s window: the trend settles; hysteresis keeps warning at 79.
	for i := 0; i < 26; i++ {
		h = r.tick(t, zoneRaw(79, 1, 1))
	}
	assert.Equal(t, "warning", h.Zone)
	assert.Equal(t, "flat", h.Trend)
	h = r.tick(t, zoneRaw(60, 1, 1))
	assert.Equal(t, "normal", h.Zone)

	// Danger.
	h = r.tick(t, zoneRaw(95, 1, 1))
	assert.Equal(t, "danger", h.Zone)
	h = r.tick(t, zoneRaw(40, 1, 4))
	assert.Equal(t, "danger", h.Zone, "critical pressure alone is danger")
}

// Modes advise and lease compute the same zones.
func TestSampler_ZonesInEveryMode(t *testing.T) {
	for _, mode := range []string{resources.ModeMeasure, resources.ModeAdvise, resources.ModeLease} {
		r := newZoneRig(t)
		r.set.set(resources.Settings{Mode: mode})
		h := r.tick(t, zoneRaw(85, 1, 1))
		assert.Equal(t, "warning", h.Zone, mode)
	}
}

// The thresholds come from the settings.
func TestSampler_ZoneUsesSettings(t *testing.T) {
	r := newZoneRig(t)
	r.set.set(resources.Settings{Mode: resources.ModeMeasure, MemWarnPct: intp(60), MemDangerPct: intp(70)})
	assert.Equal(t, "warning", r.tick(t, zoneRaw(65, 1, 1)).Zone)
	assert.Equal(t, "danger", r.tick(t, zoneRaw(72, 1, 1)).Zone)
}

// Mode off, a failing sampler and an unusable reading publish zone unknown
// and add no samples; the zone starts again from normal afterwards.
func TestSampler_UnavailableIsUnknownAndAddsNoSamples(t *testing.T) {
	r := newZoneRig(t)
	for i := 0; i < 3; i++ {
		r.tick(t, zoneRaw(85, 1, 1))
	}
	before := len(r.m.ringCopy())
	require.Equal(t, 3, before)

	r.set.set(resources.Settings{Mode: resources.ModeOff})
	h := r.tick(t, zoneRaw(85, 1, 1))
	assert.False(t, r.m.latest.Load().Available)
	assert.Equal(t, "unknown", h.Zone)
	assert.Equal(t, "unknown", h.Trend)
	assert.Len(t, r.m.ringCopy(), before, "off adds no sample")

	// Back on: the 79 would have held a warning latch, but the latch was reset.
	r.set.set(resources.Settings{Mode: resources.ModeMeasure})
	h = r.tick(t, zoneRaw(79, 1, 1))
	assert.Equal(t, "normal", h.Zone)
	assert.Len(t, r.m.ringCopy(), before+1)

	// Three failed ticks in a row publish the stale reading as unavailable.
	failing := true
	r.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		if failing {
			return resources.HostRaw{}, nil, assert.AnError
		}
		return zoneRaw(85, 1, 1), nil, nil
	}}
	n := len(r.m.ringCopy())
	for i := 0; i < 3; i++ {
		r.cur = r.cur.Add(5 * time.Second)
		r.m.tick(context.Background())
	}
	s := r.m.latest.Load()
	assert.False(t, s.Available)
	assert.Equal(t, "unknown", s.Host.Zone)
	assert.Equal(t, "unknown", s.Host.Trend)
	assert.Len(t, r.m.ringCopy(), n, "failed ticks add no sample")

	// An unusable reading (no cpu count) is a failed tick as well.
	failing = false
	r.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		bad := zoneRaw(85, 1, 1)
		bad.NCPU = 0
		return bad, nil, nil
	}}
	r.cur = r.cur.Add(5 * time.Second)
	r.m.tick(context.Background())
	assert.Len(t, r.m.ringCopy(), n)
}

// A single failed tick leaves the last good zone published (one flaky fork
// does not blank it).
func TestSampler_OneFailedTickKeepsTheZone(t *testing.T) {
	r := newZoneRig(t)
	r.tick(t, zoneRaw(85, 1, 1))
	r.m.sampler = &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return resources.HostRaw{}, nil, assert.AnError
	}}
	r.cur = r.cur.Add(5 * time.Second)
	r.m.tick(context.Background())
	s := r.m.latest.Load()
	assert.True(t, s.Available)
	assert.Equal(t, "warning", s.Host.Zone)
}

// GET /api/resources?samples=1 returns the ring, oldest first; without the
// flag the field is absent.
func TestGet_SamplesFlag(t *testing.T) {
	r := newZoneRig(t)
	for i := 0; i < 4; i++ {
		r.tick(t, zoneRaw(40+float64(i)*10, 1, 1))
	}
	get := func(url string) map[string]json.RawMessage {
		rr := httptest.NewRecorder()
		r.m.handleGet(rr, httptest.NewRequest(http.MethodGet, url, nil))
		require.Equal(t, http.StatusOK, rr.Code)
		var out map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
		return out
	}
	assert.NotContains(t, get("/api/resources"), "samples")
	assert.NotContains(t, get("/api/resources?samples=0"), "samples")

	var got []struct {
		At       int64   `json:"at"`
		Mem      float64 `json:"mem"`
		CPU      float64 `json:"cpu"`
		Pressure int     `json:"pressure"`
	}
	require.NoError(t, json.Unmarshal(get("/api/resources?samples=1")["samples"], &got))
	require.Len(t, got, 4)
	assert.Less(t, got[0].At, got[3].At)
	assert.InDelta(t, 40, got[0].Mem, 0.1)
	assert.InDelta(t, 70, got[3].Mem, 0.1)
	assert.InDelta(t, 10, got[0].CPU, 0.001) // load1 1 on 10 cores
	assert.Equal(t, 1, got[0].Pressure)
}

// The ring is read by request goroutines while the sampler writes it.
func TestGet_SamplesIsRaceFree(t *testing.T) {
	r := newZoneRig(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			rr := httptest.NewRecorder()
			r.m.handleGet(rr, httptest.NewRequest(http.MethodGet, "/api/resources?samples=1", nil))
		}
	}()
	for i := 0; i < 200; i++ {
		r.tick(t, zoneRaw(40, 1, 1))
	}
	<-done
}

func intp(n int) *int { return &n }
