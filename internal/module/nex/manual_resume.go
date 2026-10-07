package nex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/wake/purdex/internal/module/agent"
	"lab.protype.tw/wake/nexen/store"
)

// Manual resume exits the worker (conversation entity Q1, D3): a terminal
// that resumes a session a live worker holds takes the conversation over,
// and the worker exits — unless the resume is one of Purdex's own
// transfers, which hold the session's sid lock for their whole run.
//
// The handler and the transfers all use TryLock, so neither side ever
// blocks (no deadlock), and the handler skips the current event when
// sid:<S> or a worker's exec:<id> is held (D2). A skip, a transient failure
// (the terminal lookup, a candidate's re-read, or a worker-scan page erred;
// not the scan's page cap), or an exit that failed for any reason but a
// non-pdx holder's refusal (held_by) schedules a re-check of S: one pending
// per session, after manualResumeRetryDelay, at most manualResumeMaxRetries
// in a row (a real SessionStart for S resets the count). The re-check is the same
// per-session pass with every guard — it acts only while a verified
// terminal runs S, which is what makes a retry after a failed Purdex
// transfer safe.

// manualResumeTimeout bounds only the terminal lookup (LiveBySessionID).
// scanLiveWorkers and exitWorker run under detachedContext, which strips this
// deadline (and Stop's cancel); each of those operations carries its own
// timeout.
const manualResumeTimeout = 90 * time.Second

// q1StopWait caps how long Stop waits for the Q1 work in flight.
const q1StopWait = 3 * time.Second

// A skipped or failed pass re-checks S later: after manualResumeRetryDelay,
// at most manualResumeMaxRetries times in a row.
const (
	manualResumeRetryDelay = 3 * time.Second
	manualResumeMaxRetries = 20
)

// q1Retry is one session's re-check slot.
type q1Retry struct {
	timer    *time.Timer // the pending re-check; nil when none
	attempts int         // consecutive retries armed
	gaveUp   bool        // the give-up line was logged
}

// internalPrincipal is the bare host principal the daemon acts as.
func (m *Module) internalPrincipal() string { return "pdx:" + m.opts.Config.HostID }

// startManualResume creates the Q1 context and subscribes to the hub (Start).
func (m *Module) startManualResume() {
	m.q1Mu.Lock()
	if m.startsStopped.Load() || m.q1Ctx != nil {
		m.q1Mu.Unlock()
		return
	}
	m.q1Ctx, m.q1Cancel = context.WithCancel(context.Background())
	m.q1Mu.Unlock()
	unsubscribe := m.terminals.SubscribeSessionStart(m.onSessionStart)
	m.q1Mu.Lock()
	stopped := m.startsStopped.Load()
	if !stopped {
		m.unsubscribeStarts = unsubscribe
	}
	m.q1Mu.Unlock()
	if stopped {
		unsubscribe()
	}
}

// stopManualResume is Stop's first step: mark stopped (no new Q1 work
// starts), unsubscribe, cancel the Q1 context (an in-flight terminal lookup
// aborts; every handler bails at its next step), then wait for the work in
// flight. The wait is bounded by ctx and by q1StopWait, whichever comes
// first: an exit already in progress is not interrupted (aborting a
// terminate halfway would leave the row inconsistent) and runs to its own
// timeout, and shutdown or a restart must not block on that terminate.
func (m *Module) stopManualResume(ctx context.Context) {
	m.q1Mu.Lock()
	m.startsStopped.Store(true)
	unsubscribe, cancel := m.unsubscribeStarts, m.q1Cancel
	m.unsubscribeStarts = nil
	m.q1Mu.Unlock()
	if unsubscribe != nil {
		unsubscribe()
	}
	if cancel != nil {
		cancel()
	}
	m.q1Mu.Lock()
	for _, st := range m.q1Retries {
		if st.timer != nil {
			st.timer.Stop() // one that already fired finds startsStopped set and does nothing
			st.timer = nil
		}
	}
	m.q1Retries = nil
	m.q1Mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.q1Work.Wait()
		close(done)
	}()
	limit := m.q1StopCap
	if limit <= 0 {
		limit = q1StopWait
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
		if m.q1Running.Load() > 0 {
			m.logf("nex: stop: manual-resume work still running (%v); not waiting for it", ctx.Err())
		}
	case <-timer.C:
		if m.q1Running.Load() > 0 {
			m.logf("nex: stop: manual-resume work still running after %v; not waiting for it", limit)
		}
	}
}

