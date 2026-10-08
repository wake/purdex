package resourcesmod

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// leaseRow is one row of resource_leases. Times are unix milliseconds; a
// zero GrantedAt, EndedAt or WaitedMS stands for NULL (not yet).
type leaseRow struct {
	ID          string
	ClientID    string
	State       string // waiting | held | ended
	Kind        string // empty for an explicit weight
	Weight      int
	SessionID   string
	HolderPID   int
	HolderStart string
	Scope       string // process | session-new
	ToolUseID   string

	CreatedAt  int64
	DeadlineAt int64 // granted anyway from here on (spec R6)
	LeaseUntil int64 // a waiting row not renewed by then is abandoned
	GrantedAt  int64
	EndedAt    int64

	Overrun   bool
	WouldWait bool // mode advise: granted at once, but mode lease would have queued it
	EndReason string
	WaitedMS  int64

	PeakUse      float64
	MeanUse      float64
	EWMA         float64
	Samples      int
	EmptySamples int
	// Baseline is a JSON array of {pid, start_unix_ms}: the processes a
	// session-new lease does not own. NULL (empty) until P1-2b records it.
	Baseline string
}

// leaseStore is the SQLite persistence of resource_leases (resources.db).
// Every state change is a compare-and-set on the row's state, so of any
// number of concurrent writers exactly one wins.
type leaseStore struct{ db *sql.DB }

// openLeaseStore opens (or creates) resources.db at path. ":memory:" is for
// tests.
func openLeaseStore(path string) (*leaseStore, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout: a concurrent writer waits instead of failing with SQLITE_BUSY.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open resources db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS resource_leases (
			id            TEXT PRIMARY KEY,
			client_id     TEXT    NOT NULL UNIQUE,
			state         TEXT    NOT NULL CHECK (state IN ('waiting','held','ended')),
			kind          TEXT    NOT NULL DEFAULT '',
			weight        INTEGER NOT NULL,
			session_id    TEXT    NOT NULL DEFAULT '',
			holder_pid    INTEGER NOT NULL,
			holder_start  TEXT    NOT NULL DEFAULT '',
			scope         TEXT    NOT NULL CHECK (scope IN ('process','session-new')),
			tool_use_id   TEXT    NOT NULL DEFAULT '',
			created_at    INTEGER NOT NULL,
			deadline_at   INTEGER NOT NULL,
			lease_until   INTEGER NOT NULL,
			granted_at    INTEGER,
			ended_at      INTEGER,
			overrun       INTEGER NOT NULL DEFAULT 0,
			would_wait    INTEGER NOT NULL DEFAULT 0,
			end_reason    TEXT,
			waited_ms     INTEGER,
			peak_use      REAL,
			mean_use      REAL,
			ewma          REAL,
			samples       INTEGER NOT NULL DEFAULT 0,
			empty_samples INTEGER NOT NULL DEFAULT 0,
			baseline      TEXT
		);
		CREATE INDEX IF NOT EXISTS resource_leases_state ON resource_leases (state);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate resources db: %w", err)
	}
	return &leaseStore{db: db}, nil
}

// Close closes the database.
func (s *leaseStore) Close() error { return s.db.Close() }

const leaseCols = `id, client_id, state, kind, weight, session_id, holder_pid, holder_start, scope, tool_use_id,
	created_at, deadline_at, lease_until, COALESCE(granted_at,0), COALESCE(ended_at,0),
	overrun, would_wait, COALESCE(end_reason,''), COALESCE(waited_ms,0),
	COALESCE(peak_use,0), COALESCE(mean_use,0), COALESCE(ewma,0), samples, empty_samples, COALESCE(baseline,'')`

type rowScanner interface{ Scan(dest ...any) error }

