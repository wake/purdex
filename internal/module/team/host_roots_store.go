// internal/module/team/host_roots_store.go — the lead host's record of the root changes it asked member hosts for, and the cache of
// what those hosts last said (cross-host roots spec 2026-10-12 §3.2, XR-2a). The routes that write and read them are XR-2b.
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// hostRootsSchema: host_root_changes is one change this host asked a member host for (add / remove one directory of the TeamRoots it
// granted this lead host); host_roots_seen is what that host last said its roots are, the App's 已生效 list. Idempotent; the
// deployed-schema rule applies from here on (a later change is a migration).
const hostRootsSchema = `
	CREATE TABLE IF NOT EXISTS host_root_changes (
		id           TEXT    PRIMARY KEY,
		host_id      TEXT    NOT NULL,
		op           TEXT    NOT NULL CHECK (op IN ('add', 'remove')),
		path         TEXT    NOT NULL,
		state        TEXT    NOT NULL CHECK (state IN ('pending', 'done', 'refused', 'dropped')),
		code         TEXT    NOT NULL DEFAULT '',
		detail       TEXT    NOT NULL DEFAULT '',
		canonical    TEXT    NOT NULL DEFAULT '',
		first_401_at INTEGER NOT NULL DEFAULT 0,
		created_at   INTEGER NOT NULL,
		updated_at   INTEGER NOT NULL,
		CHECK (host_id <> '' AND path <> '')
	);
	CREATE INDEX IF NOT EXISTS host_root_changes_host_state ON host_root_changes (host_id, state);
	CREATE TABLE IF NOT EXISTS host_roots_seen (
		host_id    TEXT    PRIMARY KEY,
		roots_json TEXT    NOT NULL,
		rev        INTEGER NOT NULL,
		seen_at    INTEGER NOT NULL
	);`

// States of a host_root_changes row.
const (
	RootChangePending = "pending"
	RootChangeDone    = "done"
	RootChangeRefused = "refused"
	RootChangeDropped = "dropped"
)

var (
	// ErrRootChangePending: a pending change cannot be dismissed (to undo one, send the inverse op).
	ErrRootChangePending = errors.New("the change is still pending")
	// ErrNoSuchRootChange is returned by the per-id methods for an unknown id.
	ErrNoSuchRootChange = errors.New("no such root change")
)

// HostRootChange is one row of host_root_changes. Canonical is the member host's own spelling of Path (an add answers the
// canonical path, a remove the stored root that matched).
type HostRootChange struct {
	ID, HostID, Op, Path string
	State                string
	Code, Detail         string
	Canonical            string
	First401At           int64
	CreatedAt, UpdatedAt int64
}

// HostRootsSeen is the cache row: the roots the member host last answered and its revision.
type HostRootsSeen struct {
	HostID string
	Roots  []string
	Rev    int64
	SeenAt int64
}

const rootChangeCols = `id, host_id, op, path, state, code, detail, canonical, first_401_at, created_at, updated_at`

