package teammod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/hostconfig"
	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// fakeOrigins is the OriginResolver of these tests: inbox "/tmp/10.sock" is
// session sid-1 (cwd /w, in tmux), "/tmp/20.sock" is sid-2; dead marks a
// session gone for LiveSession. readErr makes ResolveOrigin answer a
// registry read error; entered/block (set before the request is sent) make
// it signal on entered and then wait on block, so a test can act while a
// create is inside the resolver.
type fakeOrigins struct {
	mu      sync.Mutex
	dead    map[string]bool
	readErr bool
	entered chan struct{}
	block   chan struct{}
	// cleared is what the registry shows AFTER a /clear: new session id →
	// the pid of the process that now carries it (ResolveOriginBySession).
	cleared map[string]int
	// unknown marks a session the registry cannot place (an unverifiable
	// file of a live pid): LiveSession false, LeadPresence unknown.
	unknown map[string]bool
	// leadAsked is what LeadPresence was last asked per session: "pid procStart".
	leadAsked map[string]string
	// shown overrides what the registry shows for a session (the roster's
	// tests: a title or tmux session of its own); hidden makes the registry
	// not list it at all. Both are by session id.
	shown  map[string]team.Origin
	hidden map[string]bool
	// batchHook, when set, runs first in every ResolveOriginsBySession (the
	// roster's build): tests block or count there. batchCalls and
	// singleCalls count the two forms of the by-session resolve.
	batchHook   func()
	batchCalls  int
	singleCalls int
}

// setReadErr makes (or stops making) every registry read fail.
func (f *fakeOrigins) setReadErr(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readErr = v
}

// show sets the registry entry of o.SessionID; the next resolve answers o.
func (f *fakeOrigins) show(o team.Origin) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.shown == nil {
		f.shown = map[string]team.Origin{}
	}
	f.shown[o.SessionID] = o
}

// hide makes the registry stop listing sid (it is no longer live).
func (f *fakeOrigins) hide(sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hidden == nil {
		f.hidden = map[string]bool{}
	}
	f.hidden[sid] = true
}

var fixtureOrigins = map[string]team.Origin{
	"/tmp/10.sock": {SessionID: "sid-1", Ref: "_abc123", Name: "n10", PID: 10, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w", Tmux: "mt0:@1.%1"},
	"/tmp/20.sock": {SessionID: "sid-2", Ref: "_def456", PID: 20, ProcStart: "Sun Sep 13 15:22:36 2026", Cwd: "/w2"},
}

func (f *fakeOrigins) ResolveOrigin(inbox string) (team.Origin, bool, error) {
	f.mu.Lock()
	readErr, entered, block := f.readErr, f.entered, f.block
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
		<-block
	}
	if readErr {
		return team.Origin{}, false, errors.New("read registry: not a directory")
	}
	o, ok := fixtureOrigins[inbox]
	f.mu.Lock()
	shown, isShown := f.shown[o.SessionID]
	f.mu.Unlock()
	if ok && isShown {
		o = shown
	}
	return o, ok, nil
}

// uid is the i-th fixture id: a valid UUID v4 (version nibble 4, variant
// 10xx), which is what the handler requires of id (spec §6.1).
func uid(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012x", i) }

// sequentialIDs mints the daemon-side ids relay begin uses: op then request,
// "11111111-…-0001", "…-0002", … so tests can name them.
func sequentialIDs() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("11111111-1111-4111-8111-%012x", n)
	}
}

func rid(i int) string { return fmt.Sprintf("11111111-1111-4111-8111-%012x", i) }

// ResolveOriginBySession answers the fixture origin with that session id.
func (f *fakeOrigins) ResolveOriginBySession(sid string) (team.Origin, bool, error) {
	f.mu.Lock()
	f.singleCalls++
	f.mu.Unlock()
	return f.lookup(sid)
}

// ResolveOriginsBySession is the batch form: one call, one hook, one count.
func (f *fakeOrigins) ResolveOriginsBySession(ids []string) (map[string]team.Origin, error) {
	return f.resolveMany(ids, f.lookup)
}

// resolveMany is the batch over one(sid): a read error fails it whole, a
// session one does not list is absent.
func (f *fakeOrigins) resolveMany(ids []string, one func(string) (team.Origin, bool, error)) (map[string]team.Origin, error) {
	f.mu.Lock()
	f.batchCalls++
	hook := f.batchHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	out := map[string]team.Origin{}
	for _, sid := range ids {
		o, ok, err := one(sid)
		if err != nil {
			return nil, err
		}
		if ok {
			out[sid] = o
		}
	}
	return out, nil
}

func (f *fakeOrigins) lookup(sid string) (team.Origin, bool, error) {
	f.mu.Lock()
	readErr, hidden := f.readErr, f.hidden[sid]
	shown, isShown := f.shown[sid]
	f.mu.Unlock()
	if readErr {
		return team.Origin{}, false, errors.New("read registry: not a directory")
	}
	if hidden {
		return team.Origin{}, false, nil
	}
	if isShown {
		return shown, true, nil
	}
	for _, o := range fixtureOrigins {
		if o.SessionID == sid {
			return o, true, nil
		}
	}
	f.mu.Lock()
	pid, ok := f.cleared[sid]
	f.mu.Unlock()
	if ok {
		return team.Origin{SessionID: sid, Ref: ipeers.RefID(sid), PID: pid}, true, nil
	}
	return team.Origin{}, false, nil
}

// fakeSwitches is the hostconfig RelaySwitchReader of these tests, and its
// RelayPromptReader (the same row in production).
type fakeSwitches struct {
	mu         sync.Mutex
	sw         hostconfig.RelaySwitches
	err        error
	prompts    team.RelayPromptBodies
	promptsErr error
}

func (f *fakeSwitches) RelayPrompts() (team.RelayPromptBodies, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts, f.promptsErr
}

func (f *fakeSwitches) RelaySwitches() (hostconfig.RelaySwitches, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sw, f.err
}

func (f *fakeSwitches) set(sw hostconfig.RelaySwitches) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sw = sw
}

func (f *fakeOrigins) LiveSession(sid string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.dead[sid] && !f.unknown[sid]
}

func (f *fakeOrigins) LeadPresence(sid string, pid int, procStart string) peersmod.Presence {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.leadAsked == nil {
		f.leadAsked = map[string]string{}
	}
	f.leadAsked[sid] = fmt.Sprintf("%d %s", pid, procStart)
	switch {
	case f.unknown[sid]:
		return peersmod.PresenceUnknown
	case f.dead[sid]:
		return peersmod.PresenceGone
	}
	return peersmod.PresenceLive
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
	t        *testing.T
	m        *Module
	mux      *http.ServeMux
	core     *core.Core
	clock    atomic.Int64 // unix ms
	origins  *fakeOrigins
	switches *fakeSwitches
	titles   *fakeTitles
	usage    *fakeUsage
	unatt    *fakeUnattended
	sub      *core.EventSubscriber
	// createReqEdit, when set, edits every createReq body.
	createReqEdit func(*team.CreateApprovalRequest)
	spawnFakes
}

// fakeUsage is the agent module's ContextUsageReader of these tests: the
// per-session statusline reading begin copies model_id / effort from.
// Registered under agent.OwnerResolverKey, as the agent module is in
// production (the team module type-asserts the reader on that service,
// as peers does).
type fakeUsage struct {
	mu sync.Mutex
	by map[string]agent.ContextUsage
}

func (f *fakeUsage) set(sid, model, effort string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.by == nil {
		f.by = map[string]agent.ContextUsage{}
	}
	f.by[sid] = agent.ContextUsage{ModelID: model, Effort: effort}
}

func (f *fakeUsage) ContextUsage(sid string) (agent.ContextUsage, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.by[sid]
	return u, ok
}

var _ agent.ContextUsageReader = (*fakeUsage)(nil)

// fakeTitles records title moves (spec §8.4); *store.PeerLabelStore in production.
type fakeTitles struct {
	mu     sync.Mutex
	moves  [][2]string
	has    map[string]bool // sessions that currently hold a title
	fail   bool            // meta.db is down: every Move errors
	claims [][2]string     // Claim(session, title): a spawned member's title
}

func (f *fakeTitles) Move(from, to string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return false, errors.New("meta.db locked")
	}
	if !f.has[from] {
		return false, nil
	}
	delete(f.has, from)
	f.has[to] = true
	f.moves = append(f.moves, [2]string{from, to})
	return true, nil
}

