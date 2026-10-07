package nex

// Plan Task 5b (permission channel spec §5.4, ruling R-PC-1): what an answer
// to a worker's permission request meets when the worker ends. Every test
// runs the REAL embedded Nexen (the mount fixture's nexen.Assemble) with
// testdata/fake-claude-permission.sh as claude, so a handoff_ask worker
// raises a real permission.requested and every answer goes through Nexen's
// own POST /v1/executions/{id}/permissions/{request_id}. The paths that need
// a terminal (take-to-terminal, take-back) or a SessionStart (Q1) run the
// handoff tests' fake tmux, sessions and terminals against that same engine.
//
// Pinned — the three guarantees, and nothing broader (no "never allowed"):
//  1. after a preempt, an answer carrying the previous holder's lease_id
//     gets 409 lease_mismatch (exit, Q1, worker-rebuild with a replaced
//     row, take-to-terminal, take-back);
//  2. after an interrupt or a terminate took effect, any answer gets 409
//     permission_not_pending, and the fixture's log shows the tool never ran;
//  3. an ending path never turns a denied, cancelled or expired request into
//     allowed.
//
// Also pinned: an interrupt while pending resolves cancelled/interrupt; a
// daemon restart's reconcile resolves cancelled/daemon_restart and a later
// answer gets permission_not_pending. The documented residual (an answer
// whose lease check passed just before the preempt still writes) is pinned
// only as "exactly one terminal outcome per request_id".

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/execution"
	"lab.protype.tw/wake/nexen/store"

	pdxconfig "github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/module/agent"
)

const (
	permRID = "e89aa38c-d064-4ea8-a9a0-fdb3f73f0143" // the request the fixture raises
	permSID = "995fb62d-4a59-4f35-af79-3b978d39ff8c" // the Claude session its init frame reports
	permTab = "tab2"                                 // the worker pane: another pdx client of this host
)

// askOpts picks the fixture's behaviour and the worker's delegate.
type askOpts struct {
	mode     string // FAKE_PERM_MODE: "interrupt" waits for an interrupt or a decision, "ask" for one decision, "cancel" withdraws the request itself
	timeoutS int    // permission_timeout_s; 0 sends none
	bound    bool   // origin and labels of a handoff of session hoCode, as take-back requires
	dataDir  string // the engine's data dir; "" is a fresh one
	root     string // the repo root; "" is a fresh one
}

// askingWorker is a handoff_ask execution on a real engine whose claude is
// the permission fixture.
type askingWorker struct {
	f   *mountFixture
	svc nexService // the engine's own service, never a spy
	id  string
	log string // the fixture's FAKE_PERM_LOG
}

// permFixtureClaude writes a claude for the engine to spawn: the fixture in
// mode, logging to logPath.
func permFixtureClaude(t *testing.T, mode, logPath string) string {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "fake-claude-permission.sh"))
	require.NoError(t, err)
	return writeScript(t, t.TempDir(), "claude",
		fmt.Sprintf("#!/bin/sh\nFAKE_PERM_MODE='%s' FAKE_PERM_LOG='%s' exec '%s' \"$@\"\n", mode, logPath, fixture))
}

// newAskingWorker assembles a real engine (max_profile handoff), delegates
// one handoff_ask execution and waits until it waits on permRID — or, for
// the cancel mode, until the fixture has withdrawn it and the turn ended.
func newAskingWorker(t *testing.T, o askOpts) *askingWorker {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "fixture.log")
	f := newMountFixtureWith(t, func(cfg *pdxconfig.Config) {
		cfg.Nex.ClaudeBin = permFixtureClaude(t, o.mode, logPath)
		cfg.Nex.Sandbox.MaxProfile = "handoff"
		if o.dataDir != "" {
			cfg.DataDir = o.dataDir
		}
		if o.root != "" {
			cfg.Nex.RepoRoots = []string{o.root}
		}
	})
	body := map[string]any{
		"provider":        "claude",
		"brief":           "run the probe",
		"sandbox_profile": "handoff_ask",
		"mounts":          []map[string]any{{"path": f.root, "role": "cwd", "writable": true}},
	}
	if o.timeoutS > 0 {
		body["permission_timeout_s"] = o.timeoutS
	}
	if o.bound {
		body["origin"] = handoffOrigin(f.hostID, hoCode)
		body["labels"] = map[string]string{handoffSessionLabel: hoCode, "source": "purdex"}
	}
	var res struct {
		ID               string `json:"id"`
		State            string `json:"state"`
		EffectiveProfile string `json:"effective_profile"`
		RejectReason     string `json:"reject_reason"`
	}
	f.doJSON(t, http.MethodPost, "/api/nex/v1/executions", body, &res)
	require.NotEmpty(t, res.ID, "delegate: %+v", res)
	require.NotEqual(t, "rejected", res.State, "delegate: %+v", res)
	require.Equal(t, "handoff_ask", res.EffectiveProfile, "delegate: %+v", res)
	w := &askingWorker{f: f, svc: f.m.sys.service, id: res.ID, log: logPath}
	if o.mode == "cancel" {
		waitFor(t, "the fixture's own cancel resolved and the turn ended", func() bool {
			return len(w.resolved(t)) == 1 && w.state(t) == "idle"
		})
	} else {
		waitFor(t, "permission request "+permRID+" pending", func() bool { return w.pending(t) == permRID })
	}
	return w
}

