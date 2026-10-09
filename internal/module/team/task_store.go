package teammod

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// Task errors. The routes (T-1b) map them to their 409 codes.
var (
	ErrTaskNotFound      = errors.New("no such task")
	ErrBadTaskTransition = errors.New("task status change not allowed")
	ErrBlockedByUnknown  = errors.New("blocked_by names a task that is not in this team")
	ErrBlockedByCycle    = errors.New("blocked_by would close a cycle")
	ErrOwnerNotActive    = errors.New("the owner is not an active member of the team")
)

// taskSchema is the T-1a1 tasks table (plan "Tables"). Every statement is
// idempotent, so it is safe on a team.db written before it existed. A task
// is keyed (team_id, seq): the wire's display id is only a prefix of the
// team id plus the seq, and every lookup carries the caller's team id.
// Rows are never deleted ("deleted" is a status), so a seq is never reused.
// owner_key is a team_members.spawn_op value (the member key). spawn_op is
// the spawn that created the task (T-2), unique when not ”.
const taskSchema = `
	CREATE TABLE IF NOT EXISTS tasks (
		team_id             TEXT    NOT NULL,
		seq                 INTEGER NOT NULL,
		subject             TEXT    NOT NULL,
		description         TEXT    NOT NULL DEFAULT '',
		done_when_json      TEXT    NOT NULL DEFAULT '[]',
		status              TEXT    NOT NULL,
		owner_key           TEXT    NOT NULL,
		blocked_by_json     TEXT    NOT NULL DEFAULT '[]',
		created_by_ref      TEXT    NOT NULL,
		spawn_op            TEXT    NOT NULL DEFAULT '',
		metadata_json       TEXT    NOT NULL DEFAULT '{}',
		last_report_kind    TEXT    NOT NULL DEFAULT '',
		last_report_summary TEXT    NOT NULL DEFAULT '',
		last_report_at      INTEGER NOT NULL DEFAULT 0,
		last_turn_summary   TEXT    NOT NULL DEFAULT '',
		last_turn_at        INTEGER NOT NULL DEFAULT 0,
		last_turn_seq       INTEGER NOT NULL DEFAULT 0,
		created_at          INTEGER NOT NULL,
		updated_at          INTEGER NOT NULL,
		PRIMARY KEY (team_id, seq)
	);
	CREATE UNIQUE INDEX IF NOT EXISTS tasks_spawn_op ON tasks (spawn_op) WHERE spawn_op != '';
	CREATE INDEX IF NOT EXISTS tasks_owner ON tasks (team_id, owner_key, status);`

// TaskRow is one tasks row. The store never reads the clock: callers pass
// CreatedAt / UpdatedAt (and the `at` of every change).
type TaskRow struct {
	TeamID       string
	Seq          int
	Subject      string
	Description  string
	DoneWhen     []string
	Status       team.TaskStatus
	OwnerKey     string
	BlockedBy    []int
	CreatedByRef string
	SpawnOp      string
	Metadata     team.TaskMetadata

	LastReportKind, LastReportSummary string
	LastReportAt                      int64
	LastTurnSummary                   string
	LastTurnAt, LastTurnSeq           int64

	CreatedAt, UpdatedAt int64
}

const taskCols = `team_id, seq, subject, description, done_when_json, status, owner_key, blocked_by_json,
	created_by_ref, spawn_op, metadata_json, last_report_kind, last_report_summary, last_report_at,
	last_turn_summary, last_turn_at, last_turn_seq, created_at, updated_at`

