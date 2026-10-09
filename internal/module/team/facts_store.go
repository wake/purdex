// internal/module/team/facts_store.go
package teammod

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// factSchema is M's facts outbox (spec §5.1): one row per fact for the lead host, written in the transaction of the
// change that caused it (§3.1 rule 2). Shaped like L's team_commands so one pump serves both directions (X2c-2).
// Idempotent; any later change is a migration.
const factSchema = `
	CREATE TABLE IF NOT EXISTS team_facts (
		id           TEXT PRIMARY KEY,
		kind         TEXT    NOT NULL,
		team_id      TEXT    NOT NULL,
		mk           TEXT    NOT NULL DEFAULT '',
		host_id      TEXT    NOT NULL,
		body_json    TEXT    NOT NULL,
		body_hash    TEXT    NOT NULL,
		state        TEXT    NOT NULL DEFAULT 'pending',
		attempts     INTEGER NOT NULL DEFAULT 0,
		next_at      INTEGER NOT NULL DEFAULT 0,
		first_401_at INTEGER NOT NULL DEFAULT 0,
		created_at   INTEGER NOT NULL,
		updated_at   INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS team_facts_host ON team_facts (host_id, state);`

// Fact states.
const (
	factPending = "pending"
	factDone    = "done"
	factDropped = "dropped" // its lead host was unpaired (spec §3.2)
)

// factRow is one team_facts row.
type factRow struct {
	ID, Kind, TeamID, MK, HostID, BodyJSON, BodyHash, State string
	Attempts                                                int
	NextAt, First401At, CreatedAt, UpdatedAt                int64
}

