// internal/module/team/commands_relay_store.go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/wake/purdex/internal/team"
)

// Member relay MR-3b (docs/specs/2026-10-10-member-relay-manual-and-cross-host-spec-plan.md D9, §3.3): the member host's half of a
// lead's relay of a remote member. The lead host sends `relay {mk, team_id, op_id}`; M opens the op (id = op_id) and tells the
// member's mod to claim it. A relay that is not done in time is voided by the lead host, and M answers from the op's state then.

// relayCommandSchema remembers which op a relay command opened: the void names the command, the op's end names the op, and
// only an op that came from a command tells the lead host when it fails (relay_failed). Idempotent; any later change is a migration.
const relayCommandSchema = `
	CREATE TABLE IF NOT EXISTS team_relay_commands (
		lead_host_id TEXT NOT NULL,
		command_id   TEXT NOT NULL,
		op_id        TEXT NOT NULL,
		mk           TEXT NOT NULL,
		team_id      TEXT NOT NULL,
		at           INTEGER NOT NULL,
		PRIMARY KEY (lead_host_id, command_id)
	);
	CREATE UNIQUE INDEX IF NOT EXISTS team_relay_commands_op ON team_relay_commands (op_id);`

// relayRemoteUnreachable is the reason of an op a void cancelled before it was claimed.
const relayRemoteUnreachable = "remote_unreachable"

// applyRelayIn is `relay`: one transaction (the caller holds createMu). Refusals in the spec's order — AllowTeam, the member row
// (active, of that team, of that lead host), the mod, the one-open-op floor — then the op (requested), the session's open ask
// accepted, the command remembered. The age refusal already ran in ApplyTeamCommand.
func applyRelayIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.cmd
	if !p.Consent {
		return refusal(http.StatusForbidden, team.ErrCommandHostNotAllowed, "this host does not accept team commands from the lead host"), nil
	}
	var m remoteMemberRow
	err := tx.QueryRow(`SELECT member_session_id, ref, team_id, lead_host_id, state, pid, proc_start, pane_id FROM remote_members WHERE mk = ?`, c.MK).
		Scan(&m.MemberSessionID, &m.Ref, &m.TeamID, &m.LeadHostID, &m.State, &m.PID, &m.ProcStart, &m.PaneID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (m.LeadHostID != p.LeadHostID || m.TeamID != c.TeamID || m.State != remoteActive)) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no active member of that team here"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	if p.ModOK == nil || !p.ModOK(m.MemberSessionID) {
		return refusal(http.StatusConflict, team.ErrRelayUnsupported, "the member's Purdex mod is absent or too old to relay"), nil
	}
	op := team.RelayOp{
		ID: c.OpID, Kind: team.RelayKindMember, HostID: p.HostID, SessionID: m.MemberSessionID, Ref: m.Ref, TeamID: c.TeamID,
		State: team.RelayRequested, HandoffPath: filepath.Join(p.HandoffDir, c.OpID+".md"),
		PID: m.PID, PaneID: m.PaneID, ProcStart: m.ProcStart, CreatedAt: p.Now, UpdatedAt: p.Now,
	}
	if err := insertRelayOpIn(tx, op); err != nil {
		if errors.Is(err, ErrRelayOpOpen) {
			return refusal(http.StatusConflict, team.ErrRelayOpen, "an op is already open for that member"), nil
		}
		return CommandResult{}, err
	}
	// The lead's relay is the answer to an open ask of that session (as CreateMemberRelayOp does), in this transaction.
	if _, err := tx.Exec(`UPDATE relay_asks SET state = 'accepted', op_id = ?, closed_at = ? WHERE session_id = ? AND state = 'open' AND expires_at > ?`,
		op.ID, p.Now, op.SessionID, p.Now); err != nil {
		return CommandResult{}, err
	}
	if _, err := tx.Exec(`INSERT INTO team_relay_commands (lead_host_id, command_id, op_id, mk, team_id, at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.LeadHostID, c.ID, c.OpID, c.MK, c.TeamID, p.Now); err != nil {
		return CommandResult{}, fmt.Errorf("remember relay command %s: %w", c.ID, err)
	}
	return okResult(team.RelayCommandOutcome{State: team.RelayCommandAccepted})
}

// voidRelayIn is `void` of a relay command that the log knows (D9), decided from the op's state now: still requested → cancelled
// (remote_unreachable), `undone`; claimed or already ended (the ack was lost) → `too_late`, nothing touched. A refused command
// applied nothing. The caller holds createMu.
func voidRelayIn(tx *sql.Tx, p CommandPlan, target string, status int) (CommandResult, error) {
	c := p.cmd
	if status != http.StatusOK {
		return okResult(team.VoidOutcome{State: team.VoidNotApplied}) // refused: nothing was applied, its stored refusal stands
	}
	var opID string
	if err := tx.QueryRow(`SELECT op_id FROM team_relay_commands WHERE lead_host_id = ? AND command_id = ?`, p.LeadHostID, target).Scan(&opID); err != nil {
		return CommandResult{}, fmt.Errorf("void of relay command %s: %w", target, err)
	}
	res, err := tx.Exec(`UPDATE relay_ops SET state = ?, reason = ?, updated_at = ? WHERE id = ? AND state = ?`,
		string(team.RelayCancelled), relayRemoteUnreachable, p.Now, opID, string(team.RelayRequested))
	if err != nil {
		return CommandResult{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return okResult(team.VoidOutcome{State: team.VoidTooLate})
	}
	if err := recordVoidIn(tx, p.LeadHostID, target, c.TeamID, c.ID, p.Now); err != nil {
		return CommandResult{}, err
	}
	return okResult(team.VoidOutcome{State: team.VoidUndone})
}

// relayCommandOf is the relay command that opened op, if one did (an op of the member's own /relay or of a local member has none).
func relayCommandOf(q dbtx, opID string) (leadHostID, mk, teamID string, ok bool, err error) {
	err = q.QueryRow(`SELECT lead_host_id, mk, team_id FROM team_relay_commands WHERE op_id = ?`, opID).Scan(&leadHostID, &mk, &teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, nil
	}
	return leadHostID, mk, teamID, err == nil, err
}

// queueRelayFailedIn tells the lead host that its relay ended failed or cancelled on this host, in the caller's transaction. An op
// that did not come from a relay command says nothing (a person's relay is the person's; a local member's has no lead host to tell).
func (s *Store) queueRelayFailedIn(tx dbtx, op team.RelayOp, at int64) error {
	leadHost, mk, teamID, ok, err := relayCommandOf(tx, op.ID)
	if err != nil || !ok {
		return err
	}
	if s.newID == nil {
		return errors.New("no id source for the relay_failed fact")
	}
	return writeFactIn(tx, team.TeamFact{ID: s.newID(), Kind: team.FactRelayFailed, ToHostID: leadHost, TeamID: teamID, MK: mk,
		OpID: op.ID, State: string(op.State), Reason: op.Reason}, at)
}
