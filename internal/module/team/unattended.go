package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/wake/purdex/internal/team"
)

// approve is THE approve of an open row (U23 D-U23-1: a click's and the
// daemon's own run the same statements): the kind's store CAS through
// closeWith, so the winner alone announces. A lead row's team is created
// in the same transaction with c.Grant, nil → the payload's (leadGrantOf);
// a self_relay row whose session became a member closes cancelled with its
// op instead (memberCancelled, U13). c must be an approval; hook kinds
// decide through decideHook and are refused here.
func (m *Module) approve(a team.Approval, c Close) (after team.Approval, won, memberCancelled bool, err error) {
	if c.State != team.StateApproved || team.IsHookKind(a.Kind) {
		return team.Approval{}, false, false, fmt.Errorf("approve %s: a %s row is not approved here (close %s)", a.ID, a.Kind, c.State)
	}
	switch a.Kind {
	case team.KindLead:
		if c.Grant == nil {
			g, err := leadGrantOf(a)
			if err != nil {
				return team.Approval{}, false, false, err
			}
			c.Grant = &g
		}
		t := leadTeamOf(a, *c.Grant, c.DecidedAt)
		after, won, err = m.closeWith(a.ID, func() (team.Approval, bool, error) { return m.store.CloseLeadApproved(a.ID, c, t) })
	case team.KindAdopt:
		after, won, err = m.approveAdopt(a, c)
	case team.KindSelfRelay:
		after, won, err = m.closeWith(a.ID, func() (row team.Approval, won bool, err error) {
			row, won, memberCancelled, err = m.store.CloseSelfRelayApproved(a.ID, c, a.Origin.SessionID)
			return row, won, err
		})
	default:
		after, won, err = m.closeAs(a.ID, c)
	}
	return after, won, memberCancelled, err
}

// leadGrantOf is a lead row's grant as requested: its payload's values.
func leadGrantOf(a team.Approval) (team.Grant, error) {
	var p team.LeadPayload
	if err := json.Unmarshal(a.Payload, &p); err != nil {
		return team.Grant{}, fmt.Errorf("lead row %s: decode payload: %w", a.ID, err)
	}
	name, label := p.TeamName, p.TeamLabel // copies: the grant owns its pointers
	return team.Grant{MaxMembers: p.MaxMembers, Roots: p.Roots, TeamName: &name, TeamLabel: &label}, nil
}

// leadTeamOf is the team a lead row's approval creates (spec §7.1); its id
// is the request's id (plan v3 deviation 1). The team's current name starts
// as the grant's approved one (D-N5); a grant without a name (nil: a
// caller that never went through leadGrantOf) is an unnamed team.
func leadTeamOf(a team.Approval, g team.Grant, at int64) team.Team {
	name := ""
	if g.TeamName != nil {
		name = *g.TeamName
	}
	// The label is the explicit one the grant carries, else the one derived from
	// the approved name (team-label D-L3, D-L5); never cut from the middle.
	label := ""
	if g.TeamLabel != nil {
		label = *g.TeamLabel
	}
	if label == "" {
		label = team.DeriveTeamLabel(name)
	}
	return team.Team{ID: a.ID, HostID: a.HostID, LeadSessionID: a.Origin.SessionID, LeadRef: a.Origin.Ref,
		TeamName: name, TeamLabel: label, Grant: g, RequestID: a.ID, CreatedAt: at}
}

// teamNote is the decision log line's suffix for an approval that created
// a team ("" for none).
func teamNote(a team.Approval) string {
	if a.Kind != team.KindLead || a.State != team.StateApproved || a.Grant == nil {
		return ""
	}
	name := ""
	if a.Grant.TeamName != nil && *a.Grant.TeamName != "" {
		name = fmt.Sprintf(" %q", *a.Grant.TeamName)
	}
	return fmt.Sprintf("; team %s%s created (max_members %d, roots %v)", a.ID, name, a.Grant.MaxMembers, a.Grant.Roots)
}

// afterApproved runs once for every close that won as approved, on every
// path — a click, create-time, the switch-on sweep, the tick, boot
// (announceClosed: the plan's one winner point). Later side effects of an
// approval (PL-1c's adopt notice) hang here, never on a route. Today it
// announces the roster: a lead approve creates a team inside the approve
// (closeLeadApprovedIn), and the other kinds leave the roster as it was,
// which rosterChanged's hash gate makes free.
func (m *Module) afterApproved(a team.Approval) {
	m.rosterChanged()
	if a.Kind == team.KindAdopt {
		m.kickNotices() // the adopted member is owed its notice
		if m.noticeKick != nil {
			m.noticeKick() // test seam: counts the kicks
		}
	}
}

// unattendedOn reads the U23 switch. Every caller holds createMu, so a
// create that reads it off committed before a switch-on's sweep, which
// then approves it. A read error is off (fail closed), logged once per
// distinct error.
func (m *Module) unattendedOn() bool {
	st, err := m.unattended.Unattended()
	if err != nil {
		if err.Error() != m.unattendedErr {
			m.unattendedErr = err.Error()
			m.logf("[team] unattended switch unreadable, treated as off: %v", err)
		}
		return false
	}
	m.unattendedErr = ""
	return st.On
}

