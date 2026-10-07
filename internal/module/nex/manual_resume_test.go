package nex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"lab.protype.tw/wake/nexen/store"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
)

func ev(source string) agent.SessionStartEvent {
	return agent.SessionStartEvent{AgentType: "cc", SessionID: tS, Source: source, TmuxSession: "proj-2", TmuxPaneID: "%4", FrameID: "F"}
}

func liveTerminal(env *handoffEnv, verified bool) {
	env.terminals.live = map[string][]agent.TerminalSession{tS: {{FrameID: "F", PaneID: "%4", SessionID: tS, AgentType: "cc", Verified: verified}}}
}

// hostEventSink collects what the module broadcasts on the host-events bus.
type hostEventSink struct {
	t   *testing.T
	sub *core.EventSubscriber
}

// captureHostEvents gives the env a real broadcaster (it has no core) and
// subscribes a test reader to it.
func (e *handoffEnv) captureHostEvents(t *testing.T) *hostEventSink {
	t.Helper()
	e.m.core = &core.Core{Events: core.NewEventsBroadcaster()}
	sub := e.m.core.Events.AddTestSubscriber()
	t.Cleanup(func() { e.m.core.Events.RemoveTestSubscriber(sub) })
	return &hostEventSink{t: t, sub: sub}
}

// events drains what is queued now and returns the frames of one type.
func (s *hostEventSink) events(typ string) []core.HostEvent {
	var out []core.HostEvent
	for {
		select {
		case raw := <-s.sub.SendCh():
			var he core.HostEvent
			require.NoError(s.t, json.Unmarshal(raw, &he))
			if he.Type == typ {
				out = append(out, he)
			}
		default:
			return out
		}
	}
}

func archivedIDs(env *handoffEnv) []string {
	env.svc.mu.Lock()
	defer env.svc.mu.Unlock()
	var ids []string
	for _, r := range env.svc.archiveReqs {
		ids = append(ids, r.ExecutionID)
	}
	return ids
}

func TestManualResume_ExitsLiveWorkersAndBroadcasts(t *testing.T) {
	env := newHandoffEnv(t)
	sub := env.captureHostEvents(t)
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1), row("E2", "running", false, "", tS, 2)}
	env.svc.lease = store.Lease{ID: "L-d"}

	env.m.onSessionStart(ev("resume"))

	if len(env.svc.terminateCalls) != 2 || len(env.svc.ArchiveCalls()) != 2 {
		t.Fatalf("terminate=%d archive=%d", len(env.svc.terminateCalls), len(env.svc.ArchiveCalls()))
	}
	got := sub.events("nex-worker-exited")
	if len(got) != 2 {
		t.Fatalf("events = %v", got)
	}
	var v map[string]string
	_ = json.Unmarshal([]byte(got[0].Value), &v)
	if v["reason"] != "manual_resume" || v["session_id"] != tS || v["tmux_session"] != "proj-2" || v["execution_id"] == "" {
		t.Fatalf("value = %v", v)
	}
	if got[0].Session != "proj-2" {
		t.Fatalf("frame session = %q", got[0].Session)
	}
	if len(env.svc.acquires) == 0 || env.svc.acquires[0] != "pdx:"+testHostID {
		t.Fatalf("the daemon acts as the bare host principal: %v", env.svc.acquires)
	}
}

// Plan Task 5a (b), ruling R-PC-1: Q1's exit preempts a pdx tab's lease
// like any exit (D22) — the tab's lease released as the holder, an own one
// acquired, terminate + archive under it, released after — so from the
// preempt on that tab's send or permission answer is refused, before the
// worker ends.
func TestManualResume_PreemptsAPdxHoldersLease(t *testing.T) {
	const self = "pdx:" + testHostID
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	lb := liveLease("L-b", tab2)
	fakeStore(env).listRows = []store.Execution{withLease(row("E1", "idle", false, tS, "", 1), lb)}
	env.svc.enforceLease = true
	env.svc.heldLease = lb
	env.svc.lease = store.Lease{ID: "L-d"}
	probe := probeAtTerminate(env.svc, "E1", lb)

	env.m.onSessionStart(ev("resume"))

	assert.Equal(t, []string{"acquire", "release", "acquire", "terminate", "archive", "release"}, env.svc.Calls())
	assert.Equal(t, []releaseCall{{"E1", lb.ID, tab2}, {"E1", "L-d", self}}, env.svc.releases)
	require.Len(t, env.svc.terminateCalls, 1)
	assert.Equal(t, "L-d", env.svc.terminateCalls[0].LeaseID)
	assert.Equal(t, self, env.svc.terminateCalls[0].PrincipalID)
	probe.assertRefused(t)
}

