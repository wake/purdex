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

// clearStaleSubagentsJSON scans every `agent_frames.subagents_json` and
// classifies rows as new ([]SubagentRef) / legacy ([]string) / malformed.
// All new → no-op. Any legacy (and nothing malformed) → TRUNCATE table;
// frames are ephemeral telemetry so clearing is lossless. Any malformed →
// return a startup error so the daemon refuses to run rather than silently
// wiping unknown on-disk state.
//
// Full-table scan (not LIMIT 1) because SQLite does not guarantee row order
// and a single probe can hit a new-format row while legacy rows survive —
// scanFrame would then crash on later ListAll/GetByIdentity calls.
func clearStaleSubagentsJSON(db *sql.DB) error {
	rows, err := db.Query(`SELECT frame_id, subagents_json FROM agent_frames`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var hasLegacy bool
	var malformedID string // non-empty = malformed detected
	for rows.Next() {
		var id string
		var js sql.NullString
		if err := rows.Scan(&id, &js); err != nil {
			return err
		}
		raw := ""
		if js.Valid {
			raw = js.String
		}
		var newDst []agentpkg.SubagentRef
		if json.Unmarshal([]byte(raw), &newDst) == nil {
			continue
		}
		var legacyDst []string
		if json.Unmarshal([]byte(raw), &legacyDst) == nil {
			hasLegacy = true
			continue
		}
		if malformedID == "" {
			malformedID = id
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if malformedID != "" {
		return fmt.Errorf("agent_frames row %q has malformed subagents_json; refusing to start — inspect or remove the row manually (Phase 2 PR-2a)", malformedID)
	}
	if !hasLegacy {
		return nil
	}
	if _, err := db.Exec(`DELETE FROM agent_frames`); err != nil {
		return err
	}
	log.Printf("[store] cleared agent_frames: legacy subagents_json schema detected (Phase 2 PR-2a upgrade)")
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

// DeleteIfUnchanged removes the frame only if its last_seen_at matches the
// provided value — a concurrent Upsert that refreshed the row will bump
// last_seen_at and cause this DELETE to match 0 rows, returning (false, nil).
// Caller should treat (false, nil) as "frame got refreshed, skip this sweep".
func (s *FramesStore) DeleteIfUnchanged(frameID string, lastSeenAt int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM agent_frames WHERE frame_id = ? AND last_seen_at = ?`, frameID, lastSeenAt)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// UpdateStatusAndLastSeen updates only the status + last_seen_at columns of
// the frame identified by frameID. Narrow by design: probe-driven status
// transitions must not round-trip through a whole-frame write because doing
// so would clobber concurrent Subagents mutations (see #632 R7). Returns
// sql.ErrNoRows if the frame does not exist.
func (s *FramesStore) UpdateStatusAndLastSeen(frameID string, status agentpkg.Status, lastSeenAt int64) error {
	res, err := s.db.Exec(`
		UPDATE agent_frames SET status = ?, last_seen_at = ?
		WHERE frame_id = ?
	`, string(status), lastSeenAt, frameID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// UpsertIfUnchanged updates an existing frame atomically, returning
// (false, zeroFrame, nil) if the row's last_seen_at no longer matches
// expectedLastSeenAt — i.e. a concurrent writer changed the row between our
// read and write. Used by subagents-mutation paths (proxy attach / detach /
// SubagentStart / SubagentStop) to serialize read-modify-write cycles.
//
// Unlike Upsert, this is update-only: the frame must already exist and
// frame.FrameID must be set. Caller retries by reloading the row, re-merging
// the subagents list against the new baseline, and calling again.
func (s *FramesStore) UpsertIfUnchanged(frame Frame, expectedLastSeenAt int64) (bool, Frame, error) {
	if frame.FrameID == "" {
		return false, Frame{}, fmt.Errorf("UpsertIfUnchanged: frame.FrameID required")
	}
	if frame.Subagents == nil {
		frame.Subagents = []agentpkg.SubagentRef{}
	}
	subagentsJSON, err := json.Marshal(frame.Subagents)
	if err != nil {
		return false, Frame{}, fmt.Errorf("marshal subagents: %w", err)
	}
	res, err := s.db.Exec(`
		UPDATE agent_frames SET
			agent_type = ?,
			ppid = ?,
			parent_frame_id = ?,
			subagents_json = ?,
			status = ?,
			started_at = ?,
			last_seen_at = ?,
			verified = ?
		WHERE frame_id = ? AND last_seen_at = ?
	`, frame.AgentType, frame.PPID, nullString(frame.ParentFrameID),
		string(subagentsJSON), string(frame.Status), frame.StartedAt, frame.LastSeenAt,
		boolToInt(frame.Verified), frame.FrameID, expectedLastSeenAt)
	if err != nil {
		return false, Frame{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, Frame{}, err
	}
	if affected == 0 {
		return false, Frame{}, nil
	}
	stored, err := s.GetByIdentity(frame.PaneID, frame.PID, frame.ProcessStartTime)
	if err != nil {
		return false, Frame{}, err
	}
	if stored == nil {
		return false, Frame{}, sql.ErrNoRows
	}
	return true, *stored, nil
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
