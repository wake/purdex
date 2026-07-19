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
	ExecutionID  string
	DispatchID   string
	RepoLocation string // canonical repo path (canonicalisation happens in admission, P.5)
	// RepoLocationJSON is the full Ploom S5 repo_location object
	// ({project_id, remote_url?, owner?, repo?, local_dir, is_origin}) as JSON,
	// persisted verbatim so the accepted report can echo every field the contract
	// requires (m0-contract §2). The canonical path above is kept separately for
	// admission/diff; this is purely the durable echo source. Empty on rows that
	// predate the object (falls back to {local_dir: RepoLocation} in the echo).
	RepoLocationJSON string
	Provider         string // M0 always "claude"
	LaunchState      LaunchState
	SessionName      string         // non-NULL crash-recovery handle (pre-generated before spawn)
	SessionCode      sql.NullString // nullable deeplink handle (derived after tmux create)
	AttemptNo        int            // M0 always 1
	Status           Status
	SeqReported      int    // highest per-execution report seq emitted
	HeadAtStart      string // repo HEAD at admission (diff base)
	DirtyAtStart     bool
	SandboxProfile   string
	OutcomeSource    sql.NullString // nullable until terminal
	CreatedAt        int64
	UpdatedAt        int64
}

// NewExecution carries the immutable admission metadata used to create an
// execution row. SessionCode is intentionally absent — it is nullable and
// assigned later (after tmux session creation).
type NewExecution struct {
	// ExecutionID, when non-empty, is used verbatim as the new row's primary
	// key instead of a store-generated one. The launch durable cut (P.6) sets
	// it so that SessionName can be derived deterministically from the same
	// execution_id before the row is inserted (spec §4.3). Empty → the store
	// generates a fresh exc_-prefixed id.
	ExecutionID  string
	DispatchID   string
	RepoLocation string
	// RepoLocationJSON is the full repo_location object (JSON) echoed on the
	// accepted report; persisted verbatim alongside the canonical RepoLocation
	// path. Empty is allowed (echo falls back to {local_dir: RepoLocation}).
	RepoLocationJSON string
	Provider         string
	SessionName      string
	// LaunchState is the launch fence the row is created with. Empty → LaunchNone.
	// The launch durable cut inserts the row already at LaunchLaunching (spec
	// §4.3), so a crash between row insert and NewSession is reconciled as a
	// failed launch rather than relaunched.
	LaunchState    LaunchState
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
			repo_location_json TEXT NOT NULL DEFAULT '',
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
	if err != nil {
		return err
	}
	// The report outbox lives in this same database so a state transition and the
	// report it implies can commit atomically (store_report.go).
	return migrateOutbox(s.db)
}

// HasLiveByRepo reports whether the given canonical repo path already has a live
// execution — one whose status is accepted or running (spec §7.2). Liveness is
// read from status only, never from launch_state: a completed execution left at
// launch_state=launched must not keep a repo permanently blocked (R2 #1). The
// caller (admission) holds the per-repo lock across this check and the
// subsequent row insert, so the check is not a racy point-in-time read.
func (s *ExecutionStore) HasLiveByRepo(ctx context.Context, canonicalPath string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM executions
		   WHERE repo_location = ? AND status IN (?, ?)
		   LIMIT 1`,
		canonicalPath, string(StatusAccepted), string(StatusRunning),
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListLive returns every execution whose status is live (accepted or running),
// oldest first (spec §5.4). It is the startup reconcile sweep's input: liveness
// is read from status only (never launch_state), so terminal rows are never
// returned — a second sweep after reconcile drove rows terminal is a no-op. The
// row set is snapshotted before any per-row load so the single :memory: pinned
// connection is released before loadByID re-acquires it.
func (s *ExecutionStore) ListLive() ([]*Execution, error) {
	ctx := context.Background()
	ids, err := func() ([]string, error) {
		rows, err := s.db.QueryContext(ctx,
			`SELECT execution_id FROM executions
			   WHERE status IN (?, ?)
			   ORDER BY created_at, execution_id`,
			string(StatusAccepted), string(StatusRunning),
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}()
	if err != nil {
		return nil, err
	}
	execs := make([]*Execution, 0, len(ids))
	for _, id := range ids {
		e, err := loadByID(ctx, s.db, id)
		if err != nil {
			return nil, err
		}
		execs = append(execs, e)
	}
	return execs, nil
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

// GetBySessionCode returns the execution whose derived deeplink session_code
// matches. The terminal seam (P.8) keys on session_code because the stream
// bridge identifies a relay by that same code (the launcher registers the relay
// under EncodeSessionID). found is false (no error) when no row matches; NULL
// session_codes (not yet launched) never match a non-empty lookup.
func (s *ExecutionStore) GetBySessionCode(sessionCode string) (*Execution, bool, error) {
	var id string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT execution_id FROM executions WHERE session_code = ?`, sessionCode).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return s.GetByID(id)
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
		SELECT execution_id, dispatch_id, repo_location, repo_location_json, provider, launch_state,
		       session_name, session_code, attempt_no, status, seq_reported,
		       head_at_start, dirty_at_start, sandbox_profile, outcome_source,
		       created_at, updated_at
		FROM executions WHERE execution_id = ?`, execID,
	).Scan(
		&e.ExecutionID, &e.DispatchID, &e.RepoLocation, &e.RepoLocationJSON, &e.Provider, &launchState,
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
