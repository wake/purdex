package sync

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SyncStore is the SQLite-backed persistence layer for the sync module.
type SyncStore struct{ db *sql.DB }

// HistoryEntry represents a single bundle push recorded in sync_history.
type HistoryEntry struct {
	ID        int64
	ClientID  string
	Device    string
	Bundle    string
	Timestamp int64
}

// OpenSyncStore opens (or creates) a SyncStore at path and runs schema migration.
// Use ":memory:" for tests.
func OpenSyncStore(path string) (*SyncStore, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sync db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	s := &SyncStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sync db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *SyncStore) Close() error { return s.db.Close() }

// migrate creates all sync tables if they don't already exist.
func (s *SyncStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sync_groups (
			group_id  TEXT    NOT NULL,
			client_id TEXT    NOT NULL,
			device    TEXT    NOT NULL DEFAULT '',
			last_seen INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (group_id, client_id)
		);

		CREATE TABLE IF NOT EXISTS sync_canonical (
			group_id   TEXT    PRIMARY KEY,
			updated_at INTEGER NOT NULL DEFAULT 0,
			bundle     TEXT    NOT NULL DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS sync_history (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id   TEXT    NOT NULL,
			client_id  TEXT    NOT NULL,
			device     TEXT    NOT NULL DEFAULT '',
			bundle     TEXT    NOT NULL DEFAULT '',
			timestamp  INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS sync_pairing (
			code       TEXT    PRIMARY KEY,
			group_id   TEXT    NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0,
			expires_at INTEGER NOT NULL DEFAULT 0,
			attempts   INTEGER NOT NULL DEFAULT 0
		);
	`)
	return err
}

// clientGroupID resolves the group_id for a given clientID.
// Returns an error if the client is not a member of any group.
func (s *SyncStore) clientGroupID(clientID string) (string, error) {
	var groupID string
	err := s.db.QueryRow(
		`SELECT group_id FROM sync_groups WHERE client_id = ? LIMIT 1`,
		clientID,
	).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("client %q not found in any group", clientID)
	}
	if err != nil {
		return "", err
	}
	return groupID, nil
}

// updateLastSeen bumps the last_seen timestamp for clientID within its group.
func (s *SyncStore) updateLastSeen(groupID, clientID string) error {
	_, err := s.db.Exec(
		`UPDATE sync_groups SET last_seen = ? WHERE group_id = ? AND client_id = ?`,
		time.Now().Unix(), groupID, clientID,
	)
	return err
}

// CreateGroup inserts a new group record into sync_canonical (no-op if exists).
func (s *SyncStore) CreateGroup(groupID string) error {
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO sync_canonical (group_id, updated_at, bundle) VALUES (?, 0, '')`,
		groupID,
	)
	return err
}

// AddClientToGroup registers clientID as a member of groupID.
func (s *SyncStore) AddClientToGroup(groupID, clientID, device string) error {
	_, err := s.db.Exec(`
		INSERT INTO sync_groups (group_id, client_id, device, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(group_id, client_id) DO UPDATE SET
			device    = excluded.device,
			last_seen = excluded.last_seen
	`, groupID, clientID, device, time.Now().Unix())
	return err
}

// PushBundle records a new canonical bundle for the group that clientID belongs
// to, and appends an entry to sync_history.
func (s *SyncStore) PushBundle(clientID, bundle string) error {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return err
	}

	if err := s.updateLastSeen(groupID, clientID); err != nil {
		return err
	}

	now := time.Now().Unix()

	// Resolve the device label for the history entry.
	var device string
	_ = s.db.QueryRow(
		`SELECT device FROM sync_groups WHERE group_id = ? AND client_id = ?`,
		groupID, clientID,
	).Scan(&device)

	// Upsert canonical bundle.
	_, err = s.db.Exec(`
		INSERT INTO sync_canonical (group_id, updated_at, bundle)
		VALUES (?, ?, ?)
		ON CONFLICT(group_id) DO UPDATE SET
			updated_at = excluded.updated_at,
			bundle     = excluded.bundle
	`, groupID, now, bundle)
	if err != nil {
		return err
	}

	// Append history record.
	_, err = s.db.Exec(`
		INSERT INTO sync_history (group_id, client_id, device, bundle, timestamp)
		VALUES (?, ?, ?, ?, ?)
	`, groupID, clientID, device, bundle, now)
	return err
}

