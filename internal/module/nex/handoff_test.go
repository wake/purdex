package nex

// P-C.3a task 3: POST /api/sessions/{code}/nex-handoff (spec §4.4). The
// fakes here mirror the stream module's orchestrator tests (fakeCCOperator,
// fakeStreamProber over tmux.FakeExecutor) — copied rather than imported,
// since Go test files do not cross packages.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen"
	nexconfig "lab.protype.tw/wake/nexen/config"
	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/sandbox"
	"lab.protype.tw/wake/nexen/store"

	pdxagent "github.com/wake/purdex/internal/agent"
	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/agent/probe"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// --- fixtures ---

const (
	hoCode      = "abc"
	hoTmuxID    = "$1"
	hoName      = "proj"
	hoTarget    = hoName + ":0"
	hoInstance  = "111:222"
	hoSessionID = "sid-1234"
	hoCwd       = "/work/proj"
)

// handoffSessions is a session provider over a map whose TmuxInstance()
// samples the fake tmux server's generation, so the daemon and the executor
// see one world (a test moves it with SetInstance to model a restart).
type handoffSessions struct {
	mu       sync.Mutex
	sessions map[string]*session.SessionInfo
	tmux     *tmux.FakeExecutor
}

func (f *handoffSessions) ListSessions() ([]session.SessionInfo, error) { return nil, nil }
func (f *handoffSessions) GetSession(code string) (*session.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sessions[code]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}
func (f *handoffSessions) UpdateMeta(string, session.MetaUpdate) error                 { return nil }
func (f *handoffSessions) HandleTerminalWS(http.ResponseWriter, *http.Request, string) {}
func (f *handoffSessions) TmuxInstance() string                                        { return f.tmux.Instance() }

// stubOwnerResolver answers ResolveSessionOwner with a fixed verdict; onResolve
// (when set) runs before the answer, with the fake tmux available to mutate —
// the seam for "the tmux server restarted while identity was being read".
type stubOwnerResolver struct {
	owner     agent.PaneOwner
	found     bool
	err       error
	onResolve func()
	calls     int
}

func (s *stubOwnerResolver) ResolveSessionOwner(context.Context, string) (agent.PaneOwner, bool, error) {
	s.calls++
	if s.onResolve != nil {
		s.onResolve()
	}
	return s.owner, s.found, s.err
}

// handoffProber reads liveness and readiness off the fake tmux pane, the
// way the real prober does off a real one (copy of stream's fakeStreamProber).
type handoffProber struct {
	tmux      *tmux.FakeExecutor
	readiness probe.ReadinessChecker
}

func (f *handoffProber) IsAliveFor(agentType, target string) bool {
	if agentType != "cc" {
		return false
	}
	cmd, err := f.tmux.PaneCurrentCommand(target)
	if err != nil {
		return false
	}
	return strings.TrimSpace(cmd) == "claude"
}

func (f *handoffProber) CheckReadiness(agentType, target string) (probe.ReadinessResult, bool) {
	if agentType != "cc" || f.readiness == nil {
		return probe.ReadinessResult{Status: pdxagent.StatusRunning}, true
	}
	return f.readiness.CheckReadiness(target), true
}

func setPaneShell(fake *tmux.FakeExecutor, target string) {
	fake.SetPaneCommand(target, "zsh")
	fake.SetPaneContent(target, "$ ")
}

func setPaneCCIdle(fake *tmux.FakeExecutor, target string) {
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "❯ ")
}

func setPaneCCRunning(fake *tmux.FakeExecutor, target string) {
	fake.SetPaneCommand(target, "claude")
	fake.SetPaneContent(target, "⠋ Working...")
}

// recordingCCOperator logs every call in order. An injected error is
// returned as-is; a gate channel parks the call until it is closed or the
// operator's ctx expires — which is how the handler's ctx sizing is
// observed: a parked call returns ctx.Err() exactly when the handler's
// deadline passes. A successful Exit flips the pane to a shell, as the real
// one leaves it; onExit (when set) runs first.
type recordingCCOperator struct {
	mu            sync.Mutex
	calls         []string
	interruptErr  error
	exitErr       error
	interruptGate chan struct{}
	exitGate      chan struct{}
	exitEntered   chan struct{} // closed once Exit is entered (nil = not observed)
	tmux          *tmux.FakeExecutor
	onExit        func()
}

