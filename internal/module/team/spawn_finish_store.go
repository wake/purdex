// internal/module/team/spawn_finish_store.go
package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

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
	var out TaskRow
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var err error
		out, err = s.insertMemberTaskIn(ctx, conn, m, t)
		return err
	})
	if err != nil {
		return TaskRow{}, err
	}
	return out, nil
}

// insertMemberTaskIn is the member row and, when t is set, the spawn's first task, in the caller's transaction.
func (s *Store) insertMemberTaskIn(ctx context.Context, conn *sql.Conn, m memberRow, t *TaskRow) (TaskRow, error) {
	if t == nil {
		return TaskRow{}, insertMemberIn(ctx, conn, m)
	}
	if m.SpawnOp == "" || t.SpawnOp != m.SpawnOp || t.OwnerKey != m.SpawnOp || t.TeamID != m.TeamID {
		return TaskRow{}, errors.New("insert member and task: the task must belong to this spawn's member")
	}
	if err := insertMemberIn(ctx, conn, m); err != nil {
		return TaskRow{}, err
	}
	var seq int
	err := conn.QueryRowContext(ctx, `SELECT seq FROM tasks WHERE spawn_op = ?`, m.SpawnOp).Scan(&seq)
	switch {
	case err == nil:
		row, _, gerr := getTaskIn(ctx, conn, m.TeamID, seq)
		return row, gerr
	case !errors.Is(err, sql.ErrNoRows):
		return TaskRow{}, fmt.Errorf("read spawn task: %w", err)
	}
	return s.createTaskIn(ctx, conn, *t)
}

// errSpawnFinishLost rolls a finish back when the op is no longer at registered / running.
var errSpawnFinishLost = errors.New("the spawn op is no longer at registered")

// FinishSpawn is registered → done of a local spawn op, the member row and the spawn's first task (when t is set) in ONE
// immediate transaction (#2105): the compare-and-set on the op comes first and the inserts follow, so a finish that loses
// it (the op was aborted or its team ended meanwhile: won false) or fails writes none of the three. A stored op row that
// fails checkRunning is an error, as in AdvanceSpawnOp.
func (s *Store) FinishSpawn(id string, m memberRow, t *TaskRow, at int64) (won bool, err error) {
	cur, ok, err := s.GetSpawnOp(id)
	if err != nil {
		return false, err
	}
	if ok && cur.Step == team.StepRegistered && cur.State == team.SpawnRunning {
		if err := cur.checkRunning(); err != nil {
			return false, fmt.Errorf("finish spawn op %s: the stored row is corrupt: %w", id, err)
		}
	}
	if s.failSpawnDone != nil {
		if err := s.failSpawnDone(); err != nil {
			return false, err
		}
	}
	err = s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx, `UPDATE spawn_ops SET step = ?, state = 'done', updated_at = ? WHERE id = ? AND step = ? AND state = 'running'`,
			team.StepRegistered, at, id, team.StepRegistered)
		if won, err = oneRow(res, err, "finish spawn op "+id); err != nil {
			return err
		}
		if !won {
			return errSpawnFinishLost
		}
		inserted, err := insertMemberRowIn(ctx, conn, m)
		if err != nil {
			return err
		}
		if !inserted { // a row of this spawn_op exists: only the same half-finished member may be finished (#2384)
			if err := sameHalfFinishedMember(ctx, conn, m); err != nil {
				return err
			}
		}
		_, err = s.insertMemberTaskIn(ctx, conn, m, t)
		return err
	})
	if errors.Is(err, errSpawnFinishLost) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

// sameHalfFinishedMember is nil when the stored member row of m.SpawnOp is the member this finish would insert: same team,
// session, host and tmux identity (id, generation, pane), same origin, and still active. Anything else — a terminal row, another
// session or tmux session, an adopted row — is an error naming what differs, and the finish rolls back (#2384).
func sameHalfFinishedMember(ctx context.Context, conn *sql.Conn, m memberRow) error {
	var have memberRow
	err := conn.QueryRowContext(ctx, `SELECT team_id, host_id, session_id, tmux_id, tmux_instance, pane_id, origin, state FROM team_members WHERE spawn_op = ?`, m.SpawnOp).
		Scan(&have.TeamID, &have.HostID, &have.SessionID, &have.TmuxID, &have.TmuxInstance, &have.PaneID, &have.Origin, &have.State)
	if err != nil {
		return fmt.Errorf("finish spawn %s: read the stored member row: %w", m.SpawnOp, err)
	}
	origin := m.Origin
	if origin == "" {
		origin = team.MemberOriginSpawned
	}
	switch {
	case have.State != team.MemberActive:
		return fmt.Errorf("finish spawn %s: its stored member row is %s, not active", m.SpawnOp, have.State)
	case have.TeamID != m.TeamID || have.SessionID != m.SessionID:
		return fmt.Errorf("finish spawn %s: its stored member row belongs to team %s / session %s", m.SpawnOp, have.TeamID, have.SessionID)
	case have.HostID != m.HostID:
		return fmt.Errorf("finish spawn %s: its stored member row belongs to host %s", m.SpawnOp, have.HostID)
	case have.TmuxID != m.TmuxID || have.TmuxInstance != m.TmuxInstance || have.PaneID != m.PaneID:
		return fmt.Errorf("finish spawn %s: its stored member row has tmux %s (%s, pane %s), not %s (%s, pane %s)", m.SpawnOp, have.TmuxID, have.TmuxInstance, have.PaneID, m.TmuxID, m.TmuxInstance, m.PaneID)
	case have.Origin != origin:
		return fmt.Errorf("finish spawn %s: its stored member row has origin %s", m.SpawnOp, have.Origin)
	}
	return nil
}
