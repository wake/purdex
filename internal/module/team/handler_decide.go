package teammod

import (
	"errors"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// handleDecide is POST /api/team/approvals/{id}/decide (spec §6.5): one
// click on any App; the client label and remote address are audit, and
// every decision is one daemon log line. A late decide answers 409
// already_decided carrying the closed row, so its client can say who
// handled it.
func (m *Module) handleDecide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req team.DecideRequest
	if !m.decodeBody(w, r, &req) {
		return
	}
	var state team.State
	switch req.Decision {
	case "approve":
		state = team.StateApproved
	case "deny":
		state = team.StateDenied
	default:
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `decision must be "approve" or "deny"`, nil)
		return
	}
	if strings.TrimSpace(req.Client.Kind) == "" || strings.TrimSpace(req.Client.Label) == "" {
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "client.kind and client.label are required", nil)
		return
	}
	if strings.EqualFold(strings.TrimSpace(req.Client.Kind), team.ClientKindUnattended) {
		// Reserved for the daemon's own approvals (RQ-0): a person's click, a terminal or an agent never decides as it.
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, `client.kind "unattended" is reserved for the daemon`, nil)
		return
	}
	client := req.Client
	client.Addr = r.RemoteAddr
	a, ok, err := m.store.Get(id)
	if err != nil {
		m.logf("[team] decide %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !ok {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if a.State != team.StateOpen {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request is already closed", &a)
		return
	}
	if team.IsHookKind(a.Kind) {
		m.decideHook(w, a, req, state, client)
		return
	}
	now := m.now()
	var grant *team.Grant
	// A self_relay approval carries no grant (its payload is a
	// SelfRelayPayload); its op moves in afterClose.
	if state == team.StateApproved && a.Kind == team.KindLead {
		// nil grant → the payload's values; an edit falls back to them
		// field by field (max_members 0, no roots).
		g, err := leadGrantOf(a)
		if err != nil {
			m.logf("[team] decide %s: %v", id, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
			return
		}
		if req.Grant != nil {
			g.MaxMembers = normaliseMaxMembers(req.Grant.MaxMembers, g.MaxMembers)
			if len(req.Grant.Roots) > 0 {
				roots, err := normaliseRoots(req.Grant.Roots, a.Origin.Cwd)
				if err != nil {
					m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, err.Error(), nil)
					return
				}
				g.Roots, g.RootsCanonical = canonicalRoots(roots), true
			}
			// D-N3: an absent key keeps the requested name (an older App
			// never wipes it); present, "" clears it.
			if req.Grant.TeamName != nil {
				name, err := team.NormaliseTeamName(*req.Grant.TeamName)
				if err != nil {
					m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "team_name: "+err.Error(), nil)
					return
				}
				g.TeamName = &name
			}
			// D-L4: absent keeps the requested label (an older App never wipes
			// it); present, "" asks for the label to be derived (D-L3).
			if req.Grant.TeamLabel != nil {
				label, err := team.NormaliseTeamLabel(*req.Grant.TeamLabel)
				if err != nil {
					m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "team_label: "+err.Error(), nil)
					return
				}
				g.TeamLabel = &label
			}
		}
		grant = &g
	}
	c := Close{State: state, DecidedAt: now, DecidedBy: &client, Grant: grant}
	var after team.Approval
	var won, memberCancelled bool
	if state == team.StateApproved {
		// The approve the daemon's own approvals run too (U23): a lead's
		// team is created in the same transaction (spec §6.2).
		after, won, memberCancelled, err = m.approve(a, c)
	} else {
		after, won, err = m.closeAs(id, c)
	}
	if errors.Is(err, ErrNoSuchApproval) {
		m.writeErr(w, http.StatusNotFound, team.ErrNotFound, "no such approval request", nil)
		return
	}
	if errors.Is(err, ErrLeadHasTeam) {
		// Both writes rolled back: the row is still open (the user may deny
		// it, or it times out). No approval in the body: a 409 carrying one
		// reads as "closed elsewhere" to the App.
		m.logf("[team] approval %s: approve by %s %q from %s refused: origin %s already leads a live team", id, client.Kind, client.Label, client.Addr, a.Origin.Ref)
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyLead, "this session already leads a live team; the request stays open", nil)
		return
	}
	if errors.Is(err, ErrMemberCannotLead) { // as already_lead: rolled back, the row stays open
		m.logf("[team] approval %s: approve by %s %q refused: origin %s is a member of a live team", id, client.Kind, client.Label, a.Origin.Ref)
		m.writeErr(w, http.StatusConflict, team.ErrMemberCannotLead, "this session is a member of a live team; the request stays open", nil)
		return
	}
	var refused *adoptRefusedError
	if errors.As(err, &refused) { // committed and announced: the request is cancelled with this code (no approval in the body, as above)
		m.logf("[team] approval %s: approve by %s %q refused: %s; request cancelled", id, client.Kind, client.Label, refused.Code)
		m.writeErr(w, http.StatusConflict, refused.Code, "the request cannot be approved ("+refused.Code+"); it is cancelled", nil)
		return
	}
	if errors.Is(err, errRegistry) {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
		return
	}
	if err != nil {
		m.logf("[team] decide %s: %v", id, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !won {
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request was closed first by someone else", &after)
		return
	}
	if memberCancelled {
		// U13 (P4-3 review H2): the approve's transaction cancelled the
		// request and its op instead; committed and broadcast (mod: exit 12).
		m.logf("[team] approval %s: approve by %s %q: origin %s is a member of a live team; request and relay op cancelled", id, client.Kind, client.Label, after.Origin.Ref)
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排; the request is cancelled", nil)
		return
	}
	m.logf("[team] approval %s %s by %s %q from %s (origin %s)%s", id, after.State, client.Kind, client.Label, client.Addr, after.Origin.Ref, teamNote(after))
	m.writeJSON(w, http.StatusOK, after)
}
