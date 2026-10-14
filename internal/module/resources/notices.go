package resourcesmod

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/resources"
)

// The notice outbox (lease v2 amendment A, plan §2.10): one persisted slot per position, sent by a goroutine of its own,
// never under stateMu. The slot is armed by the transition that makes it (the offer, the idle window that named rows,
// the end that has something unanswered); this file only sends what is armed, as long as it is still true.

const (
	noticeTick       = 2 * time.Second  // the outbox looks at the slots at least this often
	noticeRetryBusy  = 2 * time.Second  // busy / timeout / unknown: the session was mid-turn, or the job was not taken
	noticeRetryOther = 5 * time.Second  // a refused queue, a drop that may pass, no mod
	noticeSubmitMax  = 25 * time.Second // one try: the queue waits 10 s for the job to be taken and 10 s for its result
	noticeCancelAge  = 10 * time.Minute // a cancelled / drained notice is still worth sending this long after the end
)

// noticeSender is the prompt queue's notice call (promptq.Queue).
type noticeSender interface {
	SubmitNotice(ctx context.Context, sessionID, key, text string) (promptq.Result, error)
}

// noticeQueue is the queue notices go through: the test seam, else the prompt queue of the mod event channel, looked up
// at use time (it may start after this module).
func (m *Module) noticeQueue() noticeSender {
	if m.noticeQ != nil {
		return m.noticeQ
	}
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(promptq.Key)
	if !ok {
		return nil
	}
	q, ok := svc.(noticeSender)
	if !ok {
		return nil
	}
	return q
}

// noticeState is the outbox's memory: which slots have a try out, and when a slot may be tried again. It is lost on a
// restart, which only means a retry comes at once.
type noticeState struct {
	mu       sync.Mutex
	inflight map[string]bool
	retryAt  map[string]time.Time
}

func slotKey(p positionRow) string { return fmt.Sprintf("%s|%s|%d", p.ID, p.NoticeKind, p.NoticeDueAt) }

// kickNotices wakes the outbox: a transition may have armed a slot.
func (m *Module) kickNotices() {
	select {
	case m.noticeWake <- struct{}{}:
	default:
	}
}

// runNotices is the outbox goroutine: a pass per tick or per kick.
func (m *Module) runNotices(ctx context.Context) {
	defer m.wg.Done()
	t := time.NewTicker(noticeTick)
	defer t.Stop()
	for {
		m.noticePass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.noticeWake:
		}
	}
}

// noticePass starts a try for every pending slot that has none out and whose retry time has come. A try runs on its own
// goroutine (a session's mod may take seconds; another session's slot must not wait for it).
func (m *Module) noticePass(ctx context.Context) {
	if m.store == nil || m.noticeQueue() == nil {
		return
	}
	pending, err := m.store.PendingNoticePositions()
	if err != nil {
		m.logf("[resources] notices: list pending: %v", err)
		return
	}
	now := m.now()
	m.notices.mu.Lock()
	if m.notices.inflight == nil {
		m.notices.inflight, m.notices.retryAt = map[string]bool{}, map[string]time.Time{}
	}
	live := make(map[string]bool, len(pending))
	var run []positionRow
	for _, p := range pending {
		k := slotKey(p)
		live[k] = true
		if m.notices.inflight[k] || now.Before(m.notices.retryAt[k]) {
			continue
		}
		m.notices.inflight[k] = true
		run = append(run, p)
	}
	for k := range m.notices.retryAt { // a slot that is gone needs no retry time
		if !live[k] {
			delete(m.notices.retryAt, k)
		}
	}
	m.notices.mu.Unlock()
	for _, p := range run {
		m.wg.Add(1)
		go func(p positionRow) {
			defer m.wg.Done()
			retry := m.deliverNotice(ctx, p)
			m.notices.mu.Lock()
			delete(m.notices.inflight, slotKey(p))
			if retry > 0 {
				m.notices.retryAt[slotKey(p)] = m.now().Add(retry)
			}
			m.notices.mu.Unlock()
			if retry > 0 {
				return
			}
			m.kickNotices() // the slot may have changed under the try: look again
		}(p)
	}
}

// noticeText is what the slot says now, from the requests that are still named and waiting; "" when there is nothing
// true left to say (the slot is stale). An error is a failed read, which says nothing about the slot: it is tried again.
func (m *Module) noticeText(p positionRow, nowMS int64) (string, error) {
	switch p.NoticeKind {
	case "offer":
		if p.State != resources.PosOffered || p.OfferedAt != p.NoticeDueAt || p.DeliveredAt > 0 {
			return "", nil
		}
	case "idle":
		if p.State != resources.PosIdle || p.WindowUntil <= nowMS {
			return "", nil
		}
	case "cancelled", "drained":
		if p.State != resources.PosEnded || nowMS-p.EndedAt > noticeCancelAge.Milliseconds() {
			return "", nil
		}
		if p.NoticeKind == "drained" {
			return resources.DrainedNotice, nil
		}
		return resources.CancelledNotice, nil
	default:
		return "", nil
	}
	rows, err := m.store.RowsOf(p.ID)
	if err != nil {
		return "", err
	}
	var named []resources.NoticeRow
	for _, r := range rows {
		if r.State == resources.StateWaiting && r.OfferedAt > 0 {
			named = append(named, resources.NoticeRow{ID: r.ID, Mode: callMode(r.Mode), Command: r.Command, Cwd: r.Cwd, CreatedAt: time.UnixMilli(r.CreatedAt)})
		}
	}
	return resources.OfferNotice(p.ID, named, time.Local), nil
}

