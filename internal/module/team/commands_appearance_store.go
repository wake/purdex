// internal/module/team/commands_appearance_store.go
package teammod

import (
	"database/sql"
)

// applyAppearanceIn is `team.appearance` (#2288): the lead host's current name, label and colour for its team, written to
// every live row of that (lead host, team) and to the forwarded spawns still running (their row is made from the op).
// No live row is not an error: the command is simply already true. A member's notice reads its row when it is delivered,
// so a later notice already carries the new name.
func applyAppearanceIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	var color any
	if c.TeamColor != nil {
		color = *c.TeamColor
	}
	res, err := tx.Exec(`UPDATE remote_members SET team_name = ?, team_label = ?, team_color = ?, updated_at = ? WHERE lead_host_id = ? AND team_id = ? AND state = ?`,
		c.TeamName, c.TeamLabel, color, p.Now, p.LeadHostID, c.TeamID, remoteActive)
	if err != nil {
		return CommandResult{}, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Exec(`UPDATE spawn_ops SET lead_json = CASE WHEN json_valid(lead_json) THEN json_set(lead_json, '$.team_name', ?, '$.team_label', ?, '$.team_color', ?) ELSE lead_json END
		WHERE lead_host_id = ? AND team_id = ? AND state = 'running'`,
		c.TeamName, c.TeamLabel, color, p.LeadHostID, c.TeamID); err != nil {
		return CommandResult{}, err
	}
	return okResult(map[string]any{"state": "ok", "affected": n})
}