// newFixture builds the module through Init (a fake resolver in the
// registry, team.db in a TempDir), its routes on a fresh mux, and one test
// subscriber that collects every broadcast.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	// sid-1b / sid-1c are what sid-1's process (pid 10) becomes after a
	// /clear; the registry of these tests already shows them.
	f := &fixture{t: t, origins: &fakeOrigins{cleared: map[string]int{"sid-1b": 10, "sid-1c": 10}}, switches: &fakeSwitches{sw: hostconfig.DefaultRelaySwitches}, titles: &fakeTitles{has: map[string]bool{"sid-1": true}}, usage: &fakeUsage{}, unatt: &fakeUnattended{}}
	f.clock.Store(1_000_000)
	f.core = core.New(core.CoreDeps{Config: &config.Config{HostID: "h:1", DataDir: t.TempDir()}})
	f.core.Registry.Register(peersmod.OriginResolverKey, f.origins)
	f.core.Registry.Register(hostconfig.RelaySwitchesKey, f.switches)
	f.core.Registry.Register(hostconfig.RelayPromptsKey, f.switches)
	f.core.Registry.Register(hostconfig.UnattendedKey, f.unatt)
	f.core.Registry.Register(agent.OwnerResolverKey, f.usage) // the team module asserts agent.ContextUsageReader on it
	f.registerSpawnFakes()
	f.m = New().WithTitles(f.titles)
	f.m.newID = sequentialIDs()
	f.m.clearedWait, f.m.clearedPoll = 200*time.Millisecond, 10*time.Millisecond
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
	r := team.CreateApprovalRequest{ID: id, Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "split the work"}
	if f.createReqEdit != nil {
		f.createReqEdit(&r)
	}
	return r
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
			if ev.Type == team.RosterEventType {
				continue // the roster's tests read their own subscriber
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

// countOps drains the events broadcast so far and counts those with op.
func (f *fixture) countOps(op string) int {
	n := 0
	for _, ev := range f.events() {
		if ev.Op == op {
			n++
		}
	}
	return n
}

// waitForWaiter blocks until a long-poll on id has registered its waiter
// (so a close issued next is guaranteed to have someone to wake).
func waitForWaiter(t *testing.T, f *fixture, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.m.mu.Lock()
		n := len(f.m.waiters[id])
		f.m.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no long-poll registered on %s within 5 s", id)
}

func TestCreate_NewThenIdempotentThenConflict(t *testing.T) {
	f := newFixture(t)
	a := f.create(uid(1))
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
	if len(evs) != 1 || evs[0].Op != "opened" || evs[0].Approval == nil || evs[0].Approval.ID != uid(1) {
		t.Fatalf("events after create = %+v", evs)
	}

	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1))) // same request again
	if code != http.StatusOK || decodeApproval(t, body).ID != uid(1) || len(f.events()) != 0 {
		t.Fatalf("retry: %d %s (must be 200, same row, no new event)", code, body)
	}
	other := f.createReq(uid(1))
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
		{"no reason", team.CreateApprovalRequest{ID: uid(9), Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "  "}, 400, team.ErrBadRequest},
		{"bad kind", team.CreateApprovalRequest{ID: uid(9), Kind: "boss", OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrBadRequest},
		{"self_relay", team.CreateApprovalRequest{ID: uid(9), Kind: team.KindSelfRelay, OriginInbox: "/tmp/10.sock", Reason: "r"}, 400, team.ErrUnsupportedKind},
		{"unknown inbox", team.CreateApprovalRequest{ID: uid(9), Kind: team.KindLead, OriginInbox: "/tmp/99.sock", Reason: "r"}, 400, team.ErrOriginUnknown},
		{"negative wait", team.CreateApprovalRequest{ID: uid(9), Kind: team.KindLead, OriginInbox: "/tmp/10.sock", Reason: "r", WaitS: -1}, 400, team.ErrBadRequest},
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
	capped := f.createReq(uid(10))
	capped.MaxMembers, capped.WaitS, capped.Roots = 20, 999, []string{"sub/../a", "/abs/b/"}
	code, body := f.do(http.MethodPost, "/api/team/approvals", capped)
	a := decodeApproval(t, body)
	var p team.LeadPayload
	_ = json.Unmarshal(a.Payload, &p)
	if code != 201 || p.MaxMembers != 8 || a.DeadlineAt != 1_000_000+600_000 || len(p.Roots) != 2 || p.Roots[0] != "/w/a" || p.Roots[1] != "/abs/b" {
		t.Fatalf("capped: %d %+v payload=%+v", code, a, p)
	}

	// One open lead request per origin session: a second id is request_open, carrying the first.
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(2)))
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrRequestOpen || e.Approval == nil || e.Approval.ID != uid(10) {
		t.Fatalf("request_open: %d %s", code, body)
	}
	// Another session is not blocked (a tick later, so the list order below is by creation time).
	f.clock.Add(1)
	second := f.createReq(uid(3))
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
		listed.Approvals[0].ID != uid(10) || listed.Approvals[1].ID != uid(3) {
		t.Fatalf("list: %d %s err=%v", code, body, err)
	}
	if code, body := f.do(http.MethodGet, "/api/team/approvals?state=closed", nil); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
		t.Fatalf("state=closed: %d %s", code, body)
	}

	// Stopping: create answers 503 not_ready.
	_ = f.m.Stop(context.Background())
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(4)))
	if code != http.StatusServiceUnavailable || decodeErr(t, body).Error != team.ErrNotReady {
		t.Fatalf("while stopping: %d %s", code, body)
	}
}