func scanTask(r rowScanner) (TaskRow, error) {
	var t TaskRow
	var doneJSON, blockedJSON, metaJSON, status string
	if err := r.Scan(&t.TeamID, &t.Seq, &t.Subject, &t.Description, &doneJSON, &status, &t.OwnerKey, &blockedJSON,
		&t.CreatedByRef, &t.SpawnOp, &metaJSON, &t.LastReportKind, &t.LastReportSummary, &t.LastReportAt,
		&t.LastTurnSummary, &t.LastTurnAt, &t.LastTurnSeq, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return TaskRow{}, err
	}
	t.Status = team.TaskStatus(status)
	t.DoneWhen, t.BlockedBy = []string{}, []int{}
	for _, d := range []struct {
		col string
		raw string
		dst any
	}{{"done_when_json", doneJSON, &t.DoneWhen}, {"blocked_by_json", blockedJSON, &t.BlockedBy}, {"metadata_json", metaJSON, &t.Metadata}} {
		if err := json.Unmarshal([]byte(d.raw), d.dst); err != nil {
			return TaskRow{}, fmt.Errorf("decode %s of task %s/%d: %w", d.col, t.TeamID, t.Seq, err)
		}
	}
	if t.DoneWhen == nil {
		t.DoneWhen = []string{}
	}
	if t.BlockedBy == nil {
		t.BlockedBy = []int{}
	}
	return t, nil
}

// immediateTx runs fn in a transaction that took the database's write lock
// first (BEGIN IMMEDIATE), so what fn reads cannot change before it writes.
// modernc's BeginTx has no IMMEDIATE option and the DSN no _txlock, so the
// transaction lives on a dedicated connection. fn must use only conn.
//
// A connection whose transaction could not be ended (a failed COMMIT or
// ROLLBACK) or was abandoned by a panic is discarded, never returned to the
// pool: closing the SQLite connection ends the transaction, whereas a
// pooled one would fail every later BEGIN with "cannot start a transaction
// within a transaction". A failed ROLLBACK is returned joined to the error
// that caused it.
func (s *Store) immediateTx(fn func(ctx context.Context, conn *sql.Conn) error) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	settled := false
	defer func() {
		if !settled { // fn panicked: the transaction is still open
			discardConn(conn)
		}
	}()
	if err := fn(ctx, conn); err != nil {
		settled = true
		if _, rbErr := conn.ExecContext(ctx, "ROLLBACK"); rbErr != nil {
			discardConn(conn)
			return errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
		return err
	}
	var commitErr error
	if s.beforeTaskCommit != nil {
		commitErr = s.beforeTaskCommit()
	}
	if commitErr == nil {
		_, commitErr = conn.ExecContext(ctx, "COMMIT")
	}
	settled = true
	if commitErr != nil {
		discardConn(conn)
		return fmt.Errorf("commit: %w", commitErr)
	}
	return nil
}

// discardConn marks conn bad so that Close drops it instead of pooling it.
func discardConn(conn *sql.Conn) {
	conn.Raw(func(any) error { return driver.ErrBadConn })
}