func scanLease(sc rowScanner) (leaseRow, error) {
	var r leaseRow
	var overrun, wouldWait int
	err := sc.Scan(&r.ID, &r.ClientID, &r.State, &r.Kind, &r.Weight, &r.SessionID, &r.HolderPID, &r.HolderStart,
		&r.Scope, &r.ToolUseID, &r.CreatedAt, &r.DeadlineAt, &r.LeaseUntil, &r.GrantedAt, &r.EndedAt,
		&overrun, &wouldWait, &r.EndReason, &r.WaitedMS,
		&r.PeakUse, &r.MeanUse, &r.EWMA, &r.Samples, &r.EmptySamples, &r.Baseline)
	r.Overrun, r.WouldWait = overrun != 0, wouldWait != 0
	return r, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Create inserts a waiting row, idempotent on ClientID: a second create with
// the same client id inserts nothing and returns the existing row with
// created false, whatever state it has reached.
func (s *leaseStore) Create(r leaseRow) (leaseRow, bool, error) {
	res, err := s.db.Exec(`
		INSERT INTO resource_leases (id, client_id, state, kind, weight, session_id, holder_pid, holder_start,
			scope, tool_use_id, created_at, deadline_at, lease_until, baseline)
		VALUES (?, ?, 'waiting', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (client_id) DO NOTHING`,
		r.ID, r.ClientID, r.Kind, r.Weight, r.SessionID, r.HolderPID, r.HolderStart,
		r.Scope, r.ToolUseID, r.CreatedAt, r.DeadlineAt, r.LeaseUntil, nullable(r.Baseline))
	if err != nil {
		return leaseRow{}, false, fmt.Errorf("create lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return leaseRow{}, false, fmt.Errorf("create lease rows affected: %w", err)
	}
	row, ok, err := s.GetByClientID(r.ClientID)
	if err != nil {
		return leaseRow{}, false, err
	}
	if !ok {
		return leaseRow{}, false, errors.New("create lease: row vanished after insert")
	}
	return row, n == 1, nil
}

// Get returns the row of id; ok is false for an unknown id.
func (s *leaseStore) Get(id string) (leaseRow, bool, error) {
	return s.one(`SELECT `+leaseCols+` FROM resource_leases WHERE id = ?`, id)
}

// GetByClientID returns the row the client id created.
func (s *leaseStore) GetByClientID(clientID string) (leaseRow, bool, error) {
	return s.one(`SELECT `+leaseCols+` FROM resource_leases WHERE client_id = ?`, clientID)
}

func (s *leaseStore) one(query string, arg any) (leaseRow, bool, error) {
	r, err := scanLease(s.db.QueryRow(query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return leaseRow{}, false, nil
	}
	if err != nil {
		return leaseRow{}, false, fmt.Errorf("read lease: %w", err)
	}
	return r, true, nil
}

// RenewLease moves a waiting row's lease forward to until (never back). A
// held, ended or unknown row is left alone and is not an error.
func (s *leaseStore) RenewLease(id string, until int64) error {
	if _, err := s.db.Exec(`
		UPDATE resource_leases SET lease_until = MAX(lease_until, ?)
		WHERE id = ? AND state = 'waiting'`, until, id); err != nil {
		return fmt.Errorf("renew lease %s: %w", id, err)
	}
	return nil
}

// Grant moves a waiting row to held at now (compare-and-set on state). It
// reports whether this call did it: a row that was ended, or granted by
// someone else, answers false.
func (s *leaseStore) Grant(id string, now int64, overrun, wouldWait bool) (bool, error) {
	return s.changed(`
		UPDATE resource_leases
		SET state = 'held', granted_at = ?, overrun = ?, would_wait = ?, waited_ms = ? - created_at
		WHERE id = ? AND state = 'waiting'`, now, b2i(overrun), b2i(wouldWait), now, id)
}

// End moves a waiting or held row to ended with the reason (compare-and-set
// on state != ended). A row that never waited long enough to record it gets
// its waited_ms here.
func (s *leaseStore) End(id, reason string, now int64) (bool, error) {
	return s.changed(`
		UPDATE resource_leases
		SET state = 'ended', ended_at = ?, end_reason = ?, waited_ms = COALESCE(waited_ms, ? - created_at)
		WHERE id = ? AND state != 'ended'`, now, reason, now, id)
}

// CloseIfExpired is the sweeper's close of a waiting row nobody polls any
// more: End(abandoned) whose UPDATE also requires lease_until <= now, in the
// same statement, so a poll that renewed the lease between the sweeper's read
// and this close wins.
func (s *leaseStore) CloseIfExpired(id string, now int64) (bool, error) {
	return s.changed(`
		UPDATE resource_leases
		SET state = 'ended', ended_at = ?, end_reason = 'abandoned', waited_ms = COALESCE(waited_ms, ? - created_at)
		WHERE id = ? AND state = 'waiting' AND lease_until <= ?`, now, now, id, now)
}

// ExtendWaiting is the boot grace: every waiting row's lease becomes
// max(lease_until, until). It returns how many rows changed.
func (s *leaseStore) ExtendWaiting(until int64) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE resource_leases SET lease_until = ?
		WHERE state = 'waiting' AND lease_until < ?`, until, until)
	if err != nil {
		return 0, fmt.Errorf("extend waiting leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("extend waiting leases rows affected: %w", err)
	}
	return n, nil
}

// UpdateUse stores a held row's running measurements, so that a restart
// resumes the charge instead of warming up again.
func (s *leaseStore) UpdateUse(id string, ewma, peak, mean float64, samples, emptySamples int) error {
	if _, err := s.db.Exec(`
		UPDATE resource_leases SET ewma = ?, peak_use = ?, mean_use = ?, samples = ?, empty_samples = ?
		WHERE id = ? AND state = 'held'`, ewma, peak, mean, samples, emptySamples, id); err != nil {
		return fmt.Errorf("update lease use %s: %w", id, err)
	}
	return nil
}

// Active returns the held rows, oldest grant first.
func (s *leaseStore) Active() ([]leaseRow, error) {
	return s.many(`SELECT ` + leaseCols + ` FROM resource_leases WHERE state = 'held' ORDER BY granted_at, id`)
}

// Waiting returns the waiting rows in queue order (created_at, id).
func (s *leaseStore) Waiting() ([]leaseRow, error) {
	return s.many(`SELECT ` + leaseCols + ` FROM resource_leases WHERE state = 'waiting' ORDER BY created_at, id`)
}

// Recent returns up to n rows that ended at or after since, newest first.
func (s *leaseStore) Recent(n int, since int64) ([]leaseRow, error) {
	return s.many(`SELECT `+leaseCols+` FROM resource_leases
		WHERE state = 'ended' AND ended_at >= ? ORDER BY ended_at DESC, id LIMIT ?`, since, n)
}

// Prune deletes ended rows that ended before the cutoff and returns how many.
func (s *leaseStore) Prune(before int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM resource_leases WHERE state = 'ended' AND ended_at < ?`, before)
	if err != nil {
		return 0, fmt.Errorf("prune leases: %w", err)
	}
	return res.RowsAffected()
}

func (s *leaseStore) many(query string, args ...any) ([]leaseRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list leases: %w", err)
	}
	defer rows.Close()
	out := []leaseRow{}
	for rows.Next() {
		r, err := scanLease(rows)
		if err != nil {
			return nil, fmt.Errorf("list leases: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list leases: %w", err)
	}
	return out, nil
}

// changed runs one guarded UPDATE and reports whether it changed a row.
func (s *leaseStore) changed(query string, args ...any) (bool, error) {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return false, fmt.Errorf("update lease: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update lease rows affected: %w", err)
	}
	return n == 1, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