// TestCreate_RetryAfterCloseWinsOverNewerOpen: create is idempotent on id
// (spec §6.2) before it is anything else. A retry of a request that has
// since closed must answer 200 with that row even when the origin has a
// newer open request — the request_open check only applies to new ids.
func TestCreate_RetryAfterCloseWinsOverNewerOpen(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if _, won, err := f.m.store.CloseIfOpen(uid(1), Close{State: team.StateDenied, DecidedAt: f.clock.Load()}); err != nil || !won {
		t.Fatalf("close A: won=%v err=%v", won, err)
	}
	f.create(uid(2)) // B is now the origin's open request
	_ = f.events()
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if code != http.StatusOK {
		t.Fatalf("retry of closed A: %d %s (want 200 with A's row, not request_open)", code, body)
	}
	if a := decodeApproval(t, body); a.ID != uid(1) || a.State != team.StateDenied {
		t.Fatalf("retry returned %+v, want A denied", a)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after an idempotent retry", n)
	}
	// Reusing A's id with a different request is still id_conflict, not request_open.
	other := f.createReq(uid(1))
	other.Reason = "something else"
	if code, body := f.do(http.MethodPost, "/api/team/approvals", other); code != http.StatusConflict || decodeErr(t, body).Error != team.ErrIDConflict {
		t.Fatalf("id reuse after close: %d %s", code, body)
	}
}

// TestCreate_RejectsNonUUIDv4: id is the CLI-generated UUID v4 (spec §6.1);
// anything else is a bad request. Case is not significant: an uppercase v4
// is accepted and stored in canonical lowercase, so its lowercase retry is
// the same request.
func TestCreate_RejectsNonUUIDv4(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{
		"x",
		"../etc",
		"11111111-1111-1111-8111-111111111111", // version 1
		"11111111-1111-4111-1111-111111111111", // v4 nibble but a non-RFC 4122 variant
		"00000000-0000-4000-8000-00000000000",  // one hex digit short
	} {
		code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(id))
		if e := decodeErr(t, body); code != http.StatusBadRequest || e.Error != team.ErrBadRequest || e.Detail != "id must be a UUID v4" {
			t.Errorf("id %q: %d %s, want 400 bad_request 'id must be a UUID v4'", id, code, body)
		}
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after rejected ids", n)
	}
	upper := "AAAAAAAA-BBBB-4CCC-9DDD-EEEEEEEEEEEE"
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(upper))
	if code != http.StatusCreated || decodeApproval(t, body).ID != strings.ToLower(upper) {
		t.Fatalf("uppercase v4: %d %s (want 201, id stored lowercase)", code, body)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals", f.createReq(strings.ToLower(upper)))
	if code != http.StatusOK || decodeApproval(t, body).ID != strings.ToLower(upper) {
		t.Fatalf("lowercase retry of an uppercase create: %d %s (want 200, same row)", code, body)
	}
	if got := len(f.events()); got != 1 {
		t.Fatalf("%d opened events, want 1", got)
	}
}