// q1End ends one unit of Q1 work begun by q1Begin.
func (m *Module) q1End() {
	m.q1Running.Add(-1)
	m.q1Work.Done()
}

// q1Begin counts one unit of Q1 work and returns the context it runs under
// (cancelled by Stop; Background when the module was never started, as in
// tests that drive the handler directly). ok is false once Stop began. The
// caller ends the unit with m.q1Work.Done().
func (m *Module) q1Begin() (context.Context, bool) {
	m.q1Mu.Lock()
	defer m.q1Mu.Unlock()
	if m.startsStopped.Load() {
		return nil, false
	}
	m.q1Work.Add(1)
	m.q1Running.Add(1)
	if m.q1Ctx == nil {
		return context.Background(), true
	}
	return m.q1Ctx, true
}

// q1Halted: Stop began. In-flight Q1 work checks it between steps and bails.
func (m *Module) q1Halted(ctx context.Context) bool {
	return m.startsStopped.Load() || ctx.Err() != nil
}

// onSessionStart handles one SessionStart from the agent hub. The hub runs
// subscribers off the hook path, so this is synchronous. A real SessionStart
// for S (not a scheduled retry, not a recheckSession) resets S's retry count
// first: a fresh manual resume after the cap is handled normally.
func (m *Module) onSessionStart(ev agent.SessionStartEvent) {
	ctx, ok := m.q1Begin()
	if !ok {
		return
	}
	defer m.q1End()
	if !ev.Overflow && isResumeStart(ev) {
		m.resetRecheckCount(ev.SessionID)
	}
	m.handleSessionStart(ctx, ev)
}

// isResumeStart: a SessionStart Q1 acts on — Claude Code starting or
// resuming a session (clear and compact keep the same process).
func isResumeStart(ev agent.SessionStartEvent) bool {
	return ev.AgentType == "cc" && ev.SessionID != "" && (ev.Source == "startup" || ev.Source == "resume")
}

// handleSessionStart is onSessionStart's body, inside one unit of Q1 work.
func (m *Module) handleSessionStart(ctx context.Context, ev agent.SessionStartEvent) {
	if m.sys.service == nil || m.sys.store == nil || m.opts.Config == nil || m.terminals == nil {
		return
	}
	if ev.Overflow {
		m.reconcileTerminalOwners(ctx)
		return
	}
	if !isResumeStart(ev) {
		return
	}
	sid := ev.SessionID
	_, retry := m.resolveManualResume(ctx, sid, ev.TmuxSession, func(ctx context.Context) ([]store.Execution, string) {
		workers, err := m.liveWorkersFor(ctx, sid)
		if err == nil {
			return workers, ""
		}
		// Unlike an owner check (which refuses a transfer on a partial scan),
		// the terminal has ALREADY taken S over here: exiting the workers
		// found is strictly better than exiting none. A failed page is
		// transient and re-checks S; the page cap is not (the table stays
		// that large), so a truncated scan does not.
		m.logf("nex: manual resume %s: worker scan: %v (acting on %d found)", sid, err, len(workers))
		if errors.Is(err, errOwnerScanTruncated) {
			return workers, ""
		}
		return workers, "the worker scan failed"
	})
	m.settleRecheck(sid, retry)
}

