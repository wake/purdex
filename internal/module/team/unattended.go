package teammod

import (
	"encoding/json"
	"fmt"

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
	return team.Grant{MaxMembers: p.MaxMembers, Roots: p.Roots}, nil
}

// leadTeamOf is the team a lead row's approval creates (spec §7.1); its id
// is the request's id (plan v3 deviation 1).
func leadTeamOf(a team.Approval, g team.Grant, at int64) team.Team {
	return team.Team{ID: a.ID, HostID: a.HostID, LeadSessionID: a.Origin.SessionID, LeadRef: a.Origin.Ref,
		Grant: g, RequestID: a.ID, CreatedAt: at}
}

// teamNote is the decision log line's suffix for an approval that created
// a team ("" for none).
func teamNote(a team.Approval) string {
	if a.Kind != team.KindLead || a.State != team.StateApproved || a.Grant == nil {
		return ""
	}
	return fmt.Sprintf("; team %s created (max_members %d, roots %v)", a.ID, a.Grant.MaxMembers, a.Grant.Roots)
}

// afterApproved runs once for every close that won as approved, on every
// path — a click, create-time, the switch-on sweep, the tick, boot
// (announceClosed: the plan's one winner point). Later side effects of an
// approval (PL-1c's adopt notice, PL-1f's roster event) hang here, never
// on a route. Nothing yet.
func (m *Module) afterApproved(team.Approval) {}
