// internal/module/team/commands_team_store.go
package teammod

import (
	"database/sql"

	"github.com/wake/purdex/internal/team"
)

// applyTeamLevelIn is `end` (every live row → ended, notice `team_ended`) and `lead_moved` (every live row keeps
// its state and takes the new lead tuple, notice `handover`): team-level, so no mk — the rows are the team's from
// the sending lead host. No live row is not an error; the command is simply already true.
func applyTeamLevelIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	rows, err := tx.Query(`SELECT mk, lead_address, team_name FROM remote_members WHERE lead_host_id = ? AND team_id = ? AND state = ? ORDER BY created_at, mk`,
		p.LeadHostID, c.TeamID, remoteActive)
	if err != nil {
		return CommandResult{}, err
	}
	type live struct{ mk, leadAddr, teamName string }
	var mks []live
	for rows.Next() {
		var l live
		if err := rows.Scan(&l.mk, &l.leadAddr, &l.teamName); err != nil {
			rows.Close()
			return CommandResult{}, err
		}
		mks = append(mks, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CommandResult{}, err
	}
	rows.Close()
	if c.Kind == team.CommandEnd {
		// A forwarded spawn of the team that has not registered yet ends with it (#2327), in this transaction: the
		// runner's registration is a compare-and-set on a running op, so it can no longer add a member after the end.
		// No fact is queued — the lead host's team is over and it would answer `ignored`. The caller kills the sessions.
		if _, err := tx.Exec(`UPDATE spawn_ops SET state = 'failed', reason = ?, updated_at = ? WHERE lead_host_id = ? AND team_id = ? AND state = 'running'`,
			team.SpawnReasonAbandoned, p.Now, p.LeadHostID, c.TeamID); err != nil {
			return CommandResult{}, err
		}
	}
	for _, l := range mks {
		mk := l.mk
		if c.Kind == team.CommandEnd {
			if _, err := casRemoteMemberStateIn(tx, mk, []string{remoteActive}, remoteEnded, p.Now); err != nil {
				return CommandResult{}, err
			}
			if err := oweNoticeIn(tx, mk, noticeTeamEnded, c.ID, l.leadAddr, l.teamName, p.Now); err != nil {
				return CommandResult{}, err
			}
			continue
		}
		if _, err := tx.Exec(`UPDATE remote_members SET lead_session_id = ?, lead_ref = ?, lead_address = ?, lead_title = ?,
			lead_pid = ?, lead_proc_start = ?, updated_at = ? WHERE mk = ? AND state = ?`,
			c.LeadSessionID, c.LeadRef, c.Lead.Address, c.Lead.Title, c.Lead.PID, c.Lead.ProcStart, p.Now, mk, remoteActive); err != nil {
			return CommandResult{}, err
		}
		if err := oweNoticeIn(tx, mk, noticeHandover, c.ID, c.Lead.Address, l.teamName, p.Now); err != nil {
			return CommandResult{}, err
		}
	}
	return okResult(map[string]any{"state": "ok", "affected": len(mks)})
}
