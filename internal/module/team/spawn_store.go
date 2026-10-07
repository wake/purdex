package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// spawnSchema is the spawn_ops table (spec §9.3): one row per spawn, keyed
// by the client's id, persisted step by step so a retry after a restart
// continues from the recorded step. Idempotent; OpenStore runs it last.
const spawnSchema = `
	CREATE TABLE IF NOT EXISTS spawn_ops (id TEXT PRIMARY KEY, team_id TEXT NOT NULL,
		host_id TEXT NOT NULL, request_hash TEXT NOT NULL, origin_session_id TEXT NOT NULL,
		cwd TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', model TEXT NOT NULL DEFAULT '',
		effort TEXT NOT NULL DEFAULT '', tmux_name TEXT NOT NULL,
		tmux_id TEXT NOT NULL DEFAULT '', tmux_instance TEXT NOT NULL DEFAULT '',
		pane_id TEXT NOT NULL DEFAULT '', step TEXT NOT NULL, state TEXT NOT NULL,
		reason TEXT NOT NULL DEFAULT '', session_id TEXT NOT NULL DEFAULT '',
		launched_at INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
	CREATE INDEX IF NOT EXISTS spawn_ops_running ON spawn_ops (team_id) WHERE state = 'running';`

const spawnCols = `id, team_id, host_id, origin_session_id, cwd, title, model, effort, tmux_name, tmux_id,
	tmux_instance, pane_id, step, state, reason, session_id, launched_at, created_at, updated_at`

// spawnRow is one spawn_ops row but its request hash.
type spawnRow struct {
	ID, TeamID, HostID, OriginSessionID, Cwd, Title, Model, Effort string
	TmuxName, TmuxID, TmuxInstance, PaneID                         string
	Step                                                           string
	State                                                          team.SpawnState
	Reason, SessionID                                              string
	LaunchedAt, CreatedAt, UpdatedAt                               int64
}

func (r *spawnRow) dest() []any {
	return []any{&r.ID, &r.TeamID, &r.HostID, &r.OriginSessionID, &r.Cwd, &r.Title, &r.Model, &r.Effort,
		&r.TmuxName, &r.TmuxID, &r.TmuxInstance, &r.PaneID, &r.Step, &r.State, &r.Reason, &r.SessionID,
		&r.LaunchedAt, &r.CreatedAt, &r.UpdatedAt}
}

// nextSpawnStep is each step's one successor; registered is followed by done.
var nextSpawnStep = map[string]string{
	team.StepAccepted:       team.StepSessionCreated,
	team.StepSessionCreated: team.StepLaunched,
	team.StepLaunched:       team.StepRegistered,
}

var spawnReasons = map[string]bool{team.SpawnReasonStartTimeout: true, team.SpawnReasonCreateFailed: true,
	team.SpawnReasonLaunchFailed: true, team.SpawnReasonNameTaken: true, team.SpawnReasonAbandoned: true}

// CreateSpawnOp inserts op if its id is new (inserted=true); otherwise it
// inserts nothing. Either way it returns the stored row and hash, so the
// caller tells a retry (same hash) from a reuse of the id. A new op must be
// accepted and running with its team, origin and cwd, named SpawnTmuxName
// of its id (what a restarted runner looks for); else nothing is written.
func (s *Store) CreateSpawnOp(op spawnRow, hash string) (stored spawnRow, storedHash string, inserted bool, err error) {
	fail := func(err error) (spawnRow, string, bool, error) {
		return spawnRow{}, "", false, fmt.Errorf("create spawn op %s: %w", op.ID, err)
	}
	name, err := team.SpawnTmuxName(op.ID)
	if err != nil {
		return fail(err)
	}
	if op.TmuxName != name || op.TeamID == "" || op.OriginSessionID == "" || op.Cwd == "" ||
		op.Step != team.StepAccepted || op.State != team.SpawnRunning {
		return fail(fmt.Errorf("a new op needs tmux name %q (got %q), a team, an origin and a cwd, step accepted and state running (got %s, %s)",
			name, op.TmuxName, op.Step, op.State))
	}
	res, err := s.db.Exec(`INSERT INTO spawn_ops (request_hash, `+spawnCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		hash, op.ID, op.TeamID, op.HostID, op.OriginSessionID, op.Cwd, op.Title, op.Model, op.Effort, op.TmuxName,
		op.TmuxID, op.TmuxInstance, op.PaneID, op.Step, string(op.State), op.Reason, op.SessionID,
		op.LaunchedAt, op.CreatedAt, op.UpdatedAt)
	if inserted, err = oneRow(res, err, "insert"); err == nil {
		err = s.db.QueryRow(`SELECT request_hash, `+spawnCols+` FROM spawn_ops WHERE id = ?`, op.ID).
			Scan(append([]any{&storedHash}, stored.dest()...)...)
	}
	if err != nil {
		return fail(err)
	}
	return stored, storedHash, inserted, nil
}

