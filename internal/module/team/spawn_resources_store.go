package teammod

// RT-5a: the store side of an op that waits for its spawn_resources card (spec §4.6).

import (
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// spawnCardReasons are the failure reasons a spawn_resources card gives an op. They are not spawnReasons: those are what a
// runner or a forwarded fact may report, and a card's close is neither.
var spawnCardReasons = map[string]bool{
	team.SpawnReasonDenied: true, team.SpawnReasonTimeout: true, team.SpawnReasonCLIGone: true, team.ErrUnattendedRefused: true,
}

// SettleAwaitingSpawn moves an op out of awaiting_resources, once: approve → running (its runner starts), otherwise failed with
// reason (one of spawnCardReasons), which frees its seat. won is false when the op is not awaiting any more (settled before, or
// not such an op): the caller does nothing further.
func (s *Store) SettleAwaitingSpawn(id string, approve bool, reason string, at int64) (bool, error) {
	if approve {
		res, err := s.db.Exec(`UPDATE spawn_ops SET state = 'running', updated_at = ? WHERE id = ? AND state = 'awaiting_resources' AND step = ?`,
			at, id, team.StepAccepted)
		return oneRow(res, err, "approve awaiting spawn op "+id)
	}
	if !spawnCardReasons[reason] {
		return false, fmt.Errorf("settle awaiting spawn op %s: unknown reason %q", id, reason)
	}
	res, err := s.db.Exec(`UPDATE spawn_ops SET state = 'failed', reason = ?, updated_at = ? WHERE id = ? AND state = 'awaiting_resources'`,
		reason, at, id)
	return oneRow(res, err, "fail awaiting spawn op "+id)
}

// OrphanSpawnCards are the open spawn_resources cards whose op is not awaiting any more (its team ended and failed it, or the
// row is gone): nothing is left for the card to let through.
func (s *Store) OrphanSpawnCards() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM approval_requests WHERE kind = ? AND state = 'open'
		AND id NOT IN (SELECT id FROM spawn_ops WHERE state = 'awaiting_resources')`, string(team.KindSpawnResources))
	if err != nil {
		return nil, fmt.Errorf("list orphan spawn cards: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list orphan spawn cards: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AwaitingSpawnOps are the ops that wait for a card, oldest first: boot settles those whose card closed while the daemon was
// down or between the card's close and its follow-up.
func (s *Store) AwaitingSpawnOps() ([]spawnRow, error) {
	rows, err := s.db.Query(`SELECT ` + spawnCols + ` FROM spawn_ops WHERE state = 'awaiting_resources' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list awaiting spawn ops: %w", err)
	}
	defer rows.Close()
	out := []spawnRow{}
	for rows.Next() {
		var r spawnRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("list awaiting spawn ops: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
