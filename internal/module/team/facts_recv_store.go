// internal/module/team/facts_recv_store.go
package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// factLogSchema is L's side of the facts route (spec §4.1): every fact it decided, applied and refused. Keys are
// scoped by the member host that sent the fact, so one paired host can never read or replay another's fact id.
// Idempotent; any later change is a migration. (team_facts, the sender's outbox, is facts_store.go.)
const factLogSchema = `
	CREATE TABLE IF NOT EXISTS team_fact_log (
		host_id      TEXT    NOT NULL,
		id           TEXT    NOT NULL,
		kind         TEXT    NOT NULL,
		body_hash    TEXT    NOT NULL,
		status       INTEGER NOT NULL,
		outcome_json TEXT    NOT NULL,
		at           INTEGER NOT NULL,
		PRIMARY KEY (host_id, id)
	);`

// FactPlan is one fact to decide. Body is the fact as received; the store decodes and hashes it itself.
type FactPlan struct {
	FromHostID string // the authenticated member host (the principal's host id)
	Body       json.RawMessage
	Now        int64
	// Refusal, when set, is the decision the route already took from the fact's addressing or kind (wrong_host,
	// unsupported_kind): it is stored like any other answer (§3.1 rule 3, refusals included), so a copy resent after an
	// upgrade meets the stored refusal instead of being applied for the first time.
	Refusal *CommandResult
	// Invalid, when not empty, is the shape problem the route found (validateFact). It is answered 400 bad_request
	// only AFTER the stored answer of an earlier copy is consulted — validation rules may tighten across versions, and a
	// stored decision must not change — and it is not stored itself: a malformed fact has no content worth keeping.
	Invalid string

	fact team.TeamFact
	hash string
}

// connTx is one connection inside an immediate transaction, as a dbtx: the member and task writers take the connection
// itself, the plain statements take its Exec / QueryRow.
type connTx struct {
	ctx  context.Context
	conn *sql.Conn
}

func (c connTx) Exec(q string, args ...any) (sql.Result, error) {
	return c.conn.ExecContext(c.ctx, q, args...)
}
func (c connTx) QueryRow(q string, args ...any) *sql.Row {
	return c.conn.QueryRowContext(c.ctx, q, args...)
}

// ApplyTeamFact decides p in ONE transaction: the stored answer of an earlier copy (same member host, same id) when
// the content hash matches, ErrCommandIDConflict when it does not, else the fact applied or refused — the row changes
// (a member and its first task included) and the log entry (refusals included) committed together (spec §3.1 rules 2–3,
// §11 crash cut).
func (s *Store) ApplyTeamFact(p FactPlan) (CommandResult, error) {
	if err := json.Unmarshal(p.Body, &p.fact); err != nil || p.fact.ID == "" || p.fact.Kind == "" {
		return CommandResult{}, ErrCommandBadBody
	}
	p.hash = bodyHash(p.Body)
	var res CommandResult
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var err error
		res, err = s.applyFactIn(connTx{ctx, conn}, p)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrCommandIDConflict) || errors.Is(err, ErrCommandUnsupported) {
			return CommandResult{}, err
		}
		return CommandResult{}, fmt.Errorf("apply fact %s %s: %w", p.fact.Kind, p.fact.ID, err)
	}
	return res, nil
}

