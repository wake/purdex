package resourcesmod

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/resources"
)

func TestLeases_GrantImmediatelyWhenFits(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	code, r := e.post(1, resources.LeaseRequest{Kind: "test-full", SessionID: "sid-1"})
	if code != http.StatusCreated {
		t.Fatalf("status = %d", code)
	}
	if r.ID != "L1" || r.State != resources.StateHeld || !r.Granted || r.Overrun || r.WaitedMS != 0 || r.Mode != resources.ModeLease || r.WouldWait != nil {
		t.Fatalf("response = %+v", r)
	}
	row := e.row("L1")
	if row.Weight != 35 || row.Kind != "test-full" || row.Scope != resources.ScopeProcess || row.GrantedAt == 0 || row.SessionID != "sid-1" {
		t.Fatalf("row = %+v", row)
	}
}

func TestLeases_WaitThenGrantOnRelease(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	_, a := e.post(1, resources.LeaseRequest{})
	code, b := e.post(2, resources.LeaseRequest{})
	if code != http.StatusCreated || b.State != resources.StateWaiting || b.Granted || b.Position != 1 {
		t.Fatalf("second request = %d %+v, want waiting at position 1", code, b)
	}

	got := make(chan resources.LeaseResponse, 1)
	go func() { _, r := e.get(b.ID, 20); got <- r }()
	time.Sleep(30 * time.Millisecond) // the poll is parked
	select {
	case r := <-got:
		t.Fatalf("the poll returned before the release: %+v", r)
	default:
	}

	if code, rel := e.del(a.ID); code != http.StatusOK || rel.State != resources.StateEnded || rel.EndReason != resources.EndReleased {
		t.Fatalf("release = %d %+v", code, rel)
	}
	select {
	case r := <-got:
		if !r.Granted || r.State != resources.StateHeld || r.WaitedMS < 0 {
			t.Fatalf("poll answered %+v, want granted", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the release did not wake the second client's poll")
	}
}

func TestLeases_IdempotentCreate(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	c1, first := e.post(1, resources.LeaseRequest{})
	c2, again := e.post(1, resources.LeaseRequest{Weight: 90})
	if c1 != http.StatusCreated || c2 != http.StatusOK {
		t.Fatalf("statuses = %d, %d, want 201 then 200", c1, c2)
	}
	if again.ID != first.ID || again.State != resources.StateWaiting {
		t.Fatalf("replay = %+v, want the same waiting row %s", again, first.ID)
	}
	if rows, _ := e.m.store.Waiting(); len(rows) != 1 || rows[0].Weight != 35 {
		t.Fatalf("a replay must not queue a second row: %+v", rows)
	}
	// After the grant the replay returns the granted row.
	e.adm.setMax(1)
	e.m.pass(context.Background())
	_, held := e.post(1, resources.LeaseRequest{})
	if held.ID != first.ID || !held.Granted {
		t.Fatalf("replay after grant = %+v", held)
	}
}

func TestLeases_PollRenews(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, r := e.post(1, resources.LeaseRequest{})
	created := e.row(r.ID).LeaseUntil
	if want := e.clock.now().UnixMilli() + resources.LeaseS*1000; created != want {
		t.Fatalf("lease_until = %d, want %d", created, want)
	}
	e.clock.advance(20 * time.Second)
	if code, p := e.get(r.ID, 0); code != http.StatusOK || p.State != resources.StateWaiting || p.Position != 1 || p.WaitedMS != 20000 {
		t.Fatalf("poll = %d %+v", code, p)
	}
	if got, want := e.row(r.ID).LeaseUntil, e.clock.now().UnixMilli()+resources.LeaseS*1000; got != want {
		t.Fatalf("lease_until after poll = %d, want %d", got, want)
	}
	// A poll never moves the lease back.
	e.clock.advance(-10 * time.Second)
	e.get(r.ID, 0)
	if e.row(r.ID).LeaseUntil < created+20000 {
		t.Fatal("a poll moved the lease back")
	}
}

func TestLeases_DeleteWaitingCancels(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, r := e.post(1, resources.LeaseRequest{})
	code, d := e.del(r.ID)
	if code != http.StatusOK || d.State != resources.StateEnded || d.EndReason != resources.EndCancelled || d.Granted {
		t.Fatalf("delete = %d %+v", code, d)
	}
	if rows, _ := e.m.store.Waiting(); len(rows) != 0 {
		t.Fatalf("still waiting: %+v", rows)
	}
	// Idempotent: the same row again, still cancelled.
	code, d2 := e.del(r.ID)
	if code != http.StatusOK || d2.EndReason != resources.EndCancelled {
		t.Fatalf("second delete = %d %+v", code, d2)
	}
	rec := e.do(http.MethodDelete, "/api/resources/leases/nope", nil)
	if rec.Code != http.StatusNotFound || decodeErr(t, rec).Error != resources.ErrNoLease {
		t.Fatalf("unknown id = %d %s", rec.Code, rec.Body.String())
	}
}

func TestLeases_DeleteHeldReleases(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	_, r := e.post(1, resources.LeaseRequest{})
	code, d := e.del(r.ID)
	if code != http.StatusOK || d.EndReason != resources.EndReleased || !d.Granted {
		t.Fatalf("delete = %d %+v", code, d)
	}
	if row := e.row(r.ID); row.State != resources.StateEnded || row.EndedAt == 0 {
		t.Fatalf("row = %+v", row)
	}
}

func TestLeases_ModeAdviseGrantsWithWouldWait(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeAdvise)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})
	for name, r := range map[string]resources.LeaseResponse{"first": a, "second": b} {
		if !r.Granted || r.State != resources.StateHeld || r.WouldWait == nil {
			t.Fatalf("%s = %+v, want granted at once with would_wait", name, r)
		}
	}
	if *a.WouldWait {
		t.Error("the first request fits: would_wait must be false")
	}
	if !*b.WouldWait {
		t.Error("the second request would have waited in mode lease: would_wait must be true")
	}
	if rows, _ := e.m.store.Active(); len(rows) != 2 || !e.row(b.ID).WouldWait || e.row(a.ID).WouldWait {
		t.Fatalf("both rows are recorded as held, would_wait stored: %+v", rows)
	}
}

