package store

import (
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ObservationRetention is how long finished observations are kept.
const ObservationRetention = 14 * 24 * time.Hour

// SaveObservation stores one finished observation (keyed by its id; the same one again changes nothing) and drops those that began
// more than the retention before this one. An invalid observation is refused: the table holds only what a reader can decode.
func (s *Store) SaveObservation(o wire.Observation) error {
	if err := o.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	started := o.StartedAt.UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO observations (id, started_ms, rec) VALUES (?, ?, ?) ON CONFLICT(id) DO NOTHING`, o.ID, started, string(b)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM observations WHERE started_ms < ?`, started-ObservationRetention.Milliseconds()); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadObservations returns the stored observations that began at or after since, oldest first. A row that no longer decodes or
// validates is skipped, so one bad row does not hide the rest, and reported as a *SkippedRowsError next to the records.
func (s *Store) LoadObservations(since time.Time) ([]wire.Observation, error) {
	rows, err := s.db.Query(`SELECT rec FROM observations WHERE started_ms >= ? ORDER BY started_ms, id`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wire.Observation
	skipped := 0
	for rows.Next() {
		var rec string
		if err := rows.Scan(&rec); err != nil {
			return nil, err
		}
		o, err := wire.Decode[wire.Observation]([]byte(rec))
		if err != nil {
			skipped++
			continue
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if skipped > 0 {
		return out, &SkippedRowsError{N: skipped, Table: "observations"}
	}
	return out, nil
}
