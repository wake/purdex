package execution

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when an execution_id has no row.
var ErrNotFound = errors.New("execution not found")

// ErrIllegalTransition is returned by UpdateStatus when the requested status
// change is not a legal state-machine edge (spec §5.1) — in particular, a
// terminal execution can never be revived.
var ErrIllegalTransition = errors.New("illegal execution status transition")

// Execution is the Purdex runtime SOT row for one dispatch (spec §4.3). It is
// the authoritative record of an execution's durable state; the accepted-report
// immutable metadata (head/dirty/sandbox/execution_id/repo_location) all live
// here so accepted can always be rebuilt after a daemon restart.
type Execution struct {
	ExecutionID    string
	DispatchID     string
	RepoLocation   string // canonical repo path (canonicalisation happens in admission, P.5)
	Provider       string // M0 always "claude"
	LaunchState    LaunchState
	SessionName    string         // non-NULL crash-recovery handle (pre-generated before spawn)
	SessionCode    sql.NullString // nullable deeplink handle (derived after tmux create)
	AttemptNo      int            // M0 always 1
	Status         Status
	SeqReported    int    // highest per-execution report seq emitted
	HeadAtStart    string // repo HEAD at admission (diff base)
	DirtyAtStart   bool
	SandboxProfile string
	OutcomeSource  sql.NullString // nullable until terminal
	CreatedAt      int64
	UpdatedAt      int64
}

// NewExecution carries the immutable admission metadata used to create an
// execution row. SessionCode is intentionally absent — it is nullable and
// assigned later (after tmux session creation).
type NewExecution struct {
	DispatchID     string
	RepoLocation   string
	Provider       string
	SessionName    string
	HeadAtStart    string
	DirtyAtStart   bool
	SandboxProfile string
}

// ExecutionStore is the SQLite-backed runtime SOT for executions.
type ExecutionStore struct {
	db *sql.DB
	// now returns the current Unix time in seconds; overridable in tests.
	now func() int64
}

// OpenExecution opens (or creates) an ExecutionStore at path and runs schema
// migration. Use ":memory:" for tests.
func OpenExecution(path string) (*ExecutionStore, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout: block concurrent writers on the write lock instead of
		// failing fast with SQLITE_BUSY.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open execution db: %w", err)
	}
	// A :memory: DB is only shared across a single pinned connection.
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
	}
	s := &ExecutionStore{db: db, now: func() int64 { return time.Now().Unix() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate execution db: %w", err)
	}
	return s, nil
}

// Close closes the underlying DB connection.
func (s *ExecutionStore) Close() error { return s.db.Close() }

