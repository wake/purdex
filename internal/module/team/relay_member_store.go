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
	return s.createMemberRelayOp(op, gate, nil)
}

// CreateRemoteMemberRelayOp is CreateMemberRelayOp for a member that lives on another host (member relay spec §3.4): the
// membership read accepts the remote row of op.HostID, the op is inserted `forwarded` and enqueue — the `relay` command —
// runs in the SAME transaction, after the gate (which may spend the pool) and the insert. Any failure writes nothing and spends
// nothing. A gate that holds the op for a card leaves it `awaiting_approval` with no command: the approve forwards it
// (CloseMemberRelayApproved).
func (s *Store) CreateRemoteMemberRelayOp(op team.RelayOp, gate MemberRelayGate, enqueue func(tx *sql.Tx, op team.RelayOp, at int64) error) (team.RelayOp, bool, error) {
	return s.createMemberRelayOp(op, gate, enqueue)
}

func (s *Store) createMemberRelayOp(op team.RelayOp, gate MemberRelayGate, enqueue func(tx *sql.Tx, op team.RelayOp, at int64) error) (team.RelayOp, bool, error) {
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
	loc, locArgs := s.local("m.host_id")
	if enqueue != nil { // the member's row is the remote one of op.HostID
		loc, locArgs = `m.host_id = ? AND m.host_id <> ?`, []any{op.HostID, s.localHostID}
	}
	err = tx.QueryRow(`SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.team_id = ? AND m.state = 'active' AND t.ended_at = 0 AND `+loc, append([]any{op.SessionID, op.TeamID}, locArgs...)...).Scan(&one)
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
	forward := enqueue != nil && !needs // a held op waits for its card; the approve forwards it
	if forward {
		op.State = team.RelayForwarded
	}
	if err := insertRelayOpIn(tx, op); err != nil {
		return fail(err)
	}
	if forward {
		if err := s.enqueueRelayIn(tx, enqueue, op, op.CreatedAt); err != nil {
			return fail(err)
		}
	}
	if s.afterMemberOpInsert != nil {
		if err := s.afterMemberOpInsert(); err != nil {
			return fail(err)
		}
	}
	// The lead's relay is the answer to an open ask (member relay ask §3.2): in this transaction, so a failure anywhere
	// above or below leaves no op, no spent pool and the ask open. A window that has passed is no longer an ask (D9).
	if _, err := tx.Exec(`UPDATE relay_asks SET state = 'accepted', op_id = ?, closed_at = ? WHERE session_id = ? AND state = 'open' AND expires_at > ?`,
		op.ID, op.CreatedAt, op.SessionID, op.CreatedAt); err != nil {
		return fail(err)
	}
	if s.afterAskAccept != nil {
		if err := s.afterAskAccept(tx); err != nil {
			return fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	s.notifyOp(op.ID)
	return op, needs, nil
}

// notifyOp tells the module an op changed, after the commit that changed it.
func (s *Store) notifyOp(opID string) {
	if s.opChanged != nil && opID != "" {
		s.opChanged(opID)
	}
}

// MarkRelaySeen records that the member's mod saw the op's control message: seen_at = at, once, while the op is still
// requested (a compare-and-set on seen_at = 0). It touches nothing else — updated_at stays, so the unseen timer's
// anchor (the entry into requested, P6-4b) is not moved by it. seen says whether this call set it; the op is returned
// as it is after the call.
func (s *Store) MarkRelaySeen(id string, at int64) (op team.RelayOp, seen bool, err error) {
	res, err := s.db.Exec(`UPDATE relay_ops SET seen_at = ? WHERE id = ? AND kind = 'member' AND state = 'requested' AND seen_at = 0`, at, id)
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("mark relay %s seen: %w", id, err)
	}
	n, _ := res.RowsAffected()
	op, ok, err := s.GetRelayOp(id)
	if err == nil && !ok {
		err = ErrNoSuchRelayOp
	}
	if err != nil {
		return team.RelayOp{}, false, err
	}
	if n == 1 {
		s.notifyOp(id)
	}
	return op, n == 1, nil
}

// enqueueRelayIn runs the `relay` command's enqueue in tx (the op's transaction), then the test seam.
func (s *Store) enqueueRelayIn(tx *sql.Tx, enqueue func(tx *sql.Tx, op team.RelayOp, at int64) error, op team.RelayOp, at int64) error {
	if err := enqueue(tx, op, at); err != nil {
		return err
	}
	if s.afterRelayCommandEnqueue != nil {
		return s.afterRelayCommandEnqueue()
	}
	return nil
}

// isRemoteOp says whether the op is a lead's relay of a member that lives on another host.
func (s *Store) isRemoteOp(op team.RelayOp) bool {
	return op.HostID != "" && s.localHostID != "" && op.HostID != s.localHostID
}

// forwardAwaitingOpIn moves a remote op awaiting_approval → forwarded and queues its `relay` command, in tx. at is when the
// command is queued (its created_at, which the member host's age check reads).
func (s *Store) forwardAwaitingOpIn(tx *sql.Tx, forward func(tx *sql.Tx, op team.RelayOp, at int64) error, op team.RelayOp, at int64) error {
	if forward == nil {
		return fmt.Errorf("op %s is a remote member's: it needs its relay command to be forwarded", op.ID)
	}
	r, err := tx.Exec(`UPDATE relay_ops SET state = 'forwarded', updated_at = ? WHERE id = ? AND state = 'awaiting_approval'`, at, op.ID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return fmt.Errorf("op %s is no longer awaiting approval", op.ID)
	}
	return s.enqueueRelayIn(tx, forward, op, at)
}

// ForwardAwaitingOp is the boot's repair of a remote op whose approved row did not carry it along: forwarded, with its command.
func (s *Store) ForwardAwaitingOp(op team.RelayOp, forward func(tx *sql.Tx, op team.RelayOp, at int64) error, at int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.forwardAwaitingOpIn(tx, forward, op, at); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notifyOp(op.ID)
	return nil
}
