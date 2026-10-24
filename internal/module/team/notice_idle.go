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

// noticeIdleMinutes is how long a member is idle before its lead is told: PDX_RELAY_IDLE_MINUTES (1–50, for the
// acceptance; a longer wait would outlast the idle compaction it is meant to beat), else team.RelayIdleMinutes. It sets this notice only.
func noticeIdleMinutes() int {
	return envInt("PDX_RELAY_IDLE_MINUTES", 1, 50, team.RelayIdleMinutes)
}

// noticeIdleThreshold is the context percentage the idle notice needs: PDX_RELAY_IDLE_THRESHOLD (1–100), else
// team.RelayIdleThresholdPct.
func noticeIdleThreshold() int {
	return envInt("PDX_RELAY_IDLE_THRESHOLD", 1, 100, team.RelayIdleThresholdPct)
}

// idlePeriod is one stretch of idleness of one session. gen is unique per period, so a notice goroutine started in one
// period can never send for, or unmark, a later one (the status flickers; a relay moves the member to a new session).
type idlePeriod struct {
	since int64
	gen   uint64
	told  bool
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
	autoOn := m.memberAutoOn() // off: the idle periods are still kept; only the claim and the send are skipped
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
		if m.modInterceptAcked(mr.SessionID) {
			continue // its mod stops the idle compaction and the daemon tells the lead then (RT-4): the period is kept, nothing is said here
		}
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
		if !autoOn {
			continue
		}
		gen, won := m.claimIdleTold(mr.SessionID)
		if !won {
			continue // this idle period is already told
		}
		mr, pct, minutes := mr, int(*u.UsedPercentage), int((now-since)/60_000)
		if !m.goTracked(func() { m.idleNotice(mr, pct, minutes, gen) }) {
			m.releaseIdleTold(mr.SessionID, gen)
		}
	}
	m.pruneIdle(live)
}

// idleNotice sends the notice of the idle period gen. Looked at again right before the send, as usageNotice does: the
// member may have started a turn, a relay or an ask may have opened, it may have been released or moved to a new session,
// or the period may have ended, since the check; then nothing is sent, and a period that is still the same is given back.
// A send that fails is not retried (advisory: the lead still sees CTX in `pdx team`).
func (m *Module) idleNotice(mr memberRow, pct, minutes int, gen uint64) {
	if st, ok := m.status.AgentStatus(mr.TmuxSession); !ok || st != agentIdle || m.relayOpen(mr.SessionID) || m.askOpen(mr.SessionID) {
		m.releaseIdleTold(mr.SessionID, gen)
		return
	}
	if !m.idleToldIs(mr.SessionID, gen) {
		return // the period ended (the session ran, or was moved): its notice is void
	}
	cur, t, ok, err := m.store.ActiveMemberInLiveTeam(mr.SessionID)
	if err != nil {
		m.logf("[team] idle notice (member %s): %v", mr.Ref, err)
		m.releaseIdleTold(mr.SessionID, gen) // a store error is not a told period: the next tick tries again
		return
	}
	if !ok || cur.SpawnOp != mr.SpawnOp || cur.TeamID != mr.TeamID {
		return // released, moved or the team ended since the check
	}
	address, title := m.memberNoticeName(mr)
	ref := strings.TrimPrefix(mr.Ref, "_")
	m.noticeToLead(mr, t, fmt.Sprintf(UsageIdleNoticeFmt, address, ref, title, minutes, pct, ref), "idle notice")
}

// idleStart is when the session's current idle period began: now (a new period) the first time it is seen idle.
func (m *Module) idleStart(sessionID string, now int64) int64 {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if m.memberIdle == nil {
		m.memberIdle = map[string]*idlePeriod{}
	}
	if p, ok := m.memberIdle[sessionID]; ok {
		return p.since
	}
	m.idleGenSeq++
	m.memberIdle[sessionID] = &idlePeriod{since: now, gen: m.idleGenSeq}
	return now
}

