package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ActiveAlerts are the host's alerts that are not cleared, ordered by key. It is never nil: an empty table is an empty list. A row
// that is not a valid wire.Alert (a bad key, text that is not text) is an error, not a silent gap: the reader would otherwise show a
// quieter host than it is. The one exception is a kind or level this build does not know (S3-3a): a row a later release wrote and this
// one was started back on, which is left out and said once in the log (SetLogf), the rest of the list being what it is.
func (s *Store) ActiveAlerts() ([]wire.Alert, error) {
	rows, err := s.db.Query(`SELECT key, kind, level, since_ms, last_ms, detail FROM alerts WHERE cleared_ms IS NULL ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wire.Alert{}
	for rows.Next() {
		var a wire.Alert
		var kind, level, detail string
		var since, last int64
		if err := rows.Scan(&a.Key, &kind, &level, &since, &last, &detail); err != nil {
			return nil, err
		}
		a.Kind, a.Level = wire.AlertKind(kind), wire.AlertLevel(level)
		a.Detail = wire.TruncateDetail(detail) // a text over the limit is cut on the way out, not a reason to hide the alert
		a.Since, a.Last = time.UnixMilli(since).UTC(), time.UnixMilli(last).UTC()
		if err := a.Validate(); err != nil {
			if wire.IsUnknownAlert(err) {
				if what, first := s.alertSkips.First(a, err); first {
					s.logf("store: the alerts table has a row (key %q) that this build does not know, left out of the list: %s (said once)", a.Key, what)
				}
				continue
			}
			return nil, fmt.Errorf("store: alerts row %q: %w", a.Key, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AlertsRetention is how long a cleared alert's row is kept: a short history, not a log.
const AlertsRetention = 7 * 24 * time.Hour

// RaiseAlert records that a rule holds at the given time (spec §13.3) and reports whether anything a reader would notice changed:
// the alert is new (or was cleared and now holds again, which starts it over), or its level, kind or detail is different. An alert
// that is active and unchanged only has its "last" moved forward, and is not a change. The detail is cut to what a push can carry. The
// rows of alerts cleared more than AlertsRetention before at are dropped in the same transaction.
func (s *Store) RaiseAlert(a wire.Alert, at time.Time) (changed bool, err error) {
	a.Detail = wire.TruncateDetail(a.Detail)
	a.Since, a.Last, a.ClearedAt = at, at, nil
	if err := a.Validate(); err != nil {
		return false, err
	}
	s.alertMu.Lock()
	defer s.alertMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var kind, level, detail string
	var last int64
	var cleared sql.NullInt64
	switch err := tx.QueryRow(`SELECT kind, level, detail, last_ms, cleared_ms FROM alerts WHERE key = ?`, a.Key).Scan(&kind, &level, &detail, &last, &cleared); {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO alerts (key, kind, level, since_ms, last_ms, cleared_ms, detail) VALUES (?, ?, ?, ?, ?, NULL, ?)`,
			a.Key, string(a.Kind), string(a.Level), at.UnixMilli(), at.UnixMilli(), a.Detail); err != nil {
			return false, err
		}
		changed = true
	case err != nil:
		return false, err
	case cleared.Valid: // it holds again: a new alert with a new start
		if _, err := tx.Exec(`UPDATE alerts SET kind = ?, level = ?, detail = ?, since_ms = ?, last_ms = ?, cleared_ms = NULL WHERE key = ?`,
			string(a.Kind), string(a.Level), a.Detail, at.UnixMilli(), at.UnixMilli(), a.Key); err != nil {
			return false, err
		}
		changed = true
	default:
		changed = kind != string(a.Kind) || level != string(a.Level) || detail != a.Detail
		if _, err := tx.Exec(`UPDATE alerts SET kind = ?, level = ?, detail = ?, last_ms = ? WHERE key = ?`,
			string(a.Kind), string(a.Level), a.Detail, max(last, at.UnixMilli()), a.Key); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM alerts WHERE cleared_ms IS NOT NULL AND cleared_ms < ?`, at.Add(-AlertsRetention).UnixMilli()); err != nil {
		return false, err
	}
	return changed, tx.Commit()
}

// ClearAlert records that the rule no longer holds and reports whether an active alert was cleared. The clear is never earlier than the
// alert's last time (a clock that stepped back must not make cleared_at precede last).
func (s *Store) ClearAlert(key string, at time.Time) (bool, error) {
	s.alertMu.Lock() // a write between RaiseAlert's read and its write would make that upgrade fail with SQLITE_BUSY
	defer s.alertMu.Unlock()
	res, err := s.db.Exec(`UPDATE alerts SET cleared_ms = max(?, last_ms) WHERE key = ? AND cleared_ms IS NULL`, at.UnixMilli(), key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
