// Package profiles stores sync profiles on the daemon: the profile list, each
// profile's per-section payloads (the source of truth a client syncs against),
// and which client is attached to which profile.
package profiles

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrProfileNotFound is returned by writes that target a profile id with no row.
	ErrProfileNotFound = errors.New("profile not found")
	// ErrProfileAttached is returned by DeleteProfile while any client is still
	// attached to the profile (spec decision 16).
	ErrProfileAttached = errors.New("profile has attached clients")
)

// Profile is one named sync profile.
type Profile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Attachment records that a client's master profile is ProfileID. A client has
// at most one attachment (client_id is the primary key).
type Attachment struct {
	ClientID   string `json:"clientId"`
	ProfileID  string `json:"profileId"`
	DeviceName string `json:"deviceName"`
	AttachedAt int64  `json:"attachedAt"`
	LastSeen   int64  `json:"lastSeen"`
}

// Store is the SQLite-backed persistence layer for profiles.
type Store struct {
	db    *sql.DB
	now   func() int64           // daemon clock in ms; injectable for tests
	newID func() (string, error) // profile id source; injectable for tests

	// afterSectionRead, when set, runs between PutSection's read of the current
	// row and its conditional write — the window a racing writer has to hit.
	// Tests use it to lose a race deterministically; it is nil in production.
	afterSectionRead func()
}

// OpenStore opens (or creates) a Store at path. Use ":memory:" for tests that
// do not need real concurrency.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout makes a writer that loses the WAL write lock wait for it
		// instead of failing with SQLITE_BUSY; the conditional writes below
		// rely on "every statement eventually runs", not on retry loops.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open profiles db: %w", err)
	}
	if path == ":memory:" {
		// Each pooled connection would otherwise see its own empty DB.
		db.SetMaxOpenConns(1)
	}
	s := &Store{
		db:    db,
		now:   func() int64 { return time.Now().UnixMilli() },
		newID: randomProfileID,
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate profiles db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *Store) Close() error { return s.db.Close() }

// randomProfileID returns "p_" + 12 hex chars from crypto/rand.
func randomProfileID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "p_" + hex.EncodeToString(b[:]), nil
}

// migrate creates the three tables of spec §4.8. There is deliberately no
// schema-version table (nothing else in the daemon has one) and no foreign
// key: DeleteProfile removes dependent rows itself, and PRAGMA foreign_keys is
// per connection, so it would have to live in the DSN to mean anything (see
// internal/store/agent_event.go).
func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS profiles (
			id          TEXT PRIMARY KEY,
			name        TEXT    NOT NULL,
			created_at  INTEGER NOT NULL,
			updated_at  INTEGER NOT NULL
		);

		CREATE TABLE IF NOT EXISTS profile_sections (
			profile_id  TEXT    NOT NULL,
			section     TEXT    NOT NULL,
			rev         INTEGER NOT NULL,
			hash        TEXT    NOT NULL,
			fingerprint TEXT    NOT NULL,
			ordinal     INTEGER NOT NULL,
			payload     TEXT    NOT NULL,
			writer      TEXT    NOT NULL,
			updated_at  INTEGER NOT NULL,
			deleted     INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (profile_id, section)
		);

		CREATE TABLE IF NOT EXISTS profile_attachments (
			client_id   TEXT PRIMARY KEY,
			profile_id  TEXT    NOT NULL,
			device_name TEXT    NOT NULL,
			attached_at INTEGER NOT NULL,
			last_seen   INTEGER NOT NULL
		);
	`)
	return err
}

// CreateProfile inserts a new profile with a store-generated id. Names may
// repeat (spec decision 15); the id is the identity.
func (s *Store) CreateProfile(name string) (Profile, error) {
	id, err := s.newID()
	if err != nil {
		return Profile{}, fmt.Errorf("generate profile id: %w", err)
	}
	now := s.now()
	p := Profile{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}
	if _, err := s.db.Exec(`
		INSERT INTO profiles (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		p.ID, p.Name, p.CreatedAt, p.UpdatedAt,
	); err != nil {
		return Profile{}, fmt.Errorf("create profile: %w", err)
	}
	return p, nil
}

