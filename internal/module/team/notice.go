package teammod

import (
	"context"
	"fmt"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The lead-handover notice (spec §8.4 "When a lead relays, the daemon also tells each active member"; plan v3 P6-1′,
// as ruled 2026-10-09): after a `cleared` report was APPLIED and the team's lead is now the new session, every active
// member is told, from the NEW lead's inbox through the in-process sender (PL-1d1) — so a member's reply reaches the real
// lead, not the daemon. Best effort: a failure is logged; nothing retries it (the old ref keeps working through lineage
// either way, so a member that missed the notice loses nothing but the news).

// HandoverNoticeFmt takes the new lead's address and ref.
const HandoverNoticeFmt = "[pdx team] 你的 lead 已換手：%s [%s]（舊 ref 仍可用）"

// handoverNoticeAsync sends the notice from its own goroutine, so the report's answer never waits for N peer sends.
func (m *Module) handoverNoticeAsync(op team.RelayOp) {
	if m.sender == nil || op.State != team.RelayCleared || op.NewSessionID == "" {
		return
	}
	m.goTracked(func() { m.handoverNotice(op) })
}

// goTracked runs fn on a goroutine Stop waits for; false (fn not run) once Stop has begun. The Add happens under
// noticeMu, which Stop passes right after its cancel, so no Add can follow Stop's Wait.
func (m *Module) goTracked(fn func()) bool {
	m.noticeMu.Lock()
	if m.stopping() {
		m.noticeMu.Unlock()
		return false
	}
	m.sweepWG.Add(1)
	m.noticeMu.Unlock()
	go func() {
		defer m.sweepWG.Done()
		fn()
	}()
	return true
}

// handoverNotice does the work: the live team led by op.NewSessionID (the cleared moved the role in its transaction),
// its active members, one message each.
func (m *Module) handoverNotice(op team.RelayOp) {
	t, found, err := m.store.LiveTeamByLead(op.NewSessionID)
	if err != nil || !found {
		if err != nil {
			m.logf("[team] handover notice (op %s): %v", op.ID, err)
		}
		return // the relayed session leads no live team: nothing to announce
	}
	rows, err := m.store.MembersOf(t.ID)
	if err != nil {
		m.logf("[team] handover notice (op %s): %v", op.ID, err)
		return
	}
	inbox, ok, err := m.origins.InboxOf(op.NewSessionID)
	if err != nil || !ok {
		m.logf("[team] handover notice (op %s): the new lead has no live inbox (%v)", op.ID, err)
		return
	}
	alias, _ := m.selfHost()
	newRef := ipeers.RefID(op.NewSessionID)
	address := alias + "/" + newRef
	if o, ok, err := m.origins.ResolveOriginBySession(op.NewSessionID); err == nil && ok && o.Address != "" {
		address = o.Address
	}
	text := fmt.Sprintf(HandoverNoticeFmt, address, newRef)
	for _, mr := range rows {
		if mr.State != team.MemberActive || m.isRemoteRow(mr) || m.stopping() {
			continue // a remote member's notices are written by its own host (cross-host spec §4.4)
		}
		ctx, cancel := context.WithTimeout(m.stopCtx, noticeSendTimeout)
		_, err := m.sender.Send(ctx, ipeers.SendRequest{To: alias + "/" + mr.Ref, Text: text, OriginInbox: inbox})
		cancel()
		if err != nil {
			m.logf("[team] handover notice to %s (op %s): %v", mr.Ref, op.ID, err)
		}
	}
}
