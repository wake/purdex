package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/google/uuid"
	agentpkg "github.com/wake/purdex/internal/agent"
)

type Frame struct {
	FrameID          string
	PaneID           string
	AgentType        string
	PID              int
	PPID             int
	ProcessStartTime string
	ParentFrameID    string
	Subagents        []agentpkg.SubagentRef
	Status           agentpkg.Status
	StartedAt        int64
	LastSeenAt       int64
	Verified         bool
}

type FramesStore struct {
	db *sql.DB
}

func migrateFramesDB(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_frames (
			frame_id            TEXT PRIMARY KEY,
			pane_id             TEXT NOT NULL,
			agent_type          TEXT NOT NULL,
			pid                 INTEGER NOT NULL,
			ppid                INTEGER NOT NULL,
			process_start_time  TEXT NOT NULL,
			parent_frame_id     TEXT,
			subagents_json      TEXT NOT NULL DEFAULT '[]',
			status              TEXT NOT NULL,
			started_at          INTEGER NOT NULL,
			last_seen_at        INTEGER NOT NULL,
			verified            INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (parent_frame_id) REFERENCES agent_frames(frame_id) ON DELETE SET NULL
		)
	`)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_frames_pane_pid_start ON agent_frames(pane_id, pid, process_start_time)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_frames_pane ON agent_frames(pane_id)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_frames_agent_type ON agent_frames(agent_type)`); err != nil {
		return err
	}
	return clearStaleSubagentsJSON(db)
}

// clearStaleSubagentsJSON detects pre-Phase-2 `subagents_json` rows (string
// array shape `["id"]`) and truncates the table. Frames are ephemeral
// telemetry — clearing them on schema upgrade is lossless and avoids the
// alternative: every subsequent hook 500s because scanFrame can't unmarshal
// the old shape into []SubagentRef.
func clearStaleSubagentsJSON(db *sql.DB) error {
	var probe sql.NullString
	err := db.QueryRow(`SELECT subagents_json FROM agent_frames LIMIT 1`).Scan(&probe)
	if err == sql.ErrNoRows {
		return nil // empty table, nothing to check
	}
	if err != nil {
		return err
	}
	if !probe.Valid {
		return nil
	}
	var dst []agentpkg.SubagentRef
	if json.Unmarshal([]byte(probe.String), &dst) == nil {
		return nil // already in new format
	}
	// Unmarshal failed — row is in legacy shape. Truncate the table.
	if _, err := db.Exec(`DELETE FROM agent_frames`); err != nil {
		return err
	}
	log.Printf("[store] cleared agent_frames: legacy subagents_json schema detected, see Phase 2 PR-2a notes")
	return nil
}

func (s *FramesStore) Upsert(frame Frame) (Frame, error) {
	existing, err := s.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
	if err != nil {
		return Frame{}, err
	}
	if existing != nil {
		if frame.FrameID == "" {
			frame.FrameID = existing.FrameID
		}
		if frame.StartedAt == 0 {
			frame.StartedAt = existing.StartedAt
		}
		if frame.Subagents == nil {
			frame.Subagents = existing.Subagents
		}
		if frame.ParentFrameID == "" {
			frame.ParentFrameID = existing.ParentFrameID
		}
	} else {
		if frame.FrameID == "" {
			frame.FrameID = uuid.NewString()
		}
		if frame.StartedAt == 0 {
			frame.StartedAt = frame.LastSeenAt
		}
		if frame.Subagents == nil {
			frame.Subagents = []agentpkg.SubagentRef{}
		}
	}
	if frame.LastSeenAt == 0 {
		frame.LastSeenAt = frame.StartedAt
	}
	subagentsJSON, err := json.Marshal(frame.Subagents)
	if err != nil {
		return Frame{}, fmt.Errorf("marshal subagents: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO agent_frames (
			frame_id, pane_id, agent_type, pid, ppid, process_start_time,
			parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pane_id, pid, process_start_time) DO UPDATE SET
			agent_type = excluded.agent_type,
			ppid = excluded.ppid,
			parent_frame_id = excluded.parent_frame_id,
			subagents_json = excluded.subagents_json,
			status = excluded.status,
			started_at = excluded.started_at,
			last_seen_at = excluded.last_seen_at,
			verified = excluded.verified
	`, frame.FrameID, frame.PaneID, frame.AgentType, frame.PID, frame.PPID, frame.ProcessStartTime,
		nullString(frame.ParentFrameID), string(subagentsJSON), string(frame.Status), frame.StartedAt, frame.LastSeenAt, boolToInt(frame.Verified))
	if err != nil {
		return Frame{}, err
	}
	stored, err := s.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
	if err != nil {
		return Frame{}, err
	}
	if stored == nil {
		return Frame{}, sql.ErrNoRows
	}
	return *stored, nil
}

func (s *FramesStore) GetByIdentity(paneID string, pid int, startTime string) (*Frame, error) {
	row := s.db.QueryRow(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
		FROM agent_frames
		WHERE pane_id = ? AND pid = ? AND process_start_time = ?
	`, paneID, pid, startTime)
	frame, err := scanFrame(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &frame, nil
}

func (s *FramesStore) FindByPanePID(paneID string, pid int) (*Frame, error) {
	row := s.db.QueryRow(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
		FROM agent_frames
		WHERE pane_id = ? AND pid = ?
		ORDER BY started_at DESC
		LIMIT 1
	`, paneID, pid)
	frame, err := scanFrame(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &frame, nil
}

func (s *FramesStore) ListByPane(paneID string) ([]Frame, error) {
	rows, err := s.db.Query(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
		FROM agent_frames
		WHERE pane_id = ?
		ORDER BY started_at ASC
	`, paneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFrames(rows)
}

func (s *FramesStore) ListAll() ([]Frame, error) {
	rows, err := s.db.Query(`
		SELECT frame_id, pane_id, agent_type, pid, ppid, process_start_time,
		       parent_frame_id, subagents_json, status, started_at, last_seen_at, verified
		FROM agent_frames
		ORDER BY pane_id ASC, started_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectFrames(rows)
}

func (s *FramesStore) Delete(frameID string) error {
	_, err := s.db.Exec(`DELETE FROM agent_frames WHERE frame_id = ?`, frameID)
	return err
}

func collectFrames(rows *sql.Rows) ([]Frame, error) {
	var frames []Frame
	for rows.Next() {
		frame, err := scanFrame(rows)
		if err != nil {
			return nil, err
		}
		frames = append(frames, frame)
	}
	return frames, rows.Err()
}

type frameScanner interface {
	Scan(dest ...any) error
}

func scanFrame(scanner frameScanner) (Frame, error) {
	var frame Frame
	var parent sql.NullString
	var subagentsJSON string
	var status string
	var verified int
	err := scanner.Scan(
		&frame.FrameID,
		&frame.PaneID,
		&frame.AgentType,
		&frame.PID,
		&frame.PPID,
		&frame.ProcessStartTime,
		&parent,
		&subagentsJSON,
		&status,
		&frame.StartedAt,
		&frame.LastSeenAt,
		&verified,
	)
	if err != nil {
		return Frame{}, err
	}
	frame.ParentFrameID = parent.String
	frame.Status = agentpkg.Status(status)
	frame.Verified = verified != 0
	if err := json.Unmarshal([]byte(subagentsJSON), &frame.Subagents); err != nil {
		return Frame{}, fmt.Errorf("unmarshal subagents: %w", err)
	}
	if frame.Subagents == nil {
		frame.Subagents = []agentpkg.SubagentRef{}
	}
	return frame, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