func scanRootChange(sc interface{ Scan(...any) error }) (HostRootChange, error) {
	var c HostRootChange
	err := sc.Scan(&c.ID, &c.HostID, &c.Op, &c.Path, &c.State, &c.Code, &c.Detail, &c.Canonical, &c.First401At, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

// InsertHostRootChange stores a pending change. The id is a UUID per change (the lead never reuses one): a duplicate is an error.
func (s *Store) InsertHostRootChange(c HostRootChange) error {
	_, err := s.db.Exec(`INSERT INTO host_root_changes (id, host_id, op, path, state, created_at, updated_at) VALUES (?, ?, ?, ?, 'pending', ?, ?)`,
		c.ID, c.HostID, c.Op, c.Path, c.CreatedAt, c.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert root change %s: %w", c.ID, err)
	}
	return nil
}

// RootChange reads one change.
func (s *Store) RootChange(id string) (HostRootChange, bool, error) {
	c, err := scanRootChange(s.db.QueryRow(`SELECT `+rootChangeCols+` FROM host_root_changes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return HostRootChange{}, false, nil
	}
	return c, err == nil, err
}

// PendingRootChangeHosts are the hosts with a pending change, in host id order.
func (s *Store) PendingRootChangeHosts() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT host_id FROM host_root_changes WHERE state = 'pending' ORDER BY host_id`)
	if err != nil {
		return nil, fmt.Errorf("hosts with root changes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HeadRootChange is the oldest pending change of the host (rowid order): the one to send next. Order within a host is kept.
func (s *Store) HeadRootChange(hostID string) (HostRootChange, bool, error) {
	c, err := scanRootChange(s.db.QueryRow(`SELECT `+rootChangeCols+` FROM host_root_changes WHERE host_id = ? AND state = 'pending' ORDER BY rowid LIMIT 1`, hostID))
	if errors.Is(err, sql.ErrNoRows) {
		return HostRootChange{}, false, nil
	}
	return c, err == nil, err
}

// SettleRootChangeDone is the 200 answer, in ONE transaction: the change `done` with the member host's canonical spelling, and the
// cache moved to the roots and revision the answer carried (only forward, see UpsertHostRootsSeen). A change that is no longer
// pending (refused, dropped or dismissed meanwhile) is left alone and the cache with it. A failing cache write rolls the row back.
func (s *Store) SettleRootChangeDone(id, canonical string, roots []string, rev, at int64) error {
	fail := func(err error) error { return fmt.Errorf("settle root change %s: %w", id, err) }
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE host_root_changes SET state = 'done', canonical = ?, first_401_at = 0, updated_at = ? WHERE id = ? AND state = 'pending'`, canonical, at, id)
	if err != nil {
		return fail(err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fail(err)
	} else if n == 0 {
		return nil
	}
	var host string
	if err := tx.QueryRow(`SELECT host_id FROM host_root_changes WHERE id = ?`, id).Scan(&host); err != nil {
		return fail(err)
	}
	if s.failBeforeRootsCache != nil {
		if err := s.failBeforeRootsCache(); err != nil {
			return fail(err)
		}
	}
	if err := upsertRootsSeenIn(tx, host, roots, rev, at); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	return nil
}

// RefuseRootChange is the member host's JSON refusal: the change `refused` with its code and detail (cut to what the transport keeps).
func (s *Store) RefuseRootChange(id, code, detail string, at int64) error {
	_, err := s.db.Exec(`UPDATE host_root_changes SET state = 'refused', code = ?, detail = ?, first_401_at = 0, updated_at = ? WHERE id = ? AND state = 'pending'`, code, detail, at, id)
	if err != nil {
		return fmt.Errorf("refuse root change %s: %w", id, err)
	}
	return nil
}

// DropRootChangesOfHost drops the host's pending changes and forgets its cached roots (it is no longer paired); the number dropped.
func (s *Store) DropRootChangesOfHost(hostID string, at int64) (int64, error) {
	return dropRootChangesIn(s.db, hostID, at)
}

// dropRootChangesIn is DropRootChangesOfHost on a caller's transaction: unpairHost ends the host's root changes with the rest of the
// relation — its pending changes are dropped and its cached roots forgotten. The cache is monotonic in rev, and a re-paired host's
// grant may start again from rev 0: what an earlier relation said must not freeze the new one's cache (codex).
func dropRootChangesIn(ex dbtx, hostID string, at int64) (int64, error) {
	res, err := ex.Exec(`UPDATE host_root_changes SET state = 'dropped', first_401_at = 0, updated_at = ? WHERE host_id = ? AND state = 'pending'`, at, hostID)
	if err != nil {
		return 0, fmt.Errorf("drop root changes of %s: %w", hostID, err)
	}
	if _, err := ex.Exec(`DELETE FROM host_roots_seen WHERE host_id = ?`, hostID); err != nil {
		return 0, fmt.Errorf("forget the roots of %s: %w", hostID, err)
	}
	return res.RowsAffected()
}

// DismissRootChange deletes a refused or dropped change (the App's dismiss button). A pending one is ErrRootChangePending; a done
// one is reflected in the roots and goes too.
func (s *Store) DismissRootChange(id string) error {
	c, ok, err := s.RootChange(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoSuchRootChange
	}
	if c.State == RootChangePending {
		return ErrRootChangePending
	}
	if _, err := s.db.Exec(`DELETE FROM host_root_changes WHERE id = ? AND state <> 'pending'`, id); err != nil {
		return fmt.Errorf("dismiss root change %s: %w", id, err)
	}
	return nil
}

// Stamp401OfHead records the first 401 of a run on the host's head change (kept across a restart): an already stamped head keeps its
// first time. It returns the first time of the run, 0 when the host has nothing pending.
func (s *Store) Stamp401OfHead(hostID string, at int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id string
	var first int64
	switch err := tx.QueryRow(`SELECT id, first_401_at FROM host_root_changes WHERE host_id = ? AND state = 'pending' ORDER BY rowid LIMIT 1`, hostID).Scan(&id, &first); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, err
	}
	if first == 0 {
		first = at
		if _, err := tx.Exec(`UPDATE host_root_changes SET first_401_at = ? WHERE id = ?`, first, id); err != nil {
			return 0, err
		}
	}
	return first, tx.Commit()
}

// Clear401OfHead ends a run of 401s: any other answer clears the stamp.
func (s *Store) Clear401OfHead(hostID string) error {
	_, err := s.db.Exec(`UPDATE host_root_changes SET first_401_at = 0 WHERE host_id = ? AND state = 'pending' AND first_401_at <> 0`, hostID)
	return err
}

// UpsertHostRootsSeen records what a member host said its roots are. Only forward: a late, older answer (a lower rev) never
// overwrites a newer one; the same rev refreshes seen_at.
func (s *Store) UpsertHostRootsSeen(hostID string, roots []string, rev, at int64) error {
	return upsertRootsSeenIn(s.db, hostID, roots, rev, at)
}

func upsertRootsSeenIn(ex dbtx, hostID string, roots []string, rev, at int64) error {
	if roots == nil {
		roots = []string{}
	}
	b, err := json.Marshal(roots)
	if err != nil {
		return err
	}
	_, err = ex.Exec(`INSERT INTO host_roots_seen (host_id, roots_json, rev, seen_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (host_id) DO UPDATE SET roots_json = excluded.roots_json, rev = excluded.rev, seen_at = excluded.seen_at
		WHERE excluded.rev >= host_roots_seen.rev`, hostID, string(b), rev, at)
	if err != nil {
		return fmt.Errorf("cache the roots of %s: %w", hostID, err)
	}
	return nil
}

// HostRootsSeen reads the cache row of a host.
func (s *Store) HostRootsSeen(hostID string) (HostRootsSeen, bool, error) {
	var h HostRootsSeen
	var raw string
	err := s.db.QueryRow(`SELECT host_id, roots_json, rev, seen_at FROM host_roots_seen WHERE host_id = ?`, hostID).Scan(&h.HostID, &raw, &h.Rev, &h.SeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HostRootsSeen{}, false, nil
	}
	if err != nil {
		return HostRootsSeen{}, false, err
	}
	if err := json.Unmarshal([]byte(raw), &h.Roots); err != nil {
		return HostRootsSeen{}, false, fmt.Errorf("roots of %s: %w", hostID, err)
	}
	return h, true, nil
}
