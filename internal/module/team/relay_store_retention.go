package teammod

import (
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// ListUnprunedRelayOps returns every op whose file has not been pruned, in
// any state, oldest first. The retention sweeper's input.
func (s *Store) ListUnprunedRelayOps() ([]team.RelayOp, error) {
	rows, err := s.db.Query(`SELECT ` + relayCols + ` FROM relay_ops WHERE pruned = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list unpruned relay ops: %w", err)
	}
	defer rows.Close()
	out := []team.RelayOp{}
	for rows.Next() {
		op, err := scanRelayOp(rows)
		if err != nil {
			return nil, fmt.Errorf("list unpruned relay ops: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// MarkRelayPruned records that op's handoff file is gone (spec §8.3
// retention: "the row keeps the path, marked pruned").
func (s *Store) MarkRelayPruned(id string) error {
	// Only an op that has ended is ever pruned: the guard beside the rule in
	// retentionVictims, so a row that is (somehow) still in flight keeps
	// pruned = 0 and stays visible to the next sweep.
	if _, err := s.db.Exec(`UPDATE relay_ops SET pruned = 1 WHERE id = ? AND state IN ('done', 'failed', 'cancelled')`, id); err != nil {
		return fmt.Errorf("mark relay op %s pruned: %w", id, err)
	}
	return nil
}