func TestManualResume_DoesNothingWhen(t *testing.T) {
	cases := map[string]func(env *handoffEnv) agent.SessionStartEvent{
		"source is clear":   func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); return ev("clear") },
		"source is compact": func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, true); return ev("compact") },
		"not cc": func(env *handoffEnv) agent.SessionStartEvent {
			liveTerminal(env, true)
			e := ev("resume")
			e.AgentType = "codex"
			return e
		},
		"a Purdex transfer holds S": func(env *handoffEnv) agent.SessionStartEvent {
			liveTerminal(env, true)
			env.m.locks.TryLock(sidLockKey(tS))
			return ev("resume")
		},
		"the terminal is gone":     func(env *handoffEnv) agent.SessionStartEvent { return ev("resume") },
		"only an unverified frame": func(env *handoffEnv) agent.SessionStartEvent { liveTerminal(env, false); return ev("resume") },
		"no live worker": func(env *handoffEnv) agent.SessionStartEvent {
			liveTerminal(env, true)
			fakeStore(env).listRows = nil
			return ev("startup")
		},
		"engine unavailable": func(env *handoffEnv) agent.SessionStartEvent {
			liveTerminal(env, true)
			env.m.sys = engine{}
			return ev("resume")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1)}
			e := setup(env)
			env.m.onSessionStart(e)
			if len(env.svc.terminateCalls)+len(env.svc.ArchiveCalls()) != 0 {
				t.Fatal("must not exit")
			}
		})
	}
}

func TestManualResume_SkipsAWorkerBeingMovedAndReportsOnlySuccesses(t *testing.T) {
	env := newHandoffEnv(t)
	sub := env.captureHostEvents(t)
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1), row("E2", "failed", false, tS, "", 2)}
	env.m.locks.TryLock(takeToTerminalLockKey("E1")) // E1 is mid-exit elsewhere
	env.svc.archiveErr = errors.New("db busy")       // E2's archive fails -> not exited
	env.m.onSessionStart(ev("resume"))
	if len(sub.events("nex-worker-exited")) != 0 {
		t.Fatal("no event for a worker that did not exit")
	}
}

func TestManualResume_OverflowReconcilesEverySession(t *testing.T) {
	env := newHandoffEnv(t)
	// S has a verified terminal; T has none. Both have a live worker.
	env.terminals.live = map[string][]agent.TerminalSession{tS: {{FrameID: "F", PaneID: "%4", SessionID: tS, AgentType: "cc", Verified: true}}}
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1), row("E2", "idle", false, tT, "", 2)}
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	if ids := archivedIDs(env); len(ids) != 1 || ids[0] != "E1" {
		t.Fatalf("archived = %v; want only E1 (S is in a terminal, T is not)", ids)
	}
}

// #1624 Task 11: the overflow path does not know the tmux session name
// (agent.TerminalSession has no such field). The worker still exits, and
// nex-worker-exited carries tmux_session "" — the fallback the reconcile's
// comment documents and the SPA toast handles.
func TestManualResume_OverflowExitsWithAnEmptyTmuxSession(t *testing.T) {
	env := newHandoffEnv(t)
	sub := env.captureHostEvents(t)
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1)}
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	assert.Equal(t, []string{"E1"}, archivedIDs(env))
	got := sub.events("nex-worker-exited")
	require.Len(t, got, 1)
	var v map[string]string
	require.NoError(t, json.Unmarshal([]byte(got[0].Value), &v))
	assert.Equal(t, map[string]string{"execution_id": "E1", "session_id": tS, "reason": "manual_resume", "tmux_session": ""}, v)
	assert.Equal(t, "", got[0].Session, "the frame carries no session name either")
}

func TestManualResume_TruncatedScanStillExitsWhatItFound(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	rows := make([]store.Execution, ownerScanPageSize*ownerScanMaxPages+1)
	for i := range rows {
		rows[i] = row(fmt.Sprintf("%06d", i), "terminated", false, tS, "", int64(i))
	}
	rows[0] = row("000000", "idle", false, tS, "", 0) // on page 1
	fakeStore(env).listRows = rows
	env.m.onSessionStart(ev("resume"))
	if len(env.svc.ArchiveCalls()) != 1 {
		t.Fatal("the worker found before the cap must still exit")
	}
	assert.False(t, pendingRecheck(env, tS), "truncation is persistent: no re-check")
}

// PR #1590 R1-2: a page error must not drop the worker page 1 already found.
func TestManualResume_PageErrorStillExitsWhatItFound(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	pageTwoFails(fakeStore(env))
	env.m.onSessionStart(ev("resume"))
	assert.Equal(t, []string{"000000"}, archivedIDs(env))
	assert.Equal(t, 2, fakeStore(env).listCalls)
}