// TestCreate_StopDuringCreateRefuses: a create that passed the entry
// stopping check before Stop ran must still not open a row. Stop and the
// create's write section are serialised on createMu, and the create
// re-checks stopping after taking it. The resolver blocks the create at a
// point after the entry check and before the lock so the interleaving is
// deterministic.
func TestCreate_StopDuringCreateRefuses(t *testing.T) {
	f := newFixture(t)
	f.origins.entered = make(chan struct{})
	f.origins.block = make(chan struct{})
	type result struct {
		code int
		body []byte
	}
	done := make(chan result, 1)
	go func() {
		code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
		done <- result{code, body}
	}()
	<-f.origins.entered // the create is inside ResolveOrigin: past the entry check
	if err := f.m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(f.origins.block)
	res := <-done
	if res.code != http.StatusServiceUnavailable || decodeErr(t, res.body).Error != team.ErrNotReady {
		t.Fatalf("create racing Stop: %d %s, want 503 not_ready", res.code, res.body)
	}
	open, err := f.m.store.ListOpen()
	if err != nil || len(open) != 0 {
		t.Fatalf("open rows after a refused create = %d err=%v", len(open), err)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after a refused create", n)
	}
}

// TestCreate_RegistryReadErrorIs503: a registry that cannot be read is not
// an unknown origin. The create answers 503 not_ready so the restart-aware
// CLI retries, instead of 400 origin_unknown, which it would give up on.
func TestCreate_RegistryReadErrorIs503(t *testing.T) {
	f := newFixture(t)
	f.origins.readErr = true
	code, body := f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(1)))
	if e := decodeErr(t, body); code != http.StatusServiceUnavailable || e.Error != team.ErrNotReady || !strings.Contains(e.Detail, "registry unavailable") {
		t.Fatalf("registry read error: %d %s, want 503 not_ready 'registry unavailable; retry'", code, body)
	}
	if open, err := f.m.store.ListOpen(); err != nil || len(open) != 0 || len(f.events()) != 0 {
		t.Fatalf("a refused create must leave no row or event: open=%d err=%v", len(open), err)
	}
	f.origins.readErr = false
	f.create(uid(1)) // the retry succeeds once the registry reads again
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
				codes[i], _ = f.do(http.MethodPost, "/api/team/approvals", f.createReq(uid(100+i)))
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

func TestList_OpenOnly(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.clock.Add(1)
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	code, body := f.do(http.MethodGet, "/api/team/approvals?state=open", nil)
	var out struct {
		Approvals []team.Approval `json:"approvals"`
	}
	if err := json.Unmarshal(body, &out); err != nil || code != 200 || len(out.Approvals) != 1 || out.Approvals[0].ID != uid(2) {
		t.Fatalf("list: %d %s err=%v", code, body, err)
	}
	if code, _ := f.do(http.MethodGet, "/api/team/approvals?state=closed", nil); code != 400 {
		t.Fatalf("state=closed: %d", code)
	}
}