func (w *askingWorker) state(t *testing.T) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(w.f.summary(t, w.id)["state"], &s))
	return s
}

// pendingOf decodes a summary's pending_permission to its request id ("" for null).
func pendingOf(raw json.RawMessage) (string, error) {
	var p *struct {
		RequestID string `json:"request_id"`
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("summary has no pending_permission field")
	}
	if err := json.Unmarshal(raw, &p); err != nil || p == nil {
		return "", err
	}
	return p.RequestID, nil
}

// pending is the summary's pending request id ("" when none is pending).
func (w *askingWorker) pending(t *testing.T) string {
	t.Helper()
	id, err := pendingOf(w.f.summary(t, w.id)["pending_permission"])
	require.NoError(t, err)
	return id
}

// rawPending is pending without t, for a hook on a server goroutine.
func (w *askingWorker) rawPending() (string, error) {
	status, raw, err := w.f.rawDo(http.MethodGet, "/api/nex/v1/executions/"+w.id, "", nil)
	if err != nil || status != http.StatusOK {
		return "", fmt.Errorf("summary: %d %s (%v)", status, raw, err)
	}
	var s map[string]json.RawMessage
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return pendingOf(s["pending_permission"])
}

// attachTab is the worker pane taking control, as an open pane does: its lease id.
func (w *askingWorker) attachTab(t *testing.T) string {
	t.Helper()
	status, raw, err := w.f.rawDo(http.MethodPost, "/api/nex/v1/executions/"+w.id+"/attach", permTab, map[string]string{"mode": "control"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "the tab's attach(control): %s", raw)
	var att struct {
		LeaseID string `json:"lease_id"`
	}
	require.NoError(t, json.Unmarshal(raw, &att))
	require.NotEmpty(t, att.LeaseID, "attach answer %s", raw)
	return att.LeaseID
}

// reattach is the tab acquiring a fresh lease after the ending path let go
// of its own: a lease Nexen's check accepts, so what refuses an answer
// under it is the request's state, never the lease.
func (w *askingWorker) reattach(t *testing.T) string {
	t.Helper()
	l, err := w.svc.AcquireLease(context.Background(), w.id, "pdx:"+w.f.hostID+"/"+permTab)
	require.NoError(t, err, "the tab re-attaches once the ending path released its lease")
	return l.ID
}

// rawAnswer is the tab's answer to permRID under leaseID: the status and the
// error code (or, on 200, the outcome). No t: hooks call it.
func (w *askingWorker) rawAnswer(leaseID, decision string) (int, string, error) {
	status, raw, err := w.f.rawDo(http.MethodPost, "/api/nex/v1/executions/"+w.id+"/permissions/"+permRID, permTab,
		map[string]string{"decision": decision, "lease_id": leaseID})
	if err != nil {
		return 0, "", err
	}
	var b struct {
		Code    string `json:"code"`
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return status, "", fmt.Errorf("answer body %s: %w", raw, err)
	}
	if status == http.StatusOK {
		return status, b.Outcome, nil
	}
	return status, b.Code, nil
}

func (w *askingWorker) answer(t *testing.T, leaseID, decision string) (int, string) {
	t.Helper()
	status, code, err := w.rawAnswer(leaseID, decision)
	require.NoError(t, err)
	return status, code
}

// resolvedView is a permission.resolved payload.
type resolvedView struct {
	RequestID       string `json:"request_id"`
	Outcome         string `json:"outcome"`
	Reason          string `json:"reason"`
	InterruptSource string `json:"interrupt_source"`
}

// resolved is every permission.resolved of permRID, in seq order.
func (w *askingWorker) resolved(t *testing.T) []resolvedView {
	t.Helper()
	var out []resolvedView
	for _, ev := range w.f.events(t, w.id) {
		if ev.Kind != "permission.resolved" {
			continue
		}
		var p resolvedView
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		if p.RequestID == permRID {
			out = append(out, p)
		}
	}
	return out
}

// recordedOutcome is the outcome column of permRID's row ("" while pending).
func (w *askingWorker) recordedOutcome(t *testing.T) string {
	t.Helper()
	st, ok := w.f.m.sys.store.(*store.Store)
	require.True(t, ok, "engine store is %T", w.f.m.sys.store)
	var outcome *string
	require.NoError(t, st.DB().QueryRowContext(context.Background(),
		`SELECT outcome FROM permission_requests WHERE execution_id = ? AND request_id = ?`, w.id, permRID).Scan(&outcome))
	if outcome == nil {
		return ""
	}
	return *outcome
}

// logLines is the fixture's log so far.
func (w *askingWorker) logLines(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(w.log)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// assertToolNeverRan: the fixture asked (the log is wired) and never played
// the tool's run — no decision to allow ever reached its stdin.
func (w *askingWorker) assertToolNeverRan(t *testing.T) {
	t.Helper()
	lines := w.logLines(t)
	assert.Contains(t, lines, "asked "+permRID, "the fixture's log is wired")
	for _, l := range lines {
		assert.False(t, strings.HasPrefix(l, "tool_ran"), "the tool ran: %q", lines)
	}
}

// assertLaterAnswerNotPending: once the request ended, an answer under a
// lease Nexen accepts gets 409 permission_not_pending.
func (w *askingWorker) assertLaterAnswerNotPending(t *testing.T) {
	t.Helper()
	status, code := w.answer(t, w.reattach(t), "allow")
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "permission_not_pending", code)
}

// endingSpy wraps the engine's service for the module: right before the
// first Interrupt or Terminate a Purdex ending path sends — it holds
// control, the worker still lives — it runs probe once.
type endingSpy struct {
	nexService
	probe func(op string)
	once  sync.Once
}

func (s *endingSpy) fire(op string) {
	if s.probe != nil {
		s.once.Do(func() { s.probe(op) })
	}
}

func (s *endingSpy) Interrupt(ctx context.Context, req execution.InterruptRequest) (execution.InterruptResult, error) {
	s.fire("interrupt")
	return s.nexService.Interrupt(ctx, req)
}

func (s *endingSpy) Terminate(ctx context.Context, req execution.TerminateRequest) error {
	s.fire("terminate")
	return s.nexService.Terminate(ctx, req)
}

// oldLeaseProbe is the preempted tab's answer under its old lease, made at
// the spy's moment, with the request's state as the summary showed it then.
type oldLeaseProbe struct {
	mu                 sync.Mutex
	ran                bool
	op, pending, code  string
	status             int
	pendingErr, ansErr error
}

func (p *oldLeaseProbe) at(w *askingWorker, lease string) func(op string) {
	return func(op string) {
		pending, perr := w.rawPending()
		status, code, aerr := w.rawAnswer(lease, "allow")
		p.mu.Lock()
		defer p.mu.Unlock()
		p.ran, p.op, p.pending, p.pendingErr, p.status, p.code, p.ansErr = true, op, pending, perr, status, code, aerr
	}
}

// assertRefusedByTheLease: the old lease was refused while the request was
// still pending — the lease, not the request's state, is what refused it.
func (p *oldLeaseProbe) assertRefusedByTheLease(t *testing.T, wantOp string) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.True(t, p.ran, "the ending path never reached %s", wantOp)
	assert.Equal(t, wantOp, p.op)
	require.NoError(t, p.pendingErr)
	assert.Equal(t, permRID, p.pending, "the request was still pending when the old lease answered")
	require.NoError(t, p.ansErr)
	assert.Equal(t, http.StatusConflict, p.status, "the preempted tab's answer: %d %s", p.status, p.code)
	assert.Equal(t, "lease_mismatch", p.code)
}

