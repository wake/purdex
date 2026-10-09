package teammod

import (
	"database/sql"
	"encoding/json"
	"fmt"

	peersmod "github.com/wake/purdex/internal/module/peers"
	"github.com/wake/purdex/internal/team"
)

// The remote row state machine on L (cross-host team spec §4.2; plan X3b-1a). The answer to a command is applied to the
// member row in the SAME transaction that marks the command done (SettleCommand), as a compare-and-set from the states the
// table allows (§3.1 rule 5): an answer whose CAS finds another state is ignored and logged once, never forced. The row is
// found by (host_id, mk) — the member key of THAT membership, so an answer for an earlier membership of the same session
// never touches a re-adopted row — and only while the team is live (the lead's team ended: the rows stay as they ended, D4;
// the `end` command is what tells the member host).

// liveRemoteRow is the WHERE of a remote row that is still in play: its team is live (an ended team's rows are left as they
// ended). Used by every L-side remote transition.
const liveTeamOfRow = `EXISTS (SELECT 1 FROM teams WHERE teams.id = team_members.team_id AND teams.ended_at = 0)`

// remoteOutcomes is the commandOutcomes of the commands outbox.
type remoteOutcomes struct{ m *Module }

var _ commandOutcomes = remoteOutcomes{}

// ApplyOutcome applies one settled command's answer. res.Class is ClassDone (res.Body is the receiver's answer) or a
// permanent refusal (ClassRefused / ClassWrongHost, res.Code).
func (o remoteOutcomes) ApplyOutcome(tx *sql.Tx, c commandRow, res peersmod.CallResult) error {
	refused := res.Class != peersmod.ClassDone
	now := o.m.now()
	switch c.Kind {
	case CmdAdopt:
		if refused {
			// adopt refusal → failed{code}, the seat is free
			return o.cas(tx, c, `state = ?, end_reason = ?, updated_at = ?, ended_at = ?`, []any{rowFailed, res.Code, now, now}, rowJoining)
		}
		return o.adoptApplied(tx, c, res, now)
	case CmdRelease:
		// release refused with ANY code → released{code}: the lead's intent stands (the §3.2 residual covers a member host that
		// never learns it)
		reason := ""
		if refused {
			reason = res.Code
		}
		return o.cas(tx, c, `state = ?, end_reason = ?, updated_at = ?, ended_at = ?`, []any{string(team.MemberReleased), reason, now, now}, rowReleasing)
	case CmdKill:
		return o.killAnswer(tx, c, res, refused, now)
	case CmdSpawn:
		// `accepted` changes nothing (the result comes as a fact). A refusal (host_not_allowed, cwd_outside_grant,
		// capacity_exceeded, bad_request, unsupported_kind, …) fails the forwarded op with its code: the seat is free.
		if refused {
			return failRemoteSpawnIn(tx, c.MK, c.HostID, res.Code, now)
		}
		return nil
	}
	// end, lead_moved, void (and spawn, X4b): nothing on a member row; a refusal is logged once
	if refused {
		o.m.logf("[team] command %s (%s) to %s was refused (%s); nothing to undo", c.ID, c.Kind, c.HostID, res.Code)
	}
	return nil
}

// adoptApplied: joining → active with what the member host reported. Any other state means a release / kill / ended / unpair
// got there first (or the team ended): the late `applied` is ignored.
func (o remoteOutcomes) adoptApplied(tx *sql.Tx, c commandRow, res peersmod.CallResult, now int64) error {
	var ans team.TeamCommandAnswer
	var out team.AdoptOutcome
	if err := json.Unmarshal(res.Body, &ans); err != nil || json.Unmarshal(ans.Outcome, &out) != nil || out.State != "applied" || out.MemberSession == "" || out.Ref == "" {
		return fmt.Errorf("adopt answer of command %s is not an applied outcome", c.ID)
	}
	// A remote session id equal to a LOCAL active session would break team_members_one_active for good (the settle would
	// roll back and the adopt would be sent again forever): it fails the membership instead.
	var taken int
	if err := tx.QueryRow(`SELECT 1 FROM team_members WHERE session_id = ? AND state = 'active' AND NOT (mk = ? AND host_id = ?)`, out.MemberSession, c.MK, c.HostID).Scan(&taken); err == nil {
		o.m.logf("[team] adopt %s: session %s is already an active member here; the membership fails session_conflict", c.ID, out.MemberSession)
		return o.cas(tx, c, `state = ?, end_reason = 'session_conflict', updated_at = ?, ended_at = ?`, []any{rowFailed, now, now}, rowJoining)
	}
	// The member host answers with the whole Origin.Tmux ("<session>:@<win>.%<pane>"); the row keeps the session NAME and the pane
	// apart, as a local adopt does (the name is what a workspace tab is matched by).
	tmuxSession, pane := splitTmux(out.Tmux)
	return o.cas(tx, c, `state = 'active', session_id = ?, ref = ?, pid = ?, proc_start = ?,
		title = CASE WHEN ? <> '' THEN ? ELSE title END, cwd = CASE WHEN ? <> '' THEN ? ELSE cwd END,
		tmux_session = CASE WHEN ? <> '' THEN ? ELSE tmux_session END, pane_id = CASE WHEN ? <> '' THEN ? ELSE pane_id END, updated_at = ?`,
		[]any{out.MemberSession, out.Ref, out.PID, out.ProcStart, out.Title, out.Title, out.Cwd, out.Cwd, tmuxSession, tmuxSession, pane, pane, now}, rowJoining)
}

// killAnswer: killed / gone from the answer; a refusal returns the row to active (host_not_allowed, or any code but
// not_your_member) or ends it gone{code} (not_your_member: the member host has no such live row).
func (o remoteOutcomes) killAnswer(tx *sql.Tx, c commandRow, res peersmod.CallResult, refused bool, now int64) error {
	if refused {
		if res.Code == team.ErrNotYourMember {
			return o.cas(tx, c, `state = 'gone', end_reason = ?, updated_at = ?, ended_at = ?`, []any{res.Code, now, now}, rowKilling)
		}
		return o.cas(tx, c, `state = 'active', end_reason = ?, updated_at = ?`, []any{res.Code, now}, rowKilling)
	}
	var ans team.TeamCommandAnswer
	var out struct {
		State string `json:"state"`
	}
	// only an explicit killed or gone is a result; anything else is a broken or newer peer: nothing is settled and the
	// command is sent again (a kill is never recorded as done on a guess)
	if err := json.Unmarshal(res.Body, &ans); err != nil || json.Unmarshal(ans.Outcome, &out) != nil || (out.State != "killed" && out.State != "gone") {
		return fmt.Errorf("kill answer of command %s is neither killed nor gone", c.ID)
	}
	to := out.State
	return o.cas(tx, c, `state = ?, updated_at = ?, ended_at = ?`, []any{to, now, now}, rowKilling)
}

// cas moves the command's row from the state `from` with set, while its team is live. No row changed: logged once
// (rule 5), not an error.
func (o remoteOutcomes) cas(tx *sql.Tx, c commandRow, set string, args []any, from string) error {
	args = append(args, c.MK, c.HostID, c.TeamID, from)
	r, err := tx.Exec(`UPDATE team_members SET `+set+` WHERE mk = ? AND host_id = ? AND team_id = ? AND state = ? AND `+liveTeamOfRow, args...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		o.m.logf("[team] outcome of command %s (%s) ignored: the member row is no longer %s", c.ID, c.Kind, from)
	}
	return nil
}
