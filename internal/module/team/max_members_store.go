package teammod

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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
		if err := conn.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM spawn_ops WHERE team_id = ? AND state = 'running') +
			(SELECT COUNT(*) FROM team_members WHERE team_id = ? AND state = 'active')`, teamID, teamID).Scan(&res.InUse); err != nil {
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

// InUseOfTeams is the in_use count (running spawns + active members, as the spawn cap check counts) of each team id;
// a team with none is absent. One read for the whole roster.
func (s *Store) InUseOfTeams(ids []string) (map[string]int, error) {
	out := map[string]int{}
	if len(ids) == 0 {
		return out, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, 2*len(ids))
	for i := 0; i < 2; i++ {
		for _, id := range ids {
			args = append(args, id)
		}
	}
	rows, err := s.db.Query(`SELECT team_id, COUNT(*) FROM (
		SELECT team_id FROM spawn_ops WHERE state = 'running' AND team_id IN (`+marks+`)
		UNION ALL
		SELECT team_id FROM team_members WHERE state = 'active' AND team_id IN (`+marks+`)) GROUP BY team_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("in use of teams: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("in use of teams: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}
