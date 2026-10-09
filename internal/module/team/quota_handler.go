package teammod

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

// PUT /api/team/relay-quota, the display carriers and the event of the relay quota (#2062, RQ-1a). The rule that
// spends a quota (RQ-1b) is not here.

// handleRelayQuotaPut sets absolute values for the chain of a session. The App's alone: the client kind is the
// caller's own claim and the remote address is logged — told, not enforced, as the unattended switch (the App and pdx
// share one host token). No pdx command and no mod call writes it (the skill forbids an agent to).
func (m *Module) handleRelayQuotaPut(w http.ResponseWriter, r *http.Request) {
	if m.stopping() {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "daemon is stopping", nil)
		return
	}
	var req team.RelayQuotaPutRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	bad := func(why string) { m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, why, nil) }
	switch {
	case strings.TrimSpace(req.Client.Kind) != "app" || strings.TrimSpace(req.Client.Label) == "":
		bad(`client must be {"kind":"app","label":…}`)
		return
	case strings.TrimSpace(req.SessionID) == "":
		bad("session_id is required")
		return
	case req.SelfLeft == nil && req.MemberPoolLeft == nil:
		bad("self_left or member_pool_left is required")
		return
	case !quotaInRange(req.SelfLeft) || !quotaInRange(req.MemberPoolLeft):
		bad("self_left and member_pool_left must be integers from 0 to 99")
		return
	}
	live, ok, err := m.origins.ResolveOriginBySession(req.SessionID)
	if err != nil {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if !ok {
		known, err := m.store.KnownSession(req.SessionID)
		if err != nil {
			m.logf("[team] relay quota: %v", err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		if !known {
			m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such session", nil)
			return
		}
	}
	_ = live
	label := strings.TrimSpace(req.Client.Label)
	root, row, err := m.store.SetRelayQuota(req.SessionID, req.SelfLeft, req.MemberPoolLeft, m.now(), label)
	if err != nil {
		m.logf("[team] relay quota of %s: %v", req.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] relay quota of chain %s set to self %d, pool %d by app %q from %s (session %s)", root, row.SelfLeft, row.MemberPoolLeft, label, r.RemoteAddr, req.SessionID)
	m.broadcastRelayQuota(root, row.RelayQuota)
	m.rosterChanged()
	m.writeJSON(w, http.StatusOK, team.RelayQuotaView{SessionID: req.SessionID, RootSessionID: root, RelayQuota: row.RelayQuota,
		PendingLineage: ok && m.pendingLineage(req.SessionID, root), UpdatedAt: row.UpdatedAt, UpdatedBy: row.UpdatedBy})
}

func quotaInRange(v *int) bool { return v == nil || (*v >= 0 && *v <= team.MaxRelayQuota) }

// pendingLineage is an ADVISORY flag for the route's answer: sid is its own chain root and a relay is mid-flight
// (claimed, writing or written) whose old session has already left the registry — the shape of a /clear whose cleared
// report has not committed, when the new session id exists but its lineage row does not yet. A value written under
// that provisional root is not migrated when the lineage appears (it is orphaned, harmlessly: every read walks to the
// real root). A false positive only asks the App to say so; it never changes what is stored.
func (m *Module) pendingLineage(sid, root string) bool {
	if root != sid {
		return false
	}
	ops, err := m.store.ListActiveRelayOps()
	if err != nil {
		return false
	}
	for _, op := range ops {
		switch op.State {
		case team.RelayClaimed, team.RelayWriting, team.RelayWritten:
			if op.SessionID != sid && !m.origins.LiveSession(op.SessionID) {
				return true
			}
		}
	}
	return false
}

// broadcastRelayQuota queues the changed chain to every subscriber, under eventMu like the other team events. It is
// strict (BroadcastStrict): a quota is state, and a dropped frame would leave a stepper showing a number nothing
// corrects; a subscriber that cannot take it reconnects (the roster and the unattended GET carry the numbers too).
func (m *Module) broadcastRelayQuota(root string, q team.RelayQuota) {
	v, err := json.Marshal(team.RelayQuotaEventValue{Op: "changed", RootSessionID: root, RelayQuota: q})
	if err != nil {
		m.logf("[team] encode %s event: %v", team.RelayQuotaEventType, err)
		return
	}
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	m.core.Events.BroadcastStrict(core.HostEvent{Type: team.RelayQuotaEventType, Value: string(v)})
}

// relayQuotaOf is the numbers of sid's chain for a display; a store error is logged and shows 0 (a display must
// not fail for it, and the numbers are never used for a decision here).
func (m *Module) relayQuotaOf(sid string) team.RelayQuota {
	q, _, err := m.store.RelayQuotaOf(sid)
	if err != nil && !errors.Is(err, ErrLineageCycle) {
		m.logf("[team] relay quota of %s: %v", sid, err)
	}
	return q
}

// fillQuotas fills v.Quotas: every live session of the host with its chain's numbers, leads flagged. Best-effort
// (logged, left nil): the page above must stay valid.
func (m *Module) fillQuotas(v *team.UnattendedView) {
	origins, err := m.origins.ListLiveOrigins()
	if err != nil {
		m.logf("[team] unattended quotas: %v", err)
		return
	}
	sids := make([]string, len(origins))
	for i, o := range origins {
		sids[i] = o.SessionID
	}
	quotas, roots, err := m.store.RelayQuotasOf(sids)
	if err != nil {
		m.logf("[team] unattended quotas: %v", err)
	}
	out := make([]team.SessionQuota, 0, len(origins))
	for _, o := range origins {
		root, ok := roots[o.SessionID]
		if !ok {
			continue // its chain could not be read (logged above)
		}
		_, isLead, _ := m.store.LiveTeamByLead(o.SessionID)
		out = append(out, team.SessionQuota{SessionID: o.SessionID, RootSessionID: root, Title: o.Title, Address: o.Address, IsLead: isLead, RelayQuota: quotas[o.SessionID]})
	}
	v.Quotas = out
}
