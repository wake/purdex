package resourcesmod

import (
	"fmt"
)

// The results that settle a notice slot for good (plan §2.10): nothing more is tried for it. Any other result, or none,
// leaves it pending.
const (
	noticeStale          = "stale"                   // no longer current when it came to be sent
	noticeSessionChanged = "dropped:session_changed" // the session id changed (/clear): liveness ends the position
	noticeUserAlert      = "user_alert"              // the fallback told the person instead (P2d-2): not a delivery
	noticeMember         = "member"                  // the fallback delivered it to the member's lead channel (P2d-2)
	// noticeMemberSending is NOT a settling result: the slot is claimed for one send to the member's lead, and stays pending
	// (a failed record of that send is finished by the next try, never sent twice).
	noticeMemberSending = "member_sending"
)

// PendingNoticePositions are the positions whose notice slot is armed and neither sent nor settled: the outbox's work.
// A restart finds them here, which is the replay (§2.10). An ended position is included (its cancelled / drained notice).
func (s *leaseStore) PendingNoticePositions() ([]positionRow, error) {
	rows, err := s.db.Query(`SELECT `+positionCols+` FROM resource_positions
		WHERE notice_kind != '' AND notice_sent_at = 0 AND notice_result NOT IN (?, ?, ?, ?)
		ORDER BY notice_due_at, id`, noticeStale, noticeSessionChanged, noticeUserAlert, noticeMember)
	if err != nil {
		return nil, fmt.Errorf("list pending notices: %w", err)
	}
	defer rows.Close()
	var out []positionRow
	for rows.Next() {
		p, err := scanPosition(rows)
		if err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// BumpNoticeTries counts one more try of the slot (an unknown answer: the notice may or may not have reached the
// session), recording the result. The slot is named by what armed it; a slot that has changed since is left alone.
func (s *leaseStore) BumpNoticeTries(id, kind string, dueAt int64, result string) (bool, error) {
	return s.changed(`UPDATE resource_positions SET notice_tries = notice_tries + 1, notice_result = ?
		WHERE id = ? AND notice_kind = ? AND notice_due_at = ?`, result, id, kind, dueAt)
}