// onRealEngine points a handoff-test module (fake tmux, sessions,
// terminals) at w's engine, its service through spy.
func (w *askingWorker) onRealEngine(env *handoffEnv, spy nexService) {
	env.m.sys.service = spy
	env.m.sys.store = w.f.m.sys.store
}

// The fixture's log can show an allowed tool run, so its absence elsewhere
// means something (and an answer under the holder's own lease is accepted).
func TestPermissionRace_FixtureLogsAnAllowedToolRun(t *testing.T) {
	w := newAskingWorker(t, askOpts{mode: "interrupt"})
	lease := w.attachTab(t)
	status, outcome := w.answer(t, lease, "allow")
	require.Equal(t, http.StatusOK, status, outcome)
	assert.Equal(t, "allowed", outcome)
	waitFor(t, "the turn's end", func() bool { return w.state(t) == "idle" })
	assert.Contains(t, w.logLines(t), "tool_ran toolu_01DGFR79wzyGL63u1dHNiKUc")
	got := w.resolved(t)
	require.Len(t, got, 1)
	assert.Equal(t, "allowed", got[0].Outcome)
	assert.Equal(t, "allowed", w.recordedOutcome(t))
}

// Guarantees 1 and 2 on every ending path. The tab holds control of a
// worker waiting on permRID; the path preempts it. At the path's first
// Interrupt or Terminate the tab answers under its old lease: 409
// lease_mismatch, while the request is still pending. Afterwards the request
// ended as cancelled/interrupt (the CLI withdrew it when stopped), an
// answer under a fresh lease gets permission_not_pending, and the tool
// never ran.
func TestPermissionRace_EndingPathsRefuseThePreemptedLease(t *testing.T) {
	paths := []struct {
		name  string
		bound bool   // the row a handoff of hoCode created (take-back)
		op    string // where the path first stops the worker
		run   func(t *testing.T, w *askingWorker, spy *endingSpy)
	}{
		{"exit", false, "terminate", func(t *testing.T, w *askingWorker, spy *endingSpy) {
			w.f.m.sys.service = spy
			status, raw, err := w.f.rawDo(http.MethodPost, "/api/nex/executions/"+w.id+"/exit", "", nil)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status, "exit: %s", raw)
			var out map[string]any
			require.NoError(t, json.Unmarshal(raw, &out))
			assert.Equal(t, true, out["terminated"], "%s", raw)
		}},
		{"Q1 manual resume", false, "terminate", func(t *testing.T, w *askingWorker, spy *endingSpy) {
			env := newHandoffEnv(t)
			w.onRealEngine(env, spy)
			env.terminals.live = map[string][]agent.TerminalSession{permSID: {{FrameID: "F", PaneID: "%4", SessionID: permSID, AgentType: "cc", Verified: true}}}
			env.m.onSessionStart(agent.SessionStartEvent{AgentType: "cc", SessionID: permSID, Source: "resume", TmuxSession: "proj-2", TmuxPaneID: "%4", FrameID: "F"})
			row, err := w.f.m.getExecution(context.Background(), w.id)
			require.NoError(t, err)
			assert.Equal(t, store.StateTerminated, row.State, "Q1 exited the worker")
		}},
		{"worker-rebuild with a replaced row", false, "terminate", func(t *testing.T, w *askingWorker, spy *endingSpy) {
			env := newHandoffEnv(t)
			w.onRealEngine(env, spy)
			status, body := postJSON(t, env.srv.URL+"/api/nex/worker-rebuild", map[string]any{
				"session_id": permSID, "cwd": w.f.root, "profile": "handoff_ask", "replace_execution_id": w.id,
			})
			require.Equal(t, http.StatusOK, status, "%v", body)
			assert.NotEqual(t, w.id, body["execution_id"])
		}},
		{"take-to-terminal", false, "interrupt", func(t *testing.T, w *askingWorker, spy *endingSpy) {
			env := newTTEnv(t)
			w.onRealEngine(env.handoffEnv, spy)
			status, body := env.post(t, w.id, ttBody())
			require.Equal(t, http.StatusOK, status, "%v", body)
			assert.Equal(t, true, body["exited"], "%v", body)
		}},
		{"take-back", true, "interrupt", func(t *testing.T, w *askingWorker, spy *endingSpy) {
			env := newTakebackEnv(t)
			w.onRealEngine(env.handoffEnv, spy)
			status, body := env.post(t, hoCode, map[string]any{
				"expected_tmux_instance": hoInstance, "execution_id": w.id, "resume_command": "claude --resume {id}",
			})
			require.Equal(t, http.StatusOK, status, "%v", body)
			assert.Equal(t, true, body["exited"], "%v", body)
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			w := newAskingWorker(t, askOpts{mode: "interrupt", bound: p.bound})
			lease := w.attachTab(t)
			probe := &oldLeaseProbe{}
			spy := &endingSpy{nexService: w.svc, probe: probe.at(w, lease)}

			p.run(t, w, spy)

			probe.assertRefusedByTheLease(t, p.op)
			got := w.resolved(t)
			require.Len(t, got, 1, "exactly one terminal outcome")
			assert.Equal(t, "cancelled", got[0].Outcome)
			assert.Equal(t, "interrupt", got[0].Reason)
			wantSource := execution.SourceUser // a transfer's settle interrupts
			if p.op == "terminate" {
				wantSource = execution.SourceTerminated
			}
			assert.Equal(t, wantSource, got[0].InterruptSource)
			assert.Equal(t, "cancelled", w.recordedOutcome(t))
			w.assertLaterAnswerNotPending(t)
			w.assertToolNeverRan(t)
		})
	}
}