func (s *Store) applyFactIn(tx connTx, p FactPlan) (CommandResult, error) {
	var hash, kind string
	var res CommandResult
	err := tx.QueryRow(`SELECT kind, body_hash, status, outcome_json FROM team_fact_log WHERE host_id = ? AND id = ?`,
		p.FromHostID, p.fact.ID).Scan(&kind, &hash, &res.Status, (*rawString)(&res.Body))
	switch {
	case err == nil:
		if hash != p.hash || kind != p.fact.Kind {
			return CommandResult{}, ErrCommandIDConflict
		}
		res.Replayed = true
		return res, nil
	case !errors.Is(err, sql.ErrNoRows):
		return CommandResult{}, err
	}

	switch {
	case p.Refusal != nil: // addressing and kind first, as the commands route (§6.1), after the stored answer above
		res, err = *p.Refusal, nil
	case p.Invalid != "":
		return refusal(http.StatusBadRequest, team.ErrCommandBadRequest, p.Invalid), nil
	case p.fact.Kind == team.FactEnded:
		res, err = s.applyEndedIn(tx, p)
	case p.fact.Kind == team.FactRegistered:
		res, err = s.applyRegisteredIn(tx, p)
	case p.fact.Kind == team.FactSpawnFailed:
		res, err = s.applySpawnFailedIn(tx, p)
	default:
		return CommandResult{}, ErrCommandUnsupported
	}
	if err != nil {
		return CommandResult{}, err
	}
	if s.failBeforeFactLog != nil {
		if err := s.failBeforeFactLog(); err != nil {
			return CommandResult{}, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO team_fact_log (host_id, id, kind, body_hash, status, outcome_json, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.FromHostID, p.fact.ID, p.fact.Kind, p.hash, res.Status, string(res.Body), p.Now); err != nil {
		return CommandResult{}, err
	}
	return res, nil
}

// applyEndedIn is `ended` (spec §4.2, §4.5): the membership ended on the member host. The row is the one of THIS host
// with this mk in this team — never another host's, never a local row — so an old mk cannot touch a re-adopted
// membership. A live row (joining, active, releasing, killing) → gone{reason}, seat freed, while its team is live (D4);
// a row already past that, or of an ended team, is left as it is and the fact is answered "ignored". No such row at
// all is not_your_member.
func (s *Store) applyEndedIn(tx dbtx, p FactPlan) (CommandResult, error) {
	f := p.fact
	var one int
	err := tx.QueryRow(`SELECT 1 FROM team_members WHERE host_id = ? AND host_id <> ? AND mk = ? AND team_id = ?`,
		p.FromHostID, s.localHostID, f.MK, f.TeamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no such membership of that host in that team"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	reason := f.Reason
	if reason == "" {
		reason = "ended"
	}
	r, err := tx.Exec(`UPDATE team_members SET state = 'gone', end_reason = ?, updated_at = ?, ended_at = ?
		WHERE host_id = ? AND mk = ? AND team_id = ? AND state IN `+liveRemoteStates+` AND `+liveTeamOfRow,
		reason, p.Now, p.Now, p.FromHostID, f.MK, f.TeamID)
	if err != nil {
		return CommandResult{}, err
	}
	state := team.FactIgnored
	if n, _ := r.RowsAffected(); n > 0 {
		state = team.FactApplied
	}
	return okResult(map[string]string{"state": state})
}

// applySpawnFailedIn is `spawn_failed` (spec §4.5): the forwarded op of THIS host with this id in this team ends failed
// with the member host's reason, its seat freed. An op that is no longer running (done, failed by a void or an unpairing)
// or a team that ended is left as it is — "ignored". No such op is not_your_member.
func (s *Store) applySpawnFailedIn(tx dbtx, p FactPlan) (CommandResult, error) {
	f := p.fact
	var one int
	err := tx.QueryRow(`SELECT 1 FROM remote_spawns WHERE id = ? AND host_id = ? AND team_id = ?`, f.MK, p.FromHostID, f.TeamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no such spawn of that host in that team"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	r, err := tx.Exec(`UPDATE remote_spawns SET state = 'failed', reason = ?, updated_at = ?
		WHERE id = ? AND host_id = ? AND team_id = ? AND state = 'running' AND `+liveTeamOfSpawn, f.Reason, p.Now, f.MK, p.FromHostID, f.TeamID)
	if err != nil {
		return CommandResult{}, err
	}
	return okResult(map[string]string{"state": outcomeOf(r)})
}

// liveTeamOfSpawn is liveTeamOfRow for a remote_spawns row.
const liveTeamOfSpawn = `EXISTS (SELECT 1 FROM teams WHERE teams.id = remote_spawns.team_id AND teams.ended_at = 0)`

func outcomeOf(r sql.Result) string {
	if n, _ := r.RowsAffected(); n > 0 {
		return team.FactApplied
	}
	return team.FactIgnored
}

// applyRegisteredIn is `registered` (spec §4.5, §7): the forwarded op of THIS host with this id in this team gets its
// member — the row (active, created now) and, when the op carries one, its first task ON THIS HOST — and the op is done,
// in the transaction that logs the fact. The op must still be running and its team live; otherwise the fact is "ignored".
// A session that is already an active member here fails the op `session_conflict` (the row could never be inserted, and
// the fact would roll back and be sent forever) — as an adopt answer does.
func (s *Store) applyRegisteredIn(tx connTx, p FactPlan) (CommandResult, error) {
	f := p.fact
	var op remoteSpawnRow
	err := tx.QueryRow(`SELECT `+remoteSpawnCols+` FROM remote_spawns WHERE id = ? AND host_id = ? AND team_id = ?`, f.MK, p.FromHostID, f.TeamID).
		Scan(op.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no such spawn of that host in that team"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	var live int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM teams WHERE id = ? AND ended_at = 0`, op.TeamID).Scan(&live); err != nil {
		return CommandResult{}, err
	}
	if op.State != remoteSpawnRunning || live == 0 {
		return okResult(map[string]string{"state": team.FactIgnored})
	}
	var taken int
	switch err := tx.QueryRow(`SELECT 1 FROM team_members WHERE session_id = ? AND state = 'active'`, f.MemberSession).Scan(&taken); {
	case err == nil:
		if _, err := tx.Exec(`UPDATE remote_spawns SET state = 'failed', reason = 'session_conflict', updated_at = ? WHERE id = ? AND state = 'running'`, p.Now, op.ID); err != nil {
			return CommandResult{}, err
		}
		return okResult(map[string]string{"state": team.FactApplied})
	case !errors.Is(err, sql.ErrNoRows):
		return CommandResult{}, err
	}
	title := f.Title
	if title == "" {
		title = op.Title
	}
	mem := memberRow{SpawnOp: op.ID, TeamID: op.TeamID, HostID: p.FromHostID, SessionID: f.MemberSession, Ref: f.Ref, Title: title, Cwd: op.Cwd,
		PaneID: f.Pane, PID: f.PID, ProcStart: f.ProcStart, Model: op.Model, Effort: op.Effort, State: team.MemberActive,
		Origin: team.MemberOriginSpawned, CreatedAt: p.Now, UpdatedAt: p.Now}
	if err := insertMemberIn(tx.ctx, tx.conn, mem); err != nil {
		return CommandResult{}, err
	}
	if op.TaskSubject != "" {
		done, err := op.doneWhen()
		if err != nil {
			return CommandResult{}, err
		}
		t := TaskRow{TeamID: op.TeamID, Subject: op.TaskSubject, Description: op.TaskDescription, DoneWhen: done, OwnerKey: op.ID,
			CreatedByRef: ipeers.RefID(op.OriginSessionID), SpawnOp: op.ID, CreatedAt: p.Now, UpdatedAt: p.Now}
		if _, err := s.createTaskIn(tx.ctx, tx.conn, t); err != nil {
			return CommandResult{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE remote_spawns SET state = 'done', member_session_id = ?, updated_at = ? WHERE id = ? AND state = 'running'`,
		f.MemberSession, p.Now, op.ID); err != nil {
		return CommandResult{}, err
	}
	return okResult(map[string]string{"state": team.FactApplied})
}
