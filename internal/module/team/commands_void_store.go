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
//   - applied spawn whose op still runs: the op fails (running → failed, abandoned) in this transaction, so the runner's
//     registration — a compare-and-set on a running op — can no longer add a member; the caller releases its reserved name
//     and stops its session once this commits (#3537). Exactly one of the void and the registration wins;
//   - applied spawn whose op already failed: nothing to undo; whose member registered (done): answered too_late and
//     left alone, a registered member is undone by a kill, which is X4a's;
//   - anything that is no adopt/spawn: refused.
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
		return okResult(team.VoidOutcome{State: team.VoidNotApplied})
	case err != nil:
		return CommandResult{}, err
	case kind == team.CommandRelay:
		return voidRelayIn(tx, p, target, status)
	case kind == team.CommandSpawn:
		return voidSpawnIn(tx, p, target, status)
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

// voidSpawnIn is a void of an applied spawn. The op's id is the spawn command's id.
func voidSpawnIn(tx *sql.Tx, p CommandPlan, target string, status int) (CommandResult, error) {
	c := p.cmd
	if status != http.StatusOK {
		return okResult(map[string]string{"state": "ok"}) // refused: no op was made, its stored refusal stands
	}
	var opTeam, state, poolName string
	err := tx.QueryRow(`SELECT team_id, state, pool_name FROM spawn_ops WHERE id = ? AND lead_host_id = ?`, target, p.LeadHostID).Scan(&opTeam, &state, &poolName)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CommandResult{}, err
	}
	if found && opTeam != c.TeamID { // a void belongs to a team like every command
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "that command belongs to another team"), nil
	}
	if found && state == string(team.SpawnDone) {
		// Too late: the member registered first and undoing it is X4a's. Answered 200 so the lead host settles it: a refusal
		// with unsupported_kind would be read by its outbox as a downgraded host and sent again for ever. The void is NOT
		// recorded, so a late copy of the spawn still answers its stored `accepted` and not command_void.
		return okResult(map[string]string{"state": "too_late"})
	}
	outcome := "ok"
	var ended []string
	if found && state == string(team.SpawnRunning) {
		r, err := tx.Exec(`UPDATE spawn_ops SET state = 'failed', reason = ?, updated_at = ? WHERE id = ? AND lead_host_id = ? AND state = 'running'`,
			team.SpawnReasonAbandoned, p.Now, target, p.LeadHostID)
		won, err := oneRow(r, err, "void spawn op "+target)
		if err != nil {
			return CommandResult{}, err
		}
		if won {
			// No fact is queued: the lead host already gave this spawn up and would answer `ignored`.
			outcome = "undone"
			if poolName != "" {
				ended = []string{target}
			}
		}
	}
	if err := recordVoidIn(tx, p.LeadHostID, target, c.TeamID, c.ID, p.Now); err != nil {
		return CommandResult{}, err
	}
	res, err := okResult(map[string]string{"state": outcome})
	res.endedSpawns = ended
	return res, err
}