func (f *recordingCCOperator) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *recordingCCOperator) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func wait(ctx context.Context, gate chan struct{}) error {
	if gate == nil {
		return nil
	}
	select {
	case <-gate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *recordingCCOperator) Interrupt(ctx context.Context, _ string) error {
	f.record("interrupt")
	if err := wait(ctx, f.interruptGate); err != nil {
		return err
	}
	return f.interruptErr
}

func (f *recordingCCOperator) Exit(ctx context.Context, target string) error {
	f.record("exit")
	if f.exitEntered != nil {
		close(f.exitEntered)
		f.exitEntered = nil
	}
	if err := wait(ctx, f.exitGate); err != nil {
		return err
	}
	if f.exitErr != nil {
		return f.exitErr
	}
	if f.onExit != nil {
		f.onExit()
	}
	setPaneShell(f.tmux, target)
	return nil
}

func (f *recordingCCOperator) Launch(context.Context, string, string) error {
	f.record("launch")
	return nil
}
func (f *recordingCCOperator) GetStatus(context.Context, string) (*agentcc.StatusInfo, error) {
	f.record("getstatus")
	return nil, nil
}

// fakeNexService records every call in order ("delegate" | "acquire" |
// "interrupt" | "release" | "archive") with its arguments, and answers each
// with a configurable result/error. onDelegate (when set) runs before the
// Delegate answer.
type fakeNexService struct {
	mu         sync.Mutex
	calls      []string
	requests   []execution.Request
	result     execution.Result
	err        error
	onDelegate func()

	lease         store.Lease // answer to AcquireLease
	acquireErr    error
	acquires      []string // principal per AcquireLease call
	interruptReqs []execution.InterruptRequest
	interruptErr  error
	releases      []releaseCall
	releaseErr    error
	archiveReqs   []execution.ArchiveRequest
	archiveErr    error
}

type releaseCall struct{ ExecutionID, LeaseID, PrincipalID string }

func (f *fakeNexService) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeNexService) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeNexService) Delegate(_ context.Context, req execution.Request) (execution.Result, error) {
	f.record("delegate")
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.onDelegate != nil {
		f.onDelegate()
	}
	return f.result, f.err
}

func (f *fakeNexService) Requests() []execution.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execution.Request(nil), f.requests...)
}

func (f *fakeNexService) AcquireLease(_ context.Context, _, principalID string) (store.Lease, error) {
	f.record("acquire")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires = append(f.acquires, principalID)
	if f.acquireErr != nil {
		return store.Lease{}, f.acquireErr
	}
	return f.lease, nil
}

func (f *fakeNexService) ReleaseLease(_ context.Context, executionID, leaseID, principalID string) error {
	f.record("release")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, releaseCall{executionID, leaseID, principalID})
	return f.releaseErr
}

func (f *fakeNexService) Interrupt(_ context.Context, req execution.InterruptRequest) (execution.InterruptResult, error) {
	f.record("interrupt")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interruptReqs = append(f.interruptReqs, req)
	if f.interruptErr != nil {
		return execution.InterruptResult{}, f.interruptErr
	}
	return execution.InterruptResult{State: store.StateIdle}, nil
}

func (f *fakeNexService) Archive(_ context.Context, req execution.ArchiveRequest) error {
	f.record("archive")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archiveReqs = append(f.archiveReqs, req)
	return f.archiveErr
}

var _ nexService = (*fakeNexService)(nil)

// fakeNexStore answers Get with one scripted result per call, in order; the
// last one repeats once the script is exhausted (so "first Get says running,
// re-Get says idle" is a two-entry script). onGet (when set) runs before
// each answer with the call index, the seam for "the tmux server restarted
// while the execution was being read".
type fakeNexStore struct {
	mu      sync.Mutex
	results []getResult
	calls   int
	onGet   func(call int)
}

type getResult struct {
	exec store.Execution
	err  error
}

func (f *fakeNexStore) Get(context.Context, string) (store.Execution, error) {
	f.mu.Lock()
	call := f.calls
	f.calls++
	var res getResult
	if n := len(f.results); n > 0 {
		if call < n {
			res = f.results[call]
		} else {
			res = f.results[n-1]
		}
	}
	f.mu.Unlock()
	if f.onGet != nil {
		f.onGet(call)
	}
	return res.exec, res.err
}

func (f *fakeNexStore) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ nexStore = (*fakeNexStore)(nil)

