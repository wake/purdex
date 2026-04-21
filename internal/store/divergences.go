package store

import (
	"database/sql"
)

// FrameDivergence captures a single observed mismatch between the legacy
// direct-write frame and the Arbitrator proposal during the Phase 1 dual-write
// window (spec §8.1).
type FrameDivergence struct {
	ID                 int64
	SessionID          string
	TraceID            string
	EventID            string
	ObservedGeneration int64
	OldStateRef        []byte
	ProposalStateRef   []byte
	DiffSummary        string
	Matched            bool
	ReasonCode         string
	CreatedAt          int64
}

// DivergenceStore persists frame divergences for the dual-write observation
// window.
type DivergenceStore struct {
	db *sql.DB
}

func migrateDivergencesDB(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS frame_divergences (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id          TEXT NOT NULL,
			trace_id            TEXT NOT NULL,
			event_id            TEXT NOT NULL,
			observed_generation INTEGER NOT NULL,
			old_state_ref       BLOB NOT NULL,
			proposal_state_ref  BLOB NOT NULL,
			diff_summary        TEXT NOT NULL,
			matched             INTEGER NOT NULL,
			reason_code         TEXT,
			created_at          INTEGER NOT NULL
		)
	`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_divergence_session ON frame_divergences(session_id, created_at)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_divergence_matched ON frame_divergences(matched)`); err != nil {
		return err
	}
	return nil
}

// Insert appends a divergence row and returns the generated row id.
func (s *DivergenceStore) Insert(d FrameDivergence) (int64, error) {
	matched := 0
	if d.Matched {
		matched = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO frame_divergences (
			session_id, trace_id, event_id, observed_generation,
			old_state_ref, proposal_state_ref, diff_summary, matched,
			reason_code, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, d.SessionID, d.TraceID, d.EventID, d.ObservedGeneration,
		d.OldStateRef, d.ProposalStateRef, d.DiffSummary, matched,
		nullString(d.ReasonCode), d.CreatedAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get returns the divergence row with the given id, or nil if not found.
func (s *DivergenceStore) Get(id int64) (*FrameDivergence, error) {
	var d FrameDivergence
	var matched int
	var reasonCode sql.NullString
	err := s.db.QueryRow(`
		SELECT id, session_id, trace_id, event_id, observed_generation,
		       old_state_ref, proposal_state_ref, diff_summary, matched,
		       reason_code, created_at
		FROM frame_divergences
		WHERE id = ?
	`, id).Scan(
		&d.ID,
		&d.SessionID,
		&d.TraceID,
		&d.EventID,
		&d.ObservedGeneration,
		&d.OldStateRef,
		&d.ProposalStateRef,
		&d.DiffSummary,
		&matched,
		&reasonCode,
		&d.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Matched = matched != 0
	d.ReasonCode = reasonCode.String
	return &d, nil
}
