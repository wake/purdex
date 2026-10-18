package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// MinuteRetention is how long the per-minute rollups are kept (the same 14 days
// as the retired resources.db host_minutes).
const MinuteRetention = 14 * 24 * time.Hour

// SaveMinute stores one finished minute's rollup (keyed by its start, unix ms;
// the first write of a minute stays, so a clock set back and a restart never
// replace a whole minute with a part of it) and drops the minutes older than the retention,
// counted from this one.
func (s *Store) SaveMinute(f wire.Facts) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	at := f.At.Truncate(time.Minute).UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO host_minutes (at, rec) VALUES (?, ?)
		ON CONFLICT(at) DO NOTHING`, at, string(b)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM host_minutes WHERE at < ?`, at-MinuteRetention.Milliseconds()); err != nil {
		return err
	}
	return tx.Commit()
}

// SkippedRowsError says LoadMinutes (or LoadObservations) left out N rows that did not decode; the
// records returned with it are the rest.
type SkippedRowsError struct {
	N     int
	Table string // the table the rows are in
}

func (e *SkippedRowsError) Error() string {
	return fmt.Sprintf("store: %d %s rows do not decode and were skipped", e.N, e.Table)
}

// LoadMinutes returns the stored rollups at or after since, oldest first. A row
// that no longer decodes is skipped, so one bad minute does not hide the rest,
// and reported as a *SkippedRowsError next to the records.
func (s *Store) LoadMinutes(since time.Time) ([]wire.Facts, error) {
	rows, err := s.db.Query(`SELECT rec FROM host_minutes WHERE at >= ? ORDER BY at`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wire.Facts
	skipped := 0
	for rows.Next() {
		var rec string
		if err := rows.Scan(&rec); err != nil {
			return nil, err
		}
		var f wire.Facts
		if json.Unmarshal([]byte(rec), &f) != nil {
			skipped++
			continue
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if skipped > 0 {
		return out, &SkippedRowsError{N: skipped, Table: "host_minutes"}
	}
	return out, nil
}
