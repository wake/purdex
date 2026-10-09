package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/google/uuid"
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
	tmux_instance, pane_id, step, state, reason, session_id, launched_at, created_at, updated_at,
	task_subject, task_description, task_done_json, lead_host_id, lead_json`

// spawnRow is one spawn_ops row but its request hash.
type spawnRow struct {
	ID, TeamID, HostID, OriginSessionID, Cwd, Title, Model, Effort string
	TmuxName, TmuxID, TmuxInstance, PaneID                         string
	Step                                                           string
	State                                                          team.SpawnState
	Reason, SessionID                                              string
	LaunchedAt, CreatedAt, UpdatedAt                               int64
	// The task the spawn creates with its member (T-2): TaskSubject "" = none.
	TaskSubject, TaskDescription, TaskDoneJSON string
	// LeadHostID is the lead host of a spawn FORWARDED to this host ("" = a local spawn); LeadJSON is that lead's tuple
	// and the team's name (remoteSpawnLead), what the remote member's row is written from. OriginSessionID is then the
	// lead's session on the other host, TeamID the team of that host.
	LeadHostID, LeadJSON string
}

func (r *spawnRow) dest() []any {
	return []any{&r.ID, &r.TeamID, &r.HostID, &r.OriginSessionID, &r.Cwd, &r.Title, &r.Model, &r.Effort,
		&r.TmuxName, &r.TmuxID, &r.TmuxInstance, &r.PaneID, &r.Step, &r.State, &r.Reason, &r.SessionID,
		&r.LaunchedAt, &r.CreatedAt, &r.UpdatedAt, &r.TaskSubject, &r.TaskDescription, &r.TaskDoneJSON, &r.LeadHostID, &r.LeadJSON}
}

// spawnStepRank orders the steps. A running row at rank n holds the
// milestones of ranks 1..n and none later (R2 finding 2): the three tmux
// ids (session_created), launched_at (launched), the session id (registered).
var spawnStepRank = map[string]int{team.StepAccepted: 0, team.StepSessionCreated: 1, team.StepLaunched: 2, team.StepRegistered: 3}

var spawnReasons = map[string]bool{team.SpawnReasonStartTimeout: true, team.SpawnReasonCreateFailed: true,
	team.SpawnReasonLaunchFailed: true, team.SpawnReasonNameTaken: true, team.SpawnReasonAbandoned: true}

// milestones reports, by step rank, which milestones a row or an update
// holds, and whether any is half there (some tmux ids only) or negative.
func milestones(tmuxID, tmuxInstance, paneID string, launchedAt int64, sessionID string) (has [4]bool, partial bool) {
	has[1] = tmuxID != "" && tmuxInstance != "" && paneID != ""
	partial = !has[1] && (tmuxID != "" || tmuxInstance != "" || paneID != "") || launchedAt < 0
	has[2], has[3] = launchedAt > 0, sessionID != ""
	return has, partial
}

// checkRunning says what a running row lacks or holds wrongly (R2 findings
// 2 and 3): the tmux name of its id, what its runner needs, a valid model
// and effort, no reason, and exactly the milestones of its step.
func (r spawnRow) checkRunning() error {
	name, err := team.SpawnTmuxName(r.ID)
	rank, known := spawnStepRank[r.Step]
	has, partial := milestones(r.TmuxID, r.TmuxInstance, r.PaneID, r.LaunchedAt, r.SessionID)
	switch {
	case err != nil || r.TmuxName != name:
		return fmt.Errorf("tmux name %q is not its id's", r.TmuxName)
	case r.TeamID == "" || r.HostID == "" || r.OriginSessionID == "" || r.Cwd == "" || r.CreatedAt <= 0 || r.UpdatedAt <= 0:
		return errors.New("team, host, origin, cwd and both times must be set")
	case (r.Model != "" && !team.ValidModel(r.Model)) || (r.Effort != "" && !team.ValidEffort(r.Effort)):
		return fmt.Errorf("invalid model %q or effort %q", r.Model, r.Effort)
	case r.TaskSubject != "" && r.taskErr() != nil:
		return fmt.Errorf("task: %w", r.taskErr())
	case r.TaskSubject == "" && (r.TaskDescription != "" || r.taskDoneJSON() != "[]"):
		return errors.New("a task description or done-when without a subject")
	case r.State != team.SpawnRunning || r.Reason != "" || !known:
		return fmt.Errorf("state %q, reason %q, step %q is no running step", r.State, r.Reason, r.Step)
	case partial || has[1] != (rank >= 1) || has[2] != (rank >= 2) || has[3] != (rank >= 3):
		return fmt.Errorf("its milestones do not match step %s", r.Step)
	}
	return nil
}

// taskDoneJSON is the done-when column: a JSON array, "[]" when there is none.
func (r spawnRow) taskDoneJSON() string {
	if r.TaskDoneJSON == "" {
		return "[]"
	}
	return r.TaskDoneJSON
}

// taskDoneWhen decodes the done-when column. A column that is not a JSON
// array of strings is an error, never "no conditions": the task would
// otherwise be created without the conditions that say when it is done.
func (r spawnRow) taskDoneWhen() ([]string, error) {
	var out []string
	if err := json.Unmarshal([]byte(r.taskDoneJSON()), &out); err != nil {
		return nil, fmt.Errorf("spawn %s: done-when column: %w", r.ID, err)
	}
	return out, nil
}

// taskErr says what is wrong with the task the row carries.
func (r spawnRow) taskErr() error {
	if err := team.ValidTaskSubject(r.TaskSubject); err != nil {
		return err
	}
	if err := team.ValidTaskDescription(r.TaskDescription); err != nil {
		return err
	}
	dw, err := r.taskDoneWhen()
	if err != nil {
		return err
	}
	return team.ValidDoneWhen(dw)
}

// CreateSpawnOp inserts op if its id is new (inserted=true); otherwise it
// inserts nothing. Either way it returns the stored row and hash, so the
// caller tells a retry (same hash) from a reuse of the id. A new op must be
// accepted and pass checkRunning (named SpawnTmuxName of its id, what a
// restarted runner looks for; no milestone yet) and carry its request hash;
// else nothing is written.
func (s *Store) CreateSpawnOp(op spawnRow, hash string) (stored spawnRow, storedHash string, inserted bool, err error) {
	return insertSpawnOp(s.db, op, hash)
}

// insertSpawnOp is CreateSpawnOp on q, the store or a transaction.
func insertSpawnOp(q dbtx, op spawnRow, hash string) (stored spawnRow, storedHash string, inserted bool, err error) {
	fail := func(err error) (spawnRow, string, bool, error) {
		return spawnRow{}, "", false, fmt.Errorf("create spawn op %s: %w", op.ID, err)
	}
	if hash == "" || op.Step != team.StepAccepted || op.UpdatedAt < op.CreatedAt {
		return fail(fmt.Errorf("a new op needs a request hash, step accepted (got %q) and updated_at >= created_at", op.Step))
	}
	if err := op.checkRunning(); err != nil {
		return fail(err)
	}
	res, err := q.Exec(`INSERT INTO spawn_ops (request_hash, `+spawnCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`,
		hash, op.ID, op.TeamID, op.HostID, op.OriginSessionID, op.Cwd, op.Title, op.Model, op.Effort, op.TmuxName,
		op.TmuxID, op.TmuxInstance, op.PaneID, op.Step, string(op.State), op.Reason, op.SessionID,
		op.LaunchedAt, op.CreatedAt, op.UpdatedAt, op.TaskSubject, op.TaskDescription, op.TaskDoneJSON, op.LeadHostID, op.LeadJSON)
	if inserted, err = oneRow(res, err, "insert"); err == nil {
		err = q.QueryRow(`SELECT request_hash, `+spawnCols+` FROM spawn_ops WHERE id = ?`, op.ID).
			Scan(append([]any{&storedHash}, stored.dest()...)...)
	}
	if err != nil {
		return fail(err)
	}
	return stored, storedHash, inserted, nil
}

