package resourcesmod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
)

const (
	cidA = "11111111-1111-4111-8111-111111111111"
	cidB = "22222222-2222-4222-8222-222222222222"
	cidC = "33333333-3333-4333-8333-333333333333"
)

// routeFix is a passFix with the routes mounted.
type routeFix struct {
	*passFix
	mux *http.ServeMux
}

func newRouteFix(t *testing.T, mode string) *routeFix {
	f := &routeFix{passFix: newPassFix(t, mode), mux: http.NewServeMux()}
	f.m.RegisterRoutes(f.mux)
	return f
}

func (f *routeFix) do(method, path string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
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

func apiCode(rec *httptest.ResponseRecorder) string {
	var e resources.APIError
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	return e.Error
}

func (f *routeFix) post(cid, kind string, weight int) *httptest.ResponseRecorder {
	return f.do(http.MethodPost, "/api/resources/leases", resources.LeaseRequest{
		ClientID: cid, Kind: kind, Weight: weight, HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"})
}

// A request that fits is granted by the POST itself, with the decision on its
// row.
func TestLeases_GrantImmediatelyWhenFits(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	rec := f.post(cidA, "test-full", 0)
	r := decodeLease(t, rec)
	if rec.Code != http.StatusCreated || r.State != resources.StateHeld || !r.Granted || r.Overrun || r.ID == "" || r.Mode != resources.ModeLease || r.Host.Measured != 10 {
		t.Fatalf("code %d resp %+v", rec.Code, r)
	}
	if d := f.dec(r.ID); d.recorded != 1 || d.path != resources.PathImmediate || d.weight != 35 {
		t.Errorf("decision = %+v", d)
	}
}

// B does not fit behind A: it waits at position 1; A's release (DELETE) wakes
// B's long poll, which answers granted well before its wait runs out.
func TestLeases_WaitThenGrantOnRelease(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "", 60))
	b := decodeLease(t, f.post(cidB, "", 60))
	if a.State != resources.StateHeld || b.State != resources.StateWaiting || b.Position != 1 || b.Granted {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	done := make(chan resources.LeaseResponse, 1)
	go func() { done <- decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+b.ID+"?wait=20", nil)) }()
	time.Sleep(50 * time.Millisecond)
	if rec := f.do(http.MethodDelete, "/api/resources/leases/"+a.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	select {
	case got := <-done:
		if got.State != resources.StateHeld || !got.Granted {
			t.Fatalf("poll answered %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the release did not wake B's poll")
	}
}

func TestLeases_IdempotentCreate(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	first := f.post(cidA, "test-full", 0)
	second := f.post(cidA, "test-full", 0)
	a, b := decodeLease(t, first), decodeLease(t, second)
	if first.Code != http.StatusCreated || second.Code != http.StatusOK || a.ID != b.ID || b.State != resources.StateHeld {
		t.Fatalf("first %d %+v second %d %+v", first.Code, a, second.Code, b)
	}
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM resource_leases`).Scan(&n)
	if n != 1 {
		t.Errorf("rows = %d", n)
	}
}

// Every poll of a waiting row renews its lease.
func TestLeases_PollRenews(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.post(cidA, "", 60)
	b := decodeLease(t, f.post(cidB, "", 60))
	before := f.row(b.ID).LeaseUntil
	f.advance(20 * time.Second)
	f.do(http.MethodGet, "/api/resources/leases/"+b.ID, nil)
	if after := f.row(b.ID).LeaseUntil; after != before+20000 {
		t.Errorf("lease_until %d -> %d, want +20 s", before, after)
	}
}

func TestLeases_DeleteWaitingCancels(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.post(cidA, "", 60)
	b := decodeLease(t, f.post(cidB, "", 60))
	rec := f.do(http.MethodDelete, "/api/resources/leases/"+b.ID, nil)
	got := decodeLease(t, rec)
	if got.State != resources.StateEnded || got.EndReason != resources.EndCancelled {
		t.Fatalf("resp %+v", got)
	}
	// Deleting again is the same answer; an unknown id is a 404.
	if rec := f.do(http.MethodDelete, "/api/resources/leases/"+b.ID, nil); rec.Code != 200 || decodeLease(t, rec).EndReason != resources.EndCancelled {
		t.Errorf("second delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodDelete, "/api/resources/leases/nope", nil); rec.Code != 404 || apiCode(rec) != resources.ErrNoLease {
		t.Errorf("unknown id: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLeases_DeleteHeldReleasesAndFreesTheNext(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "", 60))
	b := decodeLease(t, f.post(cidB, "", 60))
	got := decodeLease(t, f.do(http.MethodDelete, "/api/resources/leases/"+a.ID, nil))
	if got.EndReason != resources.EndReleased || f.state(b.ID) != "held" {
		t.Fatalf("a=%+v b=%s: the release did not run the pass", got, f.state(b.ID))
	}
}

func TestLeases_DeleteByClientID(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "test-full", 0))
	rec := f.do(http.MethodDelete, "/api/resources/leases?client_id="+cidA, nil)
	if got := decodeLease(t, rec); rec.Code != 200 || got.ID != a.ID || got.EndReason != resources.EndReleased {
		t.Fatalf("%d %+v", rec.Code, got)
	}
	rec = f.do(http.MethodDelete, "/api/resources/leases?client_id="+cidC, nil)
	if got := decodeLease(t, rec); rec.Code != 200 || got.State != resources.StateNone {
		t.Errorf("unknown client id: %d %+v", rec.Code, got)
	}
	if rec := f.do(http.MethodDelete, "/api/resources/leases?client_id=x", nil); rec.Code != 400 {
		t.Errorf("bad client id: %d", rec.Code)
	}
}

// Mode advise grants at once and says whether lease mode would have waited.
func TestLeases_ModeAdviseGrantsWithWouldWait(t *testing.T) {
	f := newRouteFix(t, resources.ModeAdvise)
	f.post(cidA, "", 70)
	b := decodeLease(t, f.post(cidB, "", 60)) // 70 + 60 > 100: lease mode would have queued it
	if b.State != resources.StateHeld || !b.Granted || b.WouldWait == nil || !*b.WouldWait || b.Overrun {
		t.Fatalf("resp %+v", b)
	}
	c := decodeLease(t, f.post(cidC, "", 10)) // would it have waited? 70 + 60 (advise let B in) + 10 > 100 in the host's real state
	if c.State != resources.StateHeld || c.WouldWait == nil {
		t.Fatalf("resp %+v", c)
	}
}

// Modes off and measure record nothing and grant.
func TestLeases_ModesOffAndMeasureLeaveNoRow(t *testing.T) {
	for _, mode := range []string{resources.ModeOff, resources.ModeMeasure} {
		f := newRouteFix(t, mode)
		rec := f.post(cidA, "test-full", 0)
		r := decodeLease(t, rec)
		var n int
		f.m.store.db.QueryRow(`SELECT COUNT(*) FROM resource_leases`).Scan(&n)
		if rec.Code != 200 || !r.Granted || r.State != resources.StateNone || r.ID != "" || r.Mode != mode || n != 0 {
			t.Errorf("%s: %d %+v rows %d", mode, rec.Code, r, n)
		}
		if rec := f.do(http.MethodGet, "/api/resources/leases/anything", nil); rec.Code != 404 {
			t.Errorf("%s: unknown id GET = %d", mode, rec.Code)
		}
	}
}

type oneRoot struct{ r resources.Root }

func (o oneRoot) ProcessRoots(*iagent.ProcessSnapshot) ([]resources.Root, error) {
	return []resources.Root{o.r}, nil
}

// session-new resolves the session's agent process; a session the registry
// does not know falls back to the holder pid and says so.
func TestLeases_SessionNewResolvesOrFallsBack(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) { return nil, nil }
	f.m.roots = oneRoot{resources.Root{SessionID: "sid-known", PID: 777, ProcStart: "Thu Oct  9 01:00:00 2026"}}
	req := resources.LeaseRequest{ClientID: cidA, Kind: "test-pkg", HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026", Scope: resources.ScopeSessionNew, SessionID: "sid-known"}
	r := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	row := f.row(r.ID)
	if r.ScopeFallback || row.Scope != resources.ScopeSessionNew || row.HolderPID != 777 || row.HolderStart != "Thu Oct  9 01:00:00 2026" {
		t.Fatalf("known session: resp %+v row %+v", r, row)
	}
	req.ClientID, req.SessionID = cidB, "sid-unknown"
	r = decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	row = f.row(r.ID)
	if !r.ScopeFallback || row.Scope != resources.ScopeProcess || row.HolderPID != 4242 {
		t.Fatalf("unknown session: resp %+v row %+v", r, row)
	}
}

func TestLeases_Validation(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	ok := resources.LeaseRequest{ClientID: cidA, Kind: "test-full", HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"}
	for name, mut := range map[string]func(*resources.LeaseRequest){
		"both kind and weight": func(r *resources.LeaseRequest) { r.Weight = 5 },
		"neither":              func(r *resources.LeaseRequest) { r.Kind = "" },
		"weight 201":           func(r *resources.LeaseRequest) { r.Kind, r.Weight = "", 201 },
		"weight negative":      func(r *resources.LeaseRequest) { r.Kind, r.Weight = "", -1 },
		"unknown kind":         func(r *resources.LeaseRequest) { r.Kind = "mystery" },
		"bad client id":        func(r *resources.LeaseRequest) { r.ClientID = "nope" },
		"v1 uuid":              func(r *resources.LeaseRequest) { r.ClientID = "11111111-1111-1111-8111-111111111111" },
		"wait 600":             func(r *resources.LeaseRequest) { r.WaitS = 600 },
		"wait negative":        func(r *resources.LeaseRequest) { r.WaitS = -1 },
		"no holder pid":        func(r *resources.LeaseRequest) { r.HolderPID = 0 },
		"bad scope":            func(r *resources.LeaseRequest) { r.Scope = "tree" },
		"session-new no sid":   func(r *resources.LeaseRequest) { r.Scope = resources.ScopeSessionNew },
		"no holder start":      func(r *resources.LeaseRequest) { r.HolderStart = "" },
		"garbage holder start": func(r *resources.LeaseRequest) { r.HolderStart = "garbage" },
	} {
		req := ok
		mut(&req)
		rec := f.do(http.MethodPost, "/api/resources/leases", req)
		wantCode := resources.ErrBadRequest
		if name == "unknown kind" {
			wantCode = resources.ErrUnknownKind
		}
		if rec.Code != 400 || apiCode(rec) != wantCode {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	var n int
	f.m.store.db.QueryRow(`SELECT COUNT(*) FROM resource_leases`).Scan(&n)
	if n != 0 {
		t.Errorf("a refused request left %d rows", n)
	}
	if rec := f.do(http.MethodPost, "/api/resources/leases", ok); rec.Code != 201 {
		t.Errorf("valid request: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.do(http.MethodGet, "/api/resources/leases/x?wait=abc", nil); rec.Code != 400 {
		t.Errorf("bad wait: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/resources/leases", strings.NewReader("{"))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("bad JSON: %d", rec.Code)
	}
}

// A grant that lands right after the poll read the row and let go of the lock
// is not missed: the poll answers granted, not after its whole wait.
func TestPoll_GrantBetweenReadAndWait(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "", 60))
	b := decodeLease(t, f.post(cidB, "", 60))
	once := false
	f.m.pollHook = func() {
		if once {
			return
		}
		once = true
		f.do(http.MethodDelete, "/api/resources/leases/"+a.ID, nil) // frees the room and runs the pass
	}
	start := time.Now()
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+b.ID+"?wait=20", nil))
	if got.State != resources.StateHeld || time.Since(start) > 3*time.Second {
		t.Fatalf("resp %+v after %v", got, time.Since(start))
	}
}

// A DELETE of the waiting row wakes its own poll.
func TestPoll_DeleteWakes(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.post(cidA, "", 60)
	b := decodeLease(t, f.post(cidB, "", 60))
	done := make(chan resources.LeaseResponse, 1)
	go func() { done <- decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+b.ID+"?wait=20", nil)) }()
	time.Sleep(50 * time.Millisecond)
	f.do(http.MethodDelete, "/api/resources/leases/"+b.ID, nil)
	select {
	case got := <-done:
		if got.State != resources.StateEnded || got.EndReason != resources.EndCancelled {
			t.Fatalf("resp %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not woken")
	}
}

// The sweeper's abandon (nobody polls: the lease ran out) wakes a poll too.
func TestPoll_AbandonWakes(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	f.post(cidA, "", 60)
	b := decodeLease(t, f.post(cidB, "", 60))
	done := make(chan resources.LeaseResponse, 1)
	go func() { done <- decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+b.ID+"?wait=20", nil)) }()
	time.Sleep(50 * time.Millisecond)
	f.m.store.db.Exec(`UPDATE resource_leases SET lease_until = ? WHERE id = ?`, f.nowMS()-1, b.ID)
	f.m.sweepOnce(context.Background())
	select {
	case got := <-done:
		if got.State != resources.StateEnded || got.EndReason != resources.EndAbandoned {
			t.Fatalf("resp %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("not woken")
	}
}

// A new module on the same database sees a waiting row as waiting (nothing
// per row lives in memory), and a pass of it grants it once there is room.
func TestPoll_AfterRestartObservesPersistedRow(t *testing.T) {
	dir := t.TempDir()
	set := &fakeSettings{}
	set.set(resources.Settings{Mode: resources.ModeLease})
	m1, _ := initedModule(t, dir, set, idleSampler())
	mux1 := http.NewServeMux()
	m1.RegisterRoutes(mux1)
	post := func(mux *http.ServeMux, cid string, w int) resources.LeaseResponse {
		b, _ := json.Marshal(resources.LeaseRequest{ClientID: cid, Weight: w, HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026"})
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/resources/leases", bytes.NewReader(b)))
		return decodeLease(t, rec)
	}
	a, b := post(mux1, cidA, 60), post(mux1, cidB, 60)
	if b.State != resources.StateWaiting {
		t.Fatalf("setup: %+v", b)
	}
	if err := m1.Close(); err != nil {
		t.Fatal(err)
	}
	m2, _ := initedModule(t, dir, set, idleSampler())
	mux2 := http.NewServeMux()
	m2.RegisterRoutes(mux2)
	get := func(id string) resources.LeaseResponse {
		rec := httptest.NewRecorder()
		mux2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/resources/leases/"+id, nil))
		return decodeLease(t, rec)
	}
	if got := get(b.ID); got.State != resources.StateWaiting || got.Position != 1 {
		t.Fatalf("after restart: %+v", got)
	}
	rec := httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/resources/leases/"+a.ID, nil))
	if got := get(b.ID); got.State != resources.StateHeld {
		t.Fatalf("after the release in the new module: %+v", got)
	}
}

// A misspelt field is refused, not read as "left out".
func TestLeases_UnknownFieldIsRefused(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	req := httptest.NewRequest(http.MethodPost, "/api/resources/leases",
		strings.NewReader(`{"client_id":"`+cidA+`","kind":"test-full","holder_pid":1,"holder_start":"Thu Oct  9 00:00:00 2026","holder_strat":"x"}`))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("code %d %s", rec.Code, rec.Body.String())
	}
}

// A client id belongs to the request it was made for: the same request is a
// replay, another one is a 409 that grants nothing.
func TestLeases_ClientIDReuseIsRefused(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	base := resources.LeaseRequest{ClientID: cidA, Weight: 10, HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026", ToolUseID: "tu1"}
	first := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", base))
	for name, mut := range map[string]func(*resources.LeaseRequest){
		"heavier weight": func(r *resources.LeaseRequest) { r.Weight = 200 },
		"a kind":         func(r *resources.LeaseRequest) { r.Weight, r.Kind = 0, "test-full" },
		"other holder":   func(r *resources.LeaseRequest) { r.HolderPID = 99 },
		"other start":    func(r *resources.LeaseRequest) { r.HolderStart = "Thu Oct  9 00:00:01 2026" },
		"other tool use": func(r *resources.LeaseRequest) { r.ToolUseID = "tu2" },
		"other session":  func(r *resources.LeaseRequest) { r.SessionID = "sid-x" },
	} {
		req := base
		mut(&req)
		rec := f.do(http.MethodPost, "/api/resources/leases", req)
		if rec.Code != http.StatusConflict || apiCode(rec) != resources.ErrClientIDReused {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// The identical request, also after the lease ended, is a replay.
	f.do(http.MethodDelete, "/api/resources/leases/"+first.ID, nil)
	rec := f.do(http.MethodPost, "/api/resources/leases", base)
	if got := decodeLease(t, rec); rec.Code != 200 || got.ID != first.ID || got.State != resources.StateEnded {
		t.Errorf("replay after end: %d %+v", rec.Code, got)
	}
}

// The holder start decides whether a vanished holder is noticed: a request
// without one that parses is refused, so no lease can sit out its max_hold.
func TestLeases_HolderStartThatParsesIsWhatTheSweeperJudges(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	r := decodeLease(t, f.post(cidA, "test-full", 0))
	if r.State != resources.StateHeld {
		t.Fatal(r)
	}
	f.advance(2 * time.Second)
	f.m.sweepOnce(context.Background()) // pid 4242 is not in the (empty) table
	if got := f.row(r.ID); got.State != "ended" || got.EndReason != resources.EndHolderGone {
		t.Errorf("row = %+v", got)
	}
}

// The scope fallback is told again on a replay of the same request.
func TestLeases_ReplayRepeatsTheScopeFallback(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	req := resources.LeaseRequest{ClientID: cidA, Kind: "test-pkg", HolderPID: 4242, HolderStart: "Thu Oct  9 00:00:00 2026",
		Scope: resources.ScopeSessionNew, SessionID: "sid-unknown"}
	first := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	again := decodeLease(t, f.do(http.MethodPost, "/api/resources/leases", req))
	if !first.ScopeFallback || !again.ScopeFallback || first.ID != again.ID {
		t.Errorf("first %+v again %+v", first, again)
	}
}

// A poll whose timer and whose wake come together answers with the row as it
// is, not as it was before the wait.
func TestPoll_TimeoutReadsTheRowAgain(t *testing.T) {
	f := newRouteFix(t, resources.ModeLease)
	a := decodeLease(t, f.post(cidA, "", 60))
	b := decodeLease(t, f.post(cidB, "", 60))
	f.m.pollHook = func() {
		f.m.pollHook = nil
		// The room appears with no wake the poll could see: the row changes in
		// the database only, then the timer fires.
		f.m.store.End(a.ID, resources.EndReleased, f.nowMS())
		f.m.store.GrantDecided(b.ID, f.nowMS(), false, resources.Grant{Decision: resources.Decision{Path: resources.PathWaited}}, "")
	}
	got := decodeLease(t, f.do(http.MethodGet, "/api/resources/leases/"+b.ID+"?wait=1", nil))
	if got.State != resources.StateHeld {
		t.Fatalf("resp %+v: the timeout answered with the row it read before waiting", got)
	}
}
