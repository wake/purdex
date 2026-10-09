// internal/module/team/facts_recv_store.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

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

// ApplyTeamFact decides p in ONE transaction: the stored answer of an earlier copy (same member host, same id) when
// the content hash matches, ErrCommandIDConflict when it does not, else the fact applied or refused — the row change
// and the log entry (refusals included) committed together (spec §3.1 rules 2–3, §11 crash cut).
func (s *Store) ApplyTeamFact(p FactPlan) (CommandResult, error) {
	if err := json.Unmarshal(p.Body, &p.fact); err != nil || p.fact.ID == "" || p.fact.Kind == "" {
		return CommandResult{}, ErrCommandBadBody
	}
	p.hash = bodyHash(p.Body)
	fail := func(err error) (CommandResult, error) {
		return CommandResult{}, fmt.Errorf("apply fact %s %s: %w", p.fact.Kind, p.fact.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the reads below.
	if _, err := tx.Exec(`UPDATE team_fact_log SET id = id WHERE host_id = ? AND id = ?`, p.FromHostID, p.fact.ID); err != nil {
		return fail(err)
	}
	var hash, kind string
	var res CommandResult
	err = tx.QueryRow(`SELECT kind, body_hash, status, outcome_json FROM team_fact_log WHERE host_id = ? AND id = ?`,
		p.FromHostID, p.fact.ID).Scan(&kind, &hash, &res.Status, (*rawString)(&res.Body))
	switch {
	case err == nil:
		if hash != p.hash || kind != p.fact.Kind {
			return CommandResult{}, ErrCommandIDConflict
		}
		res.Replayed = true
		return res, nil
	case !errors.Is(err, sql.ErrNoRows):
		return fail(err)
	}

	switch {
	case p.Invalid != "": // shape before addressing (spec §6.1), after the stored answer above
		return refusal(http.StatusBadRequest, team.ErrCommandBadRequest, p.Invalid), nil
	case p.Refusal != nil:
		res, err = *p.Refusal, nil
	case p.fact.Kind == team.FactEnded:
		res, err = s.applyEndedIn(tx, p)
	default:
		return CommandResult{}, ErrCommandUnsupported
	}
	if err != nil {
		return fail(err)
	}
	if s.failBeforeFactLog != nil {
		if err := s.failBeforeFactLog(); err != nil {
			return fail(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO team_fact_log (host_id, id, kind, body_hash, status, outcome_json, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.FromHostID, p.fact.ID, p.fact.Kind, p.hash, res.Status, string(res.Body), p.Now); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return res, nil
}

// applyEndedIn is `ended` (spec §4.2, §4.5): the membership ended on the member host. The row is the one of THIS host
// with this mk in this team — never another host's, never a local row — so an old mk cannot touch a re-adopted
// membership. A live row (joining, active, releasing, killing) → gone{reason}, seat freed, while its team is live (D4);
// a row already past that, or of an ended team, is left as it is and the fact is answered "ignored". No such row at
// all is not_your_member.
func (s *Store) applyEndedIn(tx *sql.Tx, p FactPlan) (CommandResult, error) {
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
