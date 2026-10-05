package nex

// Shared fixtures for the handoff (handoff_test.go) and take-back
// (takeback_test.go) tests: the fakes behind one wired Module in an
// httptest server. They mirror the stream module's orchestrator tests
// (fakeCCOperator, fakeStreamProber over tmux.FakeExecutor) — copied rather
// than imported, since Go test files do not cross packages.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
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

	// take-to-terminal seams (take_to_terminal_test.go)
	cwdErr      error                // ValidateCwd's answer
	cwdChecks   []string             // ValidateCwd arguments
	createErr   *session.CreateError // CreateSession's scripted failure
	afterCreate func()               // runs after a successful create, before the info is returned (models a tmux restart in the window)
	creates     []createCall         // CreateSession arguments
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

// SessionExists reads the fake tmux server, as the real provider does.
func (f *handoffSessions) SessionExists(name string) bool { return f.tmux.HasSession(name) }

// ValidateCwd answers cwdErr (nil = every cwd is fine) and records the call.
func (f *handoffSessions) ValidateCwd(cwd string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cwdChecks = append(f.cwdChecks, cwd)
	return f.cwdErr
}

// CreateSession models the real one over the fake tmux: HasSession →
// NewSession → look the session up → register it under a fresh code. A
// scripted createErr is returned instead (after NewSession when its
// SessionAlive() says so, so the tmux side matches the error's claim).
func (f *handoffSessions) CreateSession(name, cwd string) (*session.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, createCall{name, cwd})
	if f.tmux.HasSession(name) {
		return nil, &session.CreateError{Stage: session.CreateStageExists, Name: name, Err: session.ErrSessionExists}
	}
	if f.createErr != nil && !f.createErr.SessionAlive() {
		return nil, f.createErr
	}
	if err := f.tmux.NewSession(name, cwd); err != nil {
		return nil, &session.CreateError{Stage: session.CreateStageNewSession, Name: name, Err: err}
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	list, _ := f.tmux.ListSessions(context.Background())
	for _, s := range list {
		if s.Name != name {
			continue
		}
		code, err := session.EncodeSessionID(s.ID)
		if err != nil {
			return nil, &session.CreateError{Stage: session.CreateStageEncode, Name: name, Err: err}
		}
		info := &session.SessionInfo{Code: code, TmuxID: s.ID, Name: s.Name, Exists: true, Mode: "terminal", Cwd: s.Cwd, TmuxInstance: f.tmux.Instance()}
		f.sessions[code] = info
		cp := *info
		if f.afterCreate != nil {
			f.afterCreate()
		}
		return &cp, nil
	}
	return nil, &session.CreateError{Stage: session.CreateStageList, Name: name, Err: errors.New("session created but not found")}
}

// Creates returns every CreateSession call so far.
func (f *handoffSessions) Creates() []createCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]createCall(nil), f.creates...)
}

// CwdChecks returns every ValidateCwd argument so far.
func (f *handoffSessions) CwdChecks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cwdChecks...)
}

type createCall struct{ Name, Cwd string }

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
// Delegate answer. A gate channel parks the call until it is closed or the
// call's ctx expires — which is how the handler's ctx sizing is observed:
// a parked call returns ctx.Err() exactly when the handler's deadline
// passes (the same device as recordingCCOperator).
type fakeNexService struct {
	mu           sync.Mutex
	calls        []string
	requests     []execution.Request
	result       execution.Result
	err          error
	onDelegate   func()
	delegateGate chan struct{}

	lease          store.Lease // answer to AcquireLease
	acquireErr     error
	acquires       []string // principal per AcquireLease call
	interruptReqs  []execution.InterruptRequest
	interruptErr   error
	interruptGate  chan struct{}
	releases       []releaseCall
	releaseCtxErrs []error // ctx.Err() as seen on entry to each ReleaseLease
	releaseErr     error
	archiveReqs    []execution.ArchiveRequest
	archiveCtxErrs []error // ctx.Err() as seen on entry to each Archive
	archiveErr     error
	unarchiveErr   error // answer to an Archive with Archived:false (archiveErr answers both when set)

	terminateCalls []execution.TerminateRequest
	terminateErr   error
	onTerminate    func(execution.TerminateRequest) // runs before the answer; lets a test flip the store row to terminated
	renewCalls     []renewCall
	renewErr       error

	// onRecord runs after each call is recorded, outside mu, with the call's
	// name: the seam that merges these calls with another fake's events into
	// one ordered timeline (take-to-terminal's resume-before-exit order).
	onRecord func(name string)

	// Opt-in lease fence, off by default so existing tests are unaffected.
	// With enforceLease, heldLease is the execution's lease as Nexen's store
	// holds it: AcquireLease refuses a live lease of another principal and
	// otherwise mints f.lease.ID for the caller; RenewLease, ReleaseLease,
	// Interrupt and Terminate check the request like store.CheckLease
	// (store/lease.go), and CheckLease stands in for a client's send.
	// A hook (onTerminate, onRecord, or a test between calls) may hand the
	// lease over with setHeldLease.
	enforceLease bool
	heldLease    store.Lease
}