// PullCanonical returns the current canonical bundle for the group that
// clientID belongs to.  Returns an empty string if no bundle has been pushed
// yet (not an error).
func (s *SyncStore) PullCanonical(clientID string) (string, error) {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return "", err
	}

	if err := s.updateLastSeen(groupID, clientID); err != nil {
		return "", err
	}

	var bundle string
	err = s.db.QueryRow(
		`SELECT bundle FROM sync_canonical WHERE group_id = ?`,
		groupID,
	).Scan(&bundle)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return bundle, err
}

// ListHistory returns up to limit history entries for the group that clientID
// belongs to, ordered by most-recent first.
func (s *SyncStore) ListHistory(clientID string, limit int) ([]HistoryEntry, error) {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT id, client_id, device, bundle, timestamp
		FROM sync_history
		WHERE group_id = ?
		ORDER BY id DESC
		LIMIT ?
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.ClientID, &e.Device, &e.Bundle, &e.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// pairingCharset is the unambiguous alphanumeric charset used for pairing codes
// (no 0/O/1/I to avoid visual confusion).
const pairingCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// CreatePairingCode generates an 8-character pairing code for the group that
// clientID belongs to.  Any existing codes for that group are replaced.
func (s *SyncStore) CreatePairingCode(clientID string) (string, error) {
	groupID, err := s.clientGroupID(clientID)
	if err != nil {
		return "", err
	}

	// Generate random code from charset.
	code, err := randomCode(8)
	if err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}

	now := time.Now().Unix()
	expiresAt := now + 300 // 5 minutes

	// Delete any existing codes for this group, then insert the new one.
	_, err = s.db.Exec(`DELETE FROM sync_pairing WHERE group_id = ?`, groupID)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`
		INSERT INTO sync_pairing (code, group_id, created_at, expires_at, attempts)
		VALUES (?, ?, ?, ?, 0)
	`, code, groupID, now, expiresAt)
	if err != nil {
		return "", err
	}
	return code, nil
}

// VerifyPairingCode validates code and returns the associated groupID on
// success.  On success the code is deleted (single-use).  On failure the
// attempts counter is incremented and an error is returned.
func (s *SyncStore) VerifyPairingCode(code string) (string, error) {
	var groupID string
	var expiresAt int64
	var attempts int

	err := s.db.QueryRow(
		`SELECT group_id, expires_at, attempts FROM sync_pairing WHERE code = ?`,
		code,
	).Scan(&groupID, &expiresAt, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("pairing code not found")
	}
	if err != nil {
		return "", err
	}

	if attempts >= 5 {
		return "", fmt.Errorf("pairing code locked: too many attempts")
	}

	if time.Now().Unix() > expiresAt {
		_, _ = s.db.Exec(`UPDATE sync_pairing SET attempts = attempts + 1 WHERE code = ?`, code)
		return "", fmt.Errorf("pairing code expired")
	}

	// Success: delete code and return groupID.
	_, err = s.db.Exec(`DELETE FROM sync_pairing WHERE code = ?`, code)
	if err != nil {
		return "", err
	}
	return groupID, nil
}

// generateGroupID returns a new unique group identifier prefixed with "g_"
// followed by 16 random hex characters.
func generateGroupID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate group id: %w", err)
	}
	return "g_" + hex.EncodeToString(b), nil
}

// randomCode generates an n-character string sampled uniformly from
// pairingCharset using crypto/rand.
func randomCode(n int) (string, error) {
	charset := []byte(pairingCharset)
	buf := make([]byte, n)
	random := make([]byte, n)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	for i, b := range random {
		buf[i] = charset[int(b)%len(charset)]
	}
	return string(buf), nil
}