func TestGet_RenewsLeaseAndWakesOnDelete(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	f.clock.Add(10_000)
	code, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=0", nil)
	if a := decodeApproval(t, body); code != 200 || a.LeaseUntil != 1_010_000+30_000 {
		t.Fatalf("poll must renew the lease: %d %+v", code, a)
	}
	if code, body := f.do(http.MethodGet, "/api/team/approvals/nope", nil); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Fatalf("unknown id: %d %s", code, body)
	}
	if code, _ := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=abc", nil); code != 400 {
		t.Fatalf("bad wait: %d", code)
	}
	if code, _ := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=-1", nil); code != 400 {
		t.Fatalf("negative wait: %d", code)
	}

	done := make(chan team.Approval, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=20", nil)
		done <- decodeApproval(t, body)
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	code, body = f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateCancelled || a.DecidedAt != 1_010_000 || a.DecidedBy != nil {
		t.Fatalf("delete: %d %+v", code, a)
	}
	select {
	case a := <-done:
		if a.State != team.StateCancelled || time.Since(start) > 5*time.Second {
			t.Fatalf("long-poll woke with %+v after %s", a, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake on cancel")
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
	// A closed row is answered at once, whatever wait says, and its lease
	// is left alone (RenewLease only touches open rows).
	start = time.Now()
	code, body = f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=20", nil)
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateCancelled || a.LeaseUntil != 1_040_000 || time.Since(start) > 2*time.Second {
		t.Fatalf("get after close: %d %+v after %s", code, a, time.Since(start))
	}
}

func TestGet_LongPollReturnsOnStopAndOnTimer(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	start := time.Now()
	if code, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=1", nil); code != 200 || decodeApproval(t, body).State != team.StateOpen {
		t.Fatalf("timer expiry: %d %s", code, body)
	}
	if d := time.Since(start); d < 900*time.Millisecond || d > 5*time.Second {
		t.Fatalf("wait=1 returned after %s", d)
	}

	done := make(chan int, 1)
	go func() {
		code, _ := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=20", nil)
		done <- code
	}()
	time.Sleep(50 * time.Millisecond)
	start = time.Now()
	_ = f.m.Stop(context.Background())
	select {
	case code := <-done:
		if code != 200 || time.Since(start) > 5*time.Second {
			t.Fatalf("on stop: %d after %s", code, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not return on Stop")
	}
	// The row is still open: Stop cuts the wait, it does not close anything.
	if a, _, _ := f.m.store.Get(uid(1)); a.State != team.StateOpen {
		t.Fatalf("row after a Stop-cut long-poll = %s, want open", a.State)
	}
}

// TestGet_WaitIsCappedAndEndsWhenTheClientLeaves: wait=600 is accepted but
// the timer is 25 s at most, so the CLI re-polls (and renews its lease)
// well inside the 30 s lease; and a client that goes away (r.Context()
// cancelled) ends the wait too, otherwise a stuck CLI pins a handler.
func TestGet_WaitIsCappedAndEndsWhenTheClientLeaves(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	if got, err := pollWait("600"); err != nil || got != team.MaxPollWaitS {
		t.Fatalf("pollWait(600) = %d, %v; want %d", got, err, team.MaxPollWaitS)
	}
	if got, err := pollWait("7"); err != nil || got != 7 {
		t.Fatalf("pollWait(7) = %d, %v", got, err)
	}
	if got, err := pollWait(""); err != nil || got != 0 {
		t.Fatalf("pollWait(\"\") = %d, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=20", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		f.mux.ServeHTTP(rec, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
		if rec.Code != 200 || decodeApproval(t, rec.Body.Bytes()).State != team.StateOpen {
			t.Fatalf("after the client left: %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not end when the client went away")
	}
	// No waiter is left behind.
	f.m.mu.Lock()
	n := len(f.m.waiters[uid(1)])
	f.m.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d waiters left after the poll ended", n)
	}
}

// TestGet_WaiterIsRegisteredBeforeTheRead is the mutation gate for the
// long-poll's ordering (plan deviation 11): the row is closed in the window
// between handleGet's read and its wait, through the afterRead hook. With
// the waiter registered before the read, the close wakes it and the poll
// answers the cancelled row at once; registered after the read, the wake is
// lost and the poll would sit out its timer.
func TestGet_WaiterIsRegisteredBeforeTheRead(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	var once sync.Once
	f.m.afterRead = func(id string) {
		once.Do(func() {
			if code, _ := f.do(http.MethodDelete, "/api/team/approvals/"+id, nil); code != 200 {
				t.Errorf("delete inside the window: %d", code)
			}
		})
	}
	start := time.Now()
	code, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=5", nil)
	elapsed := time.Since(start)
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateCancelled {
		t.Fatalf("poll: %d %+v", code, a)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("poll took %s: the close between read and wait was not seen (waiter registered too late)", elapsed)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
}

func TestDelete_RepeatAndUnknown(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	f.events()
	if code, body := f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil); code != 200 || decodeApproval(t, body).State != team.StateCancelled {
		t.Fatalf("delete: %d %s", code, body)
	}
	if n := f.countOps("closed"); n != 1 {
		t.Fatalf("closed events = %d, want 1", n)
	}
	// A second DELETE answers the row as it is and emits nothing.
	code, body := f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	if code != 200 || decodeApproval(t, body).State != team.StateCancelled || len(f.events()) != 0 {
		t.Fatalf("second delete: %d %s", code, body)
	}
	if code, body := f.do(http.MethodDelete, "/api/team/approvals/nope", nil); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Fatalf("delete unknown: %d %s", code, body)
	}
	// The origin may open a new request once the old one is closed.
	f.clock.Add(1)
	f.create(uid(2))
}

func TestDecide_ApproveDenyAlreadyDecided(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	f.events()
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}

	code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide",
		team.DecideRequest{Decision: "approve", Grant: &team.Grant{MaxMembers: 2, Roots: []string{"x", "/y/"}}, Client: client})
	a := decodeApproval(t, body)
	if code != 200 || a.State != team.StateApproved || a.Grant == nil || a.Grant.MaxMembers != 2 ||
		len(a.Grant.Roots) != 2 || a.Grant.Roots[0] != "/w/x" || a.Grant.Roots[1] != "/y" ||
		a.DecidedBy == nil || a.DecidedBy.Kind != "app" || a.DecidedBy.Label != client.Label || a.DecidedBy.Addr != "100.64.0.4:51234" || a.DecidedAt != 1_000_000 {
		t.Fatalf("approve: %d %+v grant=%+v by=%+v", code, a, a.Grant, a.DecidedBy)
	}
	evs := f.events()
	if len(evs) != 1 || evs[0].Op != "closed" || evs[0].Approval.State != team.StateApproved || evs[0].Approval.DecidedBy.Label != client.Label {
		t.Fatalf("events after approve = %+v", evs)
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", team.DecideRequest{Decision: "deny", Client: team.Client{Kind: "app", Label: "Purdex.app @ a19"}})
	e := decodeErr(t, body)
	if code != http.StatusConflict || e.Error != team.ErrAlreadyDecided || e.Approval == nil || e.Approval.DecidedBy == nil || e.Approval.DecidedBy.Label != client.Label {
		t.Fatalf("late decide: %d %s (409 must carry who handled it)", code, body)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after a late decide", n)
	}

	// Deny without a grant edit; approve with a nil grant takes the payload's values.
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+uid(2)+"/decide", team.DecideRequest{Decision: "deny", Client: client})
	if a := decodeApproval(t, body); code != 200 || a.State != team.StateDenied || a.Grant != nil || a.DecidedBy == nil {
		t.Fatalf("deny: %d %+v", code, a)
	}
	f.clock.Add(1)
	third := f.createReq(uid(3))
	third.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", third); code != 201 {
		t.Fatal("third create")
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+uid(3)+"/decide", team.DecideRequest{Decision: "approve", Client: client})
	if a := decodeApproval(t, body); code != 200 || a.Grant == nil || a.Grant.MaxMembers != 3 || len(a.Grant.Roots) != 1 || a.Grant.Roots[0] != "/w2" {
		t.Fatalf("approve with nil grant: %d %+v grant=%+v", code, a, a.Grant)
	}
	// A grant edit with max_members over the cap is capped; no roots keeps the payload's.
	// sid-2 now leads uid(3)'s team (P4-2); it must end before sid-2 may ask again.
	if ended, err := f.m.store.EndTeam(uid(3), "sid-2", team.TeamEndLeadGone, f.clock.Load()); err != nil || !ended {
		t.Fatalf("end uid(3)'s team: ended=%v err=%v", ended, err)
	}
	f.clock.Add(1)
	fourth := f.createReq(uid(4))
	fourth.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", fourth); code != 201 {
		t.Fatal("fourth create")
	}
	code, body = f.do(http.MethodPost, "/api/team/approvals/"+uid(4)+"/decide", team.DecideRequest{Decision: "approve", Grant: &team.Grant{MaxMembers: 20}, Client: client})
	if a := decodeApproval(t, body); code != 200 || a.Grant == nil || a.Grant.MaxMembers != 8 || len(a.Grant.Roots) != 1 || a.Grant.Roots[0] != "/w2" {
		t.Fatalf("approve with a capped grant: %d %+v grant=%+v", code, a, a.Grant)
	}
	f.events()

	for name, req := range map[string]any{
		"bad decision": team.DecideRequest{Decision: "maybe", Client: client},
		"no client":    team.DecideRequest{Decision: "approve"},
		"no label":     team.DecideRequest{Decision: "approve", Client: team.Client{Kind: "app", Label: " "}},
		"bad json":     `{`,
	} {
		if code, body := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", req); code != 400 || decodeErr(t, body).Error != team.ErrBadRequest {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if code, body := f.do(http.MethodPost, "/api/team/approvals/nope/decide", team.DecideRequest{Decision: "deny", Client: client}); code != 404 || decodeErr(t, body).Error != team.ErrNotFound {
		t.Fatalf("unknown id: %d %s", code, body)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after rejected decides", n)
	}
}

// TestDecide_WakesLongPoll: a decide releases the requester's long-poll
// with the decided row, as DELETE does.
func TestDecide_WakesLongPoll(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1))
	done := make(chan team.Approval, 1)
	go func() {
		_, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=20", nil)
		done <- decodeApproval(t, body)
	}()
	time.Sleep(50 * time.Millisecond)
	client := team.Client{Kind: "app", Label: "Purdex.app @ air26"}
	if code, _ := f.do(http.MethodPost, "/api/team/approvals/"+uid(1)+"/decide", team.DecideRequest{Decision: "deny", Client: client}); code != 200 {
		t.Fatalf("deny: %d", code)
	}
	select {
	case a := <-done:
		if a.State != team.StateDenied || a.DecidedBy == nil || a.DecidedBy.Label != client.Label {
			t.Fatalf("long-poll woke with %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long-poll did not wake on decide")
	}
}