// FactsOfHost lists the lead host's facts, in the order they were written.
func (s *Store) FactsOfHost(hostID string) ([]factRow, error) {
	rows, err := s.db.Query(`SELECT id, kind, team_id, mk, host_id, body_json, body_hash, state, attempts, next_at, first_401_at, created_at, updated_at
		FROM team_facts WHERE host_id = ? ORDER BY created_at, id`, hostID)
	if err != nil {
		return nil, fmt.Errorf("facts of %s: %w", hostID, err)
	}
	defer rows.Close()
	out := []factRow{}
	for rows.Next() {
		var f factRow
		if err := rows.Scan(&f.ID, &f.Kind, &f.TeamID, &f.MK, &f.HostID, &f.BodyJSON, &f.BodyHash, &f.State, &f.Attempts, &f.NextAt, &f.First401At, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ActiveRemoteMembers lists the live remote rows (the sweeper's liveness check), oldest first.
func (s *Store) ActiveRemoteMembers() ([]remoteMemberRow, error) {
	rows, err := s.db.Query(`SELECT `+remoteMemberCols+` FROM remote_members WHERE state = ? ORDER BY created_at, mk`, remoteActive)
	if err != nil {
		return nil, fmt.Errorf("active remote members: %w", err)
	}
	defer rows.Close()
	out := []remoteMemberRow{}
	for rows.Next() {
		var r remoteMemberRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// writeEndedFactIn queues an `ended` fact for the lead host in the caller's transaction.
func writeEndedFactIn(tx dbtx, factID, leadHostID, teamID, mk, reason string, at int64) error {
	return writeFactIn(tx, team.TeamFact{ID: factID, Kind: team.FactEnded, ToHostID: leadHostID, TeamID: teamID, MK: mk, Reason: reason}, at)
}

// writeFactIn queues fact (its ToHostID is the lead host) in the caller's transaction.
func writeFactIn(tx dbtx, f team.TeamFact, at int64) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	_, err = tx.Exec(`INSERT INTO team_facts (id, kind, team_id, mk, host_id, body_json, body_hash, state, created_at, updated_at, next_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.ID, f.Kind, f.TeamID, f.MK, f.ToHostID, string(body), hex.EncodeToString(sum[:]), factPending, at, at, at)
	if err != nil {
		return fmt.Errorf("queue %s fact %s: %w", f.Kind, f.ID, err)
	}
	return nil
}

// MarkRemoteMemberGone is the sweeper's verdict on a remote member whose session is no longer live (spec §5.2:
// active → gone, with the `ended` fact in the same transaction, §3.1 rule 2). It is a CAS on the row AND the
// session the sweeper looked at, so a row that moved on (released, ended, re-adopted for another session) is
// left alone. No notice: nobody is there to read it. false when nothing changed.
func (s *Store) MarkRemoteMemberGone(mk, memberSessionID, factID string, at int64) (bool, error) {
	fail := func(err error) (bool, error) { return false, fmt.Errorf("mark remote member %s gone: %w", mk, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the read (a deferred read-then-write transaction fails
	// SQLITE_BUSY_SNAPSHOT when another writer commits in between).
	if _, err := tx.Exec(`UPDATE remote_members SET mk = mk WHERE mk = ?`, mk); err != nil {
		return fail(err)
	}
	var leadHost, teamID, sid string
	err = tx.QueryRow(`SELECT lead_host_id, team_id, member_session_id FROM remote_members WHERE mk = ? AND state = ?`, mk, remoteActive).Scan(&leadHost, &teamID, &sid)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && sid != memberSessionID) {
		return false, nil
	}
	if err != nil {
		return fail(err)
	}
	if ok, err := casRemoteMemberStateIn(tx, mk, []string{remoteActive}, remoteGone, at); err != nil || !ok {
		return false, err
	}
	if err := writeEndedFactIn(tx, factID, leadHost, teamID, mk, team.FactReasonSessionGone, at); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return true, nil
}

// remoteEndResult is the outcome of EndRemoteMemberLocally.
type remoteEndResult int

const (
	remoteEndEnded remoteEndResult = iota + 1
	remoteEndNotFound
	remoteEndNotLive
)

// EndRemoteMemberLocally is the operator on this host ending a remote member (spec §5.2 / §3.2: active → ended
// {local_end}): the state change, the `ended` fact for the lead host and the notice for the member in ONE
// transaction. The role goes with the state, so self relay is on again. causeID names the notice's cause.
func (s *Store) EndRemoteMemberLocally(mk, factID, causeID string, at int64) (remoteEndResult, error) {
	fail := func(err error) (remoteEndResult, error) { return 0, fmt.Errorf("end remote member %s: %w", mk, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE remote_members SET mk = mk WHERE mk = ?`, mk); err != nil { // the write lock first, as above
		return fail(err)
	}
	var leadHost, teamID, leadAddr, teamName, state string
	err = tx.QueryRow(`SELECT lead_host_id, team_id, lead_address, team_name, state FROM remote_members WHERE mk = ?`, mk).
		Scan(&leadHost, &teamID, &leadAddr, &teamName, &state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return remoteEndNotFound, nil
	case err != nil:
		return fail(err)
	case state != remoteActive:
		return remoteEndNotLive, nil
	}
	if ok, err := casRemoteMemberStateIn(tx, mk, []string{remoteActive}, remoteEnded, at); err != nil {
		return fail(err)
	} else if !ok {
		return remoteEndNotLive, nil
	}
	if err := writeEndedFactIn(tx, factID, leadHost, teamID, mk, team.FactReasonLocalEnd, at); err != nil {
		return fail(err)
	}
	if s.failAfterFactInsert != nil {
		if err := s.failAfterFactInsert(); err != nil {
			return fail(err)
		}
	}
	if err := oweNoticeIn(tx, mk, noticeLocalEnd, causeID, leadAddr, teamName, at); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return remoteEndEnded, nil
}

// EndUnpairedRemoteMembers is spec §3.2 on the member host: the lead host's peer entry is gone, so every live
// remote row of it ends (active → ended) and every fact still queued for a host that is no longer paired is
// dropped. No notice and no fact: the lead host is not bound, there is nobody to tell and nobody to hear it.
// pairedHostIDs are the host ids of the live peer entries. Terminal rows stay as they ended. It returns how many
// live rows it ended.
func (s *Store) EndUnpairedRemoteMembers(pairedHostIDs []string, at int64) (int, error) {
	fail := func(err error) (int, error) { return 0, fmt.Errorf("end unpaired remote members: %w", err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the reads below.
	if _, err := tx.Exec(`UPDATE remote_members SET mk = mk WHERE mk = ''`); err != nil {
		return fail(err)
	}
	rows, err := tx.Query(`SELECT mk, lead_host_id FROM remote_members WHERE state = ?`, remoteActive)
	if err != nil {
		return fail(err)
	}
	var due []string
	for rows.Next() {
		var mk, host string
		if err := rows.Scan(&mk, &host); err != nil {
			rows.Close()
			return fail(err)
		}
		if !containsString(pairedHostIDs, host) {
			due = append(due, mk)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fail(err)
	}
	rows.Close()
	ended := 0
	for _, mk := range due {
		if ok, err := casRemoteMemberStateIn(tx, mk, []string{remoteActive}, remoteEnded, at); err != nil {
			return fail(err)
		} else if ok {
			ended++
		}
	}
	q := `UPDATE team_facts SET state = ?, updated_at = ? WHERE state = ?`
	args := []any{factDropped, at, factPending}
	if len(pairedHostIDs) > 0 {
		q += ` AND host_id NOT IN (` + strings.TrimSuffix(strings.Repeat("?, ", len(pairedHostIDs)), ", ") + `)`
		for _, h := range pairedHostIDs {
			args = append(args, h)
		}
	}
	if _, err := tx.Exec(q, args...); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return ended, nil
}

// EndRemoteMembersOfHost is spec §3.2 on the member host for ONE lead host (the facts pump found it unpaired, or
// unpaired_by_peer): its live remote rows end, its still-queued facts are dropped, in one transaction. No notice,
// no fact. Rows of other hosts and terminal rows are untouched. It returns how many live rows it ended.
func (s *Store) EndRemoteMembersOfHost(hostID string, at int64) (int, error) {
	fail := func(err error) (int, error) { return 0, fmt.Errorf("end remote members of %s: %w", hostID, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE remote_members SET mk = mk WHERE mk = ''`); err != nil { // the write lock first
		return fail(err)
	}
	rows, err := tx.Query(`SELECT mk FROM remote_members WHERE lead_host_id = ? AND state = ?`, hostID, remoteActive)
	if err != nil {
		return fail(err)
	}
	var due []string
	for rows.Next() {
		var mk string
		if err := rows.Scan(&mk); err != nil {
			rows.Close()
			return fail(err)
		}
		due = append(due, mk)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fail(err)
	}
	rows.Close()
	ended := 0
	for _, mk := range due {
		if ok, err := casRemoteMemberStateIn(tx, mk, []string{remoteActive}, remoteEnded, at); err != nil {
			return fail(err)
		} else if ok {
			ended++
		}
	}
	if _, err := tx.Exec(`UPDATE team_facts SET state = ?, updated_at = ? WHERE host_id = ? AND state = ?`, factDropped, at, hostID, factPending); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return ended, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
