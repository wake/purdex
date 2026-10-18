package store

import (
	"encoding/json"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// MinuteRetention is how long the per-minute rollups are kept (the same 14 days
// as the retired resources.db host_minutes).
const MinuteRetention = 14 * 24 * time.Hour

// SaveMinute stores one finished minute's rollup (keyed by its start, unix ms;
// the same minute replaces) and drops the minutes older than the retention,
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
		ON CONFLICT(at) DO UPDATE SET rec = excluded.rec`, at, string(b)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM host_minutes WHERE at < ?`, at-MinuteRetention.Milliseconds()); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadMinutes returns the stored rollups at or after since, oldest first. A row
// that no longer decodes is skipped: one bad minute must not hide the rest.
func (s *Store) LoadMinutes(since time.Time) ([]wire.Facts, error) {
	rows, err := s.db.Query(`SELECT rec FROM host_minutes WHERE at >= ? ORDER BY at`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []wire.Facts
	for rows.Next() {
		var rec string
		if err := rows.Scan(&rec); err != nil {
			return nil, err
		}
		var f wire.Facts
		if json.Unmarshal([]byte(rec), &f) == nil {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}