// exitWorkerOverHTTP is POST /exit with no caller lease (a non-pane
// client): it must exit the worker.
func (w *askingWorker) exitWorkerOverHTTP(t *testing.T) {
	t.Helper()
	status, raw, err := w.f.rawDo(http.MethodPost, "/api/nex/executions/"+w.id+"/exit", "", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "exit: %s", raw)
}

// An interrupt while a request is pending: the CLI withdraws it, recorded
// as cancelled/interrupt (source user); the holder's answer under its still
// valid lease then gets permission_not_pending, and the tool never ran.
func TestPermissionRace_InterruptWhilePending(t *testing.T) {
	w := newAskingWorker(t, askOpts{mode: "interrupt"})
	lease := w.attachTab(t)
	status, raw, err := w.f.rawDo(http.MethodPost, "/api/nex/v1/executions/"+w.id+"/interrupt", permTab, map[string]string{"lease_id": lease})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, "interrupt: %s", raw)

	got := w.resolved(t)
	require.Len(t, got, 1)
	assert.Equal(t, resolvedView{RequestID: permRID, Outcome: "cancelled", Reason: "interrupt", InterruptSource: execution.SourceUser}, got[0])
	assert.Equal(t, "cancelled", w.recordedOutcome(t))
	status, code := w.answer(t, lease, "allow")
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "permission_not_pending", code, "the lease is still the tab's: only the request's state refuses it")
	w.assertToolNeverRan(t)
}

