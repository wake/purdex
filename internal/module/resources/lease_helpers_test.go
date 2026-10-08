package resourcesmod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
)

// fakeClock is a settable clock in unix milliseconds.
type fakeClock struct{ ms atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ms.Store(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).UnixMilli())
	return c
}
func (c *fakeClock) now() time.Time          { return time.UnixMilli(c.ms.Load()) }
func (c *fakeClock) advance(d time.Duration) { c.ms.Add(d.Milliseconds()) }

// fakeSettings is the hostconfig reader.
type fakeSettings struct {
	mu  sync.Mutex
	s   resources.Settings
	err error
}

func (f *fakeSettings) ResourcesSettings() (resources.Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return resources.Settings{}, f.err
	}
	return f.s.Effective(), nil
}

func (f *fakeSettings) set(s resources.Settings) {
	f.mu.Lock()
	f.s, f.err = s, nil
	f.mu.Unlock()
}

func (f *fakeSettings) fail(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

// fakeAdmit grants waiters in queue order while fewer than maxHeld leases are
// held (counting the ones it grants), and overruns every waiter whose deadline
// has passed. fn, when set, replaces that. It records every input.
type fakeAdmit struct {
	mu      sync.Mutex
	maxHeld int
	fn      func(in AdmitInput) (grant, overrun []string)
	inputs  []AdmitInput
}

func (f *fakeAdmit) Admit(in AdmitInput) (grant, overrun []string) {
	f.mu.Lock()
	f.inputs = append(f.inputs, in)
	fn, maxHeld := f.fn, f.maxHeld
	f.mu.Unlock()
	if fn != nil {
		return fn(in)
	}
	held := len(in.Leases)
	for _, w := range in.Waiters {
		switch {
		case !w.Deadline.After(in.Now):
			overrun = append(overrun, w.ID)
			held++
		case held < maxHeld:
			grant = append(grant, w.ID)
			held++
		}
	}
	return grant, overrun
}

func (f *fakeAdmit) setMax(n int) {
	f.mu.Lock()
	f.maxHeld = n
	f.mu.Unlock()
}

func (f *fakeAdmit) last() AdmitInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inputs[len(f.inputs)-1]
}

// leaseEnv is a module with a file-backed store, a fake clock, fake settings
// and a fake admitter, served through its own mux.
type leaseEnv struct {
	t     *testing.T
	m     *Module
	clock *fakeClock
	set   *fakeSettings
	adm   *fakeAdmit
	mux   *http.ServeMux
	seq   atomic.Int64
	logs  atomic.Int64
}

func newLeaseEnv(t *testing.T, mode string) *leaseEnv {
	t.Helper()
	return newLeaseEnvAt(t, mode, filepath.Join(t.TempDir(), "resources.db"), newFakeClock(), nil)
}

// newLeaseEnvAt builds a module on the database at path (several modules on
// one path emulate a restart). A nil adm gets the one-lease fake.
func newLeaseEnvAt(t *testing.T, mode, path string, clock *fakeClock, adm *fakeAdmit) *leaseEnv {
	t.Helper()
	st, err := openLeaseStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if adm == nil {
		adm = &fakeAdmit{maxHeld: 1}
	}
	e := &leaseEnv{t: t, clock: clock, set: &fakeSettings{}, adm: adm}
	e.set.set(resources.Settings{Mode: mode})
	m := newTestModule(&fakeSampler{}, nil)
	m.store = st
	m.settingsSrc = e.set
	m.admit = adm
	m.now = clock.now
	m.logf = func(string, ...any) { e.logs.Add(1) }
	m.newID = func() string { return fmt.Sprintf("L%d", e.seq.Add(1)) }
	m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) { return &iagent.ProcessSnapshot{}, nil }
	e.m = m
	e.mux = http.NewServeMux()
	m.RegisterRoutes(e.mux)
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return e
}

// clientID is a valid UUID v4 for n.
func clientID(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func (e *leaseEnv) do(method, target string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd *bytes.Reader
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case string:
		rd = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rd)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func decodeLease(t *testing.T, rec *httptest.ResponseRecorder) resources.LeaseResponse {
	t.Helper()
	var r resources.LeaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return r
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) resources.APIError {
	t.Helper()
	var r resources.APIError
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return r
}

// post creates a lease with the given client number.
func (e *leaseEnv) post(client int, req resources.LeaseRequest) (int, resources.LeaseResponse) {
	e.t.Helper()
	req.ClientID = clientID(client)
	if req.HolderPID == 0 {
		req.HolderPID = 4000 + client
	}
	if req.Kind == "" && req.Weight == 0 {
		req.Kind = "test-full"
	}
	rec := e.do(http.MethodPost, "/api/resources/leases", req)
	return rec.Code, decodeLease(e.t, rec)
}

func (e *leaseEnv) get(id string, wait int) (int, resources.LeaseResponse) {
	e.t.Helper()
	target := "/api/resources/leases/" + id
	if wait > 0 {
		target += fmt.Sprintf("?wait=%d", wait)
	}
	rec := e.do(http.MethodGet, target, nil)
	return rec.Code, decodeLease(e.t, rec)
}

func (e *leaseEnv) del(id string) (int, resources.LeaseResponse) {
	e.t.Helper()
	rec := e.do(http.MethodDelete, "/api/resources/leases/"+id, nil)
	return rec.Code, decodeLease(e.t, rec)
}

func (e *leaseEnv) snapshot() resources.Snapshot {
	e.t.Helper()
	rec := e.do(http.MethodGet, "/api/resources", nil)
	var s resources.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		e.t.Fatalf("decode snapshot %q: %v", rec.Body.String(), err)
	}
	return s
}

func (e *leaseEnv) row(id string) leaseRow {
	e.t.Helper()
	r, ok, err := e.m.store.Get(id)
	if err != nil || !ok {
		e.t.Fatalf("row %s: ok=%v err=%v", id, ok, err)
	}
	return r
}

// within runs fn in a goroutine and fails the test unless it returns inside d.
func within[T any](t *testing.T, d time.Duration, what string, fn func() T) T {
	t.Helper()
	ch := make(chan T, 1)
	go func() { ch <- fn() }()
	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
		panic("unreachable")
	}
}
