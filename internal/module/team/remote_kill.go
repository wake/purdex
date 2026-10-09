// internal/module/team/remote_kill.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"syscall"

	"github.com/wake/purdex/internal/team"
)

// The `kill` command on the member host (cross-host team spec §5.2, §6.2, plan X4a). The lead host kills one of its remote
// members. Order matters, because a signal cannot be taken back:
//
//  1. prepareKill only LOOKS: is the member's own process still alive? (no signal) — that decides killed or gone;
//  2. ApplyTeamCommand decides in one transaction, as for every command: idempotency and id_conflict, consent, the row's
//     owner, the CAS active → killed|gone, the log — a refused, replayed or conflicting command has signalled nothing;
//  3. only then signalKill sends SIGTERM, to the process the row recorded (never to whatever the registry shows now).
//
// A crash, or a failed signal, between 2 and 3 leaves the row `killed` with the process alive. The command is answered
// with a retryable error then, so the lead host sends the SAME command again: that is a replay, and a replay of a killed
// answer signals again (guarded by the same identity check) until it takes.

// killTarget is the member's process as the registry and the process table show it NOW, judged against what the row
// recorded: live is false when there is nothing of the member to signal (the session is not live, its process ended or its
// pid was reused, or the live process is not the one the row recorded). A non-zero status is a retryable error.
func (m *Module) killTarget(r remoteMemberRow) (pid int, live bool, status int, code, detail string) {
	o, found, err := m.origins.ResolveOriginBySession(r.MemberSessionID)
	if err != nil {
		return 0, false, http.StatusServiceUnavailable, team.ErrNotReady, "registry unavailable; retry: " + err.Error()
	}
	if !found {
		return 0, false, 0, "", ""
	}
	if r.PID != 0 && (o.PID != r.PID || o.ProcStart != r.ProcStart) {
		return 0, false, 0, "", "" // the session id now belongs to another process than the member's
	}
	same, err := m.origins.SameProcess(o.PID, o.ProcStart)
	if err != nil {
		return 0, false, http.StatusServiceUnavailable, team.ErrNotReady, "the member's process could not be verified; retry: " + err.Error()
	}
	if !same {
		return 0, false, 0, "", ""
	}
	if o.PID <= 1 { // 0, a negative pid and init are never a target (kill(2) would signal a group or everything)
		return 0, false, http.StatusInternalServerError, team.ErrKillFailed, fmt.Sprintf("refusing to signal pid %d", o.PID)
	}
	return o.PID, true, 0, "", ""
}

// prepareKill looks at the target of kill command cmd (already bound, in shape and consented) and records on the plan what
// it found. It signals nothing. A non-zero status is an error to answer; zero with an empty plan.KillState means there is
// no live row of this lead host to kill (the store refuses it).
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
		_, live, status, code, why := m.killTarget(row)
		if status != 0 {
			return status, code, why
		}
		plan.KillState = remoteGone
		if live {
			plan.KillState = remoteKilled
		}
	}
	return 0, "", ""
}

// signalKill sends the SIGTERM of an applied (or replayed) kill: the member's process, found again and checked against
// the row. Nothing live is nothing to do. A failed signal is the error to answer (status != 0), so the command is retried.
func (m *Module) signalKill(mk string) (status int, code, detail string) {
	row, found, err := m.store.RemoteMember(mk)
	if err != nil {
		return http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log"
	}
	if !found || row.State != remoteKilled {
		return 0, "", ""
	}
	pid, live, status, code, why := m.killTarget(row)
	if status != 0 {
		return status, code, why
	}
	if !live {
		return 0, "", ""
	}
	switch err := m.killProcess(pid); {
	case err == nil, errors.Is(err, syscall.ESRCH):
		return 0, "", ""
	default:
		m.logf("[team] kill of %s: signalling process %d failed: %v", row.MemberSessionID, pid, err)
		return http.StatusInternalServerError, team.ErrKillFailed, fmt.Sprintf("signalling process %d failed: %v", pid, err)
	}
}

// killedOutcome says whether a kill's answer is `killed` (the process is to be signalled), as opposed to `gone`.
func killedOutcome(body []byte) bool {
	var out struct {
		State string `json:"state"`
	}
	return json.Unmarshal(body, &out) == nil && out.State == remoteKilled
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
			return CommandResult{}, fmt.Errorf("kill %s: the member row of %s appeared after the look at its process", c.ID, c.MK)
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
