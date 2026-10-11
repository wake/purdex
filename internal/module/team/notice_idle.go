package teammod

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// The member idle notice (idle relay spec §3.3, D3; extends the 70% notice of notice_usage.go). Claude Code compacts an
// idle session about 54 minutes after its last request, often long before the 70% threshold. A local member that has
// been idle for RelayIdleMinutes with its context at RelayIdleThresholdPct or more tells its lead ONCE per idle period,
// while the prompt cache is still warm: the lead decides (`pdx relay _<ref>`, U9: the daemon only reports). No ask row
// and no new wire. Independent of the 70% notice's armed flag, and not skipped for a mod that asks for itself (its own
// ask fires only at the threshold). The clock and the told mark are in memory: a restart starts the clock again.

// UsageIdleNoticeFmt takes the member's address, its bare ref, its title, the minutes idle, the integer percentage and
// the ref again.
const UsageIdleNoticeFmt = "[pdx team] member %s [%s]「%s」閒置 %d 分鐘、已用 %d%%，快取快過期。要趁現在接力請執行：pdx relay _%s（不接力的話，Claude Code 稍後會自行壓縮）"

// envInt is the integer in [lo, hi] of the daemon's own environment variable, else def.
func envInt(name string, lo, hi, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name))); err == nil && n >= lo && n <= hi {
		return n
	}
	return def
}

// noticeIdleMinutes is how long a member is idle before its lead is told: PDX_RELAY_IDLE_MINUTES (1–1440, for the
// acceptance), else team.RelayIdleMinutes. It sets this notice only.
func noticeIdleMinutes() int {
	return envInt("PDX_RELAY_IDLE_MINUTES", 1, 1440, team.RelayIdleMinutes)
}

// noticeIdleThreshold is the context percentage the idle notice needs: PDX_RELAY_IDLE_THRESHOLD (1–100), else
// team.RelayIdleThresholdPct.
func noticeIdleThreshold() int {
	return envInt("PDX_RELAY_IDLE_THRESHOLD", 1, 100, team.RelayIdleThresholdPct)
}

// noticeIdle is the check: called on the liveness tick, after noticeUsage.
func (m *Module) noticeIdle() {
	if m.usage == nil || m.status == nil || m.sender == nil || m.stopping() {
		return
	}
	members, err := m.store.ActiveMembersOfLiveTeams()
	if err != nil {
		m.logf("[team] idle notice: %v", err)
		return
	}
	now := m.now()
	after := int64(m.idleMinutes) * 60_000
	live := make(map[string]struct{}, len(members))
	for _, mr := range members {
		live[mr.SessionID] = struct{}{}
		if st, ok := m.status.AgentStatus(mr.TmuxSession); !ok || st != agentIdle {
			m.endIdlePeriod(mr.SessionID) // running, waiting on a person, or unknown: not idle time
			continue
		}
		since := m.idleStart(mr.SessionID, now)
		if now-since < after {
			continue
		}
		u, ok := m.usage.ContextUsage(mr.SessionID)
		if !ok || u.UsedPercentage == nil || *u.UsedPercentage < float64(m.idleThreshold) {
			continue
		}
		if m.relayOpen(mr.SessionID) || m.askOpen(mr.SessionID) {
			continue // the relay (or the member's own ask) is the answer
		}
		if !m.markIdleTold(mr.SessionID) {
			continue // this idle period is already told
		}
		mr, pct, minutes := mr, int(*u.UsedPercentage), int((now-since)/60_000)
		if !m.goTracked(func() { m.idleNotice(mr, pct, minutes) }) {
			m.unmarkIdleTold(mr.SessionID)
		}
	}
	m.pruneIdle(live)
}

// idleNotice sends the notice. Looked at again right before the send, as usageNotice does: the member may have started
// a turn, or a relay or an ask may have opened, since the check; then nothing is sent and the period is not marked told.
// A send that fails is not retried (advisory: the lead still sees CTX in `pdx team`).
func (m *Module) idleNotice(mr memberRow, pct, minutes int) {
	if st, ok := m.status.AgentStatus(mr.TmuxSession); !ok || st != agentIdle || m.relayOpen(mr.SessionID) || m.askOpen(mr.SessionID) {
		m.unmarkIdleTold(mr.SessionID)
		return
	}
	t, ok, err := m.store.TeamByID(mr.TeamID)
	if err != nil || !ok || t.EndedAt != 0 {
		return // no live team to tell
	}
	address, title := m.memberNoticeName(mr)
	ref := strings.TrimPrefix(mr.Ref, "_")
	m.noticeToLead(mr, t, fmt.Sprintf(UsageIdleNoticeFmt, address, ref, title, minutes, pct, ref), "idle notice")
}

// idleStart is when the session's current idle period began: now, the first time it is seen idle.
func (m *Module) idleStart(sessionID string, now int64) int64 {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if m.memberIdleSince == nil {
		m.memberIdleSince = map[string]int64{}
	}
	if s, ok := m.memberIdleSince[sessionID]; ok {
		return s
	}
	m.memberIdleSince[sessionID] = now
	return now
}

// endIdlePeriod forgets the clock and the told mark: the next idle period starts from scratch.
func (m *Module) endIdlePeriod(sessionID string) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	delete(m.memberIdleSince, sessionID)
	delete(m.idleTold, sessionID)
}

// markIdleTold marks the period told and says whether THIS call did.
func (m *Module) markIdleTold(sessionID string) bool {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if m.idleTold == nil {
		m.idleTold = map[string]bool{}
	}
	if m.idleTold[sessionID] {
		return false
	}
	m.idleTold[sessionID] = true
	return true
}

func (m *Module) unmarkIdleTold(sessionID string) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	delete(m.idleTold, sessionID)
}

// pruneIdle drops the sessions that are no longer active members of a live team.
func (m *Module) pruneIdle(live map[string]struct{}) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	for sid := range m.memberIdleSince {
		if _, ok := live[sid]; !ok {
			delete(m.memberIdleSince, sid)
			delete(m.idleTold, sid)
		}
	}
}
