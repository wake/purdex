package daemon

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

var s0 = time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)

// fakeSources returns a host whose pressure level is read from *level.
func fakeSources(level *int, owner *wire.OwnerState) facts.Sources {
	return facts.Sources{
		Now: func() time.Time { return s0 }, // overridden by Tick(now)
		CPU: func() (facts.RawCPU, error) { return facts.RawCPU{NCPU: 10, Load1: 2, Busy: 100, Total: 1000}, nil },
		Mem: func() (facts.RawMem, error) {
			return facts.RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 100000, Inactive: 100000, Level: *level}, nil
		},
		Disks: func() ([]facts.RawDisk, error) {
			return []facts.RawDisk{{Mount: "/", TotalBytes: 100 << 30, AvailBytes: 50 << 30, FreeBytes: 50 << 30}}, nil
		},
		Owner: func(facts.ProcSnapshot, facts.ProcContext) *wire.OwnerState { return owner },
		Procs: func(time.Time) (facts.ProcSnapshot, facts.ProcContext, error) {
			return facts.ProcSnapshot{}, facts.ProcContext{}, nil
		},
	}
}

func newSampler(level *int, owner *wire.OwnerState) *Sampler {
	return NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2",
		Sources: fakeSources(level, owner), Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
	})
}

func TestTickBuildsAFullValidRecord(t *testing.T) {
	level := 1
	s := newSampler(&level, nil)
	s.Tick(s0)
	f, ok := s.Latest()
	if !ok {
		t.Fatal("no record")
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if f.V != wire.WriteProto || f.HostID != "h1" || f.Alias != "mlab" || f.OSVersion != "26.6.2" || f.Platform == "" || f.Arch == "" || !f.At.Equal(s0) {
		t.Fatalf("identity: %+v", f)
	}
	if f.CPU.NCPU != 10 || f.Mem.TotalMB != 16384 || len(f.Disk) != 1 {
		t.Fatalf("readings: %+v", f)
	}
	if f.Derived.Zone != wire.ZoneNormal || f.Derived.Headroom != (wire.Slots{Heavy: 1, Light: 2}) || f.Derived.SessionsRoom == 0 {
		t.Fatalf("derived: %+v", f.Derived)
	}
	if !f.Worker.Enabled || f.Worker.Slots != (wire.Slots{Heavy: 1, Light: 2}) || f.Worker.Running != (wire.Slots{}) {
		t.Fatalf("worker: %+v", f.Worker)
	}
	if f.Exception == nil || len(f.Exception) != 0 || f.Owner != nil {
		t.Fatalf("exceptions %#v owner %+v", f.Exception, f.Owner)
	}
	// the record round-trips through the contract decoder
	b, _ := json.Marshal(f)
	if _, err := wire.Decode[wire.Facts](b); err != nil {
		t.Fatal(err)
	}
}

func TestTickDerivedFollowsPressureAndOwner(t *testing.T) {
	level := 1
	owner := &wire.OwnerState{Active: false, Reason: "idle"}
	s := newSampler(&level, owner)
	s.Tick(s0)
	if f, _ := s.Latest(); f.Derived.Headroom.Heavy != 1 {
		t.Fatalf("idle: %+v", f.Derived)
	}
	level = 2
	s.Tick(s0.Add(5 * time.Second))
	if f, _ := s.Latest(); f.Derived.Zone != wire.ZoneWarning || f.Derived.Headroom.Heavy != 0 || f.Derived.Headroom.Light != 2 {
		t.Fatalf("pressure 2: %+v", f.Derived)
	}
	level = 1
	*owner = wire.OwnerState{Active: true, Reason: "hid"}
	s.Tick(s0.Add(10 * time.Second))
	f, _ := s.Latest()
	if f.Derived.Headroom != (wire.Slots{}) || f.Owner == nil || !f.Owner.Active {
		t.Fatalf("owner active: %+v owner=%+v", f.Derived, f.Owner)
	}
}

func TestHistoryFiveSecondAndOneMinute(t *testing.T) {
	level := 1
	s := newSampler(&level, nil)
	for i := 0; i < 36; i++ { // three minutes of 5 s records
		s.Tick(s0.Add(time.Duration(i) * 5 * time.Second))
	}
	five := s.History(s0.Add(2*time.Minute), wire.Step5s)
	if len(five) != 12 || !five[0].At.Equal(s0.Add(2*time.Minute)) {
		t.Fatalf("5s: %d records, first %v", len(five), five[0].At)
	}
	one := s.History(s0, wire.Step1m)
	if len(one) != 3 {
		t.Fatalf("1m: %d records", len(one))
	}
	for i, r := range one {
		if !r.At.Equal(s0.Add(time.Duration(i) * time.Minute)) {
			t.Errorf("rollup %d at %v", i, r.At)
		}
	}
	// the minute still being filled is included as a partial rollup
	s.Tick(s0.Add(3 * time.Minute))
	if got := s.History(s0, wire.Step1m); len(got) != 4 {
		t.Fatalf("with the open minute: %d", len(got))
	}
}

func TestFactsRoutes(t *testing.T) {
	level := 1
	d := New(Options{HostID: "h1", Alias: "mlab", Version: "v0", Token: testToken, Sampler: newSampler(&level, nil)})
	h := d.TCPHandler()
	auth := "Bearer " + testToken

	// before the first sample
	if w := do(h, "GET", "/v1/facts", auth); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("before the first sample: %d", w.Code)
	}
	for i := 0; i < 3; i++ {
		d.opts.Sampler.Tick(s0.Add(time.Duration(i) * 5 * time.Second))
	}
	w := do(h, "GET", "/v1/facts", auth)
	if w.Code != 200 {
		t.Fatalf("facts: %d %s", w.Code, w.Body)
	}
	if _, err := wire.Decode[wire.Facts](w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	q := url.Values{"since": {s0.Format(time.RFC3339)}}
	w = do(h, "GET", "/v1/facts/history?"+q.Encode(), auth)
	if w.Code != 200 {
		t.Fatalf("history: %d %s", w.Code, w.Body)
	}
	resp, err := wire.Decode[wire.FactsHistoryResponse](w.Body.Bytes())
	if err != nil || resp.Step != wire.Step5s || len(resp.Records) != 3 {
		t.Fatalf("%+v %v", resp, err)
	}
	for _, bad := range []string{"", "since=nope", "since=" + s0.Format(time.RFC3339) + "&step=2h"} {
		if w := do(h, "GET", "/v1/facts/history?"+bad, auth); w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d want 400", bad, w.Code)
		}
	}
	// token still required
	if w := do(h, "GET", "/v1/facts", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
	// the socket serves the same without a token
	if w := do(d.SocketHandler(), "GET", "/v1/facts", ""); w.Code != 200 {
		t.Fatalf("socket: %d", w.Code)
	}
}
