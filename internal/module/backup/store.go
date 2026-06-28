package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrHashMismatch is returned by PutBlob when sha256(content) != the claimed
// hash. The handler maps it to HTTP 400.
var ErrHashMismatch = errors.New("blob content hash mismatch")

// ErrInvalidRequest wraps any append validation failure (manifest, parent, or
// size). The handler maps it to HTTP 400.
var ErrInvalidRequest = errors.New("invalid snapshot request")

// ErrBlobMissing is returned when a manifest references a blob the daemon does
// not have. The handler maps it to HTTP 409 (finish blob upload first).
var ErrBlobMissing = errors.New("referenced blob missing")

// AppendRequest is the input to AppendSnapshot.
type AppendRequest struct {
	StoreID  string
	Device   string
	ParentID *int64 // nil = root (first backup)
	Trigger  string
	Manifest []ManifestEntry
}

// AppendResult is the outcome of AppendSnapshot. Written distinguishes a real
// insert from a content-keyed no-op (where no new row was written and
// SnapshotID is the existing head's id).
type AppendResult struct {
	StoreID       string
	SnapshotID    int64
	CurrentHeadID int64
	IsFork        bool
	Device        string
	Trigger       string
	CreatedAt     int64
	Written       bool
}

// ManifestEntry is one entry in a snapshot manifest. Directory entries
// (Kind=="dir") carry Hash=="", Size==0, Words==0 so empty dirs round-trip.
type ManifestEntry struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"` // "file" | "dir"
	Hash  string `json:"hash"`
	Size  int64  `json:"size"`
	Words int64  `json:"words"`
}

// BackupStore is the SQLite-backed, content-addressed, append-only snapshot
// store for the In-App /buffer tree (subsystem 2).
type BackupStore struct {
	db *sql.DB
	// now returns the current Unix time in seconds. Default time.Now().Unix();
	// overridable in tests to drive created_at, the 90-day age window, and the
	// blob grace period deterministically.
	now func() int64
	// afterReadHead is an unexported test seam: nil in production (zero cost).
	// AppendSnapshot invokes it (when non-nil) inside the BEGIN IMMEDIATE
	// transaction, right after reading the store head but before inserting, so
	// the serialisation test can force the race window deterministically.
	afterReadHead func()
}

