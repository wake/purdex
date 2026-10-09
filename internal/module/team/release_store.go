package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// Release and the notice outbox in the store (adopt plan PL-1b2, adopt spec D-U24-3).

// ReleaseMember lets an ACTIVE member go (pdx release): in one guarded UPDATE the row becomes `released` with
// ended_at, and the `released` notice is owed to its session (superseding an `adopted` notice not yet sent).
// The guard is in the statement, as MarkMemberGone's is: the row still holds sessionID (a relay moved it
// otherwise), is active, and the session has NO relay op in any non-terminal state (awaiting_approval,
// requested, claimed, writing, written): a member is not released in the middle of its own relay, the lead
// answers relay_open instead. The `released` notice is addressed by the row's ref, which a relay of the session
// later redirects through lineage (the old ref still reaches it), so a relay right after the release loses
// nothing. released says whether this call did it; a false answer leaves the row as it is.
func (s *Store) ReleaseMember(rowKey, sessionID string, at int64) (released bool, err error) {
	res, err := s.db.Exec(`UPDATE team_members
		SET state = ?, ended_at = ?, updated_at = ?, notice_pending = ?, notice_since = ?
		WHERE spawn_op = ? AND session_id = ? AND state = 'active'
		  AND NOT EXISTS (SELECT 1 FROM relay_ops WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled'))`,
		string(team.MemberReleased), at, at, team.NoticeReleased, at, rowKey, sessionID, sessionID)
	return oneRow(res, err, "release member "+rowKey)
}

// PendingNotices are the rows that owe their session a notice, oldest first: an `adopted` notice while the row
// is still active, and a `released` notice whatever happened to the row since (the session is told it left,
// even if it was then marked gone).
func (s *Store) PendingNotices() ([]memberRow, error) {
	return s.queryMembers("pending notices", `SELECT `+memberCols+`, `+memberUsageCols+`
		FROM team_members
		WHERE (notice_pending = ? AND state = 'active') OR notice_pending = ?
		ORDER BY notice_since, spawn_op`, team.NoticeAdopted, team.NoticeReleased)
}

// DropStaleAdoptNotices clears the `adopted` notice of every row that is no longer active (killed or gone
// before it was sent): the session is no member any more, so the notice is not owed and PendingNotices would
// never read it again. n is how many rows it cleared.
func (s *Store) DropStaleAdoptNotices() (n int64, err error) {
	res, err := s.db.Exec(`UPDATE team_members SET notice_pending = '', notice_since = 0
		WHERE notice_pending = ? AND state <> 'active'`, team.NoticeAdopted)
	if err != nil {
		return 0, fmt.Errorf("drop stale adopt notices: %w", err)
	}
	return res.RowsAffected()
}

// ClearNotice clears the notice of kind owed since `since` on the row, once it was sent. It is conditional on
// both, so a newer notice (a release after an adoption) is never cleared by the older one's send. cleared
// says whether this call changed the row.
func (s *Store) ClearNotice(rowKey, kind string, since int64) (cleared bool, err error) {
	res, err := s.db.Exec(`UPDATE team_members SET notice_pending = '', notice_since = 0
		WHERE spawn_op = ? AND notice_pending = ? AND notice_since = ?`, rowKey, kind, since)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return oneRow(res, err, fmt.Sprintf("clear the %s notice of %s", kind, rowKey))
}
