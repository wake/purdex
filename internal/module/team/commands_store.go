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
	"unicode"
	"unicode/utf8"

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
	CREATE INDEX IF NOT EXISTS team_command_log_at ON team_command_log (at);
	CREATE TABLE IF NOT EXISTS remote_notices (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		mk         TEXT    NOT NULL,
		kind       TEXT    NOT NULL,
		cause_id   TEXT    NOT NULL,
		lead_address TEXT  NOT NULL DEFAULT '',
		team_name  TEXT    NOT NULL DEFAULT '',
		state      TEXT    NOT NULL,
		attempts   INTEGER NOT NULL DEFAULT 0,
		next_at    INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		UNIQUE (mk, kind, cause_id)
	);
	CREATE INDEX IF NOT EXISTS remote_notices_owed ON remote_notices (state, next_at);
	CREATE TABLE IF NOT EXISTS team_command_voids (
		lead_host_id TEXT    NOT NULL,
		command_id   TEXT    NOT NULL,
		team_id      TEXT    NOT NULL,
		void_id      TEXT    NOT NULL,
		at           INTEGER NOT NULL,
		PRIMARY KEY (lead_host_id, command_id)
	);`

// Notice kinds (spec §4.4) and states. Only the rows are written here; their delivery is X3d's.
const (
	noticeAdopted   = "adopted"
	noticeReleased  = "released"
	noticeHandover  = "handover"
	noticeTeamEnded = "team_ended"
	noticeLocalEnd  = "local_end" // the operator on this host ended the membership
	noticeOwed      = "owed"
)

// ErrCommandIDConflict: the command id is stored for another content (spec §3.1 rule 3: 409 id_conflict).
var ErrCommandIDConflict = errors.New("the command id is already used by a different command")

// ErrCommandUnsupported: a kind this version does not apply (the route refuses it before the store).
var ErrCommandUnsupported = errors.New("unsupported command kind")

// ErrCommandBadBody: the plan's body is not a command.
var ErrCommandBadBody = errors.New("the command body is not a TeamCommand")

// bodyHash is the content hash of a command: its bytes as the sender wrote them. The sender resends the very
// bytes it stored, so a replay hashes the same; and a field this version does not know still counts (a
// version-skewed sender cannot slip a different command in under an old id as a "replay").
func bodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CommandPlan is one command to decide. Body is the command as received; the store decodes it and hashes it
// itself — a caller cannot name a hash or a kind. Consent (the lead host's AllowTeam, read by the route from the
// live entry) and Target (the live session the adopt names, nil when none) are inputs the route resolved; the
// store stays free of config and registry.
type CommandPlan struct {
	LeadHostID string
	Body       json.RawMessage
	Consent    bool
	Target     *team.Origin
	Now        int64
	// KillState is what the handler found when it signalled a kill's target: "killed" (signalled), "gone" (nothing was
	// left to signal) or the state an earlier kill left the row in; "" = no live row to kill (the store refuses).
	KillState string
	// HostID is this host's id (a spawn's op belongs to it); SpawnCwd the canonical cwd of a spawn, "" when it lies under
	// none of the roots the lead host's entry grants.
	HostID, SpawnCwd string

	cmd  team.TeamCommand // decoded from Body by ApplyTeamCommand
	hash string
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
	if err := json.Unmarshal(p.Body, &p.cmd); err != nil || p.cmd.ID == "" || p.cmd.Kind == "" {
		return CommandResult{}, ErrCommandBadBody
	}
	p.hash = bodyHash(p.Body)
	fail := func(err error) (CommandResult, error) {
		return CommandResult{}, fmt.Errorf("apply command %s %s: %w", p.cmd.Kind, p.cmd.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the reads below.
	if _, err := tx.Exec(`UPDATE team_command_log SET id = id WHERE lead_host_id = ? AND id = ?`, p.LeadHostID, p.cmd.ID); err != nil {
		return fail(err)
	}

	// The void table is read BEFORE the log (spec §3.3): a command id the lead host voided answers 409
	// command_void, whatever a copy of it once stored. Not logged — the table is the answer.
	// Only an adopt or a spawn can be voided; a void that arrived early did not know its target's kind, so it must
	// not swallow a release, end or lead_moved carrying the same id.
	if p.cmd.Kind == team.CommandAdopt || p.cmd.Kind == team.CommandSpawn {
		var voided int
		switch err := tx.QueryRow(`SELECT 1 FROM team_command_voids WHERE lead_host_id = ? AND command_id = ? AND team_id = ?`,
			p.LeadHostID, p.cmd.ID, p.cmd.TeamID).Scan(&voided); {
		case err == nil:
			return refusal(http.StatusConflict, team.ErrCommandVoided, "the lead host voided this command"), nil
		case !errors.Is(err, sql.ErrNoRows):
			return fail(err)
		}
	}

	var hash, kind string
	var res CommandResult
	err = tx.QueryRow(`SELECT kind, body_hash, status, outcome_json FROM team_command_log WHERE lead_host_id = ? AND id = ?`,
		p.LeadHostID, p.cmd.ID).Scan(&kind, &hash, &res.Status, (*rawString)(&res.Body))
	switch {
	case err == nil:
		if hash != p.hash || kind != p.cmd.Kind {
			return CommandResult{}, ErrCommandIDConflict
		}
		res.Replayed = true
		return res, nil
	case !errors.Is(err, sql.ErrNoRows):
		return fail(err)
	}

	switch p.cmd.Kind {
	case team.CommandAdopt:
		res, err = applyAdoptIn(tx, p)
	case team.CommandRelease:
		res, err = applyReleaseIn(tx, p)
	case team.CommandKill:
		res, err = applyKillIn(tx, p)
	case team.CommandSpawn:
		res, err = applySpawnIn(tx, p)
	case team.CommandEnd, team.CommandLeadMoved:
		res, err = applyTeamLevelIn(tx, p)
	case team.CommandVoid:
		res, err = applyVoidIn(tx, p)
	case team.CommandAppearance:
		res, err = applyAppearanceIn(tx, p)
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
		p.LeadHostID, p.cmd.ID, p.cmd.Kind, p.hash, res.Status, string(res.Body), p.Now); err != nil {
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

// oweNoticeIn writes the notice a command owes its member, in the command's own transaction (spec §4.4). One
// per (member, kind, causing command), so a re-applied transaction cannot double it. The two strings are the
// snapshot the notice is about — the lead address and team name as THIS event left them, cleaned and bounded —
// so a later lead_moved cannot change what an earlier notice says; delivery (X3d) fills M's fixed template
// from them.
func oweNoticeIn(tx dbtx, mk, kind, causeID, leadAddress, teamName string, at int64) error {
	_, err := tx.Exec(`INSERT INTO remote_notices (mk, kind, cause_id, lead_address, team_name, state, attempts, next_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?, ?) ON CONFLICT (mk, kind, cause_id) DO NOTHING`,
		mk, kind, causeID, cleanNoticeField(leadAddress), cleanNoticeField(teamName), noticeOwed, at, at, at)
	if err != nil {
		return fmt.Errorf("owe %s notice to %s: %w", kind, mk, err)
	}
	return nil
}

// cleanNoticeField is s as a notice may carry it: control characters dropped, cut to 64 bytes on a rune boundary.
func cleanNoticeField(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) <= 64 {
		return s
	}
	cut := 64
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
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
	ID          int64
	MK          string
	Kind        string
	CauseID     string
	LeadAddress string
	TeamName    string
	State       string
	Attempts    int
	NextAt      int64
}

// RemoteNotices lists the notices owed to (or sent to) the member mk, oldest first.
func (s *Store) RemoteNotices(mk string) ([]remoteNoticeRow, error) {
	rows, err := s.db.Query(`SELECT id, mk, kind, cause_id, lead_address, team_name, state, attempts, next_at FROM remote_notices WHERE mk = ? ORDER BY id`, mk)
	if err != nil {
		return nil, fmt.Errorf("remote notices %s: %w", mk, err)
	}
	defer rows.Close()
	out := []remoteNoticeRow{}
	for rows.Next() {
		var n remoteNoticeRow
		if err := rows.Scan(&n.ID, &n.MK, &n.Kind, &n.CauseID, &n.LeadAddress, &n.TeamName, &n.State, &n.Attempts, &n.NextAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

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

// PruneCommandLog deletes at most batch decided-command records older than before (unix ms) and reports how many; the
// caller repeats until it reports fewer than batch. team_command_voids is never touched (the lead host's late void is
// answered from it). The receiver has no age check of its own for adopt / spawn — only the lead host settles or voids them
// within 10 minutes — so the record of an adopt whose member row is still active stays however old it is: a late void
// finds it and undoes the adoption. Every other record (a refusal, an adopt whose member ended, spawn, release, end,
// lead_moved, team.appearance, void) is replayed as an idempotent no-op or never resent, and goes after the retention
// (#2265). lead_moved and team.appearance are not monotonic, but the lead host sends them strictly in order per host (its
// outbox is head-of-line FIFO, and an older one settles before a newer one is sent), so a replay of an old one can only
// land before the newer one and is overwritten by it. An overdue adopt / spawn is voided at the lead host's boot before
// its pump starts (Start), so the lead host cannot resend one after its record is gone.
func (s *Store) PruneCommandLog(before int64, batch int) (int, error) {
	res, err := s.db.Exec(`DELETE FROM team_command_log WHERE rowid IN (
			SELECT l.rowid FROM team_command_log l
			WHERE l.at < ?
			  AND NOT (l.kind = 'adopt' AND EXISTS (
				SELECT 1 FROM remote_members r WHERE r.mk = l.id AND r.lead_host_id = l.lead_host_id AND r.state = 'active'))
			ORDER BY l.at LIMIT ?)`, before, batch)
	if err != nil {
		return 0, fmt.Errorf("prune command log: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
