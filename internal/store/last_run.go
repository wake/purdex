package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// The last agent run of a tmux session, for the no-agent launch panel (spec 2026-10-12-no-agent-launch-panel §2.1):
// when a ROOT frame ends, what it was is kept under the session's code so the panel can offer to resume it. The frame
// carries the two facts the write needs — the tmux generation it was created under, and a root flag fixed at creation —
// and the write shares the transaction of the SessionEnd claim-delete, so only the caller that wins the delete writes.

// RunEnd asks ClaimEnd to record the ended run under a session code.
type RunEnd struct {
	// Code is the tmux session's code; empty records nothing.
	Code string
	// EndedAt is Unix milliseconds on the daemon's clock.
	EndedAt int64
}

// LastRun is the one row kept per session code (overwritten by each root run that ends).
type LastRun struct {
	Code         string
	TmuxInstance string // the generation of the FRAME (not of the daemon at the time it ended)
	AgentType    string
	SessionID    string
	Cwd          string
	EndedAt      int64
	// PaneID is the tmux pane the run ran in ("" for a row written before the column existed): a tmux session can hold
	// several panes, each with its own run, and only the same pane's run is a /clear's predecessor.
	PaneID string
}

// addFrameRootColumns brings a table created before tmux_instance / is_root existed up to the current schema.
// Additive, and run once per column: existing rows get an empty generation (a run that never recorded one is not
// resumable) and is_root from their parent link AS IT IS AT MIGRATION TIME — the best that can be known for a row
// written before the flag. The backfill runs only in the migration that adds the column, in the same transaction as
// the ALTERs, so a later start never re-derives a flag from a link that ON DELETE SET NULL cleared since.
func addFrameRootColumns(db *sql.DB) error {
	existing, err := frameColumnNames(db)
	if err != nil {
		return err
	}
	needInstance, needRoot := !existing["tmux_instance"], !existing["is_root"]
	if !needInstance && !needRoot {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if needInstance {
		if _, err := tx.Exec(`ALTER TABLE agent_frames ADD COLUMN tmux_instance TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add agent_frames.tmux_instance: %w", err)
		}
	}
	if needRoot {
		if _, err := tx.Exec(`ALTER TABLE agent_frames ADD COLUMN is_root INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add agent_frames.is_root: %w", err)
		}
		if _, err := tx.Exec(`UPDATE agent_frames SET is_root = 1 WHERE parent_frame_id IS NULL OR parent_frame_id = ''`); err != nil {
			return fmt.Errorf("backfill agent_frames.is_root: %w", err)
		}
	}
	return tx.Commit()
}

// migrateLastRunTable creates the table. It is disposable: an empty or missing table means "no record".
func migrateLastRunTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS last_agent_run (
			code          TEXT PRIMARY KEY,
			tmux_instance TEXT NOT NULL,
			agent_type    TEXT NOT NULL,
			session_id    TEXT NOT NULL,
			cwd           TEXT NOT NULL,
			ended_at      INTEGER NOT NULL,
			pane_id       TEXT NOT NULL DEFAULT ''
		)
	`)
	if err != nil {
		return err
	}
	var have int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('last_agent_run') WHERE name = 'pane_id'`).Scan(&have); err != nil {
		return err
	}
	if have == 0 {
		if _, err := db.Exec(`ALTER TABLE last_agent_run ADD COLUMN pane_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add last_agent_run.pane_id: %w", err)
		}
	}
	return nil
}

// SetTmuxInstance records the tmux generation a root frame was created or taken over under (the root SessionStart's
// sample). An empty instance is written as empty: a frame whose generation is unknown must not keep an older one.
// Returns sql.ErrNoRows if the frame does not exist.
func (s *FramesStore) SetTmuxInstance(frameID, instance string) error {
	res, err := s.db.Exec(`UPDATE agent_frames SET tmux_instance = ? WHERE frame_id = ?`, instance, frameID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RootInfo reads the two facts a frame carries for the last-run write.
func (s *FramesStore) RootInfo(frameID string) (tmuxInstance string, isRoot, found bool, err error) {
	var root int
	err = s.db.QueryRow(`SELECT tmux_instance, is_root FROM agent_frames WHERE frame_id = ?`, frameID).Scan(&tmuxInstance, &root)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return tmuxInstance, root != 0, true, nil
}

// ClaimEnd is ClaimDelete that also records the run. In ONE write transaction it claims the frame exactly as
// ClaimDelete does (sessionID "" is the sweep's claim; a non-empty one needs the row's recorded session_id to equal it)
// and, only when this call removed the row, upserts the session's last run from the DELETED row — so the record is the
// frame's own, whatever the SessionEnd's arrival time says. Written only for a root frame with a known generation and
// a non-empty code; a child, an unstamped frame or a nil `end` records nothing but is still claimed. The upsert never
// lets an OLDER tmux generation replace a newer one's record (a SessionEnd of the old server arriving after a restart,
// after the new server's run already ended): same generation, the later end wins; different, the later server start.
// A failure to record rolls the delete back: the frame stays and the caller's retry ends it (the sweep, which carries
// no run, may get there first when the process is already dead — then that run is simply not recorded).
func (s *FramesStore) ClaimEnd(frameID, sessionID string, end *RunEnd) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit

	var (
		agentType, sid, cwd, instance, pane string
		root                                int
	)
	err = tx.QueryRow(
		`DELETE FROM agent_frames WHERE frame_id = ? AND (? = '' OR session_id = ?)
		 RETURNING agent_type, session_id, cwd, tmux_instance, is_root, pane_id`,
		frameID, sessionID, sessionID,
	).Scan(&agentType, &sid, &cwd, &instance, &root, &pane)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if end != nil && end.Code != "" && root != 0 && instance != "" {
		if _, err := tx.Exec(`
			INSERT INTO last_agent_run (code, tmux_instance, agent_type, session_id, cwd, ended_at, pane_id)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(code) DO UPDATE SET
				tmux_instance = excluded.tmux_instance,
				agent_type = excluded.agent_type,
				session_id = excluded.session_id,
				cwd = excluded.cwd,
				ended_at = excluded.ended_at,
				pane_id = excluded.pane_id
			WHERE last_agent_run.tmux_instance = excluded.tmux_instance
			   OR `+instanceStart("last_agent_run.tmux_instance")+` <= `+instanceStart("excluded.tmux_instance")+`
		`, end.Code, instance, agentType, sid, cwd, end.EndedAt, pane); err != nil {
			return false, fmt.Errorf("record last run: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// instanceStart is the tmux server start time of a "pid:start_time" generation (config.GetTmuxInstance), as an SQL
// integer expression; unparseable parts read as 0.
func instanceStart(col string) string {
	return "CAST(substr(" + col + ", instr(" + col + ", ':') + 1) AS INTEGER)"
}

// LastRun returns the last root run recorded for a session code, or nil.
func (s *FramesStore) LastRun(code string) (*LastRun, error) {
	var r LastRun
	err := s.db.QueryRow(`
		SELECT code, tmux_instance, agent_type, session_id, cwd, ended_at, pane_id FROM last_agent_run WHERE code = ?
	`, code).Scan(&r.Code, &r.TmuxInstance, &r.AgentType, &r.SessionID, &r.Cwd, &r.EndedAt, &r.PaneID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
