package store

import (
	"database/sql"
	"encoding/json"
)

// FrameDivergence captures a single observed mismatch between the legacy
// direct-write frame and the Arbitrator proposal during the Phase 1 dual-write
// window (spec §8.1).
//
// OldStateRef / ProposalStateRef are JSON snapshots. The storage column is
// TEXT (not BLOB, contrary to the initial spec DDL draft) so json.Marshal of
// this struct surfaces them as nested JSON rather than base64 strings.
type FrameDivergence struct {
	ID                 int64
	SessionID          string
	TraceID            string
	EventID            string
	ObservedGeneration int64
	OldStateRef        json.RawMessage
	ProposalStateRef   json.RawMessage
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

// migrateDivergencesDBFn is the indirection tests use to simulate an
// unrecoverable migration failure without touching the real schema.
var migrateDivergencesDBFn = migrateDivergencesDB

func migrateDivergencesDB(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS frame_divergences (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id          TEXT NOT NULL,
			trace_id            TEXT NOT NULL,
			event_id            TEXT NOT NULL,
			observed_generation INTEGER NOT NULL,
			old_state_ref       TEXT NOT NULL,
			proposal_state_ref  TEXT NOT NULL,
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
	// Idempotency key: retrying the same observation (session/trace/event/
	// generation tuple) must not produce duplicate rows.
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_divergence_idempotency ON frame_divergences(session_id, trace_id, event_id, observed_generation)`); err != nil {
		return err
	}
	return nil
}

// Insert appends a divergence row and returns the generated row id. Duplicate
// (session_id, trace_id, event_id, observed_generation) tuples are ignored and
// return 0 so callers can retry safely without double-counting.
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
		ON CONFLICT(session_id, trace_id, event_id, observed_generation) DO NOTHING
	`, d.SessionID, d.TraceID, d.EventID, d.ObservedGeneration,
		jsonRefText(d.OldStateRef), jsonRefText(d.ProposalStateRef), d.DiffSummary, matched,
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
	var oldState, proposalState string
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
		&oldState,
		&proposalState,
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
	d.OldStateRef = json.RawMessage(oldState)
	d.ProposalStateRef = json.RawMessage(proposalState)
	return &d, nil
}

// jsonRefText normalizes a nil/empty JSON snapshot to "null" so the TEXT
// column's NOT NULL constraint always holds without special-casing callers.
func jsonRefText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	return string(raw)
}