// migrate creates the executions table if it doesn't already exist. No foreign
// keys (avoids the #850 DSN-pragma footgun). The UNIQUE constraint on
// dispatch_id is what enforces one-execution-per-dispatch idempotency (§8).
func (s *ExecutionStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS executions (
			execution_id    TEXT    PRIMARY KEY,
			dispatch_id     TEXT    NOT NULL UNIQUE,
			repo_location   TEXT    NOT NULL DEFAULT '',
			provider        TEXT    NOT NULL DEFAULT 'claude',
			launch_state    TEXT    NOT NULL DEFAULT 'none',
			session_name    TEXT    NOT NULL,
			session_code    TEXT,
			attempt_no      INTEGER NOT NULL DEFAULT 1,
			status          TEXT    NOT NULL DEFAULT 'accepted',
			seq_reported    INTEGER NOT NULL DEFAULT 0,
			head_at_start   TEXT    NOT NULL DEFAULT '',
			dirty_at_start  INTEGER NOT NULL DEFAULT 0,
			sandbox_profile TEXT    NOT NULL DEFAULT '',
			outcome_source  TEXT,
			created_at      INTEGER NOT NULL DEFAULT 0,
			updated_at      INTEGER NOT NULL DEFAULT 0
		);
	`)
	return err
}

// UpsertByDispatch creates a new execution for req.DispatchID, or returns the
// existing execution unchanged if the dispatch was already admitted. created is
// true only when a new row was inserted. The whole operation runs in a single
// BEGIN IMMEDIATE transaction so two concurrent claims of the same dispatch_id
// cannot both insert (contract §8; the UNIQUE dispatch_id index is the backstop).
func (s *ExecutionStore) UpsertByDispatch(req NewExecution) (*Execution, bool, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, false, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	// Existing dispatch → return its execution unchanged (idempotent, no rebuild).
	existingID, err := scanExecutionID(conn.QueryRowContext(ctx,
		`SELECT execution_id FROM executions WHERE dispatch_id = ?`, req.DispatchID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	if err == nil {
		exec, err := loadByID(ctx, conn, existingID)
		if err != nil {
			return nil, false, err
		}
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return nil, false, err
		}
		committed = true
		return exec, false, nil
	}

	// New dispatch → insert with a freshly generated execution_id.
	now := s.now()
	provider := req.Provider
	if provider == "" {
		provider = "claude"
	}
	execID := newExecutionID()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO executions (
			execution_id, dispatch_id, repo_location, provider, launch_state,
			session_name, session_code, attempt_no, status, seq_reported,
			head_at_start, dirty_at_start, sandbox_profile, outcome_source,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, NULL, ?, ?)`,
		execID, req.DispatchID, req.RepoLocation, provider, string(LaunchNone),
		req.SessionName, 1, string(StatusAccepted), 0,
		req.HeadAtStart, boolToInt(req.DirtyAtStart), req.SandboxProfile,
		now, now,
	)
	if err != nil {
		return nil, false, err
	}
	exec, err := loadByID(ctx, conn, execID)
	if err != nil {
		return nil, false, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, false, err
	}
	committed = true
	return exec, true, nil
}

// GetByID returns the execution with the given id. found is false (no error)
// when the row is absent.
func (s *ExecutionStore) GetByID(execID string) (*Execution, bool, error) {
	exec, err := loadByID(context.Background(), s.db, execID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return exec, true, nil
}

// UpdateStatus advances an execution to a new status, enforcing the state
// machine (spec §5.1). Returns ErrNotFound if the id is unknown, or
// ErrIllegalTransition if the move is not a legal edge (e.g. terminal→running).
func (s *ExecutionStore) UpdateStatus(execID string, to Status) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	var cur string
	err = conn.QueryRowContext(ctx,
		`SELECT status FROM executions WHERE execution_id = ?`, execID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !CanTransition(Status(cur), to) {
		return fmt.Errorf("%w: %s → %s", ErrIllegalTransition, cur, to)
	}
	if _, err := conn.ExecContext(ctx,
		`UPDATE executions SET status = ?, updated_at = ? WHERE execution_id = ?`,
		string(to), s.now(), execID,
	); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// rowQuerier is satisfied by both *sql.DB and *sql.Conn.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func loadByID(ctx context.Context, q rowQuerier, execID string) (*Execution, error) {
	var (
		e           Execution
		launchState string
		status      string
		dirty       int64
	)
	err := q.QueryRowContext(ctx, `
		SELECT execution_id, dispatch_id, repo_location, provider, launch_state,
		       session_name, session_code, attempt_no, status, seq_reported,
		       head_at_start, dirty_at_start, sandbox_profile, outcome_source,
		       created_at, updated_at
		FROM executions WHERE execution_id = ?`, execID,
	).Scan(
		&e.ExecutionID, &e.DispatchID, &e.RepoLocation, &e.Provider, &launchState,
		&e.SessionName, &e.SessionCode, &e.AttemptNo, &status, &e.SeqReported,
		&e.HeadAtStart, &dirty, &e.SandboxProfile, &e.OutcomeSource,
		&e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	e.LaunchState = LaunchState(launchState)
	e.Status = Status(status)
	e.DirtyAtStart = dirty != 0
	return &e, nil
}

func scanExecutionID(row *sql.Row) (string, error) {
	var id string
	err := row.Scan(&id)
	return id, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// newExecutionID returns a fresh globally-unique execution id with the exc_
// prefix (contract §2). 16 hex chars (8 random bytes) is ample for M0.
func newExecutionID() string {
	var buf [8]byte
	rand.Read(buf[:])
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(buf)*2)
	for i, b := range buf {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return "exc_" + string(out)
}
