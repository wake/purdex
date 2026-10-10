package teammod

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// The member auto-compact notice (plan v3 P7-2; spec §8.5, decision 12). A session's mod reports every compaction it does
// not intercept — from any role, because the first hello may answer before the member row exists — and the DAEMON decides:
// only an active local member's AUTO compaction tells the lead (a manual one is the person's own doing). The 70% idle
// notice is disarmed at the same time and NOT armed again here: the last reading may still be over the threshold until the
// next statusline refresh, so arming would send the 70% notice on the very next tick (codex finding 5). It is armed
// again only as P7-1 says: a relay's `cleared`, or a reading under the threshold. Members of another host are the X
// series' (X2a).

// CompactedNoticeFmt takes the member's ref (`_xxxxxx`); the %% is a literal percent sign.
const CompactedNoticeFmt = "[pdx team] %s 已自動壓縮（lead 未在 70%% 時接力）"

// handleRelayCompacted is POST /api/relay/compacted.
func (m *Module) handleRelayCompacted(w http.ResponseWriter, r *http.Request) {
	var req team.RelayCompactedRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "session_id is required", nil)
		return
	}
	if req.Trigger != "auto" && req.Trigger != "manual" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `trigger must be "auto" or "manual"`, nil)
		return
	}
	if req.Trigger != "auto" {
		m.writeJSON(w, http.StatusOK, team.RelayCompactedResponse{})
		return
	}
	mr, t, ok, err := m.store.ActiveMemberInLiveTeam(req.SessionID)
	if err != nil {
		m.logf("[team] compacted %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeJSON(w, http.StatusOK, team.RelayCompactedResponse{})
		return
	}
	// The member compacted on its own, so an open ask is moot (member relay ask §3.3); the compaction notice below is the
	// only message, the withdrawal adds none. A withdrawal that fails fails the call, before anything is sent: the ask
	// would stay open and be notified again, so the mod retries (disarming and withdrawing are both idempotent).
	m.askMu.Lock() // behind an ask notice that is on the wire (see sendAskNotice)
	_, err = m.store.WithdrawRelayAsk(mr.SessionID, team.RelayAskWithdrawCompacted, m.now())
	m.askMu.Unlock()
	if err != nil {
		m.logf("[team] compacted %s: withdraw the relay ask: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if m.sender == nil || m.stopping() {
		m.writeJSON(w, http.StatusOK, team.RelayCompactedResponse{})
		return
	}
	if _, err := m.store.DisarmNoticeForce(mr.SpawnOp, mr.SessionID); err != nil {
		m.logf("[team] compacted %s: %v", req.SessionID, err)
	}
	text := fmt.Sprintf(CompactedNoticeFmt, mr.Ref)
	// Best effort, and a notice that does not go is NOT given back as an armed 70% notice: the last reading is from before
	// the compaction, so arming would send a stale "over 70%" at the next tick (codex finding 5). The lead sees CTX in `pdx team`.
	if !m.goTracked(func() { m.noticeToLead(mr, t, text, "compaction notice") }) {
		m.writeJSON(w, http.StatusOK, team.RelayCompactedResponse{})
		return
	}
	m.writeJSON(w, http.StatusOK, team.RelayCompactedResponse{Noticed: true})
}