// Guarantee 3: a request that already ended denied, cancelled or expired
// stays so through an ending path (exit), with exactly one terminal outcome,
// and an allow afterwards gets permission_not_pending.
func TestPermissionRace_EndingPathKeepsTheOutcome(t *testing.T) {
	cases := []struct {
		name         string
		opts         askOpts
		end          func(t *testing.T, w *askingWorker) // ends the request before the ending path
		want, reason string
	}{
		{"denied by the holder", askOpts{mode: "ask"}, func(t *testing.T, w *askingWorker) {
			status, outcome := w.answer(t, w.attachTab(t), "deny")
			require.Equal(t, http.StatusOK, status, outcome)
			require.Equal(t, "denied", outcome)
		}, "denied", ""},
		{"cancelled by the CLI", askOpts{mode: "cancel"}, nil, "cancelled", "cli_cancelled"},
		{"expired (permission_timeout_s)", askOpts{mode: "ask", timeoutS: 1}, nil, "expired", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newAskingWorker(t, c.opts)
			if c.end != nil {
				c.end(t, w)
			}
			waitFor(t, "the request's "+c.want+" outcome and the turn's end", func() bool {
				got := w.resolved(t)
				return len(got) == 1 && got[0].Outcome == c.want && w.state(t) == "idle"
			})

			w.exitWorkerOverHTTP(t)

			got := w.resolved(t)
			require.Len(t, got, 1, "exactly one terminal outcome, before and after the exit")
			assert.Equal(t, c.want, got[0].Outcome)
			assert.Equal(t, c.reason, got[0].Reason)
			assert.Equal(t, c.want, w.recordedOutcome(t))
			w.assertLaterAnswerNotPending(t)
			assert.Equal(t, c.want, w.recordedOutcome(t), "the later allow changed nothing")
			assert.Len(t, w.resolved(t), 1)
			w.assertToolNeverRan(t)
		})
	}
}

