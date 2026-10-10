// internal/module/team/commands_member_store.go
package teammod

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// applyAdoptIn is the adopt command (spec §5.2 "—, adopt → active"): consent, a live target, no role, then the
// remote row and its owed `adopted` notice.
func applyAdoptIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	switch {
	case !p.Consent:
		return refusal(http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team commands from the lead host"), nil
	case p.Target == nil:
		return refusal(http.StatusConflict, team.ErrAdoptTargetNotFound, "no live session answers to the target"), nil
	}
	role, err := sessionRoleIn(tx, p.Target.SessionID)
	if err != nil {
		return CommandResult{}, err
	}
	switch {
	case role == sessionRoleLead:
		return refusal(http.StatusConflict, team.ErrAdoptTargetIsLead, "the target leads a live team"), nil
	case role.isMember():
		return refusal(http.StatusConflict, team.ErrAdoptAlreadyMember, "the target is already a member of a live team"), nil
	}
	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM remote_members WHERE mk = ?`, c.MK).Scan(&one); {
	case err == nil:
		return refusal(http.StatusConflict, team.ErrCommandMKConflict, "the member key is stored for another membership"), nil
	case !errors.Is(err, sql.ErrNoRows):
		return CommandResult{}, err
	}
	o := p.Target
	tmuxSession, pane := splitTmux(o.Tmux)
	row := remoteMemberRow{MK: c.MK, MemberSessionID: o.SessionID, Ref: o.Ref, TeamID: c.TeamID, TeamName: c.TeamName,
		LeadHostID: p.LeadHostID, LeadSessionID: c.Lead.SessionID, LeadRef: c.Lead.Ref, LeadAddress: c.Lead.Address,
		LeadTitle: c.Lead.Title, LeadPID: c.Lead.PID, LeadProcStart: c.Lead.ProcStart, Origin: team.MemberOriginAdopted,
		State: remoteActive, PID: o.PID, ProcStart: o.ProcStart, PaneID: pane, TmuxSession: tmuxSession, Cwd: o.Cwd,
		Title: o.Title, TeamLabel: c.TeamLabel, CreatedAt: p.Now, UpdatedAt: p.Now}
	if c.TeamColor != nil { // absent = an older lead host, or automatic: the row starts without one either way
		row.TeamColor = sql.NullInt64{Int64: int64(*c.TeamColor), Valid: true}
	}
	if err := insertRemoteMemberIn(tx, row); err != nil {
		return CommandResult{}, err
	}
	if err := oweNoticeIn(tx, c.MK, noticeAdopted, c.ID, c.Lead.Address, c.TeamName, p.Now); err != nil {
		return CommandResult{}, err
	}
	return okResult(team.AdoptOutcome{State: "applied", MemberSession: o.SessionID, Ref: o.Ref, PID: o.PID,
		ProcStart: o.ProcStart, Title: o.Title, Cwd: o.Cwd, Tmux: o.Tmux})
}

// applyReleaseIn is `release` (active → released, notice `released`). Only the lead host's own live row of that
// team is releasable; anything else is not_your_member.
func applyReleaseIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	var host, teamID, state, leadAddr, teamName string
	err := tx.QueryRow(`SELECT lead_host_id, team_id, state, lead_address, team_name FROM remote_members WHERE mk = ?`, c.MK).
		Scan(&host, &teamID, &state, &leadAddr, &teamName)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (host != p.LeadHostID || teamID != c.TeamID || state != remoteActive)) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no live member of that team here"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	if ok, err := casRemoteMemberStateIn(tx, c.MK, []string{remoteActive}, remoteReleased, p.Now); err != nil {
		return CommandResult{}, err
	} else if !ok {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no live member of that team here"), nil
	}
	if err := oweNoticeIn(tx, c.MK, noticeReleased, c.ID, leadAddr, teamName, p.Now); err != nil {
		return CommandResult{}, err
	}
	return okResult(map[string]string{"state": "ok"})
}
