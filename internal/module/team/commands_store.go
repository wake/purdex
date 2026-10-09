// internal/module/team/commands_store.go
package teammod

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// commandSchema is M's side of the cross-host commands (spec §5.1): the log of every command it decided
// (applied and refused), and the notices a command owes the member. Keys are scoped by the lead host that sent
// the command, so one paired host can never read or replay another's command id. Idempotent; any later change
// is a migration. (team_command_voids is X2b-2's.)
const commandSchema = `
	CREATE TABLE IF NOT EXISTS team_command_log (
		lead_host_id TEXT    NOT NULL,
		id           TEXT    NOT NULL,
		kind         TEXT    NOT NULL,
		body_hash    TEXT    NOT NULL,
		status       INTEGER NOT NULL,
		outcome_json TEXT    NOT NULL,
		at           INTEGER NOT NULL,
		PRIMARY KEY (lead_host_id, id)
	);
	CREATE TABLE IF NOT EXISTS remote_notices (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		mk         TEXT    NOT NULL,
		kind       TEXT    NOT NULL,
		cause_id   TEXT    NOT NULL,
		state      TEXT    NOT NULL,
		attempts   INTEGER NOT NULL DEFAULT 0,
		next_at    INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (mk, kind, cause_id)
	);
	CREATE INDEX IF NOT EXISTS remote_notices_owed ON remote_notices (state, next_at);`

// Notice kinds (spec §4.4) and states. Only the rows are written here; their delivery is X3d's.
const (
	noticeAdopted   = "adopted"
	noticeReleased  = "released"
	noticeHandover  = "handover"
	noticeTeamEnded = "team_ended"
	noticeOwed      = "owed"
)

// ErrCommandIDConflict: the command id is stored for another content (spec §3.1 rule 3: 409 id_conflict).
var ErrCommandIDConflict = errors.New("the command id is already used by a different command")

// ErrCommandUnsupported: a kind this version does not apply (the route refuses it before the store).
var ErrCommandUnsupported = errors.New("unsupported command kind")

