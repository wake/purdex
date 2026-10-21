package notify

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

const dbFileMode = 0o600

// Label is one group the App wrote for a root session.
type Label struct{ Root, Group string }

// Entry is the group that stands for a root: the newest write of any device.
type Entry struct {
	Group string
	Seq   int64
}

// Store keeps the groups the App wrote back (notify.db). A row is (device, root, group); the sequence number is the total order of
// the writes (not the clock: two writes in one millisecond still have an order).
type Store struct {
	db  *sql.DB
	now func() int64 // unix milliseconds

	failInsertAt int // tests: the next Replace fails when it comes to this row (0-based); -1: never (OpenStore sets it)
}

// OpenStore opens (and migrates) the file, creating it owner-only. ":memory:" is for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, dbFileMode)
		if err != nil {
			return nil, errors.New("open notify db: cannot create the file")
		}
		f.Close()
		if err := os.Chmod(path, dbFileMode); err != nil {
			return nil, errors.New("open notify db: cannot restrict the file")
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open notify db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS group_labels (
			seq        INTEGER PRIMARY KEY AUTOINCREMENT,
			device     TEXT    NOT NULL,
			root       TEXT    NOT NULL,
			grp        TEXT    NOT NULL,
			written_at INTEGER NOT NULL,
			UNIQUE (device, root)
		);
		CREATE INDEX IF NOT EXISTS group_labels_root ON group_labels (root);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate notify db: %w", err)
	}
	if path != ":memory:" {
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := os.Chmod(path+suffix, dbFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
				db.Close()
				return nil, errors.New("open notify db: cannot restrict a sidecar file")
			}
		}
	}
	return &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }, failInsertAt: -1}, nil
}

// Replace makes labels the whole set of the device, in one transaction: a reader sees the old set or the new one, never an empty or a
// half set, and a failure leaves the old one. A root named twice in labels keeps its last.
func (s *Store) Replace(device string, labels []Label) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // a no-op after Commit
	if _, err := tx.Exec(`DELETE FROM group_labels WHERE device = ?`, device); err != nil {
		return err
	}
	at := s.now()
	last := map[string]int{} // a root named twice keeps its last
	for i, l := range labels {
		last[l.Root] = i
	}
	n := 0
	for i, l := range labels {
		if last[l.Root] != i {
			continue
		}
		if n == s.failInsertAt {
			s.failInsertAt = -1
			return errors.New("notify: injected failure")
		}
		n++
		if _, err := tx.Exec(`INSERT INTO group_labels (device, root, grp, written_at) VALUES (?, ?, ?, ?)`, device, l.Root, l.Group, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Best is the group of every root: the row with the highest sequence number.
func (s *Store) Best() (map[string]Entry, error) {
	rows, err := s.db.Query(`SELECT root, grp, seq FROM group_labels g WHERE seq = (SELECT MAX(seq) FROM group_labels WHERE root = g.root)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Entry{}
	for rows.Next() {
		var root string
		var e Entry
		if err := rows.Scan(&root, &e.Group, &e.Seq); err != nil {
			return nil, err
		}
		out[root] = e
	}
	return out, rows.Err()
}

// Sweep removes every row of the devices whose newest write is before cutoff (unix milliseconds), and says how many rows went.
func (s *Store) Sweep(cutoff int64) (int, error) {
	res, err := s.db.Exec(`DELETE FROM group_labels WHERE device IN (SELECT device FROM group_labels GROUP BY device HAVING MAX(written_at) < ?)`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }
