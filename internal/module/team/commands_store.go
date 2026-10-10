// internal/module/team/commands_store.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

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

	// A new adopt or spawn older than the lead's expiry plus the skew allowance is refused (#2398), and the refusal is
	// logged like any other, so its replay is the same refusal. Only a command with a created_at is checked (an older
	// lead sends none), and only new ones: a replay of an applied command was answered from the log above.
	if commandTooOld(p.cmd, p.Now) {
		res, err = refusal(http.StatusConflict, team.ErrCommandExpired, "the lead host queued this command too long ago"), nil
	} else {
		res, err = s.applyIn(tx, p)
	}
	if errors.Is(err, ErrCommandUnsupported) {
		return CommandResult{}, err
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

// commandTooOld says whether c is an adopt or a spawn that its lead host queued more than commandExpiryMS +
// commandSkewMS before now (this host's clock). A command without created_at, or created in the future by this host's
// clock, is not too old.
func commandTooOld(c team.TeamCommand, now int64) bool {
	if c.CreatedAt <= 0 || (c.Kind != team.CommandAdopt && c.Kind != team.CommandSpawn) {
		return false
	}
	return now-c.CreatedAt > commandExpiryMS+commandSkewMS
}

// applyIn dispatches a new command to its kind's apply.
func (s *Store) applyIn(tx *sql.Tx, p CommandPlan) (res CommandResult, err error) {
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
	return res, err
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