// endIdlePeriod forgets the period: the next idle stretch starts from scratch.
func (m *Module) endIdlePeriod(sessionID string) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	delete(m.memberIdle, sessionID)
}

// claimIdleTold marks the session's current period told and returns its generation; false when it already was.
func (m *Module) claimIdleTold(sessionID string) (uint64, bool) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	p, ok := m.memberIdle[sessionID]
	if !ok || p.told {
		return 0, false
	}
	p.told = true
	return p.gen, true
}

// releaseIdleTold gives the period gen back (the notice did not go for a reason that is not the lead's: the next tick may
// tell). Another period, or none, is left alone.
func (m *Module) releaseIdleTold(sessionID string, gen uint64) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if p, ok := m.memberIdle[sessionID]; ok && p.gen == gen {
		p.told = false
	}
}

// idleToldIs says whether the session's current period is gen and is marked told.
func (m *Module) idleToldIs(sessionID string, gen uint64) bool {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	p, ok := m.memberIdle[sessionID]
	return ok && p.gen == gen && p.told
}

// markIdleCompacted ends the question for the current idle stretch: the session was compacted, so a notice about a cache
// about to expire would be stale. It stays quiet until the session leaves idle (a new period). The period gets a NEW
// generation, so a notice already claimed under the old one neither sends nor, by releasing, unmarks this.
func (m *Module) markIdleCompacted(sessionID string) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if m.memberIdle == nil {
		m.memberIdle = map[string]*idlePeriod{}
	}
	since := m.now()
	if p, ok := m.memberIdle[sessionID]; ok {
		since = p.since
	}
	m.idleGenSeq++
	m.memberIdle[sessionID] = &idlePeriod{since: since, gen: m.idleGenSeq, told: true}
}

// pruneIdle drops the sessions that are no longer active members of a live team.
func (m *Module) pruneIdle(live map[string]struct{}) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	for sid := range m.memberIdle {
		if _, ok := live[sid]; !ok {
			delete(m.memberIdle, sid)
		}
	}
	for sid := range m.interceptTold {
		if _, ok := live[sid]; !ok {
			delete(m.interceptTold, sid)
		}
	}
}

// interceptedIdle tells the lead that the member's mod stopped Claude Code's idle compaction (RT-4 T42): once per idle stretch, with the
// three answers. The stretch is named by period, when the member's last turn ended by the mod's clock: a short turn between two
// interceptions that the liveness tick never saw is still a new stretch (a mod that cannot say, 0, falls back to the tick's period). Not
// told when the member is running by now (the mod's report was queued behind a turn): that is no idle member. noticed says a notice
// went; member says the session is an active local member of a live team at all.
func (m *Module) interceptedIdle(sessionID string, period int64) (noticed, member bool) {
	mr, t, ok, err := m.store.ActiveMemberInLiveTeam(sessionID)
	if err != nil {
		m.logf("[team] idle intercept %s: %v", sessionID, err)
		return false, true // not known to be released: the mod keeps its role
	}
	if !ok {
		return false, false
	}
	if m.sender == nil || m.stopping() {
		return false, true
	}
	if m.status != nil {
		if st, known := m.status.AgentStatus(mr.TmuxSession); known && st != agentIdle {
			return false, true // running or waiting on a person: not idle
		}
	}
	// The whole decision is one step under idleMu: the claim is the membership's (a member released and adopted again is a new member
	// whose lead has heard nothing), a period names a stretch, and a report with no period is judged by the daemon's own.
	membership := mr.TeamID + "|" + strconv.FormatInt(mr.CreatedAt, 10)
	claim, ok := m.claimIntercept(sessionID, membership, period, m.now())
	if !ok {
		return false, true
	}
	// The membership is read again before the notice is scheduled: a release and an adopt between the first read and the claim would
	// otherwise send it to the lead of the old one.
	if cur, _, still, err := m.store.ActiveMemberInLiveTeam(sessionID); err != nil || !still || cur.TeamID != mr.TeamID || cur.CreatedAt != mr.CreatedAt {
		m.restoreIntercept(sessionID, claim)
		return false, err != nil || still
	}
	ctx := "未知"
	if m.usage != nil {
		if u, ok := m.usage.ContextUsage(sessionID); ok && u.UsedPercentage != nil {
			ctx = fmt.Sprintf("%d%%", int(*u.UsedPercentage))
		}
	}
	address, title := m.memberNoticeName(mr)
	ref := strings.TrimPrefix(mr.Ref, "_")
	text := fmt.Sprintf(team.IdleInterceptNoticeFmt, address, title, ctx, ref, ref, ref)
	if !m.goTracked(func() { m.noticeToLead(mr, t, text, "idle intercept notice") }) {
		m.restoreIntercept(sessionID, claim)
		return false, true
	}
	return true, true
}