// deliverNotice is one try of one slot. It returns how long to wait before the next try of the same slot: 0 when the
// slot is settled (sent, stale, or ended by a result), or when it was changed under the try.
func (m *Module) deliverNotice(ctx context.Context, p positionRow) time.Duration {
	nowMS := m.now().UnixMilli()
	text, err := m.noticeText(p, nowMS)
	if err != nil {
		m.logf("[resources] notices: read %s: %v", p.ID, err)
		return noticeRetryOther
	}
	if text == "" {
		// Not current any more: cleared, never sent (a stale offer would have the session re-run what was passed on).
		if _, err := m.store.SetNoticeResult(p.ID, p.NoticeKind, p.NoticeDueAt, noticeStale, 0); err != nil {
			m.logf("[resources] notices: clear %s: %v", p.ID, err)
			return noticeRetryOther // a failing write is tried again on the timer, not in a tight loop
		}
		return 0
	}
	q := m.noticeQueue()
	if q == nil {
		return noticeRetryOther
	}
	key := fmt.Sprintf("%s:%d:%s:%d", p.ID, p.NoticeDueAt, p.NoticeKind, p.NoticeTries)
	cctx, cancel := context.WithTimeout(ctx, noticeSubmitMax)
	res, err := q.SubmitNotice(cctx, p.SessionID, key, text)
	cancel()
	if ctx.Err() != nil {
		return 0
	}
	if err != nil {
		if errors.Is(err, promptq.ErrBusy) {
			return noticeRetryOther // 16 jobs wait for the session
		}
		m.logf("[resources] notices: send %s: %v", p.ID, err)
		return noticeRetryOther
	}
	return m.noticeOutcome(p, res)
}

// noticeOutcome applies the queue's answer (plan §2.10 "Outcomes").
func (m *Module) noticeOutcome(p positionRow, res promptq.Result) time.Duration {
	nowMS := m.now().UnixMilli()
	// set records a result; false when the write failed (the caller then waits for its next try, never a tight loop).
	set := func(result string, sentAt int64) bool {
		if _, err := m.store.SetNoticeResult(p.ID, p.NoticeKind, p.NoticeDueAt, result, sentAt); err != nil {
			m.logf("[resources] notices: record %s: %v", p.ID, err)
			return false
		}
		return true
	}
	switch res.Status {
	case promptq.Accepted:
		if p.NoticeKind == "offer" {
			// D1: the notice reached the session, the 3-minute window starts now (§2.8). The window and the slot's
			// result are one write: if it fails the slot stays pending and is tried again (the key is the same, so
			// the queue does not send it twice), never an offer the session got with no window recorded.
			m.stateMu.Lock()
			won, err := m.store.MarkOfferAccepted(p.ID, p.OfferedAt, nowMS, promptq.Accepted)
			m.stateMu.Unlock()
			if err != nil {
				m.logf("[resources] notices: mark %s delivered: %v", p.ID, err)
				return noticeRetryOther
			}
			if won {
				m.wake()
				return 0
			}
			// the position moved on while the notice was out: only the result is left to record
		}
		if !set(promptq.Accepted, nowMS) {
			return noticeRetryOther
		}
		return 0
	case promptq.Busy, promptq.Timeout:
		return noticeRetryBusy // never sent: the same key may be sent again
	case promptq.Unknown:
		// Handed out, no result: not a delivery. Try again with a fresh key while the slot is current; a duplicate
		// offer is harmless (the session re-runs once, later copies are stopped by the current check).
		if _, err := m.store.BumpNoticeTries(p.ID, p.NoticeKind, p.NoticeDueAt, promptq.Unknown); err != nil {
			m.logf("[resources] notices: count a try of %s: %v", p.ID, err)
		}
		return noticeRetryBusy
	case promptq.Dropped:
		if res.Reason == "session_changed" {
			if !set(noticeSessionChanged, 0) { // the /clear case: liveness ends the position
				return noticeRetryOther
			}
			return 0
		}
		set("dropped:"+res.Reason, 0)
		return noticeRetryOther
	case promptq.NoMod:
		set(promptq.NoMod, 0) // the fallback (P2d-2) is tried once per slot; the mod channel again every 5 s
		return noticeRetryOther
	}
	m.logf("[resources] notices: unexpected answer %q for %s", res.Status, p.ID)
	return noticeRetryOther
}
