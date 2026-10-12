// internal/module/team/pause_outbox.go — the pause outbox loop (unattended quota pause spec 2026-10-12 §3.1, §3.3, UP-2a).
package teammod

import (
	"context"
	"errors"
	"fmt"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The pause outbox holds what the daemon owes a paused session and the phones: flow notices, and the push request. Rows are
// written in the transaction that causes them and sent by ONE loop in seq order with head-of-line blocking: a head that fails is
// retried before anything after it, so a session never reads "resumed" before "paused". A row is deleted only after it was sent,
// so a crash in between sends it again (at least once, never lost). A notice goes out through the FlowSender — the one send that
// passes a pause — from the target session's own inbox: the daemon has no inbox of its own and the lead may be paused too.

// pauseOutboxPass bounds the rows one pass reads.
const pauseOutboxPass = 100

// pauseOutboxMaxAttempts is how many failed attempts a row gets before it is dead-lettered (one per pauseOutboxEvery: about five minutes).
const pauseOutboxMaxAttempts = 30

// pauseOutboxEvery is how long the loop waits for a kick before it looks again (a failed head is retried at this pace).
const pauseOutboxEvery = 10 * time.Second

type pauseOutboxRow struct {
	Seq                          int64
	SessionID, Kind, Text, Reask string
	CreatedAt                    int64
	Attempts                     int
}

// PauseOutboxHead lists the first limit rows by seq that are still being tried; a row that failed maxAttempts passes is
// dead-lettered: it stays in the table (visible, recoverable) and is no longer listed here.
func (s *Store) PauseOutboxHead(limit, maxAttempts int) ([]pauseOutboxRow, error) {
	rows, err := s.db.Query(`SELECT seq, session_id, kind, text, reask_id, created_at, attempts FROM pause_outbox WHERE attempts < ? ORDER BY seq LIMIT ?`, maxAttempts, limit)
	if err != nil {
		return nil, fmt.Errorf("pause outbox: %w", err)
	}
	defer rows.Close()
	var out []pauseOutboxRow
	for rows.Next() {
		var r pauseOutboxRow
		if err := rows.Scan(&r.Seq, &r.SessionID, &r.Kind, &r.Text, &r.Reask, &r.CreatedAt, &r.Attempts); err != nil {
			return nil, fmt.Errorf("pause outbox: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeletePauseOutbox removes a row that was sent (or whose target is gone).
func (s *Store) DeletePauseOutbox(seq int64) error {
	if _, err := s.db.Exec(`DELETE FROM pause_outbox WHERE seq = ?`, seq); err != nil {
		return fmt.Errorf("delete pause outbox %d: %w", seq, err)
	}
	return nil
}

// BumpPauseOutbox counts a failed attempt on a row.
func (s *Store) BumpPauseOutbox(seq int64) error {
	if _, err := s.db.Exec(`UPDATE pause_outbox SET attempts = attempts + 1 WHERE seq = ?`, seq); err != nil {
		return fmt.Errorf("bump pause outbox %d: %w", seq, err)
	}
	return nil
}

// kickPauseOutbox wakes the loop after a transaction wrote outbox rows.
func (m *Module) kickPauseOutbox() {
	select {
	case m.pauseKick <- struct{}{}:
	default: // one kick is already pending
	}
}

// runPauseOutbox is the loop: a pass at start (rows owed across a restart), then on every kick and on the timer.
func (m *Module) runPauseOutbox() {
	defer m.sweepWG.Done()
	t := time.NewTicker(pauseOutboxEvery)
	defer t.Stop()
	for {
		m.drainPauseOutbox()
		select {
		case <-m.stopCtx.Done():
			return
		case <-m.pauseKick:
		case <-t.C:
		}
	}
}

// drainPauseOutbox is one pass: the rows in seq order until one cannot be sent now.
func (m *Module) drainPauseOutbox() {
	rows, err := m.store.PauseOutboxHead(pauseOutboxPass, pauseOutboxMaxAttempts)
	if err != nil {
		m.logf("[team] pause outbox: %v", err)
		return
	}
	for _, r := range rows {
		if m.stopping() {
			return
		}
		if m.pauseRetryWaiting(r.Seq) {
			return // a failed row is retried at the timer's pace, not at the pace of kicks
		}
		if done := m.sendPauseRow(r); !done {
			if err := m.store.BumpPauseOutbox(r.Seq); err != nil {
				m.logf("[team] %v", err)
			}
			if r.Attempts+1 >= pauseOutboxMaxAttempts {
				// Dead-lettered: kept in the table and no longer tried, so one row that can never be sent does not hold every
				// session's notices for ever; the rows behind it go on.
				m.logf("[team] pause outbox %d (%s to %s): dead-lettered after %d attempts, kept in pause_outbox", r.Seq, r.Kind, r.SessionID, pauseOutboxMaxAttempts)
				m.pauseRetryForget(r.Seq)
				continue
			}
			m.pauseRetryLater(r.Seq)
			return // head-of-line blocking
		}
		m.pauseRetryForget(r.Seq)
		if r.Kind == team.PauseOutboxReask && r.Reask != "" { // the token's day starts when its reask went out
			if err := m.store.TouchReleaseToken(r.Reask, m.now()); err != nil {
				m.logf("[team] %v", err)
			}
		}
		if m.pauseOutboxCrash != nil && m.pauseOutboxCrash(r.Seq) {
			return // test seam: the process died after the send, before the delete
		}
		if err := m.store.DeletePauseOutbox(r.Seq); err != nil {
			m.logf("[team] %v", err)
			return
		}
	}
}

// sendPauseRow sends one row; done says it is finished (sent, or its target is gone) and may be deleted.
func (m *Module) sendPauseRow(r pauseOutboxRow) (done bool) {
	switch r.Kind {
	case team.PauseOutboxNotice, team.PauseOutboxReask: // a reask is a flow message too: its text is the control line
		return m.sendPauseNotice(r)
	case team.PauseOutboxPush:
		svc, ok := m.core.Registry.Get(team.PauseNotifyKey)
		if !ok {
			// The push module publishes its notifier in Start, after this module's (it depends on this one): at a restart a
			// module that is mounted and ready is only late, so the row stays. One that is absent or soft-failed never will.
			if st, mounted := m.core.ModuleStatus("push"); mounted {
				if ready, _ := st["ready"].(bool); ready {
					return false
				}
			}
			m.logf("[team] pause outbox %d: no push module, the phone push is dropped", r.Seq)
			return true
		}
		n, ok := svc.(team.PauseNotifier)
		if !ok {
			m.logf("[team] pause outbox %d: %T is not a PauseNotifier, the phone push is dropped", r.Seq, svc)
			return true
		}
		tmux := ""
		if o, live, err := m.origins.ResolveOriginBySession(r.SessionID); err == nil && live {
			tmux = tmuxName(o.Tmux)
		}
		return n.NotifyPause(team.PausePush{CollapseID: fmt.Sprintf("pause:%s:%d", r.SessionID, r.CreatedAt), Text: r.Text,
			SessionID: r.SessionID, TmuxSession: tmux, SinceMs: r.CreatedAt})
	default:
		m.logf("[team] pause outbox %d: unknown kind %q, dropped", r.Seq, r.Kind)
		return true
	}
}

func (m *Module) sendPauseNotice(r pauseOutboxRow) bool {
	inbox, ok, err := m.origins.InboxOf(r.SessionID)
	if err != nil {
		m.logf("[team] pause outbox %d: inbox of %s: %v", r.Seq, r.SessionID, err)
		return false // the registry cannot be read: try again
	}
	if !ok {
		return true // the session is gone: nobody to tell
	}
	svc, ok := m.core.Registry.Get(peersmod.FlowSenderKey)
	if !ok {
		return false
	}
	flow, ok := svc.(peersmod.FlowSender)
	if !ok {
		m.logf("[team] pause outbox %d: %T is not a FlowSender", r.Seq, svc)
		return false
	}
	alias, _ := m.selfHost()
	to := alias + "/" + ipeers.RefID(r.SessionID)
	if o, live, err := m.origins.ResolveOriginBySession(r.SessionID); err == nil && live && o.Address != "" {
		to = o.Address
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, noticeSendTimeout)
	defer cancel()
	_, err = flow.SendFlow(ctx, ipeers.SendRequest{To: to, Text: r.Text, OriginInbox: inbox})
	var se *peersmod.SendError
	switch {
	case err == nil:
		return true
	case errors.As(err, &se) && (se.API.Error == ipeers.ErrPeerNotFound || se.API.Error == ipeers.ErrTargetGone):
		return true // it left between the inbox lookup and the send
	default:
		m.logf("[team] pause outbox %d to %s: %v", r.Seq, to, err)
		return false
	}
}

// pauseRetryWaiting says a failed row's retry gap has not passed yet.
func (m *Module) pauseRetryWaiting(seq int64) bool {
	m.pauseRetryMu.Lock()
	defer m.pauseRetryMu.Unlock()
	at, ok := m.pauseRetryAt[seq]
	return ok && time.Now().Before(at)
}

func (m *Module) pauseRetryLater(seq int64) {
	m.pauseRetryMu.Lock()
	defer m.pauseRetryMu.Unlock()
	if m.pauseRetryAt == nil {
		m.pauseRetryAt = map[int64]time.Time{}
	}
	m.pauseRetryAt[seq] = time.Now().Add(m.pauseRetryGap)
}

func (m *Module) pauseRetryForget(seq int64) {
	m.pauseRetryMu.Lock()
	delete(m.pauseRetryAt, seq)
	m.pauseRetryMu.Unlock()
}
