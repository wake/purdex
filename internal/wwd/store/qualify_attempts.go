package store

import (
	"encoding/json"
	"time"
)

// qualifyAttemptsDDL (v8): the last pair of a task that did not earn its label, one row per `task:<repo>/<task>`, so the 24 h wait of spec §7
// survives a restart of wwd. The outcome is the qualification code's word and the fingerprint its JSON; the store only keeps them.
const qualifyAttemptsDDL = `CREATE TABLE IF NOT EXISTS qualify_attempts (
	label TEXT PRIMARY KEY,
	outcome TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	at_ms INTEGER NOT NULL)`

// QualifyAttempt is the last unsuccessful pair of a task.
type QualifyAttempt struct {
	Label       string
	Outcome     string
	Fingerprint map[string]string
	At          time.Time
}

// SaveQualifyAttempt inserts or replaces the row for the label.
func (s *Store) SaveQualifyAttempt(a QualifyAttempt) error {
	fp, err := fingerprintJSON(a.Fingerprint)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO qualify_attempts (label, outcome, fingerprint, at_ms) VALUES (?, ?, ?, ?)
		ON CONFLICT(label) DO UPDATE SET outcome = excluded.outcome, fingerprint = excluded.fingerprint, at_ms = excluded.at_ms`,
		a.Label, a.Outcome, fp, a.At.UnixMilli())
	return err
}

// DeleteQualifyAttempt removes the row (the label was earned, or the task is gone); a label that is not there is not an error.
func (s *Store) DeleteQualifyAttempt(label string) error {
	_, err := s.db.Exec(`DELETE FROM qualify_attempts WHERE label = ?`, label)
	return err
}

// QualifyAttempts returns every row, ordered by label. A row whose fingerprint no longer decodes is skipped with the rest returned,
// reported as a *SkippedRowsError, so one damaged row cannot hide the others.
func (s *Store) QualifyAttempts() ([]QualifyAttempt, error) {
	rows, err := s.db.Query(`SELECT label, outcome, fingerprint, at_ms FROM qualify_attempts ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QualifyAttempt{}
	skipped := 0
	for rows.Next() {
		var a QualifyAttempt
		var fp string
		var ms int64
		if err := rows.Scan(&a.Label, &a.Outcome, &fp, &ms); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(fp), &a.Fingerprint); err != nil {
			skipped++
			continue
		}
		a.At = time.UnixMilli(ms).UTC()
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if skipped > 0 {
		return out, &SkippedRowsError{N: skipped, Table: "qualify_attempts"}
	}
	return out, nil
}
