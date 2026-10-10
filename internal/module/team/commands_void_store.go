// internal/module/team/commands_void_store.go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// applyVoidIn is `void {command_id}` (spec §3.3): the lead host gave up on an adopt/spawn it never saw answered.
//   - never seen here: record the id in team_command_voids, so the late copy answers command_void;
//   - applied adopt: undo it (the row → released, the member owed the released notice), record the id so a late
//     copy no longer replays "applied";
//   - applied but the member already left, or refused: nothing to undo;
//   - anything that is no adopt/spawn: refused.
//
// A spawn is undone by a kill, which is X4a's; none can be logged before that.
func applyVoidIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	target := c.CommandID
	var kind string
	var status int
	err := tx.QueryRow(`SELECT kind, status FROM team_command_log WHERE lead_host_id = ? AND id = ?`, p.LeadHostID, target).Scan(&kind, &status)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := recordVoidIn(tx, p.LeadHostID, target, c.TeamID, c.ID, p.Now); err != nil {
			return CommandResult{}, err
		}
		return okResult(map[string]string{"state": "recorded"})
	case err != nil:
		return CommandResult{}, err
	case kind == team.CommandSpawn:
		return refusal(http.StatusBadRequest, team.ErrCommandUnsupportedKind, "undoing a spawn is not supported by this version"), nil
	case kind != team.CommandAdopt:
		return refusal(http.StatusConflict, team.ErrCommandNotVoidable, "only an adopt or a spawn can be voided"), nil
	case status != http.StatusOK:
		return okResult(map[string]string{"state": "ok"}) // refused: nothing was applied, its stored refusal stands
	}
	var leadAddr, teamName, rowTeam string
	err = tx.QueryRow(`SELECT lead_address, team_name, team_id FROM remote_members WHERE mk = ? AND lead_host_id = ?`, target, p.LeadHostID).Scan(&leadAddr, &teamName, &rowTeam)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CommandResult{}, err
	}
	if err == nil && rowTeam != c.TeamID { // a void belongs to a team like every command
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "that command belongs to another team"), nil
	}
	state := "ok"
	if err == nil {
		undone, err := casRemoteMemberStateIn(tx, target, []string{remoteActive}, remoteReleased, p.Now)
		if err != nil {
			return CommandResult{}, err
		}
		if undone {
			state = "undone"
			if err := oweNoticeIn(tx, target, noticeReleased, c.ID, leadAddr, teamName, p.Now); err != nil {
				return CommandResult{}, err
			}
		}
	}
	if err := recordVoidIn(tx, p.LeadHostID, target, c.TeamID, c.ID, p.Now); err != nil {
		return CommandResult{}, err
	}
	return okResult(map[string]string{"state": state})
}

func recordVoidIn(tx dbtx, leadHostID, commandID, teamID, voidID string, at int64) error {
	_, err := tx.Exec(`INSERT INTO team_command_voids (lead_host_id, command_id, team_id, void_id, at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (lead_host_id, command_id) DO NOTHING`, leadHostID, commandID, teamID, voidID, at)
	if err != nil {
		return fmt.Errorf("record void of %s: %w", commandID, err)
	}
	return nil
}
