package store

import (
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// ActiveAlerts are the host's alerts that are not cleared, ordered by key. It is never nil: an empty table is an empty list. A row
// that is not a valid wire.Alert (a kind or level this build does not know, a bad key) is an error, not a silent gap: the reader
// would otherwise show a quieter host than it is.
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
			return nil, fmt.Errorf("store: alerts row %q: %w", a.Key, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