// GET /api/team/inflight (spec §9.5) feeds the restart confirm: open
// requests only, and relays_active is on the wire as 0 until P6.
func TestInflight_CountsOpenApprovals(t *testing.T) {
	f := newFixture(t)
	if code, body := f.do(http.MethodGet, "/api/team/inflight", nil); code != 200 || !bytes.Contains(body, []byte(`"approvals_open":0`)) {
		t.Fatalf("none open: %d %s", code, body)
	}
	f.create(uid(1))
	second := f.createReq(uid(2))
	second.OriginInbox = "/tmp/20.sock"
	if code, _ := f.do(http.MethodPost, "/api/team/approvals", second); code != 201 {
		t.Fatal("second create")
	}
	if code, body := f.do(http.MethodGet, "/api/team/inflight", nil); code != 200 || !bytes.Contains(body, []byte(`"approvals_open":2`)) {
		t.Fatalf("two open: %d %s", code, body)
	}
	f.do(http.MethodDelete, "/api/team/approvals/"+uid(1), nil)
	code, body := f.do(http.MethodGet, "/api/team/inflight", nil)
	var got team.InflightResponse
	if err := json.Unmarshal(body, &got); err != nil || code != 200 || got.ApprovalsOpen != 1 || got.RelaysActive != 0 {
		t.Fatalf("one open: %d %s err=%v (want approvals_open 1, relays_active 0)", code, body, err)
	}
	if !bytes.Contains(body, []byte(`"relays_active":0`)) {
		t.Fatalf("relays_active must be on the wire at zero: %s", body)
	}
}