func TestManualResume_OverflowPageErrorStillReconcilesWhatItFound(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	pageTwoFails(fakeStore(env))
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	assert.Equal(t, []string{"000000"}, archivedIDs(env))
}

// verifiedTerminals gives each sid a verified terminal frame.
func verifiedTerminals(env *handoffEnv, sids ...string) {
	env.terminals.live = map[string][]agent.TerminalSession{}
	for i, sid := range sids {
		env.terminals.live[sid] = []agent.TerminalSession{{FrameID: "F" + sid, PaneID: fmt.Sprintf("%%%d", i+10), SessionID: sid, AgentType: "cc", Verified: true}}
	}
}

// PR #1590 R1-1: the overflow reconcile scans the table once, not once per
// session.
func TestManualResume_OverflowScansOnce(t *testing.T) {
	env := newHandoffEnv(t)
	sids := []string{"0a1b2c3d-0000-4000-8000-0000000000a0", "0a1b2c3d-0000-4000-8000-0000000000a1", "0a1b2c3d-0000-4000-8000-0000000000a2", "0a1b2c3d-0000-4000-8000-0000000000a3", "0a1b2c3d-0000-4000-8000-0000000000a4"}
	verifiedTerminals(env, sids...)
	var rows []store.Execution
	for i, sid := range sids {
		rows = append(rows, row(fmt.Sprintf("E%d", i), "idle", false, sid, "", int64(1000+i)))
	}
	for i := 0; i < ownerScanPageSize; i++ { // a second page
		rows = append(rows, row(fmt.Sprintf("0%05d", i), "terminated", false, "OTHER", "", int64(i)))
	}
	fakeStore(env).listRows = rows
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	assert.Equal(t, 2, fakeStore(env).listCalls, "exactly the List calls of one scan")
	assert.ElementsMatch(t, []string{"E0", "E1", "E2", "E3", "E4"}, archivedIDs(env))
}

// A row in two groups (session_id S, resume_session_id R) is exited once.
func TestManualResume_OverflowExitsARowInTwoGroupsOnce(t *testing.T) {
	env := newHandoffEnv(t)
	verifiedTerminals(env, tS, tR)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, tR, 1)}
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	assert.Equal(t, []string{"E1"}, archivedIDs(env))
	assert.Len(t, env.svc.terminateCalls, 1)
}

// Each candidate is re-read under its exec lock; one that is no longer S's
// live worker is skipped.
func TestManualResume_SkipsACandidateNoLongerLiveForS(t *testing.T) {
	archived := row("E1", "idle", true, tS, "", 1)
	terminated := row("E1", "terminated", false, tS, "", 1)
	moved := row("E1", "idle", false, "OTHER", "", 1)
	cases := map[string]struct {
		overflow bool
		reread   getResult
	}{
		"overflow: archived since the scan":   {true, getResult{exec: archived}},
		"overflow: terminated since the scan": {true, getResult{exec: terminated}},
		"resume: archived since the scan":     {false, getResult{exec: archived}},
		"resume: no longer for S":             {false, getResult{exec: moved}},
		"resume: the re-read fails":           {false, getResult{err: errors.New("db busy")}},
		"resume: gone":                        {false, getResult{err: store.ErrNotFound}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			env := newHandoffEnv(t)
			liveTerminal(env, true)
			st := fakeStore(env)
			st.listRows = []store.Execution{row("E1", "idle", false, tS, "", 1)}
			st.results = []getResult{c.reread}
			if c.overflow {
				env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
			} else {
				env.m.onSessionStart(ev("resume"))
			}
			assert.Equal(t, 1, st.Calls(), "one re-read")
			assert.Empty(t, env.svc.Calls(), "nothing done to the worker")
		})
	}
}

func TestManualResume_StartSubscribesStopUnsubscribes(t *testing.T) {
	env := newHandoffEnv(t)
	if err := env.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if env.terminals.subscribed == nil {
		t.Fatal("Start must subscribe")
	}
	_ = env.m.Stop(context.Background())
	if env.terminals.subscribed != nil {
		t.Fatal("Stop must unsubscribe")
	}
}

func TestManualResume_StartSkipsSubscriptionWhenInitFailed(t *testing.T) {
	env := newHandoffEnv(t)
	env.m.initErr = errors.New("no engine")
	require.NoError(t, env.m.Start(context.Background()))
	assert.Nil(t, env.terminals.subscribed)
}

// waitArchived waits for the fake's archive call of exec (the re-check runs
// on its own goroutine).
func waitArchived(t *testing.T, env *handoffEnv, exec string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, id := range archivedIDs(env) {
			if id == exec {
				return true
			}
		}
		return false
	}, 3*time.Second, 5*time.Millisecond, "the re-check must exit %s", exec)
}

