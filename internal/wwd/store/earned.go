package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func validateEarned(e wire.EarnedLabel) error {
	if !wire.ValidTaskLabel(e.Label) {
		return fmt.Errorf("store: earned label %q is not task:<repo>/<task>", e.Label)
	}
	if e.State != wire.EarnedLabelEarned && e.State != wire.EarnedLabelQuarantined {
		return fmt.Errorf("store: earned label %s: state %q", e.Label, e.State)
	}
	return nil
}

func fingerprintJSON(fp map[string]string) (string, error) {
	if fp == nil {
		fp = map[string]string{}
	}
	b, err := json.Marshal(fp)
	return string(b), err
}

// SaveEarnedLabel inserts or replaces the row for the label.
func (s *Store) SaveEarnedLabel(e wire.EarnedLabel) error {
	if err := validateEarned(e); err != nil {
		return err
	}
	fp, err := fingerprintJSON(e.Fingerprint)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO earned_labels (label, fingerprint, snapshot, granted_ms, state) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(label) DO UPDATE SET fingerprint = excluded.fingerprint, snapshot = excluded.snapshot,
		granted_ms = excluded.granted_ms, state = excluded.state`,
		e.Label, fp, e.Snapshot, e.GrantedAt.UnixMilli(), e.State)
	return err
}

// SetEarnedLabelState changes only the state of an existing label; ok is false when there is no such label.
func (s *Store) SetEarnedLabelState(label, state string) (ok bool, err error) {
	if state != wire.EarnedLabelEarned && state != wire.EarnedLabelQuarantined {
		return false, fmt.Errorf("store: earned label %s: state %q", label, state)
	}
	res, err := s.db.Exec(`UPDATE earned_labels SET state = ? WHERE label = ?`, state, label)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteEarnedLabel removes the label (a revoked one); a label that is not there is not an error.
func (s *Store) DeleteEarnedLabel(label string) error {
	_, err := s.db.Exec(`DELETE FROM earned_labels WHERE label = ?`, label)
	return err
}

// EarnedLabels returns every row, ordered by label. A row whose fingerprint no longer decodes is skipped with the rest returned,
// reported as a *SkippedRowsError, so one damaged row cannot hide the others.
func (s *Store) EarnedLabels() ([]wire.EarnedLabel, error) {
	rows, err := s.db.Query(`SELECT label, fingerprint, snapshot, granted_ms, state FROM earned_labels ORDER BY label`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wire.EarnedLabel{}
	var skipped []string
	for rows.Next() {
		var e wire.EarnedLabel
		var fp string
		var ms int64
		if err := rows.Scan(&e.Label, &fp, &e.Snapshot, &ms, &e.State); err != nil {
			return nil, err
		}
		if !wire.ValidTaskLabel(e.Label) || json.Unmarshal([]byte(fp), &e.Fingerprint) != nil {
			skipped = append(skipped, e.Label)
			continue
		}
		e.GrantedAt = time.UnixMilli(ms).UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(skipped) > 0 {
		return out, &SkippedRowsError{N: len(skipped), Table: "earned_labels"}
	}
	return out, nil
}

// EarnedLabel returns one row; ok is false when there is none.
func (s *Store) EarnedLabel(label string) (e wire.EarnedLabel, ok bool, err error) {
	var fp string
	var ms int64
	err = s.db.QueryRow(`SELECT label, fingerprint, snapshot, granted_ms, state FROM earned_labels WHERE label = ?`, label).
		Scan(&e.Label, &fp, &e.Snapshot, &ms, &e.State)
	if errors.Is(err, sql.ErrNoRows) {
		return wire.EarnedLabel{}, false, nil
	}
	if err != nil {
		return wire.EarnedLabel{}, false, err
	}
	if err := json.Unmarshal([]byte(fp), &e.Fingerprint); err != nil {
		return wire.EarnedLabel{}, false, fmt.Errorf("store: earned label %s: %w", label, err)
	}
	e.GrantedAt = time.UnixMilli(ms).UTC()
	return e, true, nil
}

// ReleaseEarnedLabel deletes the row of the label if it is still the one granted at granted, in one statement: a label that was granted
// again in the meantime is a different row and stays. deleted says whether a row was deleted.
func (s *Store) ReleaseEarnedLabel(label string, granted time.Time) (deleted bool, err error) {
	res, err := s.db.Exec(`DELETE FROM earned_labels WHERE label = ? AND granted_ms = ?`, label, granted.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// QuarantineEarnedLabel quarantines the label if it is earned now and was granted before at (the time of a disagreement), in one
// statement: a disagreement read again, or one from before the label was earned anew, takes nothing away. changed says whether a row
// was changed.
func (s *Store) QuarantineEarnedLabel(label string, at time.Time) (changed bool, err error) {
	res, err := s.db.Exec(`UPDATE earned_labels SET state = ? WHERE label = ? AND state = ? AND granted_ms < ?`,
		wire.EarnedLabelQuarantined, label, wire.EarnedLabelEarned, at.UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
