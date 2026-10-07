package teammod

import (
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ErrMemberRelayIsLeads is returned by CreateSelfRelayApproved when the
// origin is an active member of a live team (U13): a member's relay is the
// lead's, so nothing was written.
var ErrMemberRelayIsLeads = errors.New("the session is an active member of a live team: its relay is the lead's")

// CreateSelfRelayApproved is the begin of a self relay the daemon approves
// itself (U23 unattended), CreateApproved's counterpart for the op and its
// row: in one write transaction it inserts op (awaiting_approval) and a
// (open), runs the click's approve statements (closeSelfRelayApprovedIn)
// and moves the op to what the approved row implies (claimed), so neither
// the op nor the row is ever committed open. op must be a's (RequestID =
// a.ID) and awaiting approval. A session that became an active member of a
// live team rolls everything back with ErrMemberRelayIsLeads (U13); a
// session with an open op, with ErrRelayOpOpen (the table's floor beneath
// the begin's check); any other failure, or a close that is no approval,
// writes nothing either. It returns the row and the op as committed.
func (s *Store) CreateSelfRelayApproved(op team.RelayOp, a team.Approval, hash string, c Close) (team.Approval, team.RelayOp, error) {
	fail := func(err error) (team.Approval, team.RelayOp, error) {
		return team.Approval{}, team.RelayOp{}, fmt.Errorf("create approved self relay %s: %w", a.ID, err)
	}
	if a.Kind != team.KindSelfRelay || op.RequestID != a.ID || op.State != team.RelayAwaitingApproval {
		return fail(fmt.Errorf("op %s (request %q, %s) is not the awaiting op of %s row %s", op.ID, op.RequestID, op.State, a.Kind, a.ID))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at once.
	if err := insertRelayOpIn(tx, op); err != nil {
		return fail(err)
	}
	if _, err := insertRowIn(tx, a, hash, ""); err != nil {
		return fail(err)
	}
	if err := s.atApprovedInsert(tx); err != nil {
		return fail(err)
	}
	if _, member, err := s.closeSelfRelayApprovedIn(tx, a.ID, c, a.Origin.SessionID); err != nil {
		return fail(err)
	} else if member {
		return fail(ErrMemberRelayIsLeads)
	}
	after, err := approvedRowIn(tx, a.ID)
	if err != nil {
		return fail(err)
	}
	claimed, res, err := reportRelayIn(tx, op.ID, opReportForClosedRow(after, c.DecidedAt))
	if err != nil {
		return fail(err)
	}
	if res != ReportApplied {
		return fail(fmt.Errorf("op %s is %s; the approve could not claim it", op.ID, claimed.State))
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return after, claimed, nil
}