// fakeLeaseTTL is the lifetime (ms) the fake gives an acquired or renewed lease.
const fakeLeaseTTL = 120_000

// setHeldLease replaces the lease the fence checks against (a hand-over).
func (f *fakeNexService) setHeldLease(l store.Lease) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heldLease = l
}

// checkLease is store.CheckLease over heldLease; the caller holds f.mu.
func (f *fakeNexService) checkLease(executionID, leaseID, principalID string) error {
	switch {
	case f.heldLease.ID == "":
		return fmt.Errorf("execution %s: %w", executionID, store.ErrLeaseRequired)
	case f.heldLease.ExpiresAt <= nowMs():
		return fmt.Errorf("execution %s: %w", executionID, store.ErrLeaseExpired)
	case f.heldLease.ID != leaseID || f.heldLease.PrincipalID != principalID:
		return fmt.Errorf("execution %s: %w", executionID, store.ErrLeaseMismatch)
	}
	return nil
}

// CheckLease is the fence a client's send meets (Nexen checks the lease
// before every write): a probe for "can the holder still write?". It is
// not recorded as a call.
func (f *fakeNexService) CheckLease(executionID, leaseID, principalID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkLease(executionID, leaseID, principalID)
}

type renewCall struct{ ExecutionID, LeaseID, PrincipalID string }

type releaseCall struct{ ExecutionID, LeaseID, PrincipalID string }

func (f *fakeNexService) record(name string) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	hook := f.onRecord
	f.mu.Unlock()
	if hook != nil {
		hook(name)
	}
}

// record calls "archive" for every Archive; ArchiveCalls tells the two
// directions apart: "archive" for Archived:true, "unarchive" for false.
func (f *fakeNexService) ArchiveCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.archiveReqs {
		if r.Archived {
			out = append(out, "archive")
		} else {
			out = append(out, "unarchive")
		}
	}
	return out
}

func (f *fakeNexService) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeNexService) Delegate(ctx context.Context, req execution.Request) (execution.Result, error) {
	f.record("delegate")
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.onDelegate != nil {
		f.onDelegate()
	}
	if err := wait(ctx, f.delegateGate); err != nil {
		return execution.Result{}, err
	}
	return f.result, f.err
}

func (f *fakeNexService) Requests() []execution.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]execution.Request(nil), f.requests...)
}

func (f *fakeNexService) AcquireLease(_ context.Context, executionID, principalID string) (store.Lease, error) {
	f.record("acquire")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires = append(f.acquires, principalID)
	if f.acquireErr != nil {
		return store.Lease{}, f.acquireErr
	}
	if !f.enforceLease {
		return f.lease, nil
	}
	if h := f.heldLease; h.ID != "" && h.ExpiresAt > nowMs() && h.PrincipalID != principalID {
		return store.Lease{}, fmt.Errorf("execution %s: %w by %s", executionID, store.ErrLeaseHeld, h.PrincipalID)
	}
	f.heldLease = store.Lease{ID: f.lease.ID, PrincipalID: principalID, ExpiresAt: nowMs() + fakeLeaseTTL}
	return f.heldLease, nil
}

