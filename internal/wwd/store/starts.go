package store

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/wwd/wire"
)

// StartsRetention is how long start records are kept: a short history for the crash-loop rule (spec §13.3), not a log.
const StartsRetention = 7 * 24 * time.Hour

// RecordStart records that wwd started now (spec §13.1). version is the build; prevExit is how the run before it ended when that is
// known ("" when not). Both are cut to one line of plain text: a start must always be recorded.
func (s *Store) RecordStart(version, prevExit string) error {
	return s.RecordStartAt(time.Now(), version, prevExit)
}

// RecordStartAt is RecordStart at a given time. It also drops the records older than StartsRetention.
func (s *Store) RecordStartAt(at time.Time, version, prevExit string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// OR IGNORE on the unique started_ms: a write tried again with the same stamp (the start-up retry) is not a second start
	if _, err := tx.Exec(`INSERT OR IGNORE INTO wwd_starts (started_ms, version, prev_exit) VALUES (?, ?, ?)`,
		at.UnixMilli(), oneLine(version, 80), oneLine(prevExit, wire.MaxAlertDetailBytes)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM wwd_starts WHERE started_ms < ?`, at.Add(-StartsRetention).UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

// StartsSince is the number of recorded starts at or after since.
func (s *Store) StartsSince(since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM wwd_starts WHERE started_ms >= ?`, since.UnixMilli()).Scan(&n)
	return n, err
}

// StartTimes are the recorded starts at or after since, oldest first.
func (s *Store) StartTimes(since time.Time) ([]time.Time, error) {
	rows, err := s.db.Query(`SELECT started_ms FROM wwd_starts WHERE started_ms >= ? ORDER BY started_ms`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var ms int64
		if err := rows.Scan(&ms); err != nil {
			return nil, err
		}
		out = append(out, time.UnixMilli(ms).UTC())
	}
	return out, rows.Err()
}

// oneLine drops control characters and cuts s to at most max bytes without splitting a character.
func oneLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}
