package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// SetAppearance stores a live team's name, label and colour (nil = automatic) in one immediate transaction, so it
// serialises with every other writer of the row.
func (s *Store) SetAppearance(teamID, name, label string, color *int) (AppearanceResult, error) {
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
		_, err = conn.ExecContext(ctx, `UPDATE teams SET team_name = ?, team_label = ?, team_color = ? WHERE id = ? AND ended_at = 0`, name, label, col, teamID)
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