func (f *fakeNexService) ReleaseLease(ctx context.Context, executionID, leaseID, principalID string) error {
	f.record("release")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, releaseCall{executionID, leaseID, principalID})
	f.releaseCtxErrs = append(f.releaseCtxErrs, ctx.Err())
	if f.releaseErr != nil || !f.enforceLease {
		return f.releaseErr
	}
	// Nexen releases the holder's lease even once expired; anyone else gets CheckLease's answer.
	if f.heldLease.ID == leaseID && f.heldLease.PrincipalID == principalID {
		f.heldLease = store.Lease{}
		return nil
	}
	return f.checkLease(executionID, leaseID, principalID)
}

func (f *fakeNexService) Interrupt(ctx context.Context, req execution.InterruptRequest) (execution.InterruptResult, error) {
	f.record("interrupt")
	f.mu.Lock()
	f.interruptReqs = append(f.interruptReqs, req)
	var fenceErr error
	if f.enforceLease {
		fenceErr = f.checkLease(req.ExecutionID, req.LeaseID, req.PrincipalID)
	}
	f.mu.Unlock()
	if fenceErr != nil {
		return execution.InterruptResult{}, fenceErr
	}
	if err := wait(ctx, f.interruptGate); err != nil {
		return execution.InterruptResult{}, err
	}
	if f.interruptErr != nil {
		return execution.InterruptResult{}, f.interruptErr
	}
	return execution.InterruptResult{State: store.StateIdle}, nil
}

func (f *fakeNexService) Archive(ctx context.Context, req execution.ArchiveRequest) error {
	f.record("archive")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archiveReqs = append(f.archiveReqs, req)
	f.archiveCtxErrs = append(f.archiveCtxErrs, ctx.Err())
	if f.archiveErr != nil {
		return f.archiveErr
	}
	if !req.Archived {
		return f.unarchiveErr
	}
	return nil
}