// interceptToldRec is the last notice of a session's intercepts: the membership it was told for and the period it named (0 = none:
// told by the daemon's own period).
type interceptToldRec struct {
	member string
	period int64
}

// interceptClaim is what claimIntercept took, to give back if the notice does not go.
type interceptClaim struct {
	prev   interceptToldRec
	next   interceptToldRec
	gen    uint64 // the daemon's idle period that this claim marked told; 0 when it was marked already
	didSet bool
}

// claimIntercept decides one report of an intercepted idle compaction: true when the lead is to be told. All of it happens in one
// critical section, so two reports of one session (one with a period, one without) cannot both pass. A report that names a period is a
// new stretch unless that period was the last one told for the membership; one that does not is a new stretch unless the daemon's own
// idle period has been told. Either way the daemon's own period is marked told as well: a mod that reloads in the stretch has lost its
// clock, and its next report (no period) is judged by it. The window left: the old runtime's report, still on the way when the mod
// reloads, can arrive after its successor's and tell the same stretch a second time.
func (m *Module) claimIntercept(sessionID, membership string, period, now int64) (interceptClaim, bool) {
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if m.interceptTold == nil {
		m.interceptTold = map[string]interceptToldRec{}
	}
	if m.memberIdle == nil {
		m.memberIdle = map[string]*idlePeriod{}
	}
	prev := m.interceptTold[sessionID]
	if prev.member != membership {
		delete(m.memberIdle, sessionID) // the daemon's own period was another membership's
		prev = interceptToldRec{}
	}
	p, ok := m.memberIdle[sessionID]
	if !ok {
		m.idleGenSeq++
		p = &idlePeriod{since: now, gen: m.idleGenSeq}
		m.memberIdle[sessionID] = p
	}
	switch {
	case period > 0 && prev.member == membership && prev.period == period:
		return interceptClaim{}, false
	case period == 0 && p.told:
		return interceptClaim{}, false
	}
	c := interceptClaim{prev: m.interceptTold[sessionID], next: interceptToldRec{member: membership, period: period}, didSet: true}
	m.interceptTold[sessionID] = c.next
	if !p.told {
		p.told = true
		c.gen = p.gen
	}
	return c, true
}

// restoreIntercept gives a claim back (the notice did not go for a reason that is not the lead's): only what the claim wrote is undone.
func (m *Module) restoreIntercept(sessionID string, c interceptClaim) {
	if !c.didSet {
		return
	}
	m.idleMu.Lock()
	defer m.idleMu.Unlock()
	if cur, ok := m.interceptTold[sessionID]; ok && cur == c.next {
		if c.prev == (interceptToldRec{}) {
			delete(m.interceptTold, sessionID)
		} else {
			m.interceptTold[sessionID] = c.prev
		}
	}
	if c.gen != 0 {
		if p, ok := m.memberIdle[sessionID]; ok && p.gen == c.gen {
			p.told = false
		}
	}
}