// handoffEnv is one wired module behind an httptest server, in the
// all-good starting state: session known, generation matches, CC idle in
// the pane, identity resolves to a cc session, service accepts.
//
// registry carries the shared *session.HandoffLocks under
// session.HandoffLocksKey, and m.locks is resolved from it the way Init
// does — so a test can hold the lock "as the stream module" through the
// same instance.
type handoffEnv struct {
	m        *Module
	tmux     *tmux.FakeExecutor
	sessions *handoffSessions
	owners   *stubOwnerResolver
	ops      *recordingCCOperator
	svc      *fakeNexService
	srv      *httptest.Server
	registry *core.ServiceRegistry
}

func newHandoffEnv(t *testing.T) *handoffEnv {
	t.Helper()
	fakeTx := tmux.NewFakeExecutor()
	fakeTx.SetInstance(hoInstance)
	setPaneCCIdle(fakeTx, hoTarget)

	registry := core.NewServiceRegistry()
	registry.Register(session.HandoffLocksKey, session.NewHandoffLocks())

	sessions := &handoffSessions{
		tmux: fakeTx,
		sessions: map[string]*session.SessionInfo{
			hoCode: {Code: hoCode, TmuxID: hoTmuxID, Name: hoName, TmuxInstance: hoInstance, Cwd: hoCwd, Mode: "terminal"},
		},
	}
	owners := &stubOwnerResolver{
		owner: agent.PaneOwner{AgentType: "cc", SessionID: hoSessionID, Cwd: hoCwd},
		found: true,
	}
	ops := &recordingCCOperator{tmux: fakeTx}
	svc := &fakeNexService{result: execution.Result{ID: "exec-1", State: store.StateQueued, EffectiveProfile: "handoff"}}

	m := &Module{
		sys: engine{handler: http.NotFoundHandler(), service: svc},
		opts: nexen.Options{
			Config: &nexconfig.Config{HostID: "host1", Sandbox: sandbox.Policy{MaxProfile: "handoff", DefaultProfile: "trusted"}},
			Auth:   principalAuth("host1"),
		},
		sessions: sessions,
		owners:   owners,
		prober:   &handoffProber{tmux: fakeTx, readiness: agentcc.NewReadinessChecker(fakeTx)},
		ccOps:    ops,
		tmux:     fakeTx,
		locks:    registry.MustGet(session.HandoffLocksKey).(*session.HandoffLocks),
		logf:     discardLogf,

		handoffResolveTimeout:   time.Second,
		handoffInterruptTimeout: 100 * time.Millisecond,
		handoffExitTimeout:      100 * time.Millisecond,
		rollbackWait:            2 * time.Second,
		rollbackPoll:            5 * time.Millisecond,
	}

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &handoffEnv{m: m, tmux: fakeTx, sessions: sessions, owners: owners, ops: ops, svc: svc, srv: srv, registry: registry}
}

// post sends body (a value marshalled to JSON, or a raw string) and decodes
// the JSON response.
func (e *handoffEnv) post(t *testing.T, code string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case string:
		raw = []byte(b)
	default:
		var err error
		raw, err = json.Marshal(b)
		require.NoError(t, err)
	}
	resp, err := http.Post(e.srv.URL+"/api/sessions/"+code+"/nex-handoff", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "response is JSON")
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	return resp.StatusCode, out
}

func goodBody() map[string]any {
	return map[string]any{"expected_tmux_instance": hoInstance, "rollback_command": "claude --resume {id}"}
}

// rawKeysText joins every key string SendKeysIfInstance delivered.
func rawKeysText(fake *tmux.FakeExecutor) []string {
	var out []string
	for _, c := range fake.RawKeysSent() {
		out = append(out, strings.Join(c.Keys, ""))
	}
	return out
}

// --- preconditions ---

func TestHandoff503WhenServiceNil(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.sys.service = nil
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "nex_unavailable", body["code"])
	assert.Empty(t, env.ops.Calls())
}

// TestHandoffRouteRegisteredWhenEngineSoftFailed: the route exists outside
// RoutePrefix and is mounted even when Init soft-failed, so the SPA gets a
// structured 503 rather than a 404 it would read as "old daemon".
func TestHandoffRouteRegisteredWhenEngineSoftFailed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New()
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom"))
	m.logf = discardLogf
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.Error(t, m.initErr)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/sessions/abc/nex-handoff", strings.NewReader(`{}`)))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "nex_unavailable", body["code"])
	assert.Contains(t, body["error"], "boom")
}