func getTaskIn(ctx context.Context, conn *sql.Conn, teamID string, seq int) (TaskRow, bool, error) {
	t, err := scanTask(conn.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE team_id = ? AND seq = ?`, teamID, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRow{}, false, nil
	}
	return t, err == nil, err
}

// activeMemberIn reports whether key is an active member row of teamID.
func activeMemberIn(ctx context.Context, conn *sql.Conn, teamID, key string) (bool, error) {
	var one int
	err := conn.QueryRowContext(ctx, `SELECT 1 FROM team_members WHERE spawn_op = ? AND team_id = ? AND state = 'active'`,
		key, teamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// CreateTask stores t as the next task of its team and returns the stored
// row. The caller fills TeamID, Subject, Description, DoneWhen, OwnerKey,
// BlockedBy, CreatedByRef, SpawnOp, CreatedAt and UpdatedAt; the store
// assigns Seq (MAX+1 within the team, never reused) and Status pending.
//
// Everything is read after the write lock is taken, in one transaction:
// the seq, the blockers (each must be a task of this team; a finished one
// is fine; duplicates are dropped), that they close no cycle, and that the
// owner is an active member of the team. A unique violation on spawn_op
// surfaces as the driver's error.
func (s *Store) CreateTask(t TaskRow) (TaskRow, error) {
	if t.TeamID == "" || t.OwnerKey == "" || t.CreatedByRef == "" {
		return TaskRow{}, errors.New("create task: team, owner and creator must be set")
	}
	if err := team.ValidTaskSubject(t.Subject); err != nil {
		return TaskRow{}, fmt.Errorf("create task: %w", err)
	}
	if err := team.ValidTaskDescription(t.Description); err != nil {
		return TaskRow{}, fmt.Errorf("create task: %w", err)
	}
	if err := team.ValidDoneWhen(t.DoneWhen); err != nil {
		return TaskRow{}, fmt.Errorf("create task: %w", err)
	}
	var out TaskRow
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var err error
		out, err = s.createTaskIn(ctx, conn, t)
		return err
	})
	if err != nil {
		return TaskRow{}, err
	}
	return out, nil
}

// createTaskIn is CreateTask's body on a connection that holds the write
// lock (immediateTx), so the member insert of a spawn can share the
// transaction (T-2).
func (s *Store) createTaskIn(ctx context.Context, conn *sql.Conn, t TaskRow) (TaskRow, error) {
	var maxSeq int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM tasks WHERE team_id = ?`, t.TeamID).Scan(&maxSeq); err != nil {
		return TaskRow{}, fmt.Errorf("read next seq: %w", err)
	}
	if s.afterTaskSeqRead != nil {
		s.afterTaskSeqRead()
	}
	seq := maxSeq + 1

	active, err := activeMemberIn(ctx, conn, t.TeamID, t.OwnerKey)
	if err != nil {
		return TaskRow{}, fmt.Errorf("check owner: %w", err)
	}
	if !active {
		return TaskRow{}, ErrOwnerNotActive
	}
	blockedBy, err := checkBlockedBy(ctx, conn, t.TeamID, seq, t.BlockedBy)
	if err != nil {
		return TaskRow{}, err
	}

	row := t
	row.Seq, row.Status, row.BlockedBy = seq, team.TaskPending, blockedBy
	if row.DoneWhen == nil {
		row.DoneWhen = []string{}
	}
	row.Metadata = team.TaskMetadata{}
	doneJSON, _ := json.Marshal(row.DoneWhen)
	blockedJSON, _ := json.Marshal(row.BlockedBy)
	metaJSON, _ := json.Marshal(row.Metadata)
	if _, err := conn.ExecContext(ctx, `INSERT INTO tasks (team_id, seq, subject, description, done_when_json, status,
		owner_key, blocked_by_json, created_by_ref, spawn_op, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.TeamID, row.Seq, row.Subject, row.Description, string(doneJSON), string(row.Status),
		row.OwnerKey, string(blockedJSON), row.CreatedByRef, row.SpawnOp, string(metaJSON), row.CreatedAt, row.UpdatedAt); err != nil {
		return TaskRow{}, fmt.Errorf("insert task: %w", err)
	}
	return row, nil
}

// InsertMemberAndTask is a spawn's member insert and its first task in one
// write transaction (T-2, D-T8): a member never exists without its task, and
// neither is written when either fails. Both halves are idempotent, so a
// finish that runs again after a crash adds nothing: the member by its
// spawn_op, the task by the unique index on tasks.spawn_op (the task whose
// row already exists is returned). t == nil is InsertMember.
func (s *Store) InsertMemberAndTask(m memberRow, t *TaskRow) (TaskRow, error) {
	if t == nil {
		return TaskRow{}, s.InsertMember(m)
	}
	if m.SpawnOp == "" || t.SpawnOp != m.SpawnOp || t.OwnerKey != m.SpawnOp || t.TeamID != m.TeamID {
		return TaskRow{}, errors.New("insert member and task: the task must belong to this spawn's member")
	}
	var out TaskRow
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		if err := insertMemberIn(ctx, conn, m); err != nil {
			return err
		}
		var seq int
		err := conn.QueryRowContext(ctx, `SELECT seq FROM tasks WHERE spawn_op = ?`, m.SpawnOp).Scan(&seq)
		switch {
		case err == nil:
			row, _, gerr := getTaskIn(ctx, conn, m.TeamID, seq)
			out = row
			return gerr
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("read spawn task: %w", err)
		}
		out, err = s.createTaskIn(ctx, conn, *t)
		return err
	})
	if err != nil {
		return TaskRow{}, err
	}
	return out, nil
}

// TaskBySpawnOp is the task a spawn created, if it created one.
func (s *Store) TaskBySpawnOp(teamID, spawnOp string) (TaskRow, bool, error) {
	var seq int
	err := s.db.QueryRow(`SELECT seq FROM tasks WHERE team_id = ? AND spawn_op = ?`, teamID, spawnOp).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRow{}, false, nil
	}
	if err != nil {
		return TaskRow{}, false, fmt.Errorf("task of spawn %s: %w", spawnOp, err)
	}
	return s.GetTask(teamID, seq)
}

// checkBlockedBy returns the deduplicated blockers of the task about to get
// seq newSeq, or ErrBlockedByCycle / ErrBlockedByUnknown. A blocker naming
// newSeq itself is a cycle; otherwise every blocker must exist in the team,
// and no blocker may reach newSeq through the team's existing edges (only
// possible when a stored edge already names a seq that was not created yet).
func checkBlockedBy(ctx context.Context, conn *sql.Conn, teamID string, newSeq int, in []int) ([]int, error) {
	out := make([]int, 0, len(in))
	seen := map[int]bool{}
	for _, b := range in {
		if b == newSeq {
			return nil, ErrBlockedByCycle
		}
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	rows, err := conn.QueryContext(ctx, `SELECT seq, blocked_by_json FROM tasks WHERE team_id = ?`, teamID)
	if err != nil {
		return nil, fmt.Errorf("read blocked_by edges: %w", err)
	}
	edges := map[int][]int{}
	for rows.Next() {
		var seq int
		var raw string
		if err := rows.Scan(&seq, &raw); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read blocked_by edges: %w", err)
		}
		var to []int
		if err := json.Unmarshal([]byte(raw), &to); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode blocked_by of task %s/%d: %w", teamID, seq, err)
		}
		edges[seq] = to
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read blocked_by edges: %w", err)
	}
	rows.Close()

	for _, b := range out {
		if _, ok := edges[b]; !ok {
			return nil, ErrBlockedByUnknown
		}
	}
	visited := map[int]bool{}
	var reaches func(from int) bool
	reaches = func(from int) bool {
		if from == newSeq {
			return true
		}
		if visited[from] {
			return false
		}
		visited[from] = true
		for _, next := range edges[from] {
			if reaches(next) {
				return true
			}
		}
		return false
	}
	for _, b := range out {
		if reaches(b) {
			return nil, ErrBlockedByCycle
		}
	}
	return out, nil
}

// GetTask returns the task (teamID, seq).
func (s *Store) GetTask(teamID string, seq int) (TaskRow, bool, error) {
	t, err := scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE team_id = ? AND seq = ?`, teamID, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRow{}, false, nil
	}
	if err != nil {
		return TaskRow{}, false, fmt.Errorf("get task %s/%d: %w", teamID, seq, err)
	}
	return t, true, nil
}

