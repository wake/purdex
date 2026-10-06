package teammod

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// fakeOrigins is the OriginResolver of these tests: inbox "/tmp/10.sock" is
// session sid-1 (cwd /w, in tmux), "/tmp/20.sock" is sid-2; dead marks a
// session gone for LiveSession.
type fakeOrigins struct {
	mu   sync.Mutex
	dead map[string]bool
}

var fixtureOrigins = map[string]team.Origin{
	"/tmp/10.sock": {SessionID: "sid-1", Ref: "_abc123", Name: "n10", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w", Tmux: "mt0:@1.%1"},
	"/tmp/20.sock": {SessionID: "sid-2", Ref: "_def456", PID: 20, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w2"},
}

func (f *fakeOrigins) ResolveOrigin(inbox string) (team.Origin, bool) {
	o, ok := fixtureOrigins[inbox]
	return o, ok
}

func (f *fakeOrigins) LiveSession(sid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead[sid]
}

func (f *fakeOrigins) markDead(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dead == nil {
		f.dead = map[string]bool{}
	}
	f.dead[sid] = true
}

type fixture struct {
	t       *testing.T
	m       *Module
	mux     *http.ServeMux
	core    *core.Core
	clock   atomic.Int64 // unix ms
	origins *fakeOrigins
	sub     *core.EventSubscriber
}

// newFixture builds the module through Init (a fake resolver in the
// registry, team.db in a TempDir), its routes on a fresh mux, and one test
// subscriber that collects every broadcast.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, origins: &fakeOrigins{}}
	f.clock.Store(1_000_000)
	f.core = core.New(core.CoreDeps{Config: &config.Config{HostID: "h:1", DataDir: t.TempDir()}})
	f.core.Registry.Register(peersmod.OriginResolverKey, f.origins)
	f.m = New()
	f.m.logf = func(string, ...any) {}
	f.m.now = func() int64 { return f.clock.Load() }
	if err := f.m.Init(f.core); err != nil {
		t.Fatal(err)
	}
	f.mux = http.NewServeMux()
	f.m.RegisterRoutes(f.mux)
	f.sub = f.core.Events.AddTestSubscriber()
	t.Cleanup(func() {
		f.core.Events.RemoveTestSubscriber(f.sub)
		_ = f.m.Stop(context.Background())
		_ = f.m.Close()
	})
	return f
}

func (f *fixture) do(method, path string, body any) (int, []byte) {
	f.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "100.64.0.4:51234"
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (f *fixture) createReq(id string) team.CreateApprovalRequest {
	return team.CreateApprovalRequest{ID: id, Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "split the work"}
}

func (f *fixture) create(id string) team.Approval {
	f.t.Helper()
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(id))
	if code != http.StatusCreated {
		f.t.Fatalf("create %s: %d %s", id, code, body)
	}
	return decodeApproval(f.t, body)
}

func decodeApproval(t *testing.T, body []byte) team.Approval {
	t.Helper()
	var a team.Approval
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode approval: %v; body=%s", err, body)
	}
	return a
}

func decodeErr(t *testing.T, body []byte) team.APIError {
	t.Helper()
	var e team.APIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode APIError: %v; body=%s", err, body)
	}
	return e
}

// events drains everything broadcast so far, decoded to EventValue, in order.
func (f *fixture) events() []team.EventValue {
	f.t.Helper()
	var out []team.EventValue
	for {
		select {
		case raw := <-f.sub.SendCh():
			var ev core.HostEvent
			if err := json.Unmarshal(raw, &ev); err != nil {
				f.t.Fatalf("decode HostEvent: %v", err)
			}
			if ev.Type != team.EventType || ev.Session != "" {
				f.t.Fatalf("event = %+v", ev)
			}
			var v team.EventValue
			if err := json.Unmarshal([]byte(ev.Value), &v); err != nil {
				f.t.Fatalf("decode EventValue: %v", err)
			}
			out = append(out, v)
		default:
			return out
		}
	}
}

func TestCreate_NewThenIdempotentThenConflict(t *testing.T) {
	f := newFixture(t)
	a := f.create("id-1")
	if a.State != team.StateOpen || a.HostID != "h:1" || a.Kind != team.KindLead ||
		a.Origin.SessionID != "sid-1" || a.Origin.Ref != "_abc123" || a.Origin.Tmux != "mt0:@1.%1" ||
		a.CreatedAt != 1_000_000 || a.DeadlineAt != 1_000_000+540_000 || a.LeaseUntil != 1_000_000+30_000 {
		t.Fatalf("approval = %+v", a)
	}
	var p team.LeadPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil || p.Reason != "split the work" || p.MaxMembers != 3 || len(p.Roots) != 1 || p.Roots[0] != "/w" {
		t.Fatalf("payload = %+v err=%v (defaults: max_members 3, roots [cwd])", p, err)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval == nil || evs[0].Approval.ID != "id-1" {
		t.Fatalf("events after create = %+v", evs)
	}

	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-1")) // same request again
	if code != http.StatusOK || decodeApproval(t, body).ID != "id-1" || len(f.events()) != 0 {
		t.Fatalf("retry: %d %s (must be 200, same row, no new event)", code, body)
	}
	other := f.createReq("id-1")
	other.Reason = "something else"
	code, body = f.do(http.MethodPost, "/api/team/approvals", other)
	if code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
		t.Fatalf("id reuse: %d %s", code, body)
	}
}

