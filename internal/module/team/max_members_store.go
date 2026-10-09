package teammod

import (
	"context"
	"database/sql"
	"fmt"
)

// MaxMembersOutcome is what SetMaxMembers did.
type MaxMembersOutcome int

const (
	MaxSet        MaxMembersOutcome = iota // written
	MaxNoTeam                              // no live team with that id (an ended one included)
	MaxBelowInUse                          // max < in_use: nothing written
)

// MaxMembersResult is SetMaxMembers' answer; InUse is the count the decision used.
type MaxMembersResult struct {
	Outcome MaxMembersOutcome
	InUse   int
}

// SetMaxMembers sets teams.grant_json.$.max_members of the live team id to max (the caller checked 1..MaxMaxMembers).
// One BEGIN IMMEDIATE transaction: the write lock is taken first, so the in_use count (running spawns + active
// members, exactly the spawn cap check's) cannot change before the write, and a spawn or adopt that checks the cap
// serialises with it.
func (s *Store) SetMaxMembers(teamID string, max int) (MaxMembersResult, error) {
	var res MaxMembersResult
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var live int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM teams WHERE id = ? AND ended_at = 0`, teamID).Scan(&live); err != nil {
			return err
		}
		if live == 0 {
			res.Outcome = MaxNoTeam
			return nil
		}
		if err := conn.QueryRowContext(ctx, seatsTakenSQL, teamID, "").Scan(&res.InUse); err != nil {
			return err
		}
		if s.afterMaxMembersCount != nil {
			s.afterMaxMembersCount()
		}
		if max < res.InUse {
			res.Outcome = MaxBelowInUse
			return nil
		}
		_, err := conn.ExecContext(ctx, `UPDATE teams SET grant_json = json_set(grant_json, '$.max_members', ?) WHERE id = ? AND ended_at = 0`, max, teamID)
		return err
	})
	if err != nil {
		return MaxMembersResult{}, fmt.Errorf("set max members of team %s: %w", teamID, err)
	}
	return res, nil
}

// seatsTakenSQL is THE seat count, shared by SetMaxMembers, the spawn cap check, the adopt seat check and the roster's
// in_use. ?1 is the team, ?2 a spawn op id to leave out (the op asking; "" for none). Each active member counts once;
// a running spawn op counts only while no active member row carries its spawn_op, because spawnFinish inserts the
// member before it moves the op to done, and in that window one seat is both.
const seatsTakenSQL = `SELECT
	(SELECT COUNT(*) FROM team_members WHERE team_id = ?1 AND state = 'active') +
	(SELECT COUNT(*) FROM spawn_ops o WHERE o.team_id = ?1 AND o.state = 'running' AND o.id <> ?2
		AND NOT EXISTS (SELECT 1 FROM team_members m WHERE m.spawn_op = o.id AND m.state = 'active'))`

// seatsTaken is seatsTakenSQL's count of team teamID, leaving out spawn op exceptOp.
func seatsTaken(q dbtx, teamID, exceptOp string) (int, error) {
	var n int
	if err := q.QueryRow(seatsTakenSQL, teamID, exceptOp).Scan(&n); err != nil {
		return 0, fmt.Errorf("seats taken of team %s: %w", teamID, err)
	}
	return n, nil
}

// InUseOfTeams is the in_use count (seatsTakenSQL) of each team id; a team with none is absent.
func (s *Store) InUseOfTeams(ids []string) (map[string]int, error) {
	out := map[string]int{}
	for _, id := range ids {
		n, err := seatsTaken(s.db, id, "")
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out[id] = n
		}
	}
	return out, nil
}
