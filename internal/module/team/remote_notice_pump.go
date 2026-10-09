// internal/module/team/remote_notice_pump.go
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

// The remote-member notice pump (cross-host team spec §4.4, X3d-2). A command from a lead host, or the operator here,
// writes the notice a remote member is owed into remote_notices in its own transaction; this pump delivers it to the
// member's session through the peers module's DeliverTeamNotice seam, as the remote lead.
//
//   - the text is M's own fixed template, filled when it is sent from the member's row as it is then (a rename of the
//     team, a lead_moved) — never text the lead host sent, never text frozen when the notice was owed;
//   - at least once: the seam does not dedup, so idempotence is here — a delivered or delivery_uncertain result settles
//     the row (sent) and it is never sent again;
//   - a retryable refusal backs off like the outbox (30 s doubling to 10 min) and is retried forever — a release that
//     lands while the member's session is offline still tells it when it is back;
//   - a permanent refusal, an unbound lead host, or a member row that has left the state the notice is about
//     (superseded by what happened since) ends the row as superseded, with the reason in the log.

const remoteNoticeSendTimeout = 5 * time.Second

// Notice row states besides noticeOwed.
const (
	noticeSent       = "sent"
	noticeSuperseded = "superseded"
)

// remoteNoticeState is the member state a notice of this kind is about.
func remoteNoticeState(kind string) string {
	switch kind {
	case noticeAdopted, noticeHandover:
		return remoteActive
	case noticeReleased:
		return remoteReleased
	case noticeTeamEnded, noticeLocalEnd:
		return remoteEnded
	}
	return ""
}

// remoteNoticeText is the notice's text: the kind's template, the member's current team name and lead address (the
// notice row's snapshot when the member row has none).
func remoteNoticeText(n remoteNoticeRow, m remoteMemberRow) (string, bool) {
	lead := cleanNoticeField(m.LeadAddress)
	if lead == "" {
		lead = n.LeadAddress
	}
	if lead == "" {
		lead = cleanNoticeField(m.LeadRef)
	}
	name := cleanNoticeField(m.TeamName)
	if name == "" {
		name = n.TeamName
	}
	if name == "" {
		name = cleanNoticeField(m.TeamID)
	}
	switch n.Kind {
	case noticeAdopted:
		return fmt.Sprintf(team.AdoptNoticeFmt, lead, name, lead), true
	case noticeReleased:
		return fmt.Sprintf(team.ReleaseNoticeFmt, lead, name), true
	case noticeHandover:
		return fmt.Sprintf(team.HandoverNoticeFmt, lead, name, lead), true
	case noticeTeamEnded:
		return fmt.Sprintf(team.TeamEndedNoticeFmt, lead, name), true
	case noticeLocalEnd:
		return fmt.Sprintf(team.LocalEndNoticeFmt, name, lead), true
	}
	return "", false
}

// kickRemoteNotices wakes the pump; it never blocks.
func (m *Module) kickRemoteNotices() {
	select {
	case m.remoteNoticeSig <- struct{}{}:
	default:
	}
}

// startRemoteNoticePump starts the pump when the peers seam is there (Init).
func (m *Module) startRemoteNoticePump() {
	if m.teamNotices == nil {
		return
	}
	m.sweepWG.Add(1)
	go m.runRemoteNotices()
}

func (m *Module) runRemoteNotices() {
	defer m.sweepWG.Done()
	ticker := time.NewTicker(pumpTick)
	defer ticker.Stop()
	for {
		m.drainRemoteNotices() // first: a notice owed across a restart goes out at boot
		select {
		case <-m.stopCtx.Done():
			return
		case <-m.remoteNoticeSig:
		case <-ticker.C:
		}
	}
}

// drainRemoteNotices makes one pass over the notices that are owed and due, oldest first.
func (m *Module) drainRemoteNotices() {
	if m.teamNotices == nil || m.stopping() {
		return
	}
	rows, err := m.store.DueRemoteNotices(m.now())
	if err != nil {
		m.logf("[team] remote notices: %v", err)
		return
	}
	for _, n := range rows {
		if m.stopping() {
			return
		}
		m.sendRemoteNotice(n)
	}
}

func (m *Module) sendRemoteNotice(n remoteNoticeRow) {
	supersede := func(why string) {
		if _, err := m.store.SettleRemoteNotice(n.ID, noticeSuperseded, m.now()); err != nil {
			m.logf("[team] remote notice %d (%s to %s): %v", n.ID, n.Kind, n.MK, err)
			return
		}
		m.logf("[team] remote notice %d (%s to %s) superseded: %s", n.ID, n.Kind, n.MK, why)
	}
	retry := func(why string, err error) {
		next := m.now() + pumpBackoff(n.Attempts+1).Milliseconds()
		if e := m.store.RetryRemoteNotice(n.ID, n.Attempts+1, next, m.now()); e != nil {
			m.logf("[team] remote notice %d (%s to %s): %v", n.ID, n.Kind, n.MK, e)
			return
		}
		if n.Attempts == 0 || n.Attempts%8 == 0 { // the first failure and then now and then, not every backoff
			m.logf("[team] remote notice %d (%s to %s) kept for retry: %s: %v", n.ID, n.Kind, n.MK, why, err)
		}
	}
	member, found, err := m.store.RemoteMember(n.MK)
	if err != nil {
		retry("read member", err)
		return
	}
	want := remoteNoticeState(n.Kind)
	switch {
	case !found:
		supersede("the member is gone")
		return
	case want == "" || member.State != want:
		supersede(fmt.Sprintf("the member is %s, the notice is about %s", member.State, want))
		return
	}
	text, _ := remoteNoticeText(n, member)
	ctx, cancel := context.WithTimeout(m.stopCtx, remoteNoticeSendTimeout)
	defer cancel()
	res, err := m.teamNotices.DeliverTeamNotice(ctx, peersmod.TeamNotice{
		MsgID:      m.newID(),
		LeadHostID: member.LeadHostID,
		Lead: peersmod.TeamNoticeLead{SessionID: member.LeadSessionID, Ref: member.LeadRef, Address: member.LeadAddress,
			PID: member.LeadPID, ProcStart: member.LeadProcStart},
		Target: ipeers.WireTo{AgentSessionID: member.MemberSessionID, PID: member.PID, ProcStart: member.ProcStart},
		Text:   text,
	})
	var ne *peersmod.NoticeError
	switch {
	case err == nil:
		if res == ipeers.ResultDeliveryUncertain {
			m.logf("[team] remote notice %d (%s to %s): delivery uncertain; not resent", n.ID, n.Kind, n.MK)
		}
		if _, err := m.store.SettleRemoteNotice(n.ID, noticeSent, m.now()); err != nil {
			m.logf("[team] remote notice %d (%s to %s): settle: %v", n.ID, n.Kind, n.MK, err)
		}
	case errors.Is(err, peersmod.ErrNoticeNotBound):
		supersede("the lead host is not a bound peer")
	case errors.As(err, &ne) && !ne.Retryable():
		supersede(ne.Error())
	case errors.As(err, &ne):
		retry("refused", ne)
	default:
		retry("send", err)
	}
}
