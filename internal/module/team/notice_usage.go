package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The 70% idle notice (plan v3 P7-1; spec §8.5, U9: "the daemon decides nothing"). On the liveness tick, a local member
// whose context use is at or over the threshold while the agent module says it is idle tells its lead ONCE: the lead
// decides whether to relay it (`pdx relay _<ref>`). The member is then disarmed; it is armed again by a reading under the
// threshold or by its relay's `cleared` (moveTeamRoles). A member of a team whose lead is gone gets nothing (the team
// ended); a member with a relay op open gets nothing (the relay is the answer). Members of another host are the X series'
// (X2a): there is no remote_members table yet.

// UsageNoticeFmt takes the member's address, its bare 6-character ref, its title, the integer percentage and the ref again.
const UsageNoticeFmt = "[pdx team] member %s [%s]「%s」已用 %d%%，目前閒置。要接力請執行：pdx relay _%s"

// AgentStatusReader is the agent module's last status of a tmux session, type-asserted like agent.ContextUsageReader.
type AgentStatusReader interface {
	AgentStatus(tmuxSession string) (string, bool)
}

const agentIdle = "idle"

// noticeThreshold is the percentage this notice fires at: PDX_RELAY_THRESHOLD of the daemon's own environment when it is
// an integer 1–100 (the acceptance runs with a low one), else team.RelayThresholdPct. It sets this notice only: the hello
// answer's threshold and the mod's own reading of the variable are not touched.
func noticeThreshold() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("PDX_RELAY_THRESHOLD"))); err == nil && n >= 1 && n <= 100 {
		return n
	}
	return team.RelayThresholdPct
}

// ArmNotice arms the member's row (a reading under the threshold); ok false when nothing changed.
func (s *Store) ArmNotice(spawnOp, sessionID string) (bool, error) {
	res, err := s.db.Exec(`UPDATE team_members SET notice_armed = 1 WHERE spawn_op = ? AND session_id = ? AND state = 'active' AND notice_armed = 0`, spawnOp, sessionID)
	return oneRow(res, err, "arm notice "+spawnOp)
}

// DisarmNotice disarms an armed row and says whether THIS call did: the one that did owes the notice, so two checks
// never both send it. A session with a relay op open is refused in the same statement.
func (s *Store) DisarmNotice(spawnOp, sessionID string) (bool, error) {
	res, err := s.db.Exec(`UPDATE team_members SET notice_armed = 0
		WHERE spawn_op = ? AND session_id = ? AND state = 'active' AND notice_armed = 1
		  AND NOT EXISTS (SELECT 1 FROM relay_ops WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled'))`, spawnOp, sessionID, sessionID)
	return oneRow(res, err, "disarm notice "+spawnOp)
}

// DisarmNoticeForce disarms the row whatever its flag was (a compaction's notice stands in for the 70% one); ok says a row changed.
func (s *Store) DisarmNoticeForce(spawnOp, sessionID string) (bool, error) {
	res, err := s.db.Exec(`UPDATE team_members SET notice_armed = 0 WHERE spawn_op = ? AND session_id = ? AND state = 'active' AND notice_armed = 1`, spawnOp, sessionID)
	return oneRow(res, err, "disarm notice "+spawnOp)
}

// noticeUsage is the check: called on the liveness tick, after the readings are stored.
func (m *Module) noticeUsage() {
	if m.usage == nil || m.status == nil || m.sender == nil || m.stopping() {
		return
	}
	members, err := m.store.ActiveMembersOfLiveTeams()
	if err != nil {
		m.logf("[team] usage notice: %v", err)
		return
	}
	threshold := float64(m.noticeAt)
	for _, mr := range members {
		u, ok := m.usage.ContextUsage(mr.SessionID)
		if !ok || u.UsedPercentage == nil {
			continue
		}
		pct := *u.UsedPercentage
		if pct < threshold {
			if _, err := m.store.ArmNotice(mr.SpawnOp, mr.SessionID); err != nil {
				m.logf("[team] usage notice: %v", err)
			}
			continue
		}
		if st, ok := m.status.AgentStatus(mr.TmuxSession); !ok || st != agentIdle {
			continue // running (or unknown): wait for it to stop
		}
		won, err := m.store.DisarmNotice(mr.SpawnOp, mr.SessionID)
		if err != nil {
			m.logf("[team] usage notice: %v", err)
			continue
		}
		if !won {
			continue // already told, or a relay is open
		}
		mr, pct := mr, pct
		if !m.goTracked(func() { m.usageNotice(mr, int(pct)) }) {
			m.rearm(mr)
		}
	}
}

