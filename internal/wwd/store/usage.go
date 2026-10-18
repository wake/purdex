package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// UsageRetention is how long a finished job's usage is kept: longer than the job itself (the queue keeps a finished job 7 days), so
// the usage outlives the job's record.
const UsageRetention = 30 * 24 * time.Hour

// usageFutureSlack is how far past now a row may claim its job ended (clock steps).
const usageFutureSlack = 24 * time.Hour

// SaveUsage is SaveUsageAt with the current time.
func (s *Store) SaveUsage(u wire.JobUsage) error { return s.SaveUsageAt(u, time.Now()) }

// SaveUsageAt stores one finished job's usage (keyed by the job id; the same job again changes nothing) and drops the rows whose job
// ended more than the retention before now. The cutoff comes from now, not from the row: a wrong end time cannot erase the history,
// and a row already past the retention is not stored. An invalid row, or one ending more than a day after now, is refused.
func (s *Store) SaveUsageAt(u wire.JobUsage, now time.Time) error {
	if err := u.Validate(); err != nil {
		return err
	}
	if u.EndedAt.After(now.Add(usageFutureSlack)) {
		return fmt.Errorf("store: usage of %s ends in the future (%s)", u.JobID, u.EndedAt.Format(time.RFC3339))
	}
	cutoff := now.Add(-UsageRetention).UnixMilli()
	ended := u.EndedAt.UnixMilli()
	b, err := json.Marshal(u)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if ended >= cutoff { // a row already past the retention is not stored, but the pruning below still runs
		if _, err := tx.Exec(`INSERT INTO job_usage (job_id, ended_ms, rec) VALUES (?, ?, ?) ON CONFLICT(job_id) DO NOTHING`, u.JobID, ended, string(b)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM job_usage WHERE ended_ms < ?`, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// LoadUsage returns the rows whose job ended at or after since, oldest first (ties by job id), at most limit of them; more says there
// are rows after the last one. A row that no longer decodes or validates is skipped and reported as a *SkippedRowsError next to the
// rows.
func (s *Store) LoadUsage(since time.Time, limit int) (out []wire.JobUsage, more bool, err error) {
	if limit <= 0 {
		return nil, false, fmt.Errorf("store: usage limit %d", limit)
	}
	rows, err := s.db.Query(`SELECT rec FROM job_usage WHERE ended_ms >= ? ORDER BY ended_ms, job_id LIMIT ?`, since.UnixMilli(), limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	skipped := 0
	n := 0
	for rows.Next() {
		var rec string
		if err := rows.Scan(&rec); err != nil {
			return nil, false, err
		}
		n++
		if n > limit { // the extra row only says there is more
			more = true
			break
		}
		u, err := wire.Decode[wire.JobUsage]([]byte(rec))
		if err != nil {
			skipped++
			continue
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if skipped > 0 {
		return out, more, &SkippedRowsError{N: skipped, Table: "job_usage"}
	}
	return out, more, nil
}
