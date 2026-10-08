package resourcesmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wake/purdex/internal/resources"
)

func getResources(t *testing.T, m *Module, query string) (resources.Snapshot, map[string]json.RawMessage) {
	t.Helper()
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/resources"+query, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var snap resources.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	return snap, raw
}

func sampledModule(t *testing.T, sessions ...string) *Module {
	t.Helper()
	procs := []resources.Proc{{PID: 1, PPID: 0, Pcpu: 1, RSSBytes: 1 << 20}}
	var roots []resources.Root
	for i, sid := range sessions {
		pid := 100 + i
		procs = append(procs, resources.Proc{PID: pid, PPID: 1, Pcpu: 20, RSSBytes: 1 << 28})
		roots = append(roots, resources.Root{SessionID: sid, PID: pid})
	}
	s := &fakeSampler{fn: func(context.Context, int) (resources.HostRaw, []resources.Proc, error) {
		return goodRaw(), procs, nil
	}}
	m := newTestModule(s, &fakeRoots{roots: roots})
	m.tick(context.Background())
	return m
}

func TestAPI_SnapshotShape(t *testing.T) {
	m := sampledModule(t, "sid-a")
	snap, raw := getResources(t, m, "")

	for _, k := range []string{"sampled_at", "available", "capacity", "host", "sessions", "mode"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("top-level field %q missing", k)
		}
	}
	var host map[string]json.RawMessage
	if err := json.Unmarshal(raw["host"], &host); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"measured", "cpu", "mem", "load1", "ncpu", "mem_bytes", "mem_used_bytes",
		"pressure", "memorystatus_level", "pcpu_total", "full"} {
		if _, ok := host[k]; !ok {
			t.Errorf("host field %q missing", k)
		}
	}
	if !snap.Available || snap.Capacity != 100 || snap.Mode != "measure" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.Host.NCPU != 10 || snap.Host.Measured <= 0 {
		t.Fatalf("host = %+v", snap.Host)
	}
	if len(snap.Sessions) != 1 || snap.Sessions[0].SessionID != "sid-a" || snap.Sessions[0].Procs != 1 {
		t.Fatalf("sessions = %+v", snap.Sessions)
	}
	// P1 fields are reserved, not emitted.
	for _, k := range []string{"leases", "waiters", "reason"} {
		if _, ok := raw[k]; ok {
			t.Errorf("field %q must be absent in P0", k)
		}
	}
}

func TestAPI_WarmingUp(t *testing.T) {
	m := newTestModule(&fakeSampler{}, nil) // no tick yet
	snap, raw := getResources(t, m, "")
	if snap.Available || snap.Reason != resources.ReasonWarmingUp {
		t.Fatalf("snapshot = %+v, want unavailable warming_up", snap)
	}
	if snap.Capacity != 100 || snap.Mode != "measure" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if string(raw["sessions"]) != "[]" {
		t.Fatalf("sessions = %s, want []", raw["sessions"])
	}
}

func TestAPI_SessionFilter(t *testing.T) {
	m := sampledModule(t, "sid-a", "sid-b", "sid-c")

	snap, _ := getResources(t, m, "?session=sid-b")
	if len(snap.Sessions) != 1 || snap.Sessions[0].SessionID != "sid-b" {
		t.Fatalf("sessions = %+v, want only sid-b", snap.Sessions)
	}
	if !snap.Available || snap.Host.NCPU != 10 {
		t.Fatalf("the host part must be untouched: %+v", snap)
	}

	snap, raw := getResources(t, m, "?session=nope")
	if len(snap.Sessions) != 0 || string(raw["sessions"]) != "[]" {
		t.Fatalf("unknown id: sessions = %s, want []", raw["sessions"])
	}

	snap, _ = getResources(t, m, "")
	if len(snap.Sessions) != 3 {
		t.Fatalf("no filter: %d sessions, want 3", len(snap.Sessions))
	}

	// The filter works on a copy: the stored snapshot keeps every session.
	if got := len(m.latest.Load().Sessions); got != 3 {
		t.Fatalf("stored snapshot has %d sessions after a filtered read", got)
	}
}

// The handler reads the latest sample; it never takes one (#1777, #1794).
func TestAPI_NeverSamples(t *testing.T) {
	s := &fakeSampler{}
	m := newTestModule(s, nil)
	for i := 0; i < 3; i++ {
		getResources(t, m, "")
		getResources(t, m, "?session=x")
	}
	if n := s.calls.Load(); n != 0 {
		t.Fatalf("handler took %d samples before any tick", n)
	}

	// And with the loop running: requests do not add samples beyond the
	// ticker's own. A long interval leaves exactly the immediate first one.
	m.interval = time.Hour
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	waitFor(t, "the first sample", func() bool { return m.latest.Load() != nil })
	for i := 0; i < 5; i++ {
		getResources(t, m, "")
	}
	if n := s.calls.Load(); n != 1 {
		t.Fatalf("sampler called %d times, want 1 (the ticker's first)", n)
	}
}