// GetSpawnOp returns the op with id; ok is false when there is none.
func (s *Store) GetSpawnOp(id string) (spawnRow, bool, error) {
	var r spawnRow
	err := s.db.QueryRow(`SELECT `+spawnCols+` FROM spawn_ops WHERE id = ?`, id).Scan(r.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return spawnRow{}, false, nil
	}
	if err != nil {
		return spawnRow{}, false, fmt.Errorf("get spawn op %s: %w", id, err)
	}
	return r, true, nil
}

// spawnUpdate is what one runner step records (a zero field keeps the
// stored value). State is "" to stay running, or SpawnDone from registered.
type spawnUpdate struct {
	Step                         string
	State                        team.SpawnState
	TmuxID, TmuxInstance, PaneID string // session_created
	LaunchedAt                   int64  // launched
	SessionID                    string // registered
	At                           int64  // updated_at
}

// AdvanceSpawnOp moves a running op from fromStep to the next step (or from
// registered to done) in one compare-and-set on the step and the running
// state: a second runner, or a retry after a restart, that read the same
// step loses (won false, nothing changed), as does an unknown or ended op.
// Any other transition is an error.
func (s *Store) AdvanceSpawnOp(id, fromStep string, upd spawnUpdate) (bool, error) {
	toDone := fromStep == team.StepRegistered && upd.Step == team.StepRegistered && upd.State == team.SpawnDone
	if !toDone && (upd.State != "" || nextSpawnStep[fromStep] == "" || nextSpawnStep[fromStep] != upd.Step) {
		return false, fmt.Errorf("advance spawn op %s: %s → %s (state %q) is not a step", id, fromStep, upd.Step, upd.State)
	}
	state := team.SpawnRunning
	if toDone {
		state = team.SpawnDone
	}
	res, err := s.db.Exec(`UPDATE spawn_ops SET step = ?, state = ?,
			tmux_id = COALESCE(NULLIF(?, ''), tmux_id), tmux_instance = COALESCE(NULLIF(?, ''), tmux_instance),
			pane_id = COALESCE(NULLIF(?, ''), pane_id), launched_at = COALESCE(NULLIF(?, 0), launched_at),
			session_id = COALESCE(NULLIF(?, ''), session_id), updated_at = ?
		WHERE id = ? AND step = ? AND state = 'running'`,
		upd.Step, string(state), upd.TmuxID, upd.TmuxInstance, upd.PaneID, upd.LaunchedAt, upd.SessionID, upd.At,
		id, fromStep)
	return oneRow(res, err, "advance spawn op "+id)
}

// FailSpawnOp ends a running op as failed with reason (a SpawnReason*),
// keeping the step it reached. won is false for an unknown or already
// ended op.
func (s *Store) FailSpawnOp(id, reason string, at int64) (bool, error) {
	if !spawnReasons[reason] {
		return false, fmt.Errorf("fail spawn op %s: unknown reason %q", id, reason)
	}
	res, err := s.db.Exec(`UPDATE spawn_ops SET state = 'failed', reason = ?, updated_at = ?
		WHERE id = ? AND state = 'running'`, reason, at, id)
	return oneRow(res, err, "fail spawn op "+id)
}

// oneRow reports whether a guarded single-row UPDATE changed its row.
func oneRow(res sql.Result, err error, what string) (bool, error) {
	var n int64
	if err == nil {
		n, err = res.RowsAffected()
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", what, err)
	}
	return n == 1, nil
}

// ListRunningSpawnOps returns every running op, oldest first: what boot
// resumes (spec §9.3). Never nil.
func (s *Store) ListRunningSpawnOps() ([]spawnRow, error) {
	rows, err := s.db.Query(`SELECT ` + spawnCols + ` FROM spawn_ops WHERE state = 'running' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list running spawn ops: %w", err)
	}
	defer rows.Close()
	out := []spawnRow{}
	for rows.Next() {
		var r spawnRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("list running spawn ops: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list running spawn ops: %w", err)
	}
	return out, nil
}

// CountRunningSpawns counts the team's running ops other than exceptID
// (the op asking): with the live members, what the team limit counts.
func (s *Store) CountRunningSpawns(teamID, exceptID string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM spawn_ops WHERE team_id = ? AND state = 'running' AND id <> ?`,
		teamID, exceptID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count running spawns of %s: %w", teamID, err)
	}
	return n, nil
}
