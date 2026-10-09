// internal/module/team/remote_spawn_store.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// remoteSpawnSchema is L's forwarded spawn ops (cross-host team spec §4.5, §5.5; plan X4b): one row per `spawn` command
// sent to a member host, keyed by the op id (= the command id = the member key mk). It is NOT spawn_ops: that table is the
// runner's, and a running row there is started again at boot — a forwarded op is run by the member host. The row holds a
// seat while it runs (seatsExpr) and is closed by the member host's `registered` / `spawn_failed` fact, by the 10 minute void
// (remote_unreachable) or by an unpairing. Idempotent; any later change is a migration.
const remoteSpawnSchema = `
	CREATE TABLE IF NOT EXISTS remote_spawns (
		id                TEXT PRIMARY KEY,
		team_id           TEXT    NOT NULL,
		host_id           TEXT    NOT NULL,
		origin_session_id TEXT    NOT NULL,
		cwd               TEXT    NOT NULL,
		title             TEXT    NOT NULL DEFAULT '',
		model             TEXT    NOT NULL DEFAULT '',
		effort            TEXT    NOT NULL DEFAULT '',
		task_subject      TEXT    NOT NULL DEFAULT '',
		task_description  TEXT    NOT NULL DEFAULT '',
		task_done_json    TEXT    NOT NULL DEFAULT '',
		state             TEXT    NOT NULL,
		reason            TEXT    NOT NULL DEFAULT '',
		member_session_id TEXT    NOT NULL DEFAULT '',
		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS remote_spawns_running ON remote_spawns (team_id) WHERE state = 'running';
	CREATE INDEX IF NOT EXISTS remote_spawns_host ON remote_spawns (host_id) WHERE state = 'running';`

// Remote spawn states.
const (
	remoteSpawnRunning = "running"
	remoteSpawnDone    = "done"
	remoteSpawnFailed  = "failed"
)

// remoteSpawnRow is one remote_spawns row.
type remoteSpawnRow struct {
	ID, TeamID, HostID, OriginSessionID, Cwd, Title, Model, Effort string
	TaskSubject, TaskDescription, TaskDoneJSON                     string
	State, Reason, MemberSessionID                                 string
	CreatedAt, UpdatedAt                                           int64
}

const remoteSpawnCols = `id, team_id, host_id, origin_session_id, cwd, title, model, effort, task_subject, task_description, task_done_json,
	state, reason, member_session_id, created_at, updated_at`

const remoteSpawnInsertSQL = `INSERT INTO remote_spawns (` + remoteSpawnCols + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (r remoteSpawnRow) insertArgs() []any {
	return []any{r.ID, r.TeamID, r.HostID, r.OriginSessionID, r.Cwd, r.Title, r.Model, r.Effort, r.TaskSubject, r.TaskDescription,
		r.TaskDoneJSON, r.State, r.Reason, r.MemberSessionID, r.CreatedAt, r.UpdatedAt}
}

func (r *remoteSpawnRow) dest() []any {
	return []any{&r.ID, &r.TeamID, &r.HostID, &r.OriginSessionID, &r.Cwd, &r.Title, &r.Model, &r.Effort, &r.TaskSubject, &r.TaskDescription,
		&r.TaskDoneJSON, &r.State, &r.Reason, &r.MemberSessionID, &r.CreatedAt, &r.UpdatedAt}
}

// doneWhen decodes the done-when column: a JSON array of strings, "" = none. A damaged column is an error, never "no
// conditions" (the task would be created without the conditions that say when it is done).
func (r remoteSpawnRow) doneWhen() ([]string, error) {
	if r.TaskDoneJSON == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(r.TaskDoneJSON), &out); err != nil {
		return nil, fmt.Errorf("remote spawn %s: done-when column: %w", r.ID, err)
	}
	return out, nil
}

// InsertRemoteSpawnIn writes a running op in the transaction of the change that causes it (the spawn command's enqueue,
// §3.1 rule 2).
func InsertRemoteSpawnIn(q dbtx, r remoteSpawnRow) error {
	if _, err := q.Exec(remoteSpawnInsertSQL, r.insertArgs()...); err != nil {
		return fmt.Errorf("insert remote spawn %s: %w", r.ID, err)
	}
	return nil
}

// failRemoteSpawnsOfHostIn fails every running op of hostID with reason (an unpairing, §3.2). It returns how many.
func failRemoteSpawnsOfHostIn(q dbtx, hostID, reason string, now int64) (int64, error) {
	r, err := q.Exec(`UPDATE remote_spawns SET state = 'failed', reason = ?, updated_at = ? WHERE host_id = ? AND state = 'running'`, reason, now, hostID)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	return n, nil
}

// failRemoteSpawnIn fails one running op of hostID (the 10 minute void, §3.3).
func failRemoteSpawnIn(q dbtx, id, hostID, reason string, now int64) error {
	_, err := q.Exec(`UPDATE remote_spawns SET state = 'failed', reason = ?, updated_at = ? WHERE id = ? AND host_id = ? AND state = 'running'`,
		reason, now, id, hostID)
	return err
}

// GetRemoteSpawn returns the forwarded op with id; ok is false when there is none.
func (s *Store) GetRemoteSpawn(id string) (remoteSpawnRow, bool, error) {
	var r remoteSpawnRow
	err := s.db.QueryRow(`SELECT `+remoteSpawnCols+` FROM remote_spawns WHERE id = ?`, id).Scan(r.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteSpawnRow{}, false, nil
	}
	if err != nil {
		return remoteSpawnRow{}, false, fmt.Errorf("remote spawn %s: %w", id, err)
	}
	return r, true, nil
}

// AcceptRemoteSpawn writes the op, takes its seat and enqueues its `spawn` command in ONE transaction (spec §3.1 rule 2): the
// lead is checked (and the write lock taken) first, then the seat limit — the same two checks as a local spawn — so an op
// never exists without its command and a team never goes over its limit.
func (s *Store) AcceptRemoteSpawn(op remoteSpawnRow, cmd Command, now int64) (remoteSpawnRow, error) {
	fail := func(err error) (remoteSpawnRow, error) {
		return remoteSpawnRow{}, fmt.Errorf("accept remote spawn %s: %w", op.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE teams SET id = id WHERE id = ? AND lead_session_id = ? AND ended_at = 0`, op.TeamID, op.OriginSessionID)
	lead, err := oneRow(res, err, "lead check")
	if err == nil && !lead {
		err = ErrSpawnNotLead
	}
	var limit, used int
	if err == nil {
		err = tx.QueryRow(`SELECT json_extract(grant_json, '$.max_members') FROM teams WHERE id = ?`, op.TeamID).Scan(&limit)
		if err == nil {
			used, err = seatsTaken(tx, op.TeamID, op.ID)
		}
	}
	if err == nil && used >= limit {
		err = ErrSpawnTeamFull
	}
	if err == nil {
		err = InsertRemoteSpawnIn(tx, op)
	}
	if err == nil {
		err = s.EnqueueCommand(tx, cmd, now) // the op does not exist without its command
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fail(err)
	}
	return op, nil
}
