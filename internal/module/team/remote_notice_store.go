// internal/module/team/remote_notice_store.go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"
)

// DueRemoteNotices lists the owed notices whose next try has come (spec §4.4), oldest first.
func (s *Store) DueRemoteNotices(now int64) ([]remoteNoticeRow, error) {
	rows, err := s.db.Query(`SELECT id, mk, kind, cause_id, lead_address, team_name, state, attempts, next_at FROM remote_notices
		WHERE state = ? AND next_at <= ? ORDER BY id LIMIT 200`, noticeOwed, now)
	if err != nil {
		return nil, fmt.Errorf("due remote notices: %w", err)
	}
	defer rows.Close()
	out := []remoteNoticeRow{}
	for rows.Next() {
		var n remoteNoticeRow
		if err := rows.Scan(&n.ID, &n.MK, &n.Kind, &n.CauseID, &n.LeadAddress, &n.TeamName, &n.State, &n.Attempts, &n.NextAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SettleRemoteNotice moves an owed notice to a final state (sent | superseded); one that is no longer owed is left
// alone and reports false.
func (s *Store) SettleRemoteNotice(id int64, to string, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE remote_notices SET state = ?, updated_at = ? WHERE id = ? AND state = ?`, to, at, id, noticeOwed)
	if err != nil {
		return false, fmt.Errorf("settle remote notice %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RetryRemoteNotice records a failed try of an owed notice: the attempt count and when to try next.
func (s *Store) RetryRemoteNotice(id int64, attempts int, nextAt, at int64) error {
	if _, err := s.db.Exec(`UPDATE remote_notices SET attempts = ?, next_at = ?, updated_at = ? WHERE id = ? AND state = ?`,
		attempts, nextAt, at, id, noticeOwed); err != nil {
		return fmt.Errorf("retry remote notice %d: %w", id, err)
	}
	return nil
}

// RemoteMemberLive says whether a session of another host is a member in play (joining / active / releasing / killing) of
// a team led from this host.
func (s *Store) RemoteMemberLive(hostID, sessionID string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM team_members WHERE host_id = ? AND session_id = ? AND state IN `+liveRemoteStates, hostID, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("remote member %s/%s: %w", hostID, sessionID, err)
	}
	return true, nil
}
