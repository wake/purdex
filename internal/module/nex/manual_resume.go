package nex

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"lab.protype.tw/wake/nexen/store"
)

// Manual resume exits the worker (conversation entity Q1, D3): a terminal
// that resumes a session a live worker holds takes the conversation over,
// and the worker exits — unless the resume is one of Purdex's own
// transfers, which hold the session's sid lock for their whole run.
//
// Known, accepted trade-off: the handler and the transfers all use TryLock,
// so a re-check that holds sid:<S> while a retried transfer holds exec:<id>
// can make both back off (the worker stays live while S is in a terminal)
// until the next SessionStart or overflow reconcile. The window is narrow
// and neither side blocks, so there is no deadlock.

// manualResumeTimeout bounds only the terminal lookup (LiveBySessionID).
// scanLiveWorkers and exitWorker run under detachedContext, which strips this
// deadline; each of those operations carries its own timeout.
const manualResumeTimeout = 90 * time.Second

// internalPrincipal is the bare host principal the daemon acts as.
func (m *Module) internalPrincipal() string { return "pdx:" + m.opts.Config.HostID }

// onSessionStart handles one SessionStart from the agent hub. The hub runs
// subscribers off the hook path, so this is synchronous.
func (m *Module) onSessionStart(ev agent.SessionStartEvent) {
	if m.startsStopped.Load() {
		return
	}
	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil || m.terminals == nil {
		return
	}
	if ev.Overflow {
		m.reconcileTerminalOwners()
		return
	}
	if ev.AgentType != "cc" || ev.SessionID == "" || (ev.Source != "startup" && ev.Source != "resume") {
		return
	}
	sid := ev.SessionID
	m.resolveManualResume(context.Background(), sid, ev.TmuxSession, func(ctx context.Context) []store.Execution {
		workers, err := m.liveWorkersFor(ctx, sid)
		if err != nil {
			// Unlike an owner check (which refuses a transfer on a partial scan),
			// the terminal has ALREADY taken S over here: exiting the workers
			// found is strictly better than exiting none.
			m.logf("nex: manual resume %s: worker scan: %v (acting on %d found)", sid, err, len(workers))
		}
		return workers
	})
}

// resolveManualResume is Q1's per-session path, shared by a SessionStart
// and the overflow reconcile. Under sid:<S> (TryLock: a Purdex transfer of
// S holds it for its whole run), and only while a verified terminal frame
// runs S, it exits each of S's live workers that workers returns — called
// inside the lock, after the terminal check. Each candidate is re-read
// under its exec:<id> lock and skipped unless it is still S's live worker
// (the reconcile's rows can be stale by the time a later group runs).
// It returns the ids it exited.
func (m *Module) resolveManualResume(parent context.Context, sid, tmuxSession string, workers func(context.Context) []store.Execution) []string {
	key := sidLockKey(sid)
	if !m.locks.TryLock(key) {
		return nil // a Purdex transfer of S is in flight, or another handler has S
	}
	defer m.locks.Unlock(key)

	// Act only on a terminal that is verifiably running S right now: a late
	// hook from a session a failed transfer already killed must not exit the
	// worker that transfer left alone.
	ctx, cancel := context.WithTimeout(parent, manualResumeTimeout)
	terms, err := m.terminals.LiveBySessionID(ctx, "cc", sid)
	cancel()
	if err != nil {
		m.logf("nex: manual resume %s: terminal lookup: %v", sid, err)
		return nil
	}
	verified := false
	for _, t := range terms {
		verified = verified || t.Verified
	}
	if !verified {
		return nil
	}
	principal := m.internalPrincipal()
	var exited []string
	for _, w := range workers(parent) {
		if m.exitManualResumeWorker(parent, sid, tmuxSession, w.ID, principal) {
			exited = append(exited, w.ID)
		}
	}
	return exited
}

