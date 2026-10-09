package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// migrateTeamColor gives teams the colour the user picked in the panel (TR-1): an index 0–7, NULL = automatic (the App's
// hash). A row written before it reads NULL.
func migrateTeamColor(db *sql.DB) error {
	return ensureColumn(db, "teams", "team_color", "INTEGER")
}

// AppearanceOutcome is how SetAppearance ended.
type AppearanceOutcome int

const (
	AppearanceSet     AppearanceOutcome = iota
	AppearanceNoTeam                    // no team with that id
	AppearanceNotLive                   // the team has ended
)

// AppearanceResult carries the values the edit replaced, for the log line.
type AppearanceResult struct {
	Outcome           AppearanceOutcome
	OldName, OldLabel string
	OldColor          *int
}

// dbtxq is dbtx that can also run a multi-row query (a *sql.Tx, or a connection inside an immediate transaction).
type dbtxq interface {
	dbtx
	Query(query string, args ...any) (*sql.Rows, error)
}

func (c connTx) Query(q string, args ...any) (*sql.Rows, error) {
	return c.conn.QueryContext(c.ctx, q, args...)
}

// AppearanceFanout is what a rename sends to the member hosts (#2288): the hosts that announce team.appearance, the lead's
// tuple and the id source. Nil = nothing to send (a team with no member host, or no host caller).
type AppearanceFanout struct {
	Hosts map[string]bool
	Lead  team.TeamLead
	NewID func() string
	Now   int64
}

// SetAppearance stores a live team's name, label and colour (nil = automatic) in one immediate transaction, so it
// serialises with every other writer of the row. With a fanout the same transaction enqueues team.appearance for every
// announcing host that holds a live remote row of the team or a running forwarded spawn (spec rule 2).
func (s *Store) SetAppearance(teamID, name, label string, color *int, fan *AppearanceFanout) (AppearanceResult, error) {
	var res AppearanceResult
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var endedAt int64
		var oldColor sql.NullInt64
		err := conn.QueryRowContext(ctx, `SELECT ended_at, team_name, team_label, team_color FROM teams WHERE id = ?`, teamID).
			Scan(&endedAt, &res.OldName, &res.OldLabel, &oldColor)
		if errors.Is(err, sql.ErrNoRows) {
			res.Outcome = AppearanceNoTeam
			return nil
		}
		if err != nil {
			return err
		}
		if endedAt != 0 {
			res.Outcome = AppearanceNotLive
			return nil
		}
		if oldColor.Valid {
			c := int(oldColor.Int64)
			res.OldColor = &c
		}
		var col any
		if color != nil {
			col = *color
		}
		if _, err = conn.ExecContext(ctx, `UPDATE teams SET team_name = ?, team_label = ?, team_color = ? WHERE id = ? AND ended_at = 0`, name, label, col, teamID); err != nil {
			return err
		}
		if fan == nil || len(fan.Hosts) == 0 {
			return nil
		}
		_, err = s.enqueueTeamLevelTx(connTx{ctx, conn}, team.Team{ID: teamID, TeamName: name}, CmdAppearance, fan.Lead, func(c *team.TeamCommand) {
			c.TeamLabel, c.TeamColor = label, color
		}, fan.NewID, fan.Now, fan.Hosts)
		return err
	})
	if err != nil {
		return AppearanceResult{}, fmt.Errorf("set appearance of team %s: %w", teamID, err)
	}
	return res, nil
}

// TeamColors reads the stored colour of each team that has one (a team on automatic is absent from the map).
func (s *Store) TeamColors(teamIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(teamIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(teamIDs))
	for i, id := range teamIDs {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT id, team_color FROM teams WHERE team_color IS NOT NULL AND id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(teamIDs)), ",")+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("read team colours: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var c int
		if err := rows.Scan(&id, &c); err != nil {
			return nil, fmt.Errorf("read team colours: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}