// ListProfiles returns every profile, oldest first. It never returns a nil slice.
func (s *Store) ListProfiles() ([]Profile, error) {
	rows, err := s.db.Query(`
		SELECT id, name, created_at, updated_at
		FROM profiles
		ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	defer rows.Close()

	out := []Profile{}
	for rows.Next() {
		var p Profile
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan profile: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate profiles: %w", err)
	}
	return out, nil
}

// GetProfile returns the profile for id.
func (s *Store) GetProfile(id string) (Profile, bool, error) {
	var p Profile
	err := s.db.QueryRow(`
		SELECT id, name, created_at, updated_at FROM profiles WHERE id = ?`, id,
	).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, fmt.Errorf("get profile: %w", err)
	}
	return p, true, nil
}

// RenameProfile sets the profile's name and bumps updated_at. It returns false
// when there is no such profile.
func (s *Store) RenameProfile(id, name string) (bool, error) {
	res, err := s.db.Exec(`
		UPDATE profiles SET name = ?, updated_at = ? WHERE id = ?`, name, s.now(), id)
	if err != nil {
		return false, fmt.Errorf("rename profile: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rename profile rows affected: %w", err)
	}
	return n > 0, nil
}

// DeleteProfile removes the profile and its sections (tombstones included). It
// returns ErrProfileAttached while any client is attached to it, and
// ErrProfileNotFound when there is no such profile.
//
// The "no attachments" condition is part of the DELETE itself rather than a
// SELECT followed by a DELETE. With a check-then-write pair, a PutAttachment
// could land between the two and leave an attachment pointing at a profile
// that no longer exists — and the 409 the caller was promised would not have
// fired. As one statement, SQLite's single-writer lock orders it strictly
// before or after any competing PutAttachment.
//
// Order — the profile row first, its sections second, and no transaction
// around the pair. Every statement that adds a dependent row (PutAttachment
// here, the section insert in PutSection) carries its own
// `WHERE EXISTS (SELECT 1 FROM profiles WHERE id = ?)`, so the moment the
// profile row is gone nothing new can be attached to the id. The section
// sweep therefore runs against a set that can only shrink, and needs no
// isolation from concurrent writers. The opposite order would need a
// transaction: a section inserted after the sweep but before the profile
// delete would be orphaned. A transaction would also be the only
// multi-statement write in the module and, under database/sql, would upgrade
// read→write mid-flight (SQLITE_BUSY_SNAPSHOT, which busy_timeout does not
// retry). The price of going without: a crash between the two statements
// leaves section rows for an id that can never be reached again (ids are
// random and never reused) — dead bytes, not wrong behaviour.
func (s *Store) DeleteProfile(id string) error {
	res, err := s.db.Exec(`
		DELETE FROM profiles
		WHERE id = ?
		  AND NOT EXISTS (SELECT 1 FROM profile_attachments WHERE profile_id = ?)`,
		id, id)
	if err != nil {
		return fmt.Errorf("delete profile: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete profile rows affected: %w", err)
	}
	if n == 0 {
		// The conditional DELETE already decided not to act; this read only
		// classifies why. A racing writer can change the answer between the
		// two, but either classification is a refusal, and the profile is
		// untouched in both.
		_, found, err := s.GetProfile(id)
		if err != nil {
			return err
		}
		if !found {
			return ErrProfileNotFound
		}
		return ErrProfileAttached
	}
	if _, err := s.db.Exec(`DELETE FROM profile_sections WHERE profile_id = ?`, id); err != nil {
		return fmt.Errorf("delete profile sections: %w", err)
	}
	return nil
}

// PutAttachment makes a.ProfileID the master profile of a.ClientID, moving the
// client off any other profile (client_id is the primary key, so there is only
// ever one row per client). attached_at is set when the row is first inserted
// and kept across updates; last_seen is refreshed every call. Both come from
// the store clock — a.AttachedAt and a.LastSeen are ignored.
//
// It returns ErrProfileNotFound, and writes nothing, when the profile does not
// exist. The existence test is inside the statement (see DeleteProfile for
// why): the SELECT yields zero rows for an unknown profile, so neither the
// insert nor the DO UPDATE arm runs. RowsAffected is 1 for both an insert and
// an update, which makes 0 unambiguous — it can only mean "no such profile".
func (s *Store) PutAttachment(a Attachment) error {
	now := s.now()
	res, err := s.db.Exec(`
		INSERT INTO profile_attachments (client_id, profile_id, device_name, attached_at, last_seen)
		SELECT ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM profiles WHERE id = ?)
		ON CONFLICT(client_id) DO UPDATE SET
			profile_id  = excluded.profile_id,
			device_name = excluded.device_name,
			last_seen   = excluded.last_seen`,
		a.ClientID, a.ProfileID, a.DeviceName, now, now, a.ProfileID,
	)
	if err != nil {
		return fmt.Errorf("put attachment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("put attachment rows affected: %w", err)
	}
	if n == 0 {
		return ErrProfileNotFound
	}
	return nil
}

// DeleteAttachment detaches clientID from profileID. Both must match the row:
// a detach aimed at a profile the client is not attached to is a no-op
// (false), never somebody else's detach.
func (s *Store) DeleteAttachment(profileID, clientID string) (bool, error) {
	res, err := s.db.Exec(`
		DELETE FROM profile_attachments WHERE client_id = ? AND profile_id = ?`,
		clientID, profileID)
	if err != nil {
		return false, fmt.Errorf("delete attachment: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete attachment rows affected: %w", err)
	}
	return n > 0, nil
}

// ListAttachments returns the clients attached to profileID, oldest attachment
// first. It never returns a nil slice, including for an unknown profile.
func (s *Store) ListAttachments(profileID string) ([]Attachment, error) {
	rows, err := s.db.Query(`
		SELECT client_id, profile_id, device_name, attached_at, last_seen
		FROM profile_attachments
		WHERE profile_id = ?
		ORDER BY attached_at ASC, client_id ASC`, profileID)
	if err != nil {
		return nil, fmt.Errorf("list attachments: %w", err)
	}
	defer rows.Close()

	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ClientID, &a.ProfileID, &a.DeviceName, &a.AttachedAt, &a.LastSeen); err != nil {
			return nil, fmt.Errorf("scan attachment: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attachments: %w", err)
	}
	return out, nil
}