// ListTasks lists a team's tasks, newest change first (updated_at DESC,
// seq DESC). ownerKey "" is every owner; all=false hides completed and
// deleted ones. The slice is never nil.
func (s *Store) ListTasks(teamID, ownerKey string, all bool) ([]TaskRow, error) {
	return listTasksIn(context.Background(), s.db, teamID, ownerKey, all)
}

// currentTaskOf is the one task a member shows as its current one (plan
// D-T6), for `pdx team` TASK / LAST today and the last-turn target and the
// roster later (T-3a2, T-3b reuse it, so the rule lives here once): the
// in_progress task with the newest updated_at, ties to the highest seq; none
// in progress, the newest pending by the same order. completed and deleted
// never count. rows are one owner's tasks, in any order; ok=false when none
// qualifies. A pure function: no clock, no database.
func currentTaskOf(rows []TaskRow) (TaskRow, bool) {
	var best TaskRow
	bestRank := 0 // 0 = nothing yet, 1 = pending, 2 = in_progress
	for _, r := range rows {
		rank := 0
		switch r.Status {
		case team.TaskInProgress:
			rank = 2
		case team.TaskPending:
			rank = 1
		}
		if rank == 0 {
			continue
		}
		if rank > bestRank || (rank == bestRank && (r.UpdatedAt > best.UpdatedAt || (r.UpdatedAt == best.UpdatedAt && r.Seq > best.Seq))) {
			best, bestRank = r, rank
		}
	}
	return best, bestRank != 0
}