// Terminate runs onTerminate first (so a hook can hand the lease over), then,
// with enforceLease, fences the request as Nexen's Terminate does (CheckLease
// before anything else), and otherwise answers terminateErr.
func (f *fakeNexService) Terminate(_ context.Context, req execution.TerminateRequest) error {
	f.record("terminate")
	f.mu.Lock()
	f.terminateCalls = append(f.terminateCalls, req)
	hook := f.onTerminate
	f.mu.Unlock()
	if hook != nil {
		hook(req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enforceLease {
		if err := f.checkLease(req.ExecutionID, req.LeaseID, req.PrincipalID); err != nil {
			return err
		}
	}
	return f.terminateErr
}

func (f *fakeNexService) RenewLease(_ context.Context, executionID, leaseID, principalID string) (store.Lease, error) {
	f.record("renew")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewCalls = append(f.renewCalls, renewCall{executionID, leaseID, principalID})
	if f.renewErr != nil {
		return store.Lease{}, f.renewErr
	}
	if !f.enforceLease {
		return f.lease, nil
	}
	if err := f.checkLease(executionID, leaseID, principalID); err != nil {
		return store.Lease{}, err
	}
	f.heldLease.ExpiresAt = nowMs() + fakeLeaseTTL
	return f.heldLease, nil
}

var _ nexService = (*fakeNexService)(nil)

// fakeNexStore answers Get with one scripted result per call, in order; the
// last one repeats once the script is exhausted (so "first Get says running,
// re-Get says idle" is a two-entry script). onGet (when set) runs before
// each answer with the call index, the seam for "the tmux server restarted
// while the execution was being read". getGate parks every Get until it is
// closed or the call's ctx expires (see fakeNexService).
type fakeNexStore struct {
	mu      sync.Mutex
	results []getResult
	calls   int
	onGet   func(call int)
	getGate chan struct{}

	// List: listRows is the whole table (non-archived and archived alike);
	// List filters, orders by id and pages it like store.Store.List.
	listRows  []store.Execution
	listErr   error
	listCalls int
}

type getResult struct {
	exec store.Execution
	err  error
}

func (f *fakeNexStore) Get(ctx context.Context, _ string) (store.Execution, error) {
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
	if err := wait(ctx, f.getGate); err != nil {
		return store.Execution{}, err
	}
	return res.exec, res.err
}

// List honours IncludeArchived, Cursor (id >) and Limit; rows come back in id
// order and NextCursor is set only when more remain.
func (f *fakeNexStore) List(_ context.Context, opts store.ListOptions) (store.ListPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return store.ListPage{}, f.listErr
	}
	var rows []store.Execution
	for _, e := range f.listRows {
		if opts.Cursor != "" && e.ID <= opts.Cursor {
			continue
		}
		if !opts.IncludeArchived && e.ArchivedAt != 0 {
			continue
		}
		rows = append(rows, e)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	page := store.ListPage{}
	if len(rows) > limit {
		rows = rows[:limit]
		page.NextCursor = rows[len(rows)-1].ID
	}
	page.Items = rows
	return page, nil
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
const hoOwnerPane = "%7" // the pane of the CC being handed off

type handoffEnv struct {
	m         *Module
	tmux      *tmux.FakeExecutor
	sessions  *handoffSessions
	owners    *stubOwnerResolver
	ops       *recordingCCOperator
	svc       *fakeNexService
	srv       *httptest.Server
	registry  *core.ServiceRegistry
	terminals *stubTerminals
}

// stubTerminals is the agent.TerminalSessions provider.
type stubTerminals struct {
	mu         sync.Mutex
	live       map[string][]agent.TerminalSession // by session id
	err        error
	subscribed func(agent.SessionStartEvent)
	calls      int
	// byCall, when set, answers the n-th (1-based) LiveBySessionID call
	// instead of live: the seam for "an owner appears between two looks".
	byCall func(n int) []agent.TerminalSession
}

// Calls is the number of LiveBySessionID lookups so far.
func (s *stubTerminals) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *stubTerminals) LiveBySessionID(_ context.Context, _, sid string) ([]agent.TerminalSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	s.calls++
	if s.byCall != nil {
		return s.byCall(s.calls), nil
	}
	return append([]agent.TerminalSession(nil), s.live[sid]...), nil
}

func (s *stubTerminals) SubscribeSessionStart(fn func(agent.SessionStartEvent)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribed = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.subscribed = nil
	}
}

var _ agent.TerminalSessions = (*stubTerminals)(nil)

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
		owner: agent.PaneOwner{AgentType: "cc", SessionID: hoSessionID, Cwd: hoCwd, TmuxPaneID: hoOwnerPane},
		found: true,
	}
	ops := &recordingCCOperator{tmux: fakeTx}
	terminals := &stubTerminals{}
	svc := &fakeNexService{result: execution.Result{ID: "exec-1", State: store.StateQueued, EffectiveProfile: "handoff"}}

	m := &Module{
		sys: engine{handler: http.NotFoundHandler(), service: svc, store: &fakeNexStore{}},
		opts: nexen.Options{
			Config: &nexconfig.Config{HostID: "host1", Sandbox: sandbox.Policy{MaxProfile: "handoff", DefaultProfile: "trusted"}},
			Auth:   principalAuth("host1"),
		},
		sessions:  sessions,
		owners:    owners,
		prober:    &handoffProber{tmux: fakeTx, readiness: agentcc.NewReadinessChecker(fakeTx)},
		ccOps:     ops,
		terminals: terminals,
		tmux:      fakeTx,
		locks:     registry.MustGet(session.HandoffLocksKey).(*session.HandoffLocks),
		logf:      discardLogf,

		handoffResolveTimeout:   time.Second,
		handoffInterruptTimeout: 100 * time.Millisecond,
		handoffExitTimeout:      100 * time.Millisecond,
		rollbackWait:            2 * time.Second,
		rollbackPoll:            5 * time.Millisecond,
		ownerVisiblePoll:        2 * time.Millisecond,
		ownerVisibleTimeout:     20 * time.Millisecond,
		delegateTimeout:         2 * time.Second,
		engineOpTimeout:         2 * time.Second,
		engineInterruptTimeout:  2 * time.Second,
		engineTerminateTimeout:  2 * time.Second,
		leaseCleanupTimeout:     2 * time.Second,
	}

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &handoffEnv{m: m, tmux: fakeTx, sessions: sessions, owners: owners, ops: ops, svc: svc, srv: srv, registry: registry, terminals: terminals}
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

// script sets the Get answers, one per call (the last repeats), and resets the call count.
func (f *fakeNexStore) script(rows ...store.Execution) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = nil
	for _, r := range rows {
		f.results = append(f.results, getResult{exec: r})
	}
	f.calls = 0
}