// resolveManualResume is Q1's per-session path, shared by a SessionStart
// and the overflow reconcile. Under sid:<S> (TryLock: a Purdex transfer of
// S holds it for its whole run), and only while a verified terminal frame
// runs S, it exits each of S's live workers that workers returns — called
// inside the lock, after the terminal check, with a retry reason when its
// result may be incomplete. Each candidate is re-read
// under its exec:<id> lock and skipped unless it is still S's live worker
// (the reconcile's rows can be stale by the time a later group runs).
// It returns the ids it exited, and why S needs a re-check ("" when nothing
// is left to retry).
func (m *Module) resolveManualResume(parent context.Context, sid, tmuxSession string, workers func(context.Context) ([]store.Execution, string)) (exited []string, retry string) {
	key := sidLockKey(sid)
	if !m.locks.TryLock(key) {
		return nil, "the session is busy" // a Purdex transfer of S is in flight, or another handler has S
	}
	defer m.locks.Unlock(key)

	// Act only on a terminal that is verifiably running S right now: a late
	// hook from a session a failed transfer already killed must not exit the
	// worker that transfer left alone.
	ctx, cancel := context.WithTimeout(parent, manualResumeTimeout)
	terms, err := m.terminals.LiveBySessionID(ctx, "cc", sid)
	cancel()
	if m.q1Halted(parent) {
		return nil, ""
	}
	if err != nil {
		m.logf("nex: manual resume %s: terminal lookup: %v", sid, err)
		return nil, "the terminal lookup failed"
	}
	verified := false
	for _, t := range terms {
		verified = verified || t.Verified
	}
	if !verified {
		return nil, ""
	}
	principal := m.internalPrincipal()
	candidates, retry := workers(parent)
	for _, w := range candidates {
		if m.q1Halted(parent) {
			break // after the scan, and between candidates
		}
		ok, why := m.exitManualResumeWorker(parent, sid, tmuxSession, w.ID, principal)
		if ok {
			exited = append(exited, w.ID)
		} else if why != "" {
			retry = why
		}
	}
	return exited, retry
}

// exitManualResumeWorker exits one candidate under its exec:<id> lock, after
// re-reading it. ok when it exited; otherwise retry says why S should be
// re-checked ("" when a re-check cannot help).
func (m *Module) exitManualResumeWorker(parent context.Context, sid, tmuxSession, execID, principal string) (ok bool, retry string) {
	lk := takeToTerminalLockKey(execID)
	if !m.locks.TryLock(lk) {
		m.logf("nex: manual resume %s: %s is busy", sid, execID)
		return false, execID + " is busy"
	}
	defer m.locks.Unlock(lk)
	w, err := m.getExecution(parent, execID)
	if errors.Is(err, store.ErrNotFound) {
		return false, "" // gone since the scan
	}
	if err != nil {
		m.logf("nex: manual resume %s: re-reading %s: %v", sid, execID, err)
		return false, "re-reading " + execID + " failed"
	}
	if !isLiveExecution(w) || !executionIsFor(w, sid) {
		return false, "" // exited or moved since the scan
	}
	if m.q1Halted(parent) {
		return false, "" // Stop began: no new exit starts
	}
	// Like every ending path, the exit preempts a pdx tab's lease (D22,
	// ruling R-PC-1), so that tab can no longer answer a permission request.
	out, herr := m.exitWorker(parent, w, nil, principal)
	if herr != nil || !out.Exited() {
		m.logf("nex: manual resume %s: exiting %s failed: %v", sid, execID, herr)
		if herr != nil && herr.code == "held_by" {
			return false, "" // D4: a non-pdx holder keeps refusing; retrying cannot help
		}
		return false, "exiting " + execID + " failed"
	}
	m.logf("nex: manual resume %s in %s -> worker %s exited", sid, tmuxSession, execID)
	m.broadcastWorkerExited(execID, sid, tmuxSession)
	return true, ""
}

// reconcileTerminalOwners is the hub's overflow path: SessionStarts were
// coalesced away while this subscriber lagged, so every session that still
// has a live worker is re-checked as if it had just resumed. One scan of
// the table; its live rows are grouped by session_id and by
// resume_session_id (a row can sit in two groups), and each group goes
// through the per-session path with every guard. A worker exited under one
// key is not offered again under the other.
func (m *Module) reconcileTerminalOwners(ctx context.Context) {
	workers, err := m.scanLiveWorkers(ctx, store.ListOptions{}, func(store.Execution) bool { return true })
	if m.q1Halted(ctx) {
		return
	}
	if err != nil {
		m.logf("nex: owner reconcile: worker scan: %v (re-checking %d found)", err, len(workers))
	}
	groups := map[string][]store.Execution{}
	var order []string
	for _, w := range workers {
		for i, sid := range []string{w.SessionID, w.ResumeSessionID} {
			if sid == "" || (i == 1 && strings.EqualFold(sid, w.SessionID)) {
				continue
			}
			key := store.NormalizeResumeSessionID(sid)
			if _, ok := groups[key]; !ok {
				order = append(order, key)
			}
			groups[key] = append(groups[key], w)
		}
	}
	done := map[string]bool{}
	for _, key := range order {
		if m.q1Halted(ctx) {
			return
		}
		sid, group := key, groups[key]
		// The tmux session name is unknown on this path (agent.TerminalSession
		// has no such field), so nex-worker-exited carries tmux_session "";
		// the SPA toast (Task 18) must fall back when it is empty.
		exited, retry := m.resolveManualResume(ctx, sid, "", func(context.Context) ([]store.Execution, string) {
			var left []store.Execution
			for _, w := range group {
				if !done[w.ID] {
					left = append(left, w)
				}
			}
			return left, ""
		})
		for _, id := range exited {
			done[id] = true
		}
		m.settleRecheck(sid, retry)
	}
}

