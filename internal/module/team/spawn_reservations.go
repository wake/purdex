package teammod

import (
	"context"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// spawnReservationReader is the team.SpawnReservationsKey service (name pool v2 spec §5.2): the open spawn ops that carry a
// reserved member name, which the peers name service reads once after a boot to rebuild its in-memory reservations. The name
// service calls it with its pool lock held, so it reads team.db and nothing else (never the name service, never any peers
// lock), and it stops with its context.
type spawnReservationReader struct{ s *Store }

// OpenReservations implements ipeers.SpawnReservationReader.
func (r spawnReservationReader) OpenReservations(ctx context.Context) ([]ipeers.Reservation, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT id, pool_name, tmux_name, created_at FROM spawn_ops
		WHERE pool_name <> '' AND state IN (?, ?) ORDER BY created_at, id`, string(team.SpawnRunning), string(team.SpawnAwaitingResources))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ipeers.Reservation
	for rows.Next() {
		var res ipeers.Reservation
		var at int64
		if err := rows.Scan(&res.OpID, &res.Code, &res.TmuxName, &at); err != nil {
			return nil, err
		}
		res.CreatedAt = time.UnixMilli(at)
		out = append(out, res)
	}
	return out, rows.Err()
}
