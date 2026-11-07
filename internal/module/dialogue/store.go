package dialogue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const dbFileMode = 0o600

// SessionState is a session's cursor and segment bookkeeping (dlg_sessions).
type SessionState struct {
	Segment      int
	SegBytes     int64
	CursorTurn   string
	CursorItems  int
	CursorItemID string
	NextTurnNo   int
	SourcePath   string
	// Rebuilding: the files are being rewritten from the whole conversation and the rewrite has not finished. It stays
	// set across a crash or an interrupted run, so the next Write keeps rebuilding (Placed blocks included).
	Rebuilding bool
}

// TurnRow is one turn's allocated number and where it was written (dlg_turns).
type TurnRow struct {
	TurnID  string
	TurnNo  int
	Segment int
	EndedAt int64
}

// Store is dialogue.db: the cursors and turn numbers, owner-only, one file.
type Store struct {
	db               *sql.DB
	now              func() int64 // unix ms; injectable for tests
	wmu              sync.Mutex   // serialises the writes
	failAfterSession func() error // test seam: runs inside Commit after the session upsert; nil in production
	// failInSummaryDone: test seam; runs inside SetSummaryDone between the row change and the since_recap count.
	failInSummaryDone func() error
}

// OpenStore opens (or creates) the store at path. The file and its WAL siblings are owner-only.
func OpenStore(path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, dbFileMode)
	if err != nil {
		return nil, errors.New("open dialogue db: cannot create the file")
	}
	f.Close()
	if err := os.Chmod(path, dbFileMode); err != nil {
		return nil, errors.New("open dialogue db: cannot restrict the file")
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open dialogue db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Chmod(path+suffix, dbFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			db.Close()
			return nil, errors.New("open dialogue db: cannot restrict a sidecar file")
		}
	}
	return &Store{db: db, now: func() int64 { return time.Now().UnixMilli() }}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Session returns a session's state; false when it has none (never written).
func (s *Store) Session(sid string) (SessionState, bool, error) {
	var st SessionState
	var rebuilding int
	err := s.db.QueryRow(`SELECT segment, seg_bytes, cursor_turn, cursor_items, cursor_item_id, next_turn_no, source_path, rebuilding
		FROM dlg_sessions WHERE session_id = ?`, sid).
		Scan(&st.Segment, &st.SegBytes, &st.CursorTurn, &st.CursorItems, &st.CursorItemID, &st.NextTurnNo, &st.SourcePath, &rebuilding)
	st.Rebuilding = rebuilding != 0
	if errors.Is(err, sql.ErrNoRows) {
		return SessionState{}, false, nil
	}
	if err != nil {
		return SessionState{}, false, fmt.Errorf("read dialogue session: %w", err)
	}
	return st, true, nil
}

// Commit upserts the session state and records newTurns in ONE transaction. A turn already recorded keeps its number.
func (s *Store) Commit(sid string, st SessionState, newTurns []TurnRow) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("commit dialogue: %w", err)
	}
	defer tx.Rollback()
	now := s.now()
	if _, err := tx.Exec(`INSERT INTO dlg_sessions
		(session_id, segment, seg_bytes, cursor_turn, cursor_items, cursor_item_id, next_turn_no, source_path, rebuilding, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET segment = excluded.segment, seg_bytes = excluded.seg_bytes,
			cursor_turn = excluded.cursor_turn, cursor_items = excluded.cursor_items, cursor_item_id = excluded.cursor_item_id,
			next_turn_no = excluded.next_turn_no, source_path = excluded.source_path, rebuilding = excluded.rebuilding,
			updated_at = excluded.updated_at`,
		sid, st.Segment, st.SegBytes, st.CursorTurn, st.CursorItems, st.CursorItemID, st.NextTurnNo, st.SourcePath, rebuildingInt(st.Rebuilding), now, now); err != nil {
		return fmt.Errorf("commit dialogue session: %w", err)
	}
	if s.failAfterSession != nil {
		if err := s.failAfterSession(); err != nil {
			return err
		}
	}
	for _, t := range newTurns {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO dlg_turns (session_id, turn_id, turn_no, segment, ended_at) VALUES (?, ?, ?, ?, ?)`,
			sid, t.TurnID, t.TurnNo, t.Segment, t.EndedAt); err != nil {
			return fmt.Errorf("commit dialogue turn: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dialogue: %w", err)
	}
	return nil
}

// TurnNo returns the number allocated to a turn.
func (s *Store) TurnNo(sid, turnID string) (int, bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT turn_no FROM dlg_turns WHERE session_id = ? AND turn_id = ?`, sid, turnID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read dialogue turn: %w", err)
	}
	return n, true, nil
}