func TestTakeToTerminal_SecondCheckAbortRechecksEndToEnd(t *testing.T) {
	env := newTTEnv(t)
	env.terminals.byCall = ownerAfterFirstLook(tbSessionID)
	env.store.listRows = []store.Execution{ttExec(store.StateIdle)}
	status, _ := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusConflict, status)
	waitArchived(t, env.handoffEnv, tbExecID)
}

// installRecheckSeam replaces the re-check launcher. At call time it asserts,
// synchronously, that the sid lock and the execution lock are both free (the
// re-check must start after every unlock), and records the session.
func installRecheckSeam(t *testing.T, env *handoffEnv, execID string) *[]string {
	t.Helper()
	var calls []string
	env.m.recheck = func(sid string) {
		calls = append(calls, sid)
		if env.m.locks.TryLock(sidLockKey(sid)) {
			env.m.locks.Unlock(sidLockKey(sid))
		} else {
			t.Errorf("sid lock still held when the re-check was requested")
		}
		if env.m.locks.TryLock(takeToTerminalLockKey(execID)) {
			env.m.locks.Unlock(takeToTerminalLockKey(execID))
		} else {
			t.Errorf("execution lock still held when the re-check was requested")
		}
	}
	return &calls
}

func TestTakeToTerminal_SecondCheckAbortRechecksAfterEveryUnlock(t *testing.T) {
	env := newTTEnv(t)
	calls := installRecheckSeam(t, env.handoffEnv, tbExecID)
	env.terminals.byCall = ownerAfterFirstLook(tbSessionID)
	env.store.listRows = []store.Execution{ttExec(store.StateIdle)}
	status, _ := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, []string{tbSessionID}, *calls)
}

func TestTakeback_SecondCheckAbortRechecksAfterEveryUnlock(t *testing.T) {
	env := newTakebackEnv(t)
	calls := installRecheckSeam(t, env.handoffEnv, tbExecID)
	env.terminals.byCall = ownerAfterFirstLook(tbSessionID)
	env.store.listRows = []store.Execution{idleExec()}
	status, _ := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusConflict, status)
	assert.Equal(t, []string{tbSessionID}, *calls)
}

func TestTakeback_FirstCheckAbortTriggersNoRecheck(t *testing.T) {
	env := newTakebackEnv(t)
	calls := installRecheckSeam(t, env.handoffEnv, tbExecID)
	env.terminals.byCall = func(int) []agent.TerminalSession {
		return []agent.TerminalSession{{PaneID: "%9", SessionID: tbSessionID, AgentType: "cc", Verified: true}}
	}
	env.store.listRows = []store.Execution{idleExec()}
	status, _ := env.post(t, hoCode, takebackBody())
	require.Equal(t, http.StatusConflict, status)
	assert.Empty(t, *calls)
}

func TestManualResume_StartupExitsALiveWorker(t *testing.T) {
	env := newHandoffEnv(t)
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1)}
	env.m.onSessionStart(ev("startup"))
	assert.Equal(t, []string{"E1"}, archivedIDs(env))
}

func TestManualResume_AfterStopDoesNothing(t *testing.T) {
	env := newHandoffEnv(t)
	require.NoError(t, env.m.Start(context.Background()))
	_ = env.m.Stop(context.Background())
	liveTerminal(env, true)
	fakeStore(env).listRows = []store.Execution{row("E1", "idle", false, tS, "", 1)}
	env.m.onSessionStart(ev("resume"))
	env.m.onSessionStart(agent.SessionStartEvent{Overflow: true})
	var rechecked bool
	env.m.recheck = func(string) { rechecked = true }
	env.m.recheckSession(tS)
	assert.Empty(t, env.svc.terminateCalls)
	assert.Empty(t, env.svc.ArchiveCalls())
	assert.False(t, rechecked, "no re-check launches after Stop")
}

func TestTakeToTerminal_FirstCheckAbortTriggersNoRecheck(t *testing.T) {
	env := newTTEnv(t)
	env.terminals.byCall = func(int) []agent.TerminalSession {
		return []agent.TerminalSession{{PaneID: "%9", SessionID: tbSessionID, AgentType: "cc", Verified: true}}
	}
	env.store.listRows = []store.Execution{ttExec(store.StateIdle)}
	status, _ := env.post(t, tbExecID, ttBody())
	require.Equal(t, http.StatusConflict, status)
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, 1, env.terminals.Calls(), "no re-check lookup")
	assert.Empty(t, env.svc.ArchiveCalls())
}

// fakeStore is the env engine store (handoffEnv does not expose it).
func fakeStore(env *handoffEnv) *fakeNexStore { return env.m.sys.store.(*fakeNexStore) }
