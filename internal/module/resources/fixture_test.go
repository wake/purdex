package resourcesmod

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

// storeFix is a module with a fake sampler, an in-memory resources.db, a settable clock and settings, and a published sample
// (what the lease fixtures used to be, without the leases).
type storeFix struct {
	t     *testing.T
	m     *Module
	clock *fakeClock
	set   *fakeSettings
	logs  *logSink
}

func newStoreFix(t *testing.T) *storeFix {
	t.Helper()
	m := newTestModule(&fakeSampler{}, nil)
	f := &storeFix{t: t, m: m, clock: newFakeClock(), set: &fakeSettings{}, logs: &logSink{}}
	m.logf = f.logs.logf
	m.now = f.clock.now
	m.settingsSrc = f.set
	st, err := openResourcesStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m.store = st
	f.setHost(resources.HostUse{Measured: 10, NCPU: 10, Load1: 1})
	return f
}

func (f *storeFix) nowMS() int64 { return f.clock.ms.Load() }

// advance moves the clock.
func (f *storeFix) advance(d time.Duration) { f.clock.ms.Add(d.Milliseconds()) }

// setHost publishes h as the latest sample. A host without a zone is a calm one (normal, flat).
func (f *storeFix) setHost(h resources.HostUse) {
	if h.Zone == "" {
		h.Zone, h.Trend = "normal", "flat"
	}
	f.m.publish(&resources.Snapshot{Available: true, Capacity: resources.Capacity, Host: h, Mode: resources.ModeMeasure, SampledAt: f.m.now()})
}

// apiCode is the error code of a refused request's body.
func apiCode(rec *httptest.ResponseRecorder) string {
	var e resources.APIError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}
