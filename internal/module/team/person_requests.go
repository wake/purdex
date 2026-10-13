package teammod

import (
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// A person's typed /team (RT-1b): the lead, adopt and spawn requests that carry a valid person token are the person's own.

// personLeadGrant is what a person-granted lead gets (S-7): the default member cap and the origin's cwd as its only folder;
// whatever the request said about either is ignored. The name and label are the request's.
func personLeadGrant(origin team.Origin, teamName, teamLabel string) *team.Grant {
	roots, err := normaliseRoots(nil, origin.Cwd)
	if err != nil {
		roots = nil // no cwd: the approve's own checks refuse a grant without folders
	}
	name, label := teamName, teamLabel
	return &team.Grant{MaxMembers: team.DefaultMaxMembers, Roots: canonicalRoots(roots), RootsCanonical: true, TeamName: &name, TeamLabel: &label}
}

// personApproveLead approves the agent's open lead request as the person, with the person's grant.
func (m *Module) personApproveLead(w http.ResponseWriter, open team.Approval, g *team.Grant, token, reqID, hash string) {
	c := m.personClose(m.now())
	c.Grant = g
	m.personApprove(w, open, c, token, reqID, hash)
}

// replayOfPersonAlias answers a retry of a request id that a person's command answered by approving the agent's open request: the
// same row, not a refusal for a token that is already spent. true when it answered. Caller holds createMu.
func (m *Module) replayOfPersonAlias(w http.ResponseWriter, reqID, hash string) bool {
	approvalID, stored, ok, err := m.store.PersonAlias(reqID)
	if err != nil {
		m.logf("[team] create %s: %v", reqID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return true
	}
	if !ok {
		return false
	}
	if stored != hash {
		m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different request", nil)
		return true
	}
	row, found, err := m.store.Get(approvalID)
	if err != nil || !found {
		m.logf("[team] create %s: alias row %s: found=%v err=%v", reqID, approvalID, found, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return true
	}
	m.writeJSON(w, http.StatusOK, row)
	return true
}

// personApprove runs the approve a click runs, for the person, and answers the closed row (201). Every refusal of the approve is
// the create's own 409 and costs the token nothing. Caller holds createMu.
func (m *Module) personApprove(w http.ResponseWriter, open team.Approval, c Close, token, reqID, hash string) {
	after, won, _, err := m.approve(open, c)
	var refused *adoptRefusedError
	switch {
	case errors.Is(err, ErrLeadHasTeam):
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyLead, "this session already leads a live team; the request stays open", nil)
	case errors.Is(err, ErrMemberCannotLead):
		m.writeErr(w, http.StatusConflict, team.ErrMemberCannotLead, "this session is a member of a live team; the request stays open", nil)
	case errors.As(err, &refused): // committed and announced: the request is cancelled with this code
		m.writeErr(w, http.StatusConflict, refused.Code, "the request cannot be approved ("+refused.Code+"); it is cancelled", nil)
	case errors.Is(err, errRegistry):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry", nil)
	case err != nil:
		m.logf("[team] person approve %s: %v", open.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	case !won:
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyDecided, "this request was closed first by someone else", &after)
	default:
		// the person's request id has no row of its own: remember which row answered it, so a retry gets that row (before the token
		// is spent: a failure here leaves the token good and the retry finds the request closed, never a spent token with no trace)
		if aerr := m.store.RecordPersonAlias(reqID, after.ID, hash, m.now()); aerr != nil {
			m.logf("[team] person approve %s: %v", open.ID, aerr)
		}
		m.persons.spend(token)
		m.logf("[team] approval %s approved by the person's command: kind=%s origin=%s%s", after.ID, after.Kind, after.Origin.Ref, teamNote(after))
		m.writeJSON(w, http.StatusCreated, after)
	}
}
