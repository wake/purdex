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

// The notice outbox (adopt plan PL-1d1, decision 7): a membership change writes the notice the session
// is owed into team.db in its own transaction (PL-1b); this drain sends it from the lead's inbox, at
// least once. A send that fails leaves the notice owed (the next kick or liveness tick sends it again);
// one older than NoticeGiveUpS is given up with a log line. Nothing here holds createMu or a store
// lock across a send: one goroutine drains, so two kicks never send one row twice at the same time.

const (
	noticeSendTimeout = 5 * time.Second
	noticeLogEvery    = time.Minute
)

// kickNotices wakes the drain; it never blocks (one slot: a kick while one is pending is that kick).
func (m *Module) kickNotices() {
	select {
	case m.noticeSig <- struct{}{}:
	default:
	}
}

// runNotices is the drain goroutine: one pass per kick, plus one at start for the notices a restart
// left owed.
func (m *Module) runNotices() {
	defer m.sweepWG.Done()
	m.drainNotices()
	for {
		select {
		case <-m.stopCtx.Done():
			return
		case <-m.noticeSig:
			m.drainNotices()
		}
	}
}

// noticeText is the text of a notice: the same on every try (idempotent content).
func noticeText(kind, leadAddress, teamID string) string {
	if kind == team.NoticeReleased {
		return fmt.Sprintf(team.ReleaseNoticeFmt, leadAddress, teamID)
	}
	return fmt.Sprintf(team.AdoptNoticeFmt, leadAddress, teamID, leadAddress)
}

// drainNotices makes one pass over the owed notices, oldest first.
func (m *Module) drainNotices() {
	if m.sender == nil || m.stopping() {
		return
	}
	if n, err := m.store.DropStaleAdoptNotices(); err != nil {
		m.logf("[team] notices: %v", err)
	} else if n > 0 {
		m.logf("[team] dropped %d adopt notice(s) of members that left before they were told", n)
	}
	rows, err := m.store.PendingNotices()
	if err != nil {
		m.logf("[team] notices: %v", err)
		return
	}
	for _, r := range rows {
		if m.stopping() {
			return
		}
		m.sendNotice(r)
	}
}

// sendNotice sends row r's owed notice, or gives it up, or leaves it owed.
func (m *Module) sendNotice(r memberRow) {
	now := m.now()
	if now-r.NoticeSince >= int64(team.NoticeGiveUpS)*1000 {
		if _, err := m.store.ClearNotice(r.SpawnOp, r.NoticePending, r.NoticeSince); err != nil {
			m.logf("[team] notice %s to %s: give up: %v", r.NoticePending, r.Ref, err)
			return
		}
		m.logf("[team] notice %s to %s given up after 10 min", r.NoticePending, r.Ref)
		delete(m.noticeLogAt, r.SpawnOp)
		return
	}
	fail := func(why string, err error) {
		if last, ok := m.noticeLogAt[r.SpawnOp]; ok && now-last < noticeLogEvery.Milliseconds() {
			return
		}
		if m.noticeLogAt == nil {
			m.noticeLogAt = map[string]int64{}
		}
		m.noticeLogAt[r.SpawnOp] = now
		m.logf("[team] notice %s to %s kept for retry: %s: %v", r.NoticePending, r.Ref, why, err)
	}
	t, found, err := m.store.TeamByID(r.TeamID)
	if err != nil {
		fail("read team", err)
		return
	}
	if !found {
		fail("read team", errors.New("no such team"))
		return
	}
	inbox, ok, err := m.origins.InboxOf(t.LeadSessionID)
	if err != nil || !ok {
		if err == nil {
			err = errors.New("the lead has no live inbox")
		}
		fail("lead inbox", err)
		return
	}
	alias, _ := m.selfHost()
	leadAddress := alias + "/" + t.LeadRef
	if o, ok, err := m.origins.ResolveOriginBySession(t.LeadSessionID); err == nil && ok && o.Address != "" {
		leadAddress = o.Address
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, noticeSendTimeout)
	defer cancel()
	resp, err := m.sender.Send(ctx, ipeers.SendRequest{To: alias + "/" + r.Ref, Text: noticeText(r.NoticePending, leadAddress, t.ID), OriginInbox: inbox})
	var se *peersmod.SendError
	if err == nil && resp.Result == ipeers.ResultDeliveryUncertain {
		// The frame was written and only the wait for the receiver timed out: the peer-bridge protocol says the
		// caller does not resend (peer-bridge spec, "Timeout after write"), and the notice may well have been read.
		m.logf("[team] notice %s to %s: delivery uncertain; not resent", r.NoticePending, r.Ref)
	}
	if err != nil {
		if errors.As(err, &se) {
			fail("send refused", se)
		} else {
			fail("send", err)
		}
		return
	}
	if _, err := m.store.ClearNotice(r.SpawnOp, r.NoticePending, r.NoticeSince); err != nil {
		fail("clear", err)
	}
	delete(m.noticeLogAt, r.SpawnOp)
}
