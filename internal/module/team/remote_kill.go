// internal/module/team/remote_kill.go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// The `kill` command on the member host (cross-host team spec §5.2, §6.2, plan X4a): the lead host kills one of its
// remote members. The member's Claude Code process is signalled by the very path a local adopted kill uses (killAdopted:
// SIGTERM to the pid the registry shows now, re-verified against the process table right before the signal), and the row
// goes active → killed (or → gone when nothing was left to signal) in the command's transaction. The side effect cannot
// be inside the transaction, so it comes first and is safe to repeat: a crash between the signal and the commit leaves
// the row active, the lead host sends the command again, and the second try finds the session gone.

// prepareKill signals the target of kill command cmd (already bound, in shape and consented) and records on the plan what
// it found. A non-zero status is an error to answer (a retryable one: the registry could not be read, the signal failed);
// zero with an empty plan.KillState means there is no live row of this lead host to kill (the store refuses it).
func (m *Module) prepareKill(plan *CommandPlan, cmd team.TeamCommand) (status int, code, detail string) {
	row, found, err := m.store.RemoteMember(cmd.MK)
	switch {
	case err != nil:
		m.logf("[team] kill %s: %v", cmd.ID, err)
		return http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log"
	case !found || row.LeadHostID != plan.LeadHostID || row.TeamID != cmd.TeamID:
		return 0, "", ""
	}
	switch row.State {
	case remoteKilled, remoteGone:
		plan.KillState = row.State // an earlier kill (another command id): the answer is the same
	case remoteActive:
		ended, status, code, why := m.killAdopted(memberRow{SessionID: row.MemberSessionID})
		if why != "" {
			m.logf("[team] kill %s of %s: %s", cmd.ID, row.MemberSessionID, why)
			return status, code, why
		}
		plan.KillState = remoteKilled
		if ended {
			plan.KillState = remoteGone
		}
	}
	return 0, "", ""
}

// applyKillIn is `kill` (active → killed | gone). Only the lead host's own row of that team is killable; a row that is
// already killed or gone answers as it is (a replay under another command id); anything else is not_your_member.
func applyKillIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	if !p.Consent {
		return refusal(http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team commands from the lead host"), nil
	}
	var host, teamID, state string
	err := tx.QueryRow(`SELECT lead_host_id, team_id, state FROM remote_members WHERE mk = ?`, c.MK).Scan(&host, &teamID, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (host != p.LeadHostID || teamID != c.TeamID)) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no live member of that team here"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	switch state {
	case remoteKilled, remoteGone:
		return okResult(map[string]string{"state": state})
	case remoteActive:
		if p.KillState != remoteKilled && p.KillState != remoteGone {
			// The handler saw no live row, yet there is one: it changed between the two. Not answered, not logged: the lead
			// host sends it again.
			return CommandResult{}, fmt.Errorf("kill %s: the member row of %s appeared after the signal step", c.ID, c.MK)
		}
		if ok, err := casRemoteMemberStateIn(tx, c.MK, []string{remoteActive}, p.KillState, p.Now); err != nil {
			return CommandResult{}, err
		} else if !ok {
			return CommandResult{}, fmt.Errorf("kill %s: the member row of %s moved while it was killed", c.ID, c.MK)
		}
		return okResult(map[string]string{"state": p.KillState})
	}
	return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no live member of that team here"), nil
}