// usageNotice sends the notice to the lead's live address from the member's inbox (else the lead's own), and arms the
// member again when the send did not go, so the next check tries once more. It is an advisory, at-least-once notice: the
// armed flag is persisted before the send, so a crash between the two loses it (the lead still sees CTX in `pdx team`),
// and a send that failed after the transport took it may be sent twice; neither is worth an outbox.
func (m *Module) usageNotice(mr memberRow, pct int) {
	// Looked at again right before the send: the member may have started a turn, or a relay op may have been created,
	// since the check (the claim's statement only covers an op that existed before it).
	if st, ok := m.status.AgentStatus(mr.TmuxSession); !ok || st != agentIdle || m.relayOpen(mr.SessionID) {
		m.rearm(mr)
		return
	}
	if !m.sendUsageNotice(mr, pct) {
		m.rearm(mr)
	}
}

// rearm gives the member's notice back; a store error is logged, and the member then stays quiet until a relay or a
// reading under the threshold arms it (the row is not retried here).
func (m *Module) rearm(mr memberRow) {
	if _, err := m.store.ArmNotice(mr.SpawnOp, mr.SessionID); err != nil {
		m.logf("[team] usage notice: member %s could not be armed again (it stays quiet until a relay or a reading under the threshold): %v", mr.Ref, err)
	}
}

func (m *Module) sendUsageNotice(mr memberRow, pct int) bool {
	t, ok, err := m.store.TeamByID(mr.TeamID)
	if err != nil || !ok || t.EndedAt != 0 {
		return true // no live team to tell: nothing to retry
	}
	alias, _ := m.selfHost()
	address := alias + "/" + mr.Ref
	if o, live, err := m.origins.ResolveOriginBySession(mr.SessionID); err == nil && live && o.Address != "" {
		address = o.Address
	}
	title := mr.Title
	if title == "" {
		title = mr.TmuxSession
	}
	ref := strings.TrimPrefix(mr.Ref, "_")
	return m.noticeToLead(mr, t, fmt.Sprintf(UsageNoticeFmt, address, ref, title, pct, ref), "usage notice")
}

// noticeToLead sends text to the team's lead at its live address, from the member's inbox (else the lead's own); false
// when it did not go.
func (m *Module) noticeToLead(mr memberRow, t team.Team, text, what string) bool {
	alias, _ := m.selfHost()
	inbox, ok, err := m.origins.InboxOf(mr.SessionID)
	if err != nil || !ok {
		inbox, ok, err = m.origins.InboxOf(t.LeadSessionID)
	}
	if err != nil || !ok {
		m.logf("[team] %s (member %s): no live inbox to send from (%v)", what, mr.Ref, err)
		return false
	}
	to := alias + "/" + ipeers.RefID(t.LeadSessionID)
	if o, live, err := m.origins.ResolveOriginBySession(t.LeadSessionID); err == nil && live && o.Address != "" {
		to = o.Address
	}
	ctx, cancel := context.WithTimeout(m.stopCtx, noticeSendTimeout)
	defer cancel()
	if _, err := m.sender.Send(ctx, ipeers.SendRequest{To: to, Text: text, OriginInbox: inbox}); err != nil {
		m.logf("[team] %s (member %s) to %s: %v", what, mr.Ref, to, err)
		return false
	}
	return true
}

// relayOpen says whether the session has a relay op in a non-terminal state; an unreadable store counts as open (no notice).
func (m *Module) relayOpen(sessionID string) bool {
	var one int
	err := m.store.db.QueryRow(`SELECT 1 FROM relay_ops WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled') LIMIT 1`, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return true
}