// ErrSpawnNotLead and ErrSpawnTeamFull are AcceptSpawnOp's refusals.
var (
	ErrSpawnNotLead  = errors.New("the origin leads no live team")
	ErrSpawnTeamFull = errors.New("the team has no place left")
)

// AcceptSpawnOp is CreateSpawnOp in one write transaction with the two facts
// a new op rests on (P4-5 review H1), so a team end, a lead move or another
// writer that lands after the caller read the team wins: the op's team is
// live and led by its origin (else ErrSpawnNotLead), and the team's active
// member rows plus its running ops but this one are fewer than its grant's
// max_members (else ErrSpawnTeamFull). A refusal writes nothing.
//
// The count is team.db alone, so it holds at the commit (P4-5 critic): a
// member whose session ended still holds its place until its row is marked
// gone (P4-6's sweeper), when the place frees (spec §13 D4). Conservative:
// a registry read made before the transaction could undercount.
func (s *Store) AcceptSpawnOp(op spawnRow, hash string) (spawnRow, string, bool, error) {
	fail := func(err error) (spawnRow, string, bool, error) {
		return spawnRow{}, "", false, fmt.Errorf("accept spawn op %s: %w", op.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the reads; it is
	// also the lead check.
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
	if err != nil {
		return fail(err)
	}
	stored, storedHash, inserted, err := insertSpawnOp(tx, op, hash)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fail(err)
	}
	return stored, storedHash, inserted, nil
}

// GetSpawnOp returns the op with id; ok is false when there is none.
func (s *Store) GetSpawnOp(id string) (spawnRow, bool, error) {
	r, _, ok, err := s.spawnOpWithHash(id)
	return r, ok, err
}

// spawnOpWithHash is GetSpawnOp plus the stored request hash: what the POST
// compares a replay with before it joins the op.
func (s *Store) spawnOpWithHash(id string) (spawnRow, string, bool, error) {
	var r spawnRow
	var hash string
	err := s.db.QueryRow(`SELECT request_hash, `+spawnCols+` FROM spawn_ops WHERE id = ?`, id).Scan(append([]any{&hash}, r.dest()...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return spawnRow{}, "", false, nil
	}
	if err != nil {
		return spawnRow{}, "", false, fmt.Errorf("get spawn op %s: %w", id, err)
	}
	return r, hash, true, nil
}

// spawnUpdate is what one runner step records: the facts of the step it
// reaches and no other (a zero field keeps the stored value). State is ""
// to stay running, or SpawnDone from registered.
type spawnUpdate struct {
	Step                         string
	State                        team.SpawnState
	TmuxID, TmuxInstance, PaneID string // session_created
	LaunchedAt                   int64  // launched
	SessionID                    string // registered
	At                           int64  // updated_at
}

// fits says whether u is the next step from fromStep with exactly that
// step's facts (R2 finding 2): the three tmux ids to session_created, a
// positive launched_at to launched, the session id to registered, nothing
// to done; and a positive time.
func (u spawnUpdate) fits(fromStep string) bool {
	from, known := spawnStepRank[fromStep]
	has, partial := milestones(u.TmuxID, u.TmuxInstance, u.PaneID, u.LaunchedAt, u.SessionID)
	want := from + 1 // the rank whose milestone u carries; 4 = done, none
	switch {
	case !known || u.At <= 0 || partial:
		return false
	case u.State == team.SpawnDone:
		if from != 3 || u.Step != team.StepRegistered {
			return false
		}
	case u.State != "" || from == 3 || spawnStepRank[u.Step] != want:
		return false
	}
	return has[1] == (want == 1) && has[2] == (want == 2) && has[3] == (want == 3)
}

// AdvanceSpawnOp moves a running op from fromStep to the next step (or from
// registered to done) in one compare-and-set on the step and the running
// state: a second runner, or a retry after a restart, that read the same
// step loses (won false, nothing changed), as does an unknown or ended op.
// An update that does not fit the step, or a stored row that fails
// checkRunning, is an error and changes nothing.
func (s *Store) AdvanceSpawnOp(id, fromStep string, upd spawnUpdate) (bool, error) {
	if !upd.fits(fromStep) {
		return false, fmt.Errorf("advance spawn op %s: %+v is not the step after %s with its facts", id, upd, fromStep)
	}
	// The read only vets the row this step would build on; the CAS below
	// alone decides who wins.
	cur, ok, err := s.GetSpawnOp(id)
	if err != nil {
		return false, err
	}
	if ok && cur.Step == fromStep && cur.State == team.SpawnRunning {
		if err := cur.checkRunning(); err != nil {
			return false, fmt.Errorf("advance spawn op %s: the stored row is corrupt: %w", id, err)
		}
	}
	state := team.SpawnRunning
	if upd.State == team.SpawnDone {
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
	return s.failSpawnOp(id, "", reason, at)
}

// failSpawnOp ends a running op failed (at step when given). An op forwarded from a lead host writes its `spawn_failed`
// fact in the same transaction, so the lead host is told exactly once whatever dies afterwards.
func (s *Store) failSpawnOp(id, atStep, reason string, at int64) (bool, error) {
	if !spawnReasons[reason] {
		return false, fmt.Errorf("fail spawn op %s: unknown reason %q", id, reason)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, fmt.Errorf("fail spawn op %s: %w", id, err)
	}
	defer tx.Rollback()
	q, args := `UPDATE spawn_ops SET state = 'failed', reason = ?, updated_at = ? WHERE id = ? AND state = 'running'`, []any{reason, at, id}
	if atStep != "" {
		q, args = q+` AND step = ?`, append(args, atStep)
	}
	res, err := tx.Exec(q, args...)
	won, err := oneRow(res, err, "fail spawn op "+id)
	if err != nil || !won {
		return false, err
	}
	var leadHost, teamID string
	if err := tx.QueryRow(`SELECT lead_host_id, team_id FROM spawn_ops WHERE id = ?`, id).Scan(&leadHost, &teamID); err != nil {
		return false, fmt.Errorf("fail spawn op %s: %w", id, err)
	}
	if leadHost != "" {
		if err := writeFactIn(tx, team.TeamFact{ID: uuid.NewString(), Kind: team.FactSpawnFailed, ToHostID: leadHost, TeamID: teamID, MK: id, Reason: reason}, at); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("fail spawn op %s: %w", id, err)
	}
	return true, nil
}

// FinishRemoteSpawn is registered → done for an op forwarded from a lead host: the remote member's row and the
// `registered` fact are written in the transaction that closes the op, a compare-and-set on the step registered (won
// false: another runner, or the op ended, got there first and nothing was written).
func (s *Store) FinishRemoteSpawn(id string, mem remoteMemberRow, fact team.TeamFact, at int64) (bool, error) {
	fail := func(err error) (bool, error) { return false, fmt.Errorf("finish remote spawn %s: %w", id, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE spawn_ops SET step = ?, state = 'done', updated_at = ?
		WHERE id = ? AND step = ? AND state = 'running' AND lead_host_id <> ''`, team.StepRegistered, at, id, team.StepRegistered)
	won, err := oneRow(res, err, "close forwarded spawn op")
	if err != nil || !won {
		return false, err
	}
	if err := insertRemoteMemberIn(tx, mem); err != nil {
		return fail(err)
	}
	if err := writeFactIn(tx, fact, at); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return true, nil
}

// FailSpawnOpAtStep is FailSpawnOp only while the op is still running at
// step: one compare-and-set on the step that a step's advance makes too, so
// of the two exactly one wins (P4-5 re-review: the timeout decides before it
// kills, against the registration).
func (s *Store) FailSpawnOpAtStep(id, step, reason string, at int64) (bool, error) {
	return s.failSpawnOp(id, step, reason, at)
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
// resumes (spec §9.3). A row that fails checkRunning is failed abandoned at
// now and logged instead (R2 finding 2): resuming it would act on facts it
// does not hold, again on every start. Never nil.
func (s *Store) ListRunningSpawnOps(now int64) ([]spawnRow, error) {
	rows, err := s.db.Query(`SELECT ` + spawnCols + ` FROM spawn_ops WHERE state = 'running' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list running spawn ops: %w", err)
	}
	defer rows.Close()
	out, corrupt := []spawnRow{}, map[string]error{}
	for rows.Next() {
		var r spawnRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("list running spawn ops: %w", err)
		}
		if bad := r.checkRunning(); bad != nil {
			corrupt[r.ID] = bad
		} else {
			out = append(out, r)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list running spawn ops: %w", err)
	}
	rows.Close() // before writing: an in-memory store has one connection
	for id, bad := range corrupt {
		if _, err := s.FailSpawnOp(id, team.SpawnReasonAbandoned, now); err != nil {
			return nil, err
		}
		log.Printf("[team] spawn op %s: stored row is corrupt (%v); failed %s", id, bad, team.SpawnReasonAbandoned)
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