// TurnIDByNo returns the id of the turn with the number.
func (s *Store) TurnIDByNo(sid string, no int) (string, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT turn_id FROM dlg_turns WHERE session_id = ? AND turn_no = ?`, sid, no).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read dialogue turn: %w", err)
	}
	return id, true, nil
}

// TurnNos returns every turn number of a session by turn id (a rebuild keeps them).
func (s *Store) TurnNos(sid string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT turn_id, turn_no FROM dlg_turns WHERE session_id = ?`, sid)
	if err != nil {
		return nil, fmt.Errorf("read dialogue turns: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("read dialogue turns: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

func rebuildingInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ResetForRebuild forgets a session's cursor and segment position and marks it rebuilding; its turn numbers
// (dlg_turns, next_turn_no) and source path stay. The row is kept, so a crash after the reset still knows to rebuild.
func (s *Store) ResetForRebuild(sid string) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if _, err := s.db.Exec(`UPDATE dlg_sessions SET segment = 1, seg_bytes = 0, cursor_turn = '', cursor_items = 0,
		cursor_item_id = '', rebuilding = 1, updated_at = ? WHERE session_id = ?`, s.now(), sid); err != nil {
		return fmt.Errorf("reset dialogue session: %w", err)
	}
	return nil
}

// migrations: step i takes a file at user_version i to i+1. Never edit a shipped step; add the next one.
var migrations = []func(x stepExec) error{migrateV1, migrateV2, migrateV3}

type stepExec interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func migrate(db *sql.DB) error {
	for i := range migrations {
		if err := retryBusy(func() error { return runStep(db, i) }); err != nil {
			return fmt.Errorf("migrate dialogue db (to version %d): %w", i+1, err)
		}
	}
	return nil
}

var busyRetryFor = 5 * time.Second

func retryBusy(f func() error) error {
	deadline := time.Now().Add(busyRetryFor)
	for {
		err := f()
		busy := err != nil && (strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked"))
		if !busy {
			return err
		}
		if time.Now().After(deadline) {
			log.Printf("[dialogue] migrate: the database stayed locked for %s; giving up", busyRetryFor)
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// runStep applies step i if the file is at version i, under BEGIN IMMEDIATE with the version re-read inside it.
// A file newer than this daemon is refused. PRAGMA user_version is transactional in SQLite, so the step and its
// version write commit together.
func runStep(db *sql.DB, i int) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	var have int
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&have); err != nil {
		return err
	}
	if have > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this daemon's %d", have, len(migrations))
	}
	if have == i {
		if err := migrations[i](connTx{ctx, conn}); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	done = true
	return nil
}

type connTx struct {
	ctx  context.Context
	conn *sql.Conn
}

func (c connTx) Exec(query string, args ...any) (sql.Result, error) {
	return c.conn.ExecContext(c.ctx, query, args...)
}

func migrateV1(tx stepExec) error {
	_, err := tx.Exec(`
		CREATE TABLE dlg_sessions (
			session_id TEXT PRIMARY KEY,
			segment INTEGER NOT NULL,
			seg_bytes INTEGER NOT NULL,
			cursor_turn TEXT NOT NULL DEFAULT '',
			cursor_items INTEGER NOT NULL DEFAULT 0,
			cursor_item_id TEXT NOT NULL DEFAULT '',
			next_turn_no INTEGER NOT NULL,
			source_path TEXT NOT NULL DEFAULT '',
			rebuilding INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL);
		CREATE TABLE dlg_turns (
			session_id TEXT NOT NULL,
			turn_id TEXT NOT NULL,
			turn_no INTEGER NOT NULL,
			segment INTEGER NOT NULL,
			ended_at INTEGER NOT NULL,
			PRIMARY KEY (session_id, turn_id));`)
	return err
}

// migrateV2: the turn summaries, and the count of them since the last recap.
func migrateV2(tx stepExec) error {
	_, err := tx.Exec(`
		CREATE TABLE dlg_summaries (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			conv_key TEXT NOT NULL DEFAULT '',
			turn_id TEXT NOT NULL,
			turn_no INTEGER NOT NULL,
			turn_at INTEGER NOT NULL,
			turn_seq INTEGER NOT NULL DEFAULT 0,
			state TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			push TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			push_ready_at INTEGER NOT NULL DEFAULT 0,
			usage_in INTEGER NOT NULL DEFAULT 0,
			usage_out INTEGER NOT NULL DEFAULT 0,
			usage_cache_read INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(session_id, turn_id));
		CREATE INDEX dlg_summaries_session_at ON dlg_summaries (session_id, turn_at);
		CREATE INDEX dlg_summaries_session_no ON dlg_summaries (session_id, turn_no);
		ALTER TABLE dlg_sessions ADD COLUMN since_recap INTEGER NOT NULL DEFAULT 0;`)
	return err
}

// migrateV3: the recaps. job_id is unique so the same answer stored twice makes one row.
func migrateV3(tx stepExec) error {
	_, err := tx.Exec(`
		CREATE TABLE dlg_recaps (
			id INTEGER PRIMARY KEY,
			job_id TEXT NOT NULL UNIQUE,
			session_id TEXT NOT NULL,
			conv_key TEXT NOT NULL DEFAULT '',
			at INTEGER NOT NULL,
			from_no INTEGER NOT NULL,
			to_no INTEGER NOT NULL,
			trigger TEXT NOT NULL,
			headline TEXT NOT NULL,
			body_json TEXT NOT NULL,
			usage_in INTEGER NOT NULL DEFAULT 0,
			usage_out INTEGER NOT NULL DEFAULT 0,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL);
		CREATE INDEX dlg_recaps_session ON dlg_recaps (session_id, id);`)
	return err
}