// commandHash is the content hash of a command: its decoded form re-marshalled, so spacing and key order of the
// sender's JSON do not matter.
func commandHash(c team.TeamCommand) string {
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// CommandPlan is one command to decide. Consent (the lead host's AllowTeam, read by the route from the live
// entry) and Target (the live session the adopt names, nil when none) are inputs the route resolved; the store
// stays free of config and registry.
type CommandPlan struct {
	LeadHostID string
	Cmd        team.TeamCommand
	Hash       string
	Consent    bool
	Target     *team.Origin
	Now        int64
}

// CommandResult is the decided answer: the HTTP status and JSON body to send (a 200 body is the kind's outcome, a
// 4xx body a team.CommandRefusal). Replayed says it was the stored answer of an earlier copy.
type CommandResult struct {
	Status   int
	Body     json.RawMessage
	Replayed bool
}

// ApplyTeamCommand decides p in ONE transaction: the stored answer of an earlier copy (same lead host, same id)
// when the content hash matches, ErrCommandIDConflict when it does not, else the command applied or refused —
// the row changes, the owed notices and the log entry (refusals included) committed together, so a crash leaves
// all of it or none and the retry meets the stored answer (spec §3.1 rules 2–3).
func (s *Store) ApplyTeamCommand(p CommandPlan) (CommandResult, error) {
	fail := func(err error) (CommandResult, error) {
		return CommandResult{}, fmt.Errorf("apply command %s %s: %w", p.Cmd.Kind, p.Cmd.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the reads below.
	if _, err := tx.Exec(`UPDATE team_command_log SET id = id WHERE lead_host_id = ? AND id = ?`, p.LeadHostID, p.Cmd.ID); err != nil {
		return fail(err)
	}

	// (X2b-2: the void table is read here, before the log.)

	var hash string
	var res CommandResult
	err = tx.QueryRow(`SELECT body_hash, status, outcome_json FROM team_command_log WHERE lead_host_id = ? AND id = ?`,
		p.LeadHostID, p.Cmd.ID).Scan(&hash, &res.Status, (*rawString)(&res.Body))
	switch {
	case err == nil:
		if hash != p.Hash {
			return CommandResult{}, ErrCommandIDConflict
		}
		res.Replayed = true
		return res, nil
	case !errors.Is(err, sql.ErrNoRows):
		return fail(err)
	}

	switch p.Cmd.Kind {
	case team.CommandAdopt:
		res, err = applyAdoptIn(tx, p)
	case team.CommandRelease:
		res, err = applyReleaseIn(tx, p)
	case team.CommandEnd, team.CommandLeadMoved:
		res, err = applyTeamLevelIn(tx, p)
	default:
		return CommandResult{}, ErrCommandUnsupported
	}
	if err != nil {
		return fail(err)
	}
	if s.failBeforeCommandLog != nil {
		if err := s.failBeforeCommandLog(); err != nil {
			return fail(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO team_command_log (lead_host_id, id, kind, body_hash, status, outcome_json, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.LeadHostID, p.Cmd.ID, p.Cmd.Kind, p.Hash, res.Status, string(res.Body), p.Now); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return res, nil
}

// rawString scans a TEXT column into a json.RawMessage.
type rawString json.RawMessage

func (r *rawString) Scan(v any) error {
	switch x := v.(type) {
	case string:
		*r = rawString(x)
	case []byte:
		*r = append(rawString(nil), x...)
	default:
		return fmt.Errorf("outcome_json is %T", v)
	}
	return nil
}

func refusal(status int, code, detail string) CommandResult {
	body, _ := json.Marshal(team.CommandRefusal{Error: code, Detail: detail})
	return CommandResult{Status: status, Body: body}
}

func okResult(v any) (CommandResult, error) {
	body, err := json.Marshal(v)
	return CommandResult{Status: http.StatusOK, Body: body}, err
}

// applyAdoptIn is the adopt command (spec §5.2 "—, adopt → active"): consent, a live target, no role, then the
// remote row and its owed `adopted` notice.
func applyAdoptIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.Cmd
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
		Title: o.Title, CreatedAt: p.Now, UpdatedAt: p.Now}
	if err := insertRemoteMemberIn(tx, row); err != nil {
		return CommandResult{}, err
	}
	if err := oweNoticeIn(tx, c.MK, noticeAdopted, c.ID, p.Now); err != nil {
		return CommandResult{}, err
	}
	return okResult(team.AdoptOutcome{State: "applied", MemberSession: o.SessionID, Ref: o.Ref, PID: o.PID,
		ProcStart: o.ProcStart, Title: o.Title, Cwd: o.Cwd, Tmux: o.Tmux})
}

// applyReleaseIn is `release` (active → released, notice `released`). Only the lead host's own live row of that
// team is releasable; anything else is not_your_member.
func applyReleaseIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.Cmd
	var host, teamID, state string
	err := tx.QueryRow(`SELECT lead_host_id, team_id, state FROM remote_members WHERE mk = ?`, c.MK).Scan(&host, &teamID, &state)
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
	if err := oweNoticeIn(tx, c.MK, noticeReleased, c.ID, p.Now); err != nil {
		return CommandResult{}, err
	}
	return okResult(map[string]string{"state": "ok"})
}

// applyTeamLevelIn is `end` (every live row → ended, notice `team_ended`) and `lead_moved` (every live row keeps
// its state and takes the new lead tuple, notice `handover`): team-level, so no mk — the rows are the team's from
// the sending lead host. No live row is not an error; the command is simply already true.
func applyTeamLevelIn(tx *sql.Tx, p CommandPlan) (CommandResult, error) {
	c := p.Cmd
	rows, err := tx.Query(`SELECT mk FROM remote_members WHERE lead_host_id = ? AND team_id = ? AND state = ? ORDER BY created_at, mk`,
		p.LeadHostID, c.TeamID, remoteActive)
	if err != nil {
		return CommandResult{}, err
	}
	var mks []string
	for rows.Next() {
		var mk string
		if err := rows.Scan(&mk); err != nil {
			rows.Close()
			return CommandResult{}, err
		}
		mks = append(mks, mk)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CommandResult{}, err
	}
	rows.Close()
	for _, mk := range mks {
		if c.Kind == team.CommandEnd {
			if _, err := casRemoteMemberStateIn(tx, mk, []string{remoteActive}, remoteEnded, p.Now); err != nil {
				return CommandResult{}, err
			}
			if err := oweNoticeIn(tx, mk, noticeTeamEnded, c.ID, p.Now); err != nil {
				return CommandResult{}, err
			}
			continue
		}
		if _, err := tx.Exec(`UPDATE remote_members SET lead_session_id = ?, lead_ref = ?, lead_address = ?, lead_title = ?,
			lead_pid = ?, lead_proc_start = ?, updated_at = ? WHERE mk = ? AND state = ?`,
			c.LeadSessionID, c.LeadRef, c.Lead.Address, c.Lead.Title, c.Lead.PID, c.Lead.ProcStart, p.Now, mk, remoteActive); err != nil {
			return CommandResult{}, err
		}
		if err := oweNoticeIn(tx, mk, noticeHandover, c.ID, p.Now); err != nil {
			return CommandResult{}, err
		}
	}
	return okResult(map[string]any{"state": "ok", "affected": len(mks)})
}

// oweNoticeIn writes the notice a command owes its member, in the command's own transaction (spec §4.4). One
// per (member, kind, causing command), so a re-applied transaction cannot double it.
func oweNoticeIn(tx dbtx, mk, kind, causeID string, at int64) error {
	_, err := tx.Exec(`INSERT INTO remote_notices (mk, kind, cause_id, state, attempts, next_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?) ON CONFLICT (mk, kind, cause_id) DO NOTHING`, mk, kind, causeID, noticeOwed, at, at, at)
	if err != nil {
		return fmt.Errorf("owe %s notice to %s: %w", kind, mk, err)
	}
	return nil
}

// splitTmux splits a registry "<session>:@<win>.%<pane>" into its session name and pane id; either may be "".
func splitTmux(t string) (session, pane string) {
	if i := strings.IndexByte(t, ':'); i >= 0 {
		session = t[:i]
	} else {
		return t, ""
	}
	if j := strings.LastIndexByte(t, '.'); j >= 0 && strings.HasPrefix(t[j+1:], "%") {
		pane = t[j+1:]
	}
	return session, pane
}

// remoteNoticeRow is one remote_notices row.
type remoteNoticeRow struct {
	ID       int64
	MK       string
	Kind     string
	CauseID  string
	State    string
	Attempts int
	NextAt   int64
}

// RemoteNotices lists the notices owed to (or sent to) the member mk, oldest first.
func (s *Store) RemoteNotices(mk string) ([]remoteNoticeRow, error) {
	rows, err := s.db.Query(`SELECT id, mk, kind, cause_id, state, attempts, next_at FROM remote_notices WHERE mk = ? ORDER BY id`, mk)
	if err != nil {
		return nil, fmt.Errorf("remote notices %s: %w", mk, err)
	}
	defer rows.Close()
	out := []remoteNoticeRow{}
	for rows.Next() {
		var n remoteNoticeRow
		if err := rows.Scan(&n.ID, &n.MK, &n.Kind, &n.CauseID, &n.State, &n.Attempts, &n.NextAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
