package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ErrMemberNotActive is returned by CreateMemberRelayOp when the target's member row is no longer an active row of a
// live team (released, killed, gone, or its team ended): nothing was inserted.
var ErrMemberNotActive = errors.New("member is not active in a live team")

// MemberRelayGate runs inside CreateMemberRelayOp's transaction, after the membership confirmation and before the
// insert. It may adjust op (RQ-2: a spent pool turns it into awaiting_approval with its row) and write through tx;
// needsApproval is informational for the caller. An error rolls everything back. P6-2b-1 passes nil: always requested.
type MemberRelayGate func(tx *sql.Tx, op *team.RelayOp) (needsApproval bool, err error)

// CreateMemberRelayOp inserts a member op in ONE write transaction (alignment §4 item 1): confirm that the op's
// session is still an active member of the live team op.TeamID, run the gate, insert the op. The confirmation and the
// insert share the transaction (team.db has one connection, so nothing else commits between them), which is what
// makes it exclusive with ReleaseMember — a conditional UPDATE that refuses while any relay op of the session is
// open: a release first → ErrMemberNotActive here; this op first → the release finds it and answers relay_open.
// ErrRelayOpOpen (wrapped) is the table's one-open-op floor. Any failure writes nothing.
func (s *Store) CreateMemberRelayOp(op team.RelayOp, gate MemberRelayGate) (team.RelayOp, bool, error) {
	fail := func(err error) (team.RelayOp, bool, error) {
		return team.RelayOp{}, false, fmt.Errorf("create member relay %s: %w", op.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the membership read (as closeSelfRelayApprovedIn does): a
	// release or an EndTeam in another connection cannot slip between the read and the insert.
	if _, err := tx.Exec(`UPDATE team_members SET state = state WHERE session_id = ? AND team_id = ?`, op.SessionID, op.TeamID); err != nil {
		return fail(err)
	}
	var one int
	err = tx.QueryRow(`SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.team_id = ? AND m.state = 'active' AND t.ended_at = 0`, op.SessionID, op.TeamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(ErrMemberNotActive)
	}
	if err != nil {
		return fail(err)
	}
	needs := false
	if gate != nil {
		if needs, err = gate(tx, &op); err != nil {
			return fail(err)
		}
	}
	if err := insertRelayOpIn(tx, op); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return op, needs, nil
}