func TestHandoff409HandoffUnsupportedWhenPolicyMaxTrusted(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.opts.Config.Sandbox.MaxProfile = "trusted"
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_unsupported", body["code"])
	assert.Empty(t, env.ops.Calls())
	assert.Empty(t, env.svc.Requests())
}

func TestHandoff400MalformedBody(t *testing.T) {
	env := newHandoffEnv(t)
	status, body := env.post(t, hoCode, "{not json")
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "malformed_body", body["code"])
}

func TestHandoff400InvalidInstance(t *testing.T) {
	env := newHandoffEnv(t)
	for _, bad := range []string{"", "1,2", "a#b", "x}y"} {
		status, body := env.post(t, hoCode, map[string]any{"expected_tmux_instance": bad})
		assert.Equal(t, http.StatusBadRequest, status, "instance %q", bad)
		assert.Equal(t, "invalid_instance", body["code"], "instance %q", bad)
	}
	assert.Empty(t, env.ops.Calls())
}

// --- lock ---

// TestHandoff409HandoffInProgressWhileFirstParkedInExit: the whole sequence
// runs under the per-session lock; a second request for the same code
// while the first is inside Exit is refused without touching anything.
func TestHandoff409HandoffInProgressWhileFirstParkedInExit(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.handoffExitTimeout = 5 * time.Second
	env.ops.exitGate = make(chan struct{})
	env.ops.exitEntered = make(chan struct{})
	entered := env.ops.exitEntered

	type result struct {
		status int
		body   map[string]any
	}
	first := make(chan result, 1)
	go func() {
		s, b := env.post(t, hoCode, goodBody())
		first <- result{s, b}
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first request never reached Exit")
	}

	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])
	assert.Equal(t, []string{"exit"}, env.ops.Calls(), "the second request called nothing on the operator")

	close(env.ops.exitGate)
	r := <-first
	assert.Equal(t, http.StatusOK, r.status, "%v", r.body)
	require.Len(t, env.svc.Requests(), 1)

	// Lock released after the first completes.
	assert.True(t, env.m.locks.TryLock(hoCode))
	env.m.locks.Unlock(hoCode)
}

// TestHandoffAndTakebackExcludedByTheSharedLockInstance: the lock is one
// instance per daemon, published by the session module and taken from the
// registry by stream and nex alike (R1-1/A1). Holding it through the
// registry's instance — as the legacy /handoff in the stream module does —
// makes both nex endpoints answer 409 handoff_in_progress without touching
// anything. A private lock per module would let a legacy handoff and a nex
// handoff run on the same pane at once.
func TestHandoffAndTakebackExcludedByTheSharedLockInstance(t *testing.T) {
	env := newTakebackEnv(t)
	shared := env.registry.MustGet(session.HandoffLocksKey).(*session.HandoffLocks)
	require.True(t, shared.TryLock(hoCode), "held as the stream module would hold it")
	defer shared.Unlock(hoCode)

	status, body := env.handoffEnv.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])

	status, body = env.post(t, hoCode, takebackBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "handoff_in_progress", body["code"])

	assert.Equal(t, 0, env.owners.calls, "identity never resolved")
	assert.Empty(t, env.ops.Calls())
	env.assertUntouched(t)
}

// --- session / generation / identity / liveness ---

func TestHandoff404SessionMissing(t *testing.T) {
	env := newHandoffEnv(t)
	status, body := env.post(t, "nope", goodBody())
	assert.Equal(t, http.StatusNotFound, status)
	assert.Equal(t, "session_missing", body["code"])
	assert.Equal(t, 0, env.owners.calls)
	assert.Empty(t, env.ops.Calls())
}

func TestHandoffGenerationMismatchBeforeIdentityCallsNothing(t *testing.T) {
	env := newHandoffEnv(t)
	env.tmux.SetInstance("999:999")
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Nil(t, body["after_exit"])
	assert.Equal(t, 0, env.owners.calls, "identity never resolved")
	assert.Empty(t, env.ops.Calls())
	assert.Empty(t, env.svc.Requests())
}

func TestHandoffNoIdentityOperatorNeverCalled(t *testing.T) {
	cases := map[string]func(*stubOwnerResolver){
		"not found":        func(s *stubOwnerResolver) { s.found = false },
		"not cc":           func(s *stubOwnerResolver) { s.owner.AgentType = "opencode" },
		"empty session id": func(s *stubOwnerResolver) { s.owner.SessionID = "" },
		"lookup error":     func(s *stubOwnerResolver) { s.err = errors.New("tmux read failed") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			mutate(env.owners)
			status, body := env.post(t, hoCode, goodBody())
			assert.Equal(t, http.StatusConflict, status)
			assert.Equal(t, "no_identity", body["code"])
			assert.Empty(t, env.ops.Calls())
			assert.Empty(t, env.svc.Requests())
		})
	}
}

