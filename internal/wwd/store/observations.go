package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ObservationRetention is how long finished observations are kept.
const ObservationRetention = 14 * 24 * time.Hour

// observationFutureSlack is how far past now an observation may claim to have begun (clock steps between hosts).
const observationFutureSlack = 24 * time.Hour

// SaveObservation is SaveObservationAt with the current time.
func (s *Store) SaveObservation(o wire.Observation) error { return s.SaveObservationAt(o, time.Now()) }

// SaveObservationAt stores one finished observation (keyed by its id; the same one again changes nothing) and drops those that
// began more than the retention before now. The cutoff comes from now, not from the observation: a wrong start time cannot erase
// the history, and one older than the retention is not stored. An invalid observation, or one that begins more than a day after
// now, is refused: the table holds only what a reader can decode.
func (s *Store) SaveObservationAt(o wire.Observation, now time.Time) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if o.StartedAt.After(now.Add(observationFutureSlack)) {
		return fmt.Errorf("store: observation %s begins in the future (%s)", o.ID, o.StartedAt.Format(time.RFC3339))
	}
	cutoff := now.Add(-ObservationRetention).UnixMilli()
	started := o.StartedAt.UnixMilli()
	if started < cutoff {
		return nil // already past the retention
	}
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO observations (id, started_ms, rec) VALUES (?, ?, ?) ON CONFLICT(id) DO NOTHING`, o.ID, started, string(b)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM observations WHERE started_ms < ?`, cutoff); err != nil {
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
		o, err := wire.DecodeLenient[wire.Observation]([]byte(rec))
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