// A daemon restart while a request is pending: the next engine's startup
// reconcile ends the orphaned turn and records the request cancelled /
// daemon_restart; a later answer gets permission_not_pending. The crash is
// modelled as a shutdown whose budget is already spent — it converges
// nothing, so the turn stays running in the database, as after a kill —
// followed by closing the store; the next engine opens the same data dir.
func TestPermissionRace_DaemonRestartCancelsThePendingRequest(t *testing.T) {
	dataDir, root := t.TempDir(), t.TempDir()
	w := newAskingWorker(t, askOpts{mode: "ask", dataDir: dataDir, root: root})

	spent, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_ = w.f.m.Stop(spent) // converges nothing: the budget is gone
	require.NoError(t, w.f.m.Close())
	w.f.m.sys.shutdown, w.f.m.sys.close = nil, nil // stopped and closed: the fixture's cleanup has nothing left to do

	f2 := newMountFixtureWith(t, func(cfg *pdxconfig.Config) {
		cfg.Nex.ClaudeBin = permFixtureClaude(t, "ask", filepath.Join(t.TempDir(), "unused.log"))
		cfg.Nex.Sandbox.MaxProfile = "handoff"
		cfg.DataDir = dataDir
		cfg.Nex.RepoRoots = []string{root}
	})
	w2 := &askingWorker{f: f2, svc: f2.m.sys.service, id: w.id, log: w.log}

	got := w2.resolved(t)
	require.Len(t, got, 1)
	assert.Equal(t, "cancelled", got[0].Outcome)
	assert.Equal(t, "daemon_restart", got[0].Reason)
	assert.Equal(t, "cancelled", w2.recordedOutcome(t))
	assert.Equal(t, "", w2.pending(t), "nothing pending after the reconcile")
	status, code := w2.answer(t, w2.attachTab(t), "allow")
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "permission_not_pending", code)
	w2.assertToolNeverRan(t)
}

// The documented residual (spec §5.4 residual 1): Nexen re-checks the lease
// right before it writes an answer but does not fence the write, so an
// answer whose check passed just before the preempt is still written. Pinned
// only as: exactly one terminal outcome is recorded for the request — never
// two, never none — whichever of the answer and the exit wins.
func TestPermissionRace_AnswerAgainstAnExitRecordsOneOutcome(t *testing.T) {
	assertOneOutcome := func(t *testing.T, w *askingWorker) {
		t.Helper()
		got := w.resolved(t)
		require.Len(t, got, 1, "exactly one terminal outcome for %s: %+v", permRID, got)
		assert.Equal(t, got[0].Outcome, w.recordedOutcome(t), "the row records the same one")
		t.Logf("outcome: %+v", got[0])
	}
	t.Run("the answer's check passed before the preempt", func(t *testing.T) {
		w := newAskingWorker(t, askOpts{mode: "interrupt"})
		lease := w.attachTab(t)
		status, _ := w.answer(t, lease, "allow")
		require.Equal(t, http.StatusOK, status)
		w.exitWorkerOverHTTP(t)
		assertOneOutcome(t, w)
	})
	t.Run("the answer and the exit at once", func(t *testing.T) {
		w := newAskingWorker(t, askOpts{mode: "interrupt"})
		lease := w.attachTab(t)
		var (
			wg                    sync.WaitGroup
			start                 = make(chan struct{})
			ansStatus, exitStatus int
			ansCode               string
			ansErr, exitErr       error
			exitBody              []byte
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			ansStatus, ansCode, ansErr = w.rawAnswer(lease, "allow")
		}()
		go func() {
			defer wg.Done()
			<-start
			exitStatus, exitBody, exitErr = w.f.rawDo(http.MethodPost, "/api/nex/executions/"+w.id+"/exit", "", nil)
		}()
		close(start)
		wg.Wait()
		require.NoError(t, ansErr)
		require.NoError(t, exitErr)
		require.Equal(t, http.StatusOK, exitStatus, "the exit itself never fails (D4): %s", exitBody)
		t.Logf("answer: %d %s", ansStatus, ansCode)
		assertOneOutcome(t, w)
	})
}

// postJSON posts body as JSON to url and decodes the JSON answer.
func postJSON(t *testing.T, url string, body any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", strings.NewReader(string(raw)))
	require.NoError(t, err)
	defer resp.Body.Close()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out), "response is JSON")
	return resp.StatusCode, out
}