func TestHandoffNoCC(t *testing.T) {
	env := newHandoffEnv(t)
	setPaneShell(env.tmux, hoTarget)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "no_cc", body["code"])
	assert.Equal(t, 1, env.owners.calls, "identity is resolved before liveness (F12)")
	assert.Empty(t, env.ops.Calls())
}

// TestHandoffGenerationMismatchAfterLivenessBeforeInterrupt: the server
// restarts between the first sample and the operator step; the re-sample
// catches it and no key is ever sent.
func TestHandoffGenerationMismatchAfterLivenessBeforeInterrupt(t *testing.T) {
	env := newHandoffEnv(t)
	setPaneCCRunning(env.tmux, hoTarget) // busy: Interrupt would be the first key
	env.owners.onResolve = func() { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Nil(t, body["after_exit"])
	assert.Empty(t, env.ops.Calls(), "no interrupt, no exit")
	assert.Empty(t, env.svc.Requests())
	assert.Empty(t, env.tmux.RawKeysSent())
}

// --- operator ordering and timeouts ---

func TestHandoffBusyInterruptsThenExits(t *testing.T) {
	t.Run("busy", func(t *testing.T) {
		env := newHandoffEnv(t)
		setPaneCCRunning(env.tmux, hoTarget)
		status, body := env.post(t, hoCode, goodBody())
		assert.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []string{"interrupt", "exit"}, env.ops.Calls())
	})
	t.Run("idle", func(t *testing.T) {
		env := newHandoffEnv(t)
		status, body := env.post(t, hoCode, goodBody())
		assert.Equal(t, http.StatusOK, status, "%v", body)
		assert.Equal(t, []string{"exit"}, env.ops.Calls())
	})
}

func TestHandoffInterruptTimeout504(t *testing.T) {
	env := newHandoffEnv(t)
	setPaneCCRunning(env.tmux, hoTarget)
	env.ops.interruptGate = make(chan struct{}) // never released: only the handler's ctx ends it
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_exit_timeout", body["code"])
	assert.Equal(t, "interrupt", body["step"])
	assert.Equal(t, []string{"interrupt"}, env.ops.Calls(), "no exit after a failed interrupt")
	assert.Empty(t, env.svc.Requests())
}

func TestHandoffExitTimeout504(t *testing.T) {
	env := newHandoffEnv(t)
	env.ops.exitGate = make(chan struct{})
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_exit_timeout", body["code"])
	assert.Equal(t, "exit", body["step"])
	assert.Equal(t, []string{"exit"}, env.ops.Calls())
	assert.Empty(t, env.svc.Requests())
}

func TestHandoffExitError504(t *testing.T) {
	env := newHandoffEnv(t)
	env.ops.exitErr = errors.New("CC did not exit")
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusGatewayTimeout, status)
	assert.Equal(t, "cc_exit_timeout", body["code"])
	assert.Equal(t, "exit", body["step"])
	assert.Contains(t, body["error"], "CC did not exit")
	assert.Empty(t, env.svc.Requests())
}

// TestHandoffGenerationMismatchAfterExit: a generation change after exit is
// a different tmux server — no delegate, and no rollback into a pane that
// is not the one we exited; the session id is handed back for a manual
// resume.
func TestHandoffGenerationMismatchAfterExit(t *testing.T) {
	env := newHandoffEnv(t)
	env.ops.onExit = func() { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "tmux_instance_mismatch", body["code"])
	assert.Equal(t, true, body["after_exit"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
	assert.Empty(t, env.svc.Requests(), "nothing delegated")
	assert.Empty(t, env.tmux.RawKeysSent(), "no rollback keys")
}

// --- delegate ---

func TestHandoffSuccessBodyAndRequest(t *testing.T) {
	env := newHandoffEnv(t)
	status, body := env.post(t, hoCode, goodBody())
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, map[string]any{
		"execution_id":      "exec-1",
		"state":             "queued",
		"effective_profile": "handoff",
		"session_id":        hoSessionID,
		"cwd":               hoCwd,
	}, body)

	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, execution.Request{
		PrincipalID:     "pdx:host1",
		Provider:        "claude",
		Brief:           "(handed off from tmux session " + hoName + ")",
		SandboxProfile:  "handoff",
		Mounts:          []execution.Mount{{Path: hoCwd, Role: "cwd", Writable: true}},
		Origin:          "purdex://host/host1/session/" + hoCode,
		Labels:          map[string]string{"source": "purdex", "handoff_session": hoCode},
		ResumeSessionID: hoSessionID,
	}, reqs[0])
	assert.Empty(t, env.tmux.RawKeysSent(), "no rollback on success")
}