func TestLeases_ModeOffNoRow(t *testing.T) {
	for _, mode := range []string{resources.ModeOff, resources.ModeMeasure} {
		t.Run(mode, func(t *testing.T) {
			e := newLeaseEnv(t, mode)
			code, r := e.post(1, resources.LeaseRequest{})
			if code != http.StatusOK || !r.Granted || r.ID != "" || r.State != resources.StateNone || r.Mode != mode {
				t.Fatalf("response = %d %+v", code, r)
			}
			if rows, _ := e.m.store.Active(); len(rows) != 0 {
				t.Fatalf("a row was recorded: %+v", rows)
			}
			rec := e.do(http.MethodGet, "/api/resources/leases/L1", nil)
			if rec.Code != http.StatusNotFound || decodeErr(t, rec).Error != resources.ErrNoLease {
				t.Fatalf("GET of an unknown id = %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLeases_SessionNewFallsBackToProcess(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.m.roots = &fakeRoots{roots: []resources.Root{{SessionID: "sid-known", PID: 700, ProcStart: "Thu Oct  9 00:00:07 2026"}}}

	// A known session: the lease measures what its agent process starts.
	code, r := e.post(1, resources.LeaseRequest{Kind: "build", SessionID: "sid-known", HolderPID: 31, HolderStart: "x", Scope: resources.ScopeSessionNew})
	if code != http.StatusCreated || r.ScopeFallback {
		t.Fatalf("known session = %d %+v", code, r)
	}
	row := e.row(r.ID)
	if row.Scope != resources.ScopeSessionNew || row.HolderPID != 700 || row.HolderStart != "Thu Oct  9 00:00:07 2026" {
		t.Fatalf("row = %+v, want the registry's origin", row)
	}

	// An unknown session: falls back to the holder, and says so.
	_, r = e.post(2, resources.LeaseRequest{Kind: "build", SessionID: "sid-unknown", HolderPID: 31, HolderStart: "hs", Scope: resources.ScopeSessionNew})
	if !r.ScopeFallback {
		t.Fatalf("response = %+v, want scope_fallback", r)
	}
	row = e.row(r.ID)
	if row.Scope != resources.ScopeProcess || row.HolderPID != 31 || row.HolderStart != "hs" {
		t.Fatalf("fallback row = %+v", row)
	}

	// No root source at all falls back too; so does a registry error.
	e.m.roots = nil
	_, r = e.post(3, resources.LeaseRequest{SessionID: "sid-known", Scope: resources.ScopeSessionNew})
	if !r.ScopeFallback {
		t.Fatalf("no root source: %+v", r)
	}
	e.m.roots = &fakeRoots{err: context.DeadlineExceeded}
	_, r = e.post(4, resources.LeaseRequest{SessionID: "sid-known", Scope: resources.ScopeSessionNew})
	if !r.ScopeFallback {
		t.Fatalf("registry error: %+v", r)
	}
	// A process snapshot that cannot be taken falls back as well.
	e.m.roots = &fakeRoots{}
	e.m.procSnapshot = func(context.Context) (*iagent.ProcessSnapshot, error) { return nil, context.Canceled }
	_, r = e.post(5, resources.LeaseRequest{SessionID: "sid-known", Scope: resources.ScopeSessionNew})
	if !r.ScopeFallback {
		t.Fatalf("process table error: %+v", r)
	}
}

func TestLeases_Validation(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	good := resources.LeaseRequest{ClientID: clientID(1), Kind: "test-full", HolderPID: 10}
	cases := []struct {
		name string
		mut  func(*resources.LeaseRequest)
		code string
	}{
		{"both kind and weight", func(r *resources.LeaseRequest) { r.Weight = 20 }, resources.ErrBadRequest},
		{"neither kind nor weight", func(r *resources.LeaseRequest) { r.Kind = "" }, resources.ErrBadRequest},
		{"unknown kind", func(r *resources.LeaseRequest) { r.Kind = "mystery" }, resources.ErrUnknownKind},
		{"weight negative", func(r *resources.LeaseRequest) { r.Kind, r.Weight = "", -1 }, resources.ErrBadRequest},
		{"weight 201", func(r *resources.LeaseRequest) { r.Kind, r.Weight = "", 201 }, resources.ErrBadRequest},
		{"client id not a uuid", func(r *resources.LeaseRequest) { r.ClientID = "not-a-uuid" }, resources.ErrBadRequest},
		{"client id uuid v1", func(r *resources.LeaseRequest) { r.ClientID = "00000000-0000-1000-8000-000000000001" }, resources.ErrBadRequest},
		{"client id missing", func(r *resources.LeaseRequest) { r.ClientID = "" }, resources.ErrBadRequest},
		{"wait 600", func(r *resources.LeaseRequest) { r.WaitS = 600 }, resources.ErrBadRequest},
		{"wait negative", func(r *resources.LeaseRequest) { r.WaitS = -1 }, resources.ErrBadRequest},
		{"holder pid missing", func(r *resources.LeaseRequest) { r.HolderPID = 0 }, resources.ErrBadRequest},
		{"bad scope", func(r *resources.LeaseRequest) { r.Scope = "galaxy" }, resources.ErrBadRequest},
		{"session-new without a session", func(r *resources.LeaseRequest) { r.Scope = resources.ScopeSessionNew }, resources.ErrBadRequest},
		{"huge field", func(r *resources.LeaseRequest) { r.ToolUseID = strings.Repeat("x", 300) }, resources.ErrBadRequest},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := good
			c.mut(&req)
			rec := e.do(http.MethodPost, "/api/resources/leases", req)
			if rec.Code != http.StatusBadRequest || decodeErr(t, rec).Error != c.code {
				t.Fatalf("= %d %s, want 400 %s", rec.Code, rec.Body.String(), c.code)
			}
		})
	}
	if rows, _ := e.m.store.Waiting(); len(rows) != 0 {
		t.Fatalf("a refused request left rows: %+v", rows)
	}
	for name, body := range map[string]string{"not json": "{", "empty": ""} {
		if rec := e.do(http.MethodPost, "/api/resources/leases", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	// The boundaries are accepted, and an empty scope means process.
	for i, req := range []resources.LeaseRequest{
		{Weight: 1}, {Weight: 200}, {Kind: "test-full", WaitS: 590}, {Kind: "build", Scope: resources.ScopeProcess},
	} {
		req.ClientID, req.HolderPID = clientID(10+i), 10
		if rec := e.do(http.MethodPost, "/api/resources/leases", req); rec.Code != http.StatusCreated {
			t.Errorf("boundary %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	// Validation also holds in modes that record nothing.
	e.set.set(resources.Settings{Mode: resources.ModeOff})
	bad := good
	bad.ClientID = "x"
	if rec := e.do(http.MethodPost, "/api/resources/leases", bad); rec.Code != http.StatusBadRequest {
		t.Errorf("mode off accepted a bad request: %d", rec.Code)
	}
}

func TestLeases_WaitDefaultsToSettingsDeadline(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	dl := 120
	e.set.set(resources.Settings{Mode: resources.ModeLease, DeadlineS: &dl})
	e.adm.setMax(0)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{WaitS: 15})
	if got := e.row(a.ID).DeadlineAt - e.row(a.ID).CreatedAt; got != 120000 {
		t.Fatalf("default deadline = %d ms, want 120000", got)
	}
	if got := e.row(b.ID).DeadlineAt - e.row(b.ID).CreatedAt; got != 15000 {
		t.Fatalf("explicit deadline = %d ms, want 15000", got)
	}
}

func TestLeases_DeleteByClientID(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	_, r := e.post(7, resources.LeaseRequest{})
	if !r.Granted {
		t.Fatalf("setup: %+v", r)
	}
	rec := e.do(http.MethodDelete, "/api/resources/leases?client_id="+clientID(7), nil)
	got := decodeLease(t, rec)
	if rec.Code != http.StatusOK || got.ID != r.ID || got.State != resources.StateEnded || got.EndReason != resources.EndReleased {
		t.Fatalf("delete by client id = %d %+v", rec.Code, got)
	}
	// Idempotent.
	rec = e.do(http.MethodDelete, "/api/resources/leases?client_id="+clientID(7), nil)
	if again := decodeLease(t, rec); rec.Code != http.StatusOK || again.State != resources.StateEnded {
		t.Fatalf("second delete = %d %+v", rec.Code, again)
	}
	// A client id the daemon never saw is not an error.
	rec = e.do(http.MethodDelete, "/api/resources/leases?client_id="+clientID(99), nil)
	if none := decodeLease(t, rec); rec.Code != http.StatusOK || none.State != resources.StateNone {
		t.Fatalf("unknown client id = %d %s", rec.Code, rec.Body.String())
	}
	for _, q := range []string{"", "?client_id=nope"} {
		if rec := e.do(http.MethodDelete, "/api/resources/leases"+q, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("query %q: status %d, want 400", q, rec.Code)
		}
	}
	// A waiting row is cancelled by client id too.
	e.adm.setMax(0)
	e.post(8, resources.LeaseRequest{})
	rec = e.do(http.MethodDelete, "/api/resources/leases?client_id="+clientID(8), nil)
	if w := decodeLease(t, rec); w.EndReason != resources.EndCancelled {
		t.Fatalf("waiting by client id = %+v", w)
	}
}

func TestLeases_ReleaseFreesTheQueue(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})
	_, c := e.post(3, resources.LeaseRequest{})
	if b.Position != 1 || c.Position != 2 {
		t.Fatalf("positions = %d, %d", b.Position, c.Position)
	}
	// Cancelling the first waiter moves the second up.
	e.del(b.ID)
	if _, got := e.get(c.ID, 0); got.Position != 1 {
		t.Fatalf("after the cancel: %+v", got)
	}
	e.del(a.ID) // the release runs a pass: c starts
	if _, got := e.get(c.ID, 0); !got.Granted {
		t.Fatalf("after the release: %+v", got)
	}
}

func TestSettings_WeightChangeAppliesToNextPOST(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(10)
	_, a := e.post(1, resources.LeaseRequest{Kind: "test-full"})
	e.set.set(resources.Settings{Mode: resources.ModeLease, Kinds: map[string]int{"test-full": 60, "custom-kind": 12}})
	_, b := e.post(2, resources.LeaseRequest{Kind: "test-full"})
	_, c := e.post(3, resources.LeaseRequest{Kind: "custom-kind"})
	if e.row(a.ID).Weight != 35 || e.row(b.ID).Weight != 60 || e.row(c.ID).Weight != 12 {
		t.Fatalf("weights = %d, %d, %d, want 35, 60, 12", e.row(a.ID).Weight, e.row(b.ID).Weight, e.row(c.ID).Weight)
	}
}

// --- long-poll wake-up (plan Task 1.4, review #5) ---

func TestPoll_GrantBetweenReadAndWait(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	_, a := e.post(1, resources.LeaseRequest{})
	_, b := e.post(2, resources.LeaseRequest{})
	// Right after the poll read B as waiting (and released the lock), the
	// first lease is released and B is granted: that transition must still
	// wake the poll.
	fired := false
	e.m.afterRead = func(id string) {
		if id == b.ID && !fired {
			fired = true
			e.del(a.ID)
		}
	}
	start := time.Now()
	r := within(t, 5*time.Second, "the poll", func() resources.LeaseResponse { _, r := e.get(b.ID, 20); return r })
	if !r.Granted {
		t.Fatalf("poll = %+v, want granted", r)
	}
	if !fired {
		t.Fatal("the seam never ran")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the poll slept through the transition (%v)", time.Since(start))
	}
}

func TestPoll_DeleteWakes(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, b := e.post(1, resources.LeaseRequest{})
	got := make(chan resources.LeaseResponse, 1)
	go func() { _, r := e.get(b.ID, 20); got <- r }()
	time.Sleep(30 * time.Millisecond)
	e.del(b.ID)
	select {
	case r := <-got:
		if r.State != resources.StateEnded || r.EndReason != resources.EndCancelled {
			t.Fatalf("poll = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a delete did not wake the poll")
	}
}

func TestPoll_AbandonWakes(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, b := e.post(1, resources.LeaseRequest{})
	got := make(chan resources.LeaseResponse, 1)
	go func() { _, r := e.get(b.ID, 20); got <- r }()
	time.Sleep(30 * time.Millisecond)

	// What P1-2b's sweeper does for a waiter nobody polls any more.
	e.clock.advance(time.Minute)
	e.m.stateMu.Lock()
	won, err := e.m.closeExpiredLocked(b.ID)
	e.m.stateMu.Unlock()
	if err != nil || !won {
		t.Fatalf("close: won=%v err=%v", won, err)
	}
	select {
	case r := <-got:
		if r.State != resources.StateEnded || r.EndReason != resources.EndAbandoned {
			t.Fatalf("poll = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("an abandon did not wake the poll")
	}
}

func TestPoll_TimesOutAnsweringTheRow(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, b := e.post(1, resources.LeaseRequest{})
	start := time.Now()
	code, r := e.get(b.ID, 1)
	if code != http.StatusOK || r.State != resources.StateWaiting || r.Granted {
		t.Fatalf("poll = %d %+v", code, r)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 3*time.Second {
		t.Fatalf("a 1 s poll took %v", d)
	}
	// ?wait is clamped, and junk is a 400.
	if rec := e.do(http.MethodGet, "/api/resources/leases/"+b.ID+"?wait=abc", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("wait=abc: %d", rec.Code)
	}
}

func TestPoll_StopAnswersTheRow(t *testing.T) {
	e := newLeaseEnv(t, resources.ModeLease)
	e.adm.setMax(0)
	_, b := e.post(1, resources.LeaseRequest{})
	got := make(chan resources.LeaseResponse, 1)
	go func() { _, r := e.get(b.ID, 20); got <- r }()
	time.Sleep(30 * time.Millisecond)
	e.m.stopRun()
	select {
	case r := <-got:
		if r.State != resources.StateWaiting {
			t.Fatalf("poll = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not release the poll")
	}
}

func TestPoll_AfterRestartObservesPersistedRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.db")
	clock := newFakeClock()
	first := newLeaseEnvAt(t, resources.ModeLease, path, clock, nil)
	_, a := first.post(1, resources.LeaseRequest{})
	_, b := first.post(2, resources.LeaseRequest{})
	if !a.Granted || b.Granted {
		t.Fatalf("setup: %+v %+v", a, b)
	}
	_ = first.m.Stop(context.Background()) // the daemon restarts

	second := newLeaseEnvAt(t, resources.ModeLease, path, clock, nil)
	second.m.boot()
	got := make(chan resources.LeaseResponse, 1)
	go func() { _, r := second.get(b.ID, 20); got <- r }()
	time.Sleep(30 * time.Millisecond)
	select {
	case r := <-got:
		t.Fatalf("the persisted waiter was answered at once: %+v", r)
	default:
	}
	second.del(a.ID) // the first client releases against the new daemon
	select {
	case r := <-got:
		if !r.Granted {
			t.Fatalf("poll = %+v, want granted", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the persisted waiter never saw its grant")
	}
}