func TestCreate_Rejections(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		body   any
		status int
		code   string
	}{
		{"bad json", `{`, 400, team.ErrBadRequest},
		{"no id", team.CreateApprovalRequest{Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrBadRequest},
		{"no reason", team.CreateApprovalRequest{ID: "x", Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "  "}, 400, team.ErrBadRequest},
		{"bad kind", team.CreateApprovalRequest{ID: "x", Kind: "boss", OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrBadRequest},
		{"self_relay", team.CreateApprovalRequest{ID: "x", Kind: team.KindSelfRelay, OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrUnsupportedKind},
		{"unknown inbox", team.CreateApprovalRequest{ID: "x", Kind: team.KindLead, OriginInbox: "/tmp/99.sock", Reason: "r"}, 400, team.ErrOriginUnknown},
		{"negative wait", team.CreateApprovalRequest{ID: "x", Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "r", WaitS: -1}, 400, team.ErrBadRequest},
	}
	for _, tc := range cases {
		code, body := f.do(http.MethodPost, "/api/team/approvals", tc.body)
		if code != tc.status || decodeErr(t, body).Error != tc.code {
			t.Errorf("%s: %d %s, want %d %s", tc.name, code, body, tc.status, tc.code)
		}
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after rejected creates", n)
	}

	// Caps: max_members 20 → 8, wait_s 999 → 600; relative roots resolve against cwd.
	capped := f.createReq("id-cap")
	capped.MaxMembers, capped.WaitS, capped.Roots = 20, 999, []string{"sub/../a", "/abs/b/"}
	code, body := f.do(http.MethodPost, "/api/team/approvals", capped)
	a := decodeApproval(t, body)
	var p team.LeadPayload
	_ = json.Unmarshal(a.Payload, &p)
	if code != 201 || p.MaxMembers != 8 || a.DeadlineAt != 1_000_000+600_000 || len(p.Roots) != 2 || p.Roots[0] != "/w/a" || p.Roots[1] != "/abs/b" {
		t.Fatalf("capped: %d %+v payload=%+v", code, a, p)
	}

	// One open lead request per origin session: a second id is request_open, carrying the first.
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-2"))
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrRequestOpen || e.Approval == nil || e.Approval.ID != "id-cap" {
		t.Fatalf("request_open: %d %s", code, body)
	}
	// Another session is not blocked (a tick later, so the list order below is by creation time).
	f.clock.Add(1)
	second := f.createReq("id-3")
	second.OriginInbox = "/tmp/20.sock"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatalf("second origin: %d %s", code, body)
	}

	// The list shows both open rows, oldest first; only state=open is a valid filter.
	code, body = f.do(http.MethodGet, "/api/team/approvals?state=open", nil)
	var listed struct {
		Approvals []team.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &listed); err != nil || code != 200 || len(listed.Approvals) != 2 ||
		listed.Approvals[0].ID != "id-cap" || listed.Approvals[1].ID != "id-3" {
		t.Fatalf("list: %d %s err=%v", code, body, err)
	}
	if code, body := f.do(http.MethodGet, "/api/team/approvals?state=closed", nil); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
		t.Fatalf("state=closed: %d %s", code, body)
	}

	// Stopping: create answers 503 not_ready.
	_ = f.m.Stop(context.Background())
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-4"))
	if code != http.StatusServiceUnavailable || decodeErr(t, body).Error != team.ErrNotReady {
		t.Fatalf("while stopping: %d %s", code, body)
	}
}

// TestCreate_ConcurrentSameOriginOpensOne is the mutation gate of spec §15
// for create: without createMu serialising the OpenByOrigin check with the
// insert, two different ids from one origin posted at the same instant
// both pass the check and both open. Exactly one 201 and one 409
// request_open must come back, whichever order the handlers ran in.
func TestCreate_ConcurrentSameOriginOpensOne(t *testing.T) {
	for round := 0; round < 20; round++ {
		f := newFixture(t)
		const n = 8
		codes := make([]int, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i], _ = f.do(http.MethodPost, "/api/team/approvals", f.createReq("id-"+string(rune('a'+i))))
			}(i)
		}
		close(start)
		wg.Wait()
		created, conflicts := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusCreated:
				created++
			case http.StatusConflict:
				conflicts++
			default:
				t.Fatalf("round %d: unexpected status %d in %v", round, c, codes)
			}
		}
		if created != 1 || conflicts != n-1 {
			t.Fatalf("round %d: %d created, %d request_open (want 1 / %d): %v", round, created, conflicts, n-1, codes)
		}
		open, err := f.m.store.ListOpen()
		if err != nil || len(open) != 1 {
			t.Fatalf("round %d: open rows = %d err=%v (one origin, one open request)", round, len(open), err)
		}
		if got := len(f.events()); got != 1 {
			t.Fatalf("round %d: %d opened events, want 1", round, got)
		}
	}
}