// ListTasksForOwner is a member's list: its own tasks (ListTasks' order and
// all flag), read in the transaction that first checks the member is still
// an active member of a live team of teamID. ok=false, nothing returned,
// when it is not (or ownerKey is empty): the caller answers not_member.
func (s *Store) ListTasksForOwner(teamID, ownerKey string, all bool) ([]TaskRow, bool, error) {
	if ownerKey == "" {
		return nil, false, nil
	}
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("list tasks %s: %w", teamID, err)
	}
	defer tx.Rollback() // a read: nothing to commit
	live, err := liveMemberIn(ctx, tx, teamID, ownerKey)
	if err != nil {
		return nil, false, fmt.Errorf("list tasks %s: check owner: %w", teamID, err)
	}
	if !live {
		return nil, false, nil
	}
	rows, err := listTasksIn(ctx, tx, teamID, ownerKey, all)
	if err != nil {
		return nil, false, err
	}
	return rows, true, nil
}

// listTasksIn is ListTasks on a database or on an open transaction.
func listTasksIn(ctx context.Context, db rowsQuerier, teamID, ownerKey string, all bool) ([]TaskRow, error) {
	q := `SELECT ` + taskCols + ` FROM tasks WHERE team_id = ?`
	args := []any{teamID}
	if ownerKey != "" {
		q += ` AND owner_key = ?`
		args = append(args, ownerKey)
	}
	if !all {
		q += ` AND status IN ('pending', 'in_progress')`
	}
	q += ` ORDER BY updated_at DESC, seq DESC`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks %s: %w", teamID, err)
	}
	defer rows.Close()
	out := []TaskRow{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks %s: %w", teamID, err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks %s: %w", teamID, err)
	}
	return out, nil
}

// SetTaskStatus moves a task to `to` on behalf of `by`. ErrTaskNotFound when
// the task is absent; ErrBadTaskTransition when team.TaskTransitionAllowed
// says no. The update is guarded on the status that was read, in a
// transaction that holds the write lock, so a change is never applied on top
// of one it did not see.
func (s *Store) SetTaskStatus(teamID string, seq int, to team.TaskStatus, by team.TaskActor, at int64) (TaskRow, error) {
	return s.setTaskStatus(teamID, seq, "", to, by, at)
}

// SetTaskStatusByOwner is SetTaskStatus for a member, with the member's
// right read where the write happens: the task's owner_key must be ownerKey
// and ownerKey an active member of a live team, else ErrTaskNotFound with
// nothing written and no hint which condition failed. The owner's
// transition table applies (team.TaskByOwner).
func (s *Store) SetTaskStatusByOwner(teamID string, seq int, ownerKey string, to team.TaskStatus, at int64) (TaskRow, error) {
	if ownerKey == "" {
		return TaskRow{}, ErrTaskNotFound
	}
	return s.setTaskStatus(teamID, seq, ownerKey, to, team.TaskByOwner, at)
}

// setTaskStatus is the one status change; a non-empty ownerKey adds the
// owner's right as a condition of the same transaction.
func (s *Store) setTaskStatus(teamID string, seq int, ownerKey string, to team.TaskStatus, by team.TaskActor, at int64) (TaskRow, error) {
	var out TaskRow
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		cur, ok, err := getTaskIn(ctx, conn, teamID, seq)
		if err != nil {
			return fmt.Errorf("read task %s/%d: %w", teamID, seq, err)
		}
		if !ok {
			return ErrTaskNotFound
		}
		if ownerKey != "" {
			live := cur.OwnerKey == ownerKey
			if live {
				if live, err = liveMemberIn(ctx, conn, teamID, ownerKey); err != nil {
					return fmt.Errorf("check owner: %w", err)
				}
			}
			if !live {
				return ErrTaskNotFound
			}
		}
		if !team.TaskTransitionAllowed(cur.Status, to, by) {
			return ErrBadTaskTransition
		}
		res, err := conn.ExecContext(ctx, `UPDATE tasks SET status = ?, updated_at = ? WHERE team_id = ? AND seq = ? AND status = ?`,
			string(to), at, teamID, seq, string(cur.Status))
		if err != nil {
			return fmt.Errorf("update task %s/%d: %w", teamID, seq, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrBadTaskTransition
		}
		cur.Status, cur.UpdatedAt = to, at
		out = cur
		return nil
	})
	if err != nil {
		return TaskRow{}, err
	}
	return out, nil
}

