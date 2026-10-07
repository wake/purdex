package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Where a virtual name's base came from (peer mailbox spec §3.2). Only a
// lineage name is final; any other is upgraded once if a relay predecessor's
// name shows up later.
const (
	PeerNameSourceLineage          = "lineage"
	PeerNameSourceRegistry         = "registry"
	PeerNameSourceConversationName = "conversation_name"
	PeerNameSourceDir              = "dir"
)

// peerNameChunk bounds the number of bound parameters in one IN (...) query.
const peerNameChunk = 500

// PeerNameEntry is one stored virtual name.
type PeerNameEntry struct {
	Name, Source string
}

// PeerNameStore persists the pdx-assigned virtual name of every conversation
// (Peer Address v5, spec §3.2): one row per session id, written once at first
// sighting and never deleted. Session ids are stored lowercase.
type PeerNameStore struct{ db *sql.DB }

// PeerNames returns the virtual-name store backed by this MetaStore's DB.
func (m *MetaStore) PeerNames() *PeerNameStore {
	return &PeerNameStore{db: m.db}
}

func migratePeerNames(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS peer_names (
			session_id  TEXT PRIMARY KEY,
			ref         TEXT NOT NULL,
			name        TEXT NOT NULL,
			source      TEXT NOT NULL,
			assigned_at INTEGER NOT NULL
		)`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS peer_names_ref ON peer_names(ref)`)
	return err
}

func normPeerSID(sessionID string) string {
	return strings.ToLower(strings.TrimSpace(sessionID))
}

// Assign records name for sessionID unless the session already has one, and
// returns the row that is stored afterwards: the first writer wins and every
// concurrent caller converges on its name (INSERT … DO NOTHING, then SELECT,
// in one transaction).
func (s *PeerNameStore) Assign(ctx context.Context, sessionID, ref, name, source string, nowMs int64) (PeerNameEntry, error) {
	sid := normPeerSID(sessionID)
	switch {
	case sid == "":
		return PeerNameEntry{}, errors.New("peer name: empty session id")
	case name == "":
		return PeerNameEntry{}, errors.New("peer name: empty name")
	case source != PeerNameSourceLineage && source != PeerNameSourceRegistry &&
		source != PeerNameSourceConversationName && source != PeerNameSourceDir:
		return PeerNameEntry{}, fmt.Errorf("peer name: unknown source %q", source)
	}
	return s.writeThenRead(ctx, sid, `
		INSERT INTO peer_names (session_id, ref, name, source, assigned_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO NOTHING`,
		sid, ref, name, source, nowMs)
}

// AdoptLineage renames sessionID to its relay predecessor's name, once: a row
// whose source is already lineage is left alone, so a lineage name is never
// overwritten (spec §3.2 "lineage 晚到時升級一次"). It returns the stored row;
// an error when the session has no row at all.
func (s *PeerNameStore) AdoptLineage(ctx context.Context, sessionID, name string, nowMs int64) (PeerNameEntry, error) {
	sid := normPeerSID(sessionID)
	if sid == "" || name == "" {
		return PeerNameEntry{}, errors.New("peer name: empty session id or name")
	}
	return s.writeThenRead(ctx, sid, `
		UPDATE peer_names SET name = ?, source = 'lineage', assigned_at = ?
		WHERE session_id = ? AND source <> 'lineage'`,
		name, nowMs, sid)
}

func (s *PeerNameStore) writeThenRead(ctx context.Context, sid, write string, args ...any) (PeerNameEntry, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PeerNameEntry{}, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit succeeds
	if _, err := tx.ExecContext(ctx, write, args...); err != nil {
		return PeerNameEntry{}, err
	}
	var e PeerNameEntry
	if err := tx.QueryRowContext(ctx, `SELECT name, source FROM peer_names WHERE session_id = ?`, sid).
		Scan(&e.Name, &e.Source); err != nil {
		return PeerNameEntry{}, fmt.Errorf("peer name %s: %w", sid, err)
	}
	return e, tx.Commit()
}

// Lookup returns the stored row of every given session id that has one,
// keyed by the lowercase session id.
func (s *PeerNameStore) Lookup(ctx context.Context, sessionIDs []string) (map[string]PeerNameEntry, error) {
	out := make(map[string]PeerNameEntry)
	keys := make([]string, len(sessionIDs))
	for i, sid := range sessionIDs {
		keys[i] = normPeerSID(sid)
	}
	err := s.queryIn(ctx, `SELECT session_id, name, source FROM peer_names WHERE session_id IN (%s)`, keys,
		func(rows *sql.Rows) error {
			var sid string
			var e PeerNameEntry
			if err := rows.Scan(&sid, &e.Name, &e.Source); err != nil {
				return err
			}
			out[sid] = e
			return nil
		})
	return out, err
}

// ByRefs maps each given ref to the name stored for it. Two sessions sharing
// a ref (a 36^6 collision) answer the earliest assignment.
func (s *PeerNameStore) ByRefs(ctx context.Context, refs []string) (map[string]string, error) {
	out := make(map[string]string)
	err := s.queryIn(ctx, `SELECT ref, name FROM peer_names WHERE ref IN (%s) ORDER BY assigned_at DESC, session_id DESC`, refs,
		func(rows *sql.Rows) error {
			var ref, name string
			if err := rows.Scan(&ref, &name); err != nil {
				return err
			}
			out[ref] = name // rows run newest first, so the earliest is written last
			return nil
		})
	return out, err
}

// queryIn runs query once per chunk of args, with %s replaced by the chunk's
// placeholders, and hands every row to scan.
func (s *PeerNameStore) queryIn(ctx context.Context, query string, args []string, scan func(*sql.Rows) error) error {
	for len(args) > 0 {
		n := min(len(args), peerNameChunk)
		chunk := make([]any, n)
		for i := range chunk {
			chunk[i] = args[i]
		}
		args = args[n:]
		rows, err := s.db.QueryContext(ctx, fmt.Sprintf(query, strings.TrimSuffix(strings.Repeat("?,", n), ",")), chunk...)
		if err != nil {
			return err
		}
		for rows.Next() {
			if err := scan(rows); err != nil {
				rows.Close()
				return err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