// TestHandoffRequestHonoursProfileAndClientHeader: an explicit profile is
// passed through, and the principal is the one /api/nex would derive for
// the same request (X-Pdx-Client suffix).
func TestHandoffRequestHonoursProfileAndClientHeader(t *testing.T) {
	env := newHandoffEnv(t)
	raw, _ := json.Marshal(map[string]any{"expected_tmux_instance": hoInstance, "profile": "readonly"})
	req, err := http.NewRequest("POST", env.srv.URL+"/api/sessions/"+hoCode+"/nex-handoff", bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set(ClientHeader, "tab-7")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	reqs := env.svc.Requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, "readonly", reqs[0].SandboxProfile)
	assert.Equal(t, "pdx:host1/tab-7", reqs[0].PrincipalID)
}

// --- rejection and rollback ---

func rejectingEnv(t *testing.T) *handoffEnv {
	t.Helper()
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{State: store.StateRejected, RejectReason: "cwd outside roots"}
	return env
}

// reviveCCAfterKeys flips the pane back to CC idle once a rollback key
// string has been delivered — the fake tmux does not run commands.
func reviveCCAfterKeys(env *handoffEnv) {
	go func() {
		for i := 0; i < 400; i++ {
			if len(env.tmux.RawKeysSent()) > 0 {
				setPaneCCIdle(env.tmux, hoTarget)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
}

func TestHandoffRejectedWithRollbackKeysAndAlive(t *testing.T) {
	env := rejectingEnv(t)
	reviveCCAfterKeys(env)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, "cwd outside roots", body["reject_reason"])
	assert.Equal(t, true, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
	assert.Nil(t, body["infra_error"])

	keys := env.tmux.RawKeysSent()
	require.Len(t, keys, 1)
	assert.Equal(t, hoTmuxID+":0", keys[0].Target, "sent by session id to window 0 — the pane liveness was read from")
	assert.Equal(t, []string{"claude --resume " + hoSessionID + "\n"}, rawKeysText(env.tmux))
}

func TestHandoffRejectedRollbackTimeout(t *testing.T) {
	env := rejectingEnv(t)
	env.m.rollbackWait = 50 * time.Millisecond
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Len(t, env.tmux.RawKeysSent(), 1, "keys were sent; CC just never came back")
}

func TestHandoffRejectedRollbackSendError(t *testing.T) {
	env := rejectingEnv(t)
	env.tmux.FailSendKeys = true
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
}

// TestHandoffRejectedRollbackNotSent: the generation moves between the
// post-exit sample and the send; SendKeysIfInstance declines (nothing
// delivered) and that is reported as not rolled back.
func TestHandoffRejectedRollbackNotSent(t *testing.T) {
	env := rejectingEnv(t)
	env.svc.onDelegate = func() { env.tmux.SetInstance("999:999") }
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestHandoffRejectedWithoutRollbackCommand(t *testing.T) {
	env := rejectingEnv(t)
	status, body := env.post(t, hoCode, map[string]any{"expected_tmux_instance": hoInstance})
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, "cwd outside roots", body["reject_reason"])
	assert.Equal(t, false, body["rolled_back"])
	assert.Equal(t, hoSessionID, body["session_id"])
	assert.Empty(t, env.tmux.RawKeysSent())
}

func TestHandoffDelegateInfraError(t *testing.T) {
	env := newHandoffEnv(t)
	env.svc.result = execution.Result{}
	env.svc.err = store.ErrInvalidOrigin
	reviveCCAfterKeys(env)
	status, body := env.post(t, hoCode, goodBody())
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "delegate_rejected", body["code"])
	assert.Equal(t, true, body["infra_error"])
	assert.Equal(t, store.ErrInvalidOrigin.Error(), body["reject_reason"])
	assert.Equal(t, true, body["rolled_back"], "rollback attempted on an infra error too")
	assert.Equal(t, []string{"claude --resume " + hoSessionID + "\n"}, rawKeysText(env.tmux))
}