// rowQuerier is what *sql.Conn and *sql.Tx share for a single-row read.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// liveMemberIn reports whether key is an active member row of teamID whose
// team is live (not ended): the only state in which a member may act.
func liveMemberIn(ctx context.Context, q rowQuerier, teamID, key string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.spawn_op = ? AND m.team_id = ? AND m.state = 'active' AND t.ended_at = 0`, key, teamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// GetTaskDetail reads task (teamID, seq) and its reports (newest first, at
// most 200, as ListReports) in one transaction, so the two are one view.
// A non-empty ownerKey is the member asking: the task must be its own and
// it still an active member of a live team, checked in that same
// transaction. ok=false (nothing returned) when the task is absent or the
// owner check fails, without saying which. ownerKey "" is the lead: the
// team scope alone applies.
func (s *Store) GetTaskDetail(teamID string, seq int, ownerKey string) (TaskRow, []ReportRow, bool, error) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskRow{}, nil, false, fmt.Errorf("task detail %s/%d: %w", teamID, seq, err)
	}
	defer tx.Rollback() // a read: nothing to commit
	row, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE team_id = ? AND seq = ?`, teamID, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRow{}, nil, false, nil
	}
	if err != nil {
		return TaskRow{}, nil, false, fmt.Errorf("task detail %s/%d: %w", teamID, seq, err)
	}
	if ownerKey != "" {
		live := row.OwnerKey == ownerKey
		if live {
			if live, err = liveMemberIn(ctx, tx, teamID, ownerKey); err != nil {
				return TaskRow{}, nil, false, fmt.Errorf("task detail %s/%d: check owner: %w", teamID, seq, err)
			}
		}
		if !live {
			return TaskRow{}, nil, false, nil
		}
	}
	reports, err := listReportsIn(ctx, tx, teamID, seq, 0, maxListReports)
	if err != nil {
		return TaskRow{}, nil, false, err
	}
	return row, reports, true, nil
}

// ReassignTask hands a pending or in-progress task to another active member
// of the team: the owner changes and the status goes back to pending (the
// new owner starts it). Handing a task to the member that already owns it
// is a no-op that returns the row as it is. ErrTaskNotFound / ErrBadTaskTransition (finished
// task) / ErrOwnerNotActive.
func (s *Store) ReassignTask(teamID string, seq int, toKey string, at int64) (TaskRow, error) {
	var out TaskRow
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		cur, ok, err := getTaskIn(ctx, conn, teamID, seq)
		if err != nil {
			return fmt.Errorf("read task %s/%d: %w", teamID, seq, err)
		}
		if !ok {
			return ErrTaskNotFound
		}
		if cur.Status != team.TaskPending && cur.Status != team.TaskInProgress {
			return ErrBadTaskTransition
		}
		active, err := activeMemberIn(ctx, conn, teamID, toKey)
		if err != nil {
			return fmt.Errorf("check owner: %w", err)
		}
		if !active {
			return ErrOwnerNotActive
		}
		if cur.OwnerKey == toKey { // already theirs: nothing to hand over, status and stamp stay
			out = cur
			return nil
		}
		res, err := conn.ExecContext(ctx, `UPDATE tasks SET owner_key = ?, status = 'pending', updated_at = ?
			WHERE team_id = ? AND seq = ? AND status = ?`, toKey, at, teamID, seq, string(cur.Status))
		if err != nil {
			return fmt.Errorf("reassign task %s/%d: %w", teamID, seq, err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrBadTaskTransition
		}
		cur.OwnerKey, cur.Status, cur.UpdatedAt = toKey, team.TaskPending, at
		out = cur
		return nil
	})
	if err != nil {
		return TaskRow{}, err
	}
	return out, nil
}