// exitManualResumeWorker exits one candidate under its exec:<id> lock, after
// re-reading it; true when it exited.
func (m *Module) exitManualResumeWorker(parent context.Context, sid, tmuxSession, execID, principal string) bool {
	lk := takeToTerminalLockKey(execID)
	if !m.locks.TryLock(lk) {
		m.logf("nex: manual resume %s: %s is busy; left to its mover", sid, execID)
		return false
	}
	defer m.locks.Unlock(lk)
	w, err := m.getExecution(parent, execID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			m.logf("nex: manual resume %s: re-reading %s: %v", sid, execID, err)
		}
		return false
	}
	if !isLiveExecution(w) || !executionIsFor(w, sid) {
		return false // exited or moved since the scan
	}
	out, herr := m.exitWorker(parent, w, nil, principal)
	if herr != nil || !out.Exited() {
		m.logf("nex: manual resume %s: exiting %s failed: %v", sid, execID, herr)
		return false
	}
	m.logf("nex: manual resume %s in %s -> worker %s exited", sid, tmuxSession, execID)
	m.broadcastWorkerExited(execID, sid, tmuxSession)
	return true
}

// reconcileTerminalOwners is the hub's overflow path: SessionStarts were
// coalesced away while this subscriber lagged, so every session that still
// has a live worker is re-checked as if it had just resumed. One scan of
// the table; its live rows are grouped by session_id and by
// resume_session_id (a row can sit in two groups), and each group goes
// through the per-session path with every guard. A worker exited under one
// key is not offered again under the other.
func (m *Module) reconcileTerminalOwners() {
	ctx := context.Background()
	workers, err := m.scanLiveWorkers(ctx, func(store.Execution) bool { return true })
	if err != nil {
		m.logf("nex: owner reconcile: worker scan: %v (re-checking %d found)", err, len(workers))
	}
	groups := map[string][]store.Execution{}
	var order []string
	for _, w := range workers {
		for i, sid := range []string{w.SessionID, w.ResumeSessionID} {
			if sid == "" || (i == 1 && sid == w.SessionID) {
				continue
			}
			if _, ok := groups[sid]; !ok {
				order = append(order, sid)
			}
			groups[sid] = append(groups[sid], w)
		}
	}
	done := map[string]bool{}
	for _, sid := range order {
		if m.startsStopped.Load() {
			return
		}
		group := groups[sid]
		// The tmux session name is unknown on this path (agent.TerminalSession
		// has no such field), so nex-worker-exited carries tmux_session "";
		// the SPA toast (Task 18) must fall back when it is empty.
		exited := m.resolveManualResume(ctx, sid, "", func(context.Context) []store.Execution {
			var left []store.Execution
			for _, w := range group {
				if !done[w.ID] {
					left = append(left, w)
				}
			}
			return left
		})
		for _, id := range exited {
			done[id] = true
		}
	}
}

// recheckSession asks for one Q1 re-check of S. A transfer that aborted at
// its second owner check calls it from a defer registered before its
// sid-lock unlock, so the re-check starts after the release.
//
// Stop deliberately does not wait for in-flight handlers or re-checks: one
// exit can take a full terminate budget, and shutdown must not block on it.
// startsStopped only stops new work from starting.
func (m *Module) recheckSession(sid string) {
	if m.startsStopped.Load() {
		return
	}
	if m.recheck != nil {
		m.recheck(sid)
		return
	}
	// Same as the overflow path: the tmux session name is unknown here, so
	// tmux_session is "" in the broadcast (the SPA toast must fall back).
	go func() {
		defer func() {
			if r := recover(); r != nil {
				m.logf("nex: manual resume re-check %s: panic: %v", sid, r)
			}
		}()
		m.onSessionStart(agent.SessionStartEvent{AgentType: "cc", SessionID: sid, Source: "resume"})
	}()
}

func (m *Module) broadcastWorkerExited(execID, sid, tmuxSession string) {
	if m.core == nil || m.core.Events == nil {
		return
	}
	value, _ := json.Marshal(map[string]string{
		"execution_id": execID, "session_id": sid, "reason": "manual_resume", "tmux_session": tmuxSession,
	})
	m.core.Events.Broadcast(tmuxSession, "nex-worker-exited", string(value))
}