// OpenBackup opens (or creates) a BackupStore at path and runs schema
// migration. Use ":memory:" for tests; concurrency/lock tests need a
// file-backed path (a :memory: DB pins MaxOpenConns(1) and never exercises
// real writer contention).
func OpenBackup(path string) (*BackupStore, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout(500): make concurrent writers WAIT on the BEGIN
		// IMMEDIATE write lock instead of returning SQLITE_BUSY immediately.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open backup db: %w", err)
	}
	// :memory: is a single shared in-memory DB only if the pool is pinned to
	// one connection; file-backed DBs use a small cap to bound fd usage while
	// still allowing a second writer to contend on the write lock.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
	}
	s := &BackupStore{db: db, now: func() int64 { return time.Now().Unix() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate backup db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *BackupStore) Close() error { return s.db.Close() }

// migrate creates the backup tables and index if they don't already exist.
// No foreign keys (parent/manifest references are validated in-handler) — this
// avoids the #850 DSN-pragma footgun entirely.
func (s *BackupStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS backup_blobs (
			hash       TEXT    PRIMARY KEY,
			content    BLOB    NOT NULL,
			size       INTEGER NOT NULL,
			created_at INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS backup_snapshots (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			store_id   TEXT    NOT NULL,
			device     TEXT    NOT NULL DEFAULT '',
			parent_id  INTEGER,
			is_fork    INTEGER NOT NULL DEFAULT 0,
			trigger    TEXT    NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL DEFAULT 0,
			manifest   TEXT    NOT NULL DEFAULT ''
		);

		CREATE INDEX IF NOT EXISTS idx_snap_store ON backup_snapshots(store_id, id);
	`)
	return err
}

// PutBlob stores content under hash after verifying sha256(content)==hash.
// Idempotent: a re-PUT of an existing hash is a no-op (INSERT OR IGNORE).
// Returns ErrHashMismatch if the content does not hash to the claimed value.
func (s *BackupStore) PutBlob(hash string, content []byte) error {
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != hash {
		return ErrHashMismatch
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO backup_blobs (hash, content, size, created_at) VALUES (?, ?, ?, ?)`,
		hash, content, len(content), s.now(),
	)
	return err
}

// GetBlob returns the content stored under hash. found is false (no error) when
// the blob is absent.
func (s *BackupStore) GetBlob(hash string) (content []byte, found bool, err error) {
	err = s.db.QueryRow(`SELECT content FROM backup_blobs WHERE hash = ?`, hash).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// MissingBlobs returns the subset of hashes the daemon does not have, preserving
// input order.
func (s *BackupStore) MissingBlobs(hashes []string) ([]string, error) {
	missing := make([]string, 0)
	for _, h := range hashes {
		var one int
		err := s.db.QueryRow(`SELECT 1 FROM backup_blobs WHERE hash = ?`, h).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			missing = append(missing, h)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// AppendSnapshot runs the whole append sequence — read head → validate →
// parent/blob/size checks → content-keyed no-op → insert → GC — inside a single
// BEGIN IMMEDIATE transaction (spec §4.3, P1-3) so two concurrent posts cannot
// both record is_fork=false against the same head.
func (s *BackupStore) AppendSnapshot(req AppendRequest) (AppendResult, error) {
	now := s.now()
	ctx := context.Background()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return AppendResult{}, err
	}
	defer conn.Close()

	// BEGIN IMMEDIATE acquires the RESERVED write lock up front, so a second
	// concurrent writer blocks (busy_timeout) here rather than reading a stale
	// head. Manual SQL because database/sql's Begin uses a deferred tx.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return AppendResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	// 1. Read the current store head (max id for this store).
	var headID int64
	var headManifest string
	err = conn.QueryRowContext(ctx,
		`SELECT id, manifest FROM backup_snapshots WHERE store_id = ? ORDER BY id DESC LIMIT 1`,
		req.StoreID,
	).Scan(&headID, &headManifest)
	if errors.Is(err, sql.ErrNoRows) {
		headID, headManifest = 0, ""
	} else if err != nil {
		return AppendResult{}, err
	}

	// Test seam (Design 5): force the read-head/insert race window deterministically.
	if s.afterReadHead != nil {
		s.afterReadHead()
	}

	// 2. Validate the manifest (pure, §4.2).
	if err := ValidateManifest(req.Manifest); err != nil {
		return AppendResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}

	// 3. parentId must be null or an existing snapshot of the SAME store.
	if req.ParentID != nil {
		var one int
		e := conn.QueryRowContext(ctx,
			`SELECT 1 FROM backup_snapshots WHERE id = ? AND store_id = ?`,
			*req.ParentID, req.StoreID,
		).Scan(&one)
		if errors.Is(e, sql.ErrNoRows) {
			return AppendResult{}, fmt.Errorf("%w: parentId %d not found in store %q", ErrInvalidRequest, *req.ParentID, req.StoreID)
		} else if e != nil {
			return AppendResult{}, e
		}
	}

	// 4. Each file entry's blob must exist, and its size must match.
	for _, ent := range req.Manifest {
		if ent.Kind != "file" {
			continue
		}
		var size int64
		e := conn.QueryRowContext(ctx, `SELECT size FROM backup_blobs WHERE hash = ?`, ent.Hash).Scan(&size)
		if errors.Is(e, sql.ErrNoRows) {
			return AppendResult{}, fmt.Errorf("%w: %s (%q)", ErrBlobMissing, ent.Hash, ent.Path)
		} else if e != nil {
			return AppendResult{}, e
		}
		if size != ent.Size {
			return AppendResult{}, fmt.Errorf("%w: size %d != blob length %d for %q", ErrInvalidRequest, ent.Size, size, ent.Path)
		}
	}

	// 5. Canonical manifest JSON (entries already validated canonical/sorted).
	manifestJSON, err := json.Marshal(req.Manifest)
	if err != nil {
		return AppendResult{}, err
	}

	// 6. Content-keyed no-op (R3-Pa): identical to head → no row, regardless of
	// parentId. Only fires when a head exists.
	if headID != 0 && string(manifestJSON) == headManifest {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return AppendResult{}, err
		}
		committed = true
		return AppendResult{
			StoreID:       req.StoreID,
			SnapshotID:    headID,
			CurrentHeadID: headID,
			IsFork:        false,
			Written:       false,
		}, nil
	}

	// 7. Insert — is_fork is evaluated only when content differs from head.
	var parentVal int64
	var parentArg interface{}
	if req.ParentID != nil {
		parentVal = *req.ParentID
		parentArg = *req.ParentID
	}
	isFork := parentVal != headID
	res, err := conn.ExecContext(ctx,
		`INSERT INTO backup_snapshots (store_id, device, parent_id, is_fork, trigger, created_at, manifest)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		req.StoreID, req.Device, parentArg, boolToInt(isFork), req.Trigger, now, string(manifestJSON),
	)
	if err != nil {
		return AppendResult{}, err
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return AppendResult{}, err
	}

	// 8. GC within the same transaction.
	if err := s.gcTx(ctx, conn, req.StoreID, now); err != nil {
		return AppendResult{}, err
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return AppendResult{}, err
	}
	committed = true
	return AppendResult{
		StoreID:       req.StoreID,
		SnapshotID:    newID,
		CurrentHeadID: newID,
		IsFork:        isFork,
		Device:        req.Device,
		Trigger:       req.Trigger,
		CreatedAt:     now,
		Written:       true,
	}, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// gcTx runs garbage collection for one store inside an existing transaction.
// T2a-3 stub: replaced with the real keep-set / grace-period implementation in
// T2a-4.
func (s *BackupStore) gcTx(ctx context.Context, conn *sql.Conn, storeID string, now int64) error {
	return nil
}

// GC runs garbage collection across all store_ids using the given clock.
// T2a-0 stub: replaced with the real implementation in T2a-4.
func (s *BackupStore) GC(now int64) error {
	return nil
}