// recheckSession asks for one Q1 re-check of S, at once. A transfer that
// aborted at its second owner check calls it from a defer registered before
// its sid-lock unlock, so the re-check starts after the release. It uses
// S's re-check slot (armRecheck), so it folds into a retry already pending.
func (m *Module) recheckSession(sid string) {
	if m.startsStopped.Load() {
		return
	}
	if m.recheck != nil {
		m.recheck(sid)
		return
	}
	m.armRecheck(sid, "")
}

// settleRecheck ends a pass for S: a retry reason arms S's re-check; a
// pass with nothing to retry resets S's attempt count.
func (m *Module) settleRecheck(sid, retry string) {
	if retry != "" {
		m.armRecheck(sid, retry)
		return
	}
	m.resetRecheckCount(sid)
}

// resetRecheckCount clears S's attempt count (and give-up mark), keeping a
// pending re-check.
func (m *Module) resetRecheckCount(sid string) {
	m.q1Mu.Lock()
	defer m.q1Mu.Unlock()
	if st := m.q1Retries[sid]; st != nil {
		st.attempts, st.gaveUp = 0, false
		if st.timer == nil {
			delete(m.q1Retries, sid)
		}
	}
}

// armRecheck fills S's one re-check slot. With a reason (a skip or a failed
// exit) the re-check waits the retry delay and counts against
// manualResumeMaxRetries; with none (recheckSession) it runs at once and is
// not counted. A request that finds the slot taken folds into the pending
// re-check — an immediate one pulls a delayed one forward.
func (m *Module) armRecheck(sid, reason string) {
	m.q1Mu.Lock()
	defer m.q1Mu.Unlock()
	if m.startsStopped.Load() {
		return
	}
	if m.q1Retries == nil {
		m.q1Retries = map[string]*q1Retry{}
	}
	st := m.q1Retries[sid]
	if st == nil {
		st = &q1Retry{}
		m.q1Retries[sid] = st
	}
	if st.timer != nil {
		if reason == "" && st.timer.Stop() {
			st.timer = time.AfterFunc(0, func() { m.runRecheck(sid, st) })
		}
		return // the pending re-check (or one already firing) covers this request
	}
	var delay time.Duration
	if reason != "" {
		if st.attempts >= manualResumeMaxRetries {
			if !st.gaveUp {
				st.gaveUp = true
				m.logf("nex: manual resume %s: %s after %d re-checks; giving up until a pass for S completes", sid, reason, st.attempts)
			}
			return
		}
		st.attempts++
		delay = m.retryDelay
		if delay <= 0 {
			delay = manualResumeRetryDelay
		}
		m.logf("nex: manual resume %s: %s; re-checking in %v (%d/%d)", sid, reason, delay, st.attempts, manualResumeMaxRetries)
	}
	st.timer = time.AfterFunc(delay, func() { m.runRecheck(sid, st) })
}

// runRecheck is a fired re-check: one unit of Q1 work (Stop cancels and
// waits for it like a hub callback) through the per-session path. Its
// outcome settles S's slot again (settleRecheck).
func (m *Module) runRecheck(sid string, st *q1Retry) {
	m.q1Mu.Lock()
	st.timer = nil
	m.q1Mu.Unlock()
	ctx, ok := m.q1Begin()
	if !ok {
		return // Stop began
	}
	defer m.q1End()
	defer func() {
		if r := recover(); r != nil {
			m.logf("nex: manual resume re-check %s: panic: %v", sid, r)
		}
	}()
	// Same as the overflow path: the tmux session name is unknown here, so
	// tmux_session is "" in the broadcast (the SPA toast must fall back).
	m.handleSessionStart(ctx, agent.SessionStartEvent{AgentType: "cc", SessionID: sid, Source: "resume"})
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