// Review F3: a poll whose lease renewal failed must not answer 200 as if
// the lease were renewed — the sweeper would abandon the request while
// the CLI believes it is still polling. It answers 503 not_ready (the
// restart-aware CLI retries) and the row is untouched. The failure is a
// closed database; the row is checked through a fresh handle on the file.
func TestGet_RenewLeaseFailureIs503(t *testing.T) {
	f := newFixture(t)
	f.create(uid(1)) // lease 1_030_000
	f.events()
	f.clock.Add(10_000)
	if err := f.m.store.Close(); err != nil {
		t.Fatal(err)
	}
	code, body := f.do(http.MethodGet, "/api/team/approvals/"+uid(1)+"?wait=0", nil)
	e := decodeErr(t, body)
	if code != http.StatusServiceUnavailable || e.Error != team.ErrNotReady {
		t.Fatalf("poll with a failing RenewLease: %d %s, want 503 not_ready", code, body)
	}
	s, err := OpenStore(filepath.Join(f.core.Cfg.DataDir, "team.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, ok, err := s.Get(uid(1))
	if err != nil || !ok || a.State != team.StateOpen || a.LeaseUntil != 1_030_000 {
		t.Fatalf("row after the failed poll: %+v ok=%v err=%v, want untouched (open, lease 1030000)", a, ok, err)
	}
	if n := len(f.events()); n != 0 {
		t.Fatalf("%d events after a failed poll", n)
	}
}
