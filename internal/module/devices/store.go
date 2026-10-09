package devices

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/wake/purdex/internal/devices"

	_ "modernc.org/sqlite"
)

// ErrNotFound: no live device with that id.
var ErrNotFound = errors.New("device not found")

// Store is the SQLite persistence of the paired phones' tokens (QR pairing spec §3.1): devices.db, owner-only, one table.
// Only the SHA-256 of a token is stored; the token itself is shown once, at Mint.
type Store struct {
	db  *sql.DB
	now func() int64 // Unix ms; injectable for tests

	afterLookup func() // test seam: runs between Authenticate's lookup and its first-use statement (nil in production)
}

// Row is a device token's record, never carrying the token or its hash.
type Row struct {
	ID          string
	PairingID   string
	ProfileID   string // the one profile this token may read (SOT host only); "" = none
	Label       string
	CreatedAt   int64
	CreatedBy   string
	UseBy       int64
	FirstUsedAt int64
	LastUsedAt  int64
	RevokedAt   int64
}

const (
	dbFileMode = 0o600
	// lastUsedEvery: last_used_at is written at most this often per token (a write on every request would make every
	// phone request a database write).
	lastUsedEvery = time.Minute
	// revokedKeep: a revoked row is kept this long (so the paired-phones list can still say what happened), then swept.
	revokedKeep = 30 * 24 * time.Hour
)

const columns = `id, pairing_id, profile_id, label, created_at, created_by, use_by, first_used_at, last_used_at, revoked_at`

// OpenStore opens (or creates) the store at path. Use ":memory:" for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
		// The rows are hashes of credentials and the data dir is world-readable: create the file owner-only BEFORE SQLite
		// does (it would be 0644 under the usual umask), and tighten an older one.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, dbFileMode)
		if err != nil {
			return nil, errors.New("open devices db: cannot create the file")
		}
		f.Close()
		if err := os.Chmod(path, dbFileMode); err != nil {
			return nil, errors.New("open devices db: cannot restrict the file")
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open devices db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS device_tokens (
			id TEXT PRIMARY KEY,
			pairing_id TEXT NOT NULL,
			profile_id TEXT NOT NULL DEFAULT '',
			label TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			created_at INTEGER NOT NULL,
			created_by TEXT NOT NULL,
			use_by INTEGER NOT NULL,
			first_used_at INTEGER NOT NULL DEFAULT 0,
			last_used_at INTEGER NOT NULL DEFAULT 0,
			revoked_at INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS device_tokens_pairing ON device_tokens (pairing_id);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate devices db: %w", err)
	}
	if path != ":memory:" {
		if err := restrictSidecars(path); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}, nil
}

// chmodFn is os.Chmod; a test seam.
var chmodFn = os.Chmod