// daemonClose is the Close of an approval the daemon makes itself (U23):
// decided by {kind unattended, label 無人值守模式}, no addr.
func daemonClose(at int64, g *team.Grant) Close {
	by := team.UnattendedClient()
	return Close{State: team.StateApproved, DecidedAt: at, DecidedBy: &by, Grant: g, Auto: true}
}

// unattendedGrant is the grant of a daemon approval (U25 / D-U24-7): for a
// lead row min(requested, 3) members — an unspecified request counts as 3
// (create stored it so) — and the requested roots; nil for any other kind.
// A click's grant is the person's and never passes here.
func unattendedGrant(a team.Approval) (*team.Grant, error) {
	if a.Kind != team.KindLead {
		return nil, nil
	}
	g, err := leadGrantOf(a)
	if err != nil {
		return nil, err
	}
	g.MaxMembers = min(normaliseMaxMembers(g.MaxMembers, team.DefaultMaxMembers), team.UnattendedLeadMaxMembers)
	return &g, nil
}

// createApprovedLead is handleCreate's write while the switch is on (PU-1b2
// rule 1): the row is inserted and approved in one transaction (its first
// committed state is approved, so no snapshot or poll sees it open), then
// announced as closed only. A refusal of the approve's re-checks is the
// create's own 409 with nothing written. Caller holds createMu.
func (m *Module) createApprovedLead(w http.ResponseWriter, row team.Approval, hash string) {
	g, err := unattendedGrant(row)
	var after team.Approval
	if err == nil {
		c, t := daemonClose(row.CreatedAt, g), leadTeamOf(row, *g, row.CreatedAt)
		after, err = m.store.CreateApproved(row, hash, func(tx *sql.Tx) error {
			_, err := closeLeadApprovedIn(tx, row.ID, c, t)
			return err
		})
	}
	switch {
	case errors.Is(err, ErrLeadHasTeam):
		m.writeErr(w, http.StatusConflict, team.ErrAlreadyLead, "this session already leads a live team", nil)
	case errors.Is(err, ErrMemberCannotLead):
		m.writeErr(w, http.StatusConflict, team.ErrMemberCannotLead, "this session is a member of a live team; a member cannot lead", nil)
	case err != nil:
		m.logf("[team] create %s: %v", row.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
	default:
		m.logf("[team] approval %s approved by unattended at create: kind=%s origin=%s (%s)%s", after.ID, after.Kind, after.Origin.Ref, after.Origin.SessionID, teamNote(after))
		m.announceClosed(after, nil)
		m.writeJSON(w, http.StatusCreated, after)
	}
}

// beginApproved is handleRelayBegin's write while the switch is on (PU-1b2
// rule 2): the op (claimed) and its row (approved) in one transaction,
// announced as closed only; the 201 carries the claimed op, and the mod's
// wait answers approved at once. Caller holds createMu.
func (m *Module) beginApproved(w http.ResponseWriter, op team.RelayOp, row team.Approval, hash string) {
	after, claimed, err := m.store.CreateSelfRelayApproved(op, row, hash, daemonClose(row.CreatedAt, nil))
	switch {
	case errors.Is(err, ErrMemberRelayIsLeads):
		m.writeErr(w, http.StatusConflict, team.ErrMemberRelayIsLeads, "member 的接力由 lead 安排", nil)
		return
	case errors.Is(err, ErrRelayOpOpen) && m.writeRelayOpen(w, op.SessionID):
		return
	case err != nil:
		m.logf("[team] relay begin %s: %v", op.SessionID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] relay op %s claimed: self, origin=%s (%s), request %s approved by unattended at begin", op.ID, op.Ref, op.SessionID, after.ID)
	m.announceClosed(after, nil)
	m.writeJSON(w, http.StatusCreated, team.RelayBeginResponse{Op: claimed, RequestID: after.ID})
}

// autoApprove is the daemon's approve of an open AutoApprovable row (U23):
// approve with the decider unattended and unattendedGrant. approved says it
// won as approved; open that the row is still open — refused by a rule (a
// lead's origin became a member) or a failure — for the next sweep. A row
// it cannot approve is logged once per reason (notAutoApproved), not once
// per tick. An overdue row (deadline or lease passed at now) is never
// approved: the CAS itself requires it unexpired (Close.UnexpiredAt), so it
// loses like a row closed first and is left, neither open-for-retry nor
// logged, to the sweeper's timeout or abandonment — even when Start has
// just extended its lease, or the tick's expiry close failed. Caller holds
// createMu.
func (m *Module) autoApprove(a team.Approval) (approved, open bool) {
	g, err := unattendedGrant(a)
	if err == nil && m.beforeAutoApprove != nil {
		err = m.beforeAutoApprove(a)
	}
	if err == nil {
		var after team.Approval
		var won, memberCancelled bool
		now := m.now()
		c := daemonClose(now, g)
		c.UnexpiredAt = now
		after, won, memberCancelled, err = m.approve(a, c)
		var refused *adoptRefusedError
		switch {
		case errors.As(err, &refused): // closed cancelled by its own re-check: not retried
			m.forgetRefusal(a.ID)
			m.logf("[team] approval %s cancelled at its auto-approve: %s", a.ID, refused.Code)
			return false, false
		case err != nil: // logged below
		case !won: // closed first by a click, a cancel or the sweeper, or overdue
			return false, false
		case memberCancelled:
			m.logf("[team] approval %s cancelled at its auto-approve: origin %s is a member of a live team", a.ID, a.Origin.Ref)
			return false, false
		default:
			m.forgetRefusal(a.ID)
			m.logf("[team] approval %s approved by unattended (origin %s)%s", a.ID, a.Origin.Ref, teamNote(after))
			return true, false
		}
	}
	if m.notAutoApproved[a.ID] != err.Error() {
		m.rememberRefusal(a.ID, err.Error())
		m.logf("[team] approval %s not auto-approved: %v", a.ID, err)
	}
	return false, true
}

// rememberRefusal records reason as the one logged for open row id, and
// forgetRefusal drops it; both republish the set's size
// (notAutoApprovedN) for the tick's check without createMu. Caller holds
// createMu.
func (m *Module) rememberRefusal(id, reason string) {
	if m.notAutoApproved == nil {
		m.notAutoApproved = map[string]string{}
	}
	m.notAutoApproved[id] = reason
	m.notAutoApprovedN.Store(int64(len(m.notAutoApproved)))
}

func (m *Module) forgetRefusal(id string) {
	delete(m.notAutoApproved, id)
	m.notAutoApprovedN.Store(int64(len(m.notAutoApproved)))
}

// forgetClosedRefusals forgets every refusal whose row is not in open, a
// list read under createMu: that row closed, and a closed row never
// reopens. Caller holds createMu.
func (m *Module) forgetClosedRefusals(open []team.Approval) {
	ids := make(map[string]bool, len(open))
	for _, a := range open {
		ids[a.ID] = true
	}
	for id := range m.notAutoApproved {
		if !ids[id] {
			m.forgetRefusal(id)
		}
	}
}

// sweepTick is sweepUnattended's why for the tick's reconciliation, whose
// summary line is logged only when it approved something (a row a rule
// refuses is retried every second).
const sweepTick = "tick"

// sweepUnattended approves every open AutoApprovable row, oldest first
// (D-U23-3; hook kinds are left open): at switch-on (PU-1c), every tick
// while the switch is on (reconcileUnattended) and at boot. pending counts
// the ones still open, which the next tick tries again (decision 5).
// Caller holds createMu and has read the switch on.
func (m *Module) sweepUnattended(why string) (approved, pending int) {
	open, err := m.store.ListOpen()
	if err != nil {
		m.logf("[team] unattended sweep (%s): %v", why, err)
		return 0, 0
	}
	for _, a := range open {
		if !team.AutoApprovable(a.Kind) {
			continue
		}
		if ok, still := m.autoApprove(a); ok {
			approved++
		} else if still {
			pending++
		}
	}
	m.forgetClosedRefusals(open)
	if why != sweepTick || approved > 0 {
		m.logf("[team] unattended sweep (%s): approved %d, still open %d", why, approved, pending)
	}
	return approved, pending
}

// reconcileUnattended is the sweeper's half of decision 5 (rule 6): while
// the switch is on, every tick approves the AutoApprovable rows still
// open, so nothing the switch-on sweep or a create-time approve could not
// approve (a transient storage error, a lost race) stays open. open is
// the tick's own list, read before its closes: it only says whether a
// sweep is worth createMu; the sweep re-reads what is still open. The
// switch is read under createMu, as every caller of unattendedOn does.
// A tick that does not sweep — nothing AutoApprovable open (none open at
// all, too), or the switch off — still forgets the refusals of rows closed
// since (review M2), against a list re-read under createMu; it takes
// createMu for that only while some refusal is remembered.
func (m *Module) reconcileUnattended(open []team.Approval) {
	sweep := slices.ContainsFunc(open, func(a team.Approval) bool { return team.AutoApprovable(a.Kind) })
	if !sweep && m.notAutoApprovedN.Load() == 0 {
		return
	}
	m.createMu.Lock()
	defer m.createMu.Unlock()
	if sweep && !m.stopping() && m.unattendedOn() {
		m.sweepUnattended(sweepTick) // forgets the closed rows' refusals too
		return
	}
	if len(m.notAutoApproved) == 0 {
		return
	}
	still, err := m.store.ListOpen()
	if err != nil { // the next tick tries again; the tick's own read logs a failing store
		return
	}
	m.forgetClosedRefusals(still)
}
