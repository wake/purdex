package resourcesmod

import (
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/promptq"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// The fallback of a notice no Purdex mod channel takes (amendment A §2.10, P2d-2): a member's notice goes to its lead
// through the team module; anything else is a user alert (the WS event resources.alert), once per slot. The mod channel
// is tried again at every try while the slot is current, so a reload or a restart that is over in seconds still ends up
// in the session itself.

// leadNoticer is the team module's seam, looked up at send time, every time (it may start after this module); nil when
// no team module is registered, in which case there is no fallback and the slot keeps trying the mod channel.
func (m *Module) leadNoticer() team.LeadNoticer {
	if m.leadNotice != nil {
		return m.leadNotice
	}
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	svc, ok := m.core.Registry.Get(team.LeadNoticerKey)
	if !ok {
		return nil
	}
	ln, _ := svc.(team.LeadNoticer)
	return ln
}

// noticeFallback is the answer to no_mod. A member's lead that was told counts as delivered (an offer's D1); a lead that
// could not be reached leaves the slot pending; a session that is no member gets a user alert, which is not a delivery
// (an offer then passes by its own window).
func (m *Module) noticeFallback(p positionRow, text string, nowMS int64, set func(string, int64) bool) time.Duration {
	ln := m.leadNoticer()
	if ln == nil {
		set(promptq.NoMod, 0)
		return noticeRetryOther
	}
	if p.NoticeResult == noticeMemberSending {
		// An earlier try sent it to the lead and could not record that (the write failed): it is not sent again.
		return m.noticeDelivered(p, nowMS, noticeMember)
	}
	// The slot is claimed before the lead is asked, and only for the slot that is current: a slot cleared or replaced
	// meanwhile claims nothing and sends nothing. A frame already on its way cannot be called back (the residual: the
	// slot or the membership changing during that one send); a crash between the claim and the record leaves the claim,
	// and the lead is then not sent a second copy.
	claimed, err := m.store.SetNoticeResult(p.ID, p.NoticeKind, p.NoticeDueAt, noticeMemberSending, 0)
	if err != nil {
		m.logf("[resources] notices: claim %s: %v", p.ID, err)
		return noticeRetryOther
	}
	if !claimed {
		return 0 // the slot is not this one any more
	}
	member, delivered := ln.NoticeLeadOf(p.SessionID, resources.LeadNotice(p.SessionID, text))
	switch {
	case member && delivered:
		return m.noticeDelivered(p, nowMS, noticeMember)
	case member:
		set(promptq.NoMod, 0)
		return noticeRetryOther
	}
	// Recorded first, and the event goes only with the write that holds: a failed write is tried again, and a slot that
	// was cleared or replaced while the notice was out is not alerted about.
	changed, err := m.store.SetNoticeResult(p.ID, p.NoticeKind, p.NoticeDueAt, noticeUserAlert, 0)
	if err != nil {
		m.logf("[resources] notices: record %s: %v", p.ID, err)
		return noticeRetryOther
	}
	if changed {
		m.sendAlert(p, text, nowMS)
	}
	return 0
}

// sendAlert broadcasts the resources.alert of rule lease_notice naming the session. There is no persisted alert record
// (that is P3a's); the push is P3b's, behind alert_push.
func (m *Module) sendAlert(p positionRow, text string, nowMS int64) {
	snap := m.current()
	v, err := json.Marshal(resources.Alert{
		ID: "lease_notice:" + p.ID + ":" + p.NoticeKind, Rule: resources.AlertRuleLeaseNotice, OpenedAt: nowMS,
		Host:  &resources.AlertHost{Zone: snap.Host.Zone, Mem: snap.Host.Mem, Pressure: snap.Host.Pressure},
		Lease: &resources.AlertLease{ID: p.ID, SessionID: p.SessionID},
		Text:  resources.LeaseNoticeAlertText(p.SessionID, text),
	})
	if err != nil {
		m.logf("[resources] notices: alert for %s: %v", p.ID, err)
		return
	}
	ev := core.HostEvent{Type: resources.AlertEventType, Value: string(v)}
	if m.alertOut != nil {
		m.alertOut(ev)
		return
	}
	if m.core != nil && m.core.Events != nil {
		m.core.Events.BroadcastEvent(ev)
	}
}