// restrictSidecars chmods whichever WAL siblings exist (SQLite makes them with the main file's mode; this makes it
// explicit and covers one left over from an older run). A sidecar that exists and cannot be restricted fails the open:
// the hashes must not stay readable in it. One that does not exist is fine.
func restrictSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := chmodFn(path+suffix, dbFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("open devices db: cannot restrict a sidecar file")
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// MintRequest is what a new device token is made from.
type MintRequest struct {
	PairingID string
	ProfileID string
	Label     string
	CreatedBy string
	UseWithin time.Duration // the window after creation in which the token must be used for the first time
}

// Mint makes a device token and returns its row and the token, which is not stored and cannot be read again.
func (s *Store) Mint(req MintRequest) (Row, string, error) {
	if req.PairingID == "" || req.Label == "" || req.UseWithin <= 0 {
		return Row{}, "", errors.New("mint: pairing id, label and a use window are required")
	}
	token, err := devices.NewToken()
	if err != nil {
		return Row{}, "", err
	}
	id, err := devices.NewID()
	if err != nil {
		return Row{}, "", err
	}
	now := s.now()
	row := Row{ID: id, PairingID: req.PairingID, ProfileID: req.ProfileID, Label: req.Label, CreatedAt: now, CreatedBy: req.CreatedBy, UseBy: now + req.UseWithin.Milliseconds()}
	if _, err := s.db.Exec(`INSERT INTO device_tokens (id, pairing_id, profile_id, label, token_hash, created_at, created_by, use_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.PairingID, row.ProfileID, row.Label, devices.Hash(token), row.CreatedAt, row.CreatedBy, row.UseBy); err != nil {
		return Row{}, "", fmt.Errorf("mint: %w", err)
	}
	return row, token, nil
}

// List is every device row (revoked ones included until they are swept), oldest first.
func (s *Store) List() ([]Row, error) {
	rows, err := s.db.Query(`SELECT ` + columns + ` FROM device_tokens ORDER BY created_at, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.ID, &r.PairingID, &r.ProfileID, &r.Label, &r.CreatedAt, &r.CreatedBy, &r.UseBy, &r.FirstUsedAt, &r.LastUsedAt, &r.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevokeID revokes one device; true when it was live and is now revoked. Revoking a revoked or unknown id is not an error.
func (s *Store) RevokeID(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE device_tokens SET revoked_at = ? WHERE id = ? AND revoked_at = 0`, s.now(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RevokePairing revokes every live device of a pairing and returns their ids (the ones that changed).
func (s *Store) RevokePairing(pairingID string) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM device_tokens WHERE pairing_id = ? AND revoked_at = 0`, pairingID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	iterErr := rows.Err() // an iteration that stopped on an error is not the whole list: never revoke on part of it
	rows.Close()
	if iterErr != nil {
		return nil, iterErr
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if _, err := tx.Exec(`UPDATE device_tokens SET revoked_at = ? WHERE pairing_id = ? AND revoked_at = 0`, s.now(), pairingID); err != nil {
		return nil, err
	}
	return ids, tx.Commit()
}

// SetLabel renames a live device; ErrNotFound for an unknown or revoked one.
func (s *Store) SetLabel(id, label string) error {
	res, err := s.db.Exec(`UPDATE device_tokens SET label = ? WHERE id = ? AND revoked_at = 0`, label, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate looks a token's hash up and says who it is, or refuses. A token is live when it is not revoked and either
// was used before or is still before its use_by; the first use is decided in ONE conditional statement (so a token never
// used by use_by is refused at once, with no sweep needed, and two requests racing for the first use both succeed with
// the first use recorded once). last_used_at is written at most once a minute.
func (s *Store) Authenticate(tokenHash string) (devices.Principal, bool) {
	if len(tokenHash) != 64 {
		return devices.Principal{}, false
	}
	now := s.now()
	var (
		p                        devices.Principal
		firstUsed, lastUsed, rev int64
		useBy                    int64
	)
	err := s.db.QueryRow(`SELECT id, pairing_id, profile_id, first_used_at, last_used_at, use_by, revoked_at FROM device_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&p.ID, &p.PairingID, &p.ProfileID, &firstUsed, &lastUsed, &useBy, &rev)
	if err != nil || rev != 0 {
		return devices.Principal{}, false
	}
	if s.afterLookup != nil {
		s.afterLookup()
	}
	if firstUsed == 0 {
		res, err := s.db.Exec(`UPDATE device_tokens SET first_used_at = ?, last_used_at = ? WHERE id = ? AND first_used_at = 0 AND use_by >= ? AND revoked_at = 0`, now, now, p.ID, now)
		if err != nil {
			return devices.Principal{}, false
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return p, true
		}
		// Someone else took the first use (fine: it was used), or it is too late / was revoked meanwhile.
		var used, revoked int64
		if err := s.db.QueryRow(`SELECT first_used_at, revoked_at FROM device_tokens WHERE id = ?`, p.ID).Scan(&used, &revoked); err != nil || used == 0 || revoked != 0 {
			return devices.Principal{}, false
		}
		return p, true
	}
	if lastUsed <= now-lastUsedEvery.Milliseconds() {
		_, _ = s.db.Exec(`UPDATE device_tokens SET last_used_at = ? WHERE id = ? AND last_used_at <= ?`, now, p.ID, now-lastUsedEvery.Milliseconds())
	}
	return p, true
}

// PrincipalByID is the current principal of a device that is live and has been used (not revoked): what a redeemed ticket
// of that device is held to. False for an unknown, revoked or never-used device.
func (s *Store) PrincipalByID(id string) (devices.Principal, bool) {
	var (
		p                    devices.Principal
		firstUsed, revokedAt int64
	)
	err := s.db.QueryRow(`SELECT id, pairing_id, profile_id, first_used_at, revoked_at FROM device_tokens WHERE id = ?`, id).
		Scan(&p.ID, &p.PairingID, &p.ProfileID, &firstUsed, &revokedAt)
	if err != nil || revokedAt != 0 || firstUsed == 0 {
		return devices.Principal{}, false
	}
	return p, true
}

// IsLive: the device exists and is not revoked (what a WebSocket hijack asks, so a revoke that raced the open is caught).
func (s *Store) IsLive(id string) bool {
	var revoked int64
	if err := s.db.QueryRow(`SELECT revoked_at FROM device_tokens WHERE id = ?`, id).Scan(&revoked); err != nil {
		return false
	}
	return revoked == 0
}

// Sweep deletes the rows that can no longer work: never used and past use_by, or revoked more than 30 days ago. It returns
// how many. (Authenticate never depends on it: an expired unused token is refused at lookup.)
func (s *Store) Sweep() (int, error) {
	now := s.now()
	res, err := s.db.Exec(`DELETE FROM device_tokens WHERE (first_used_at = 0 AND use_by < ?) OR (revoked_at > 0 AND revoked_at < ?)`, now, now-revokedKeep.Milliseconds())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
