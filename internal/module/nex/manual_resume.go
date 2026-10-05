package nex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"lab.protype.tw/wake/nexen/store"
)

// Manual resume exits the worker (conversation entity Q1, D3): a terminal
// that resumes a session a live worker holds takes the conversation over,
// and the worker exits — unless the resume is one of Purdex's own
// transfers, which hold the session's sid lock for their whole run.

const manualResumeTimeout = 90 * time.Second // > one exit's terminate budget

// internalPrincipal is the bare host principal the daemon acts as.
func (m *Module) internalPrincipal() string { return "pdx:" + m.opts.Config.HostID }

// onSessionStart handles one SessionStart from the agent hub. The hub runs
// subscribers off the hook path, so this is synchronous.
func (m *Module) onSessionStart(ev agent.SessionStartEvent) {
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
	key := sidLockKey(ev.SessionID)
	if !m.locks.TryLock(key) {
		return // a Purdex transfer of S is in flight, or another handler has S
	}
	defer m.locks.Unlock(key)

	ctx, cancel := context.WithTimeout(context.Background(), manualResumeTimeout)
	defer cancel()
	// Act only on a terminal that is verifiably running S right now: a late
	// hook from a session a failed transfer already killed must not exit the
	// worker that transfer left alone.
	terms, err := m.terminals.LiveBySessionID(ctx, "cc", ev.SessionID)
	if err != nil {
		m.logf("nex: manual resume %s: terminal lookup: %v", ev.SessionID, err)
		return
	}
	verified := false
	for _, t := range terms {
		verified = verified || t.Verified
	}
	if !verified {
		return
	}
	workers, err := m.liveWorkersFor(ctx, ev.SessionID)
	if err != nil {
		// Unlike an owner check (which refuses a transfer on a partial scan),
		// the terminal has ALREADY taken S over here: exiting the workers
		// found is strictly better than exiting none.
		m.logf("nex: manual resume %s: worker scan: %v (acting on %d found)", ev.SessionID, err, len(workers))
	}
	principal := m.internalPrincipal()
	for _, w := range workers {
		lk := takeToTerminalLockKey(w.ID)
		if !m.locks.TryLock(lk) {
			m.logf("nex: manual resume %s: %s is busy; left to its mover", ev.SessionID, w.ID)
			continue
		}
		out, herr := m.exitWorker(ctx, w, nil, principal)
		m.locks.Unlock(lk)
		if herr != nil || !out.Exited() {
			m.logf("nex: manual resume %s: exiting %s failed: %v", ev.SessionID, w.ID, herr)
			continue
		}
		m.logf("nex: manual resume %s in %s -> worker %s exited", ev.SessionID, ev.TmuxSession, w.ID)
		m.broadcastWorkerExited(w.ID, ev)
	}
}

// reconcileTerminalOwners is the hub's overflow path: SessionStarts were
// coalesced away while this subscriber lagged, so every session that still
// has a live worker is re-checked as if it had just resumed. The
// per-session path keeps every guard (sid lock, verified frame).
func (m *Module) reconcileTerminalOwners() {
	ctx, cancel := context.WithTimeout(context.Background(), manualResumeTimeout)
	workers, err := m.scanLiveWorkers(ctx, func(store.Execution) bool { return true })
	cancel()
	if err != nil {
		m.logf("nex: owner reconcile: worker scan: %v (re-checking %d found)", err, len(workers))
	}
	seen := map[string]bool{}
	for _, w := range workers {
		for _, sid := range []string{w.SessionID, w.ResumeSessionID} {
			if sid == "" || seen[sid] {
				continue
			}
			seen[sid] = true
			m.onSessionStart(agent.SessionStartEvent{AgentType: "cc", SessionID: sid, Source: "resume"})
		}
	}
}

// recheckSession asks for one Q1 re-check of S. A transfer that aborted at
// its second owner check calls it from a defer registered before its
// sid-lock unlock, so the re-check starts after the release.
func (m *Module) recheckSession(sid string) {
	go m.onSessionStart(agent.SessionStartEvent{AgentType: "cc", SessionID: sid, Source: "resume"})
}

func (m *Module) broadcastWorkerExited(execID string, ev agent.SessionStartEvent) {
	if m.core == nil || m.core.Events == nil {
		return
	}
	value, _ := json.Marshal(map[string]string{
		"execution_id": execID, "session_id": ev.SessionID, "reason": "manual_resume", "tmux_session": ev.TmuxSession,
	})
	m.core.Events.Broadcast(ev.TmuxSession, "nex-worker-exited", string(value))
}
