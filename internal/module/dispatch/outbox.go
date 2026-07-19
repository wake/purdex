package dispatch

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// The report outbox is a standalone durable table (codex Q2) — not folded into
// the execution row — so the sender can scan for unacked reports, keep a
// per-execution ack cursor, and hold multiple pending lifecycle reports per
// execution cleanly (spec §3.3). Each report is stored with its full,
// ready-to-POST JSON payload; replay after a daemon restart reads straight from
// here. The accepted(seq=1) report is additionally reconstructable from the
// execution row (the durability cut) when it never made it into the outbox.

// OutboxRecord is one durably-queued E4 report awaiting ack.
type OutboxRecord struct {
	ID            int64
	ExecutionID   string
	DispatchID    string
	Seq           int
	Status        string // accepted / running / completed / failed
	Payload       []byte // full E4 request JSON, ready to POST
	Attempts      int
	NextAttemptAt int64 // unix seconds; 0 = due now
	Acked         bool
	Permanent     bool // 401/403/schema — never retried
	LastError     string
	CreatedAt     int64
	UpdatedAt     int64
}

// Outbox is the SQLite-backed durable report queue + per-execution ack cursor.
type Outbox struct {
	db  *sql.DB
	now func() int64
}

// OpenOutbox opens (or creates) an Outbox at path and runs schema migration. Use
// ":memory:" for tests.
func OpenOutbox(path string) (*Outbox, error) {
	dsn := path
	if path != ":memory:" {
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(500)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open outbox db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(2)
	}
	o := &Outbox{db: db, now: func() int64 { return time.Now().Unix() }}
	if err := o.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate outbox db: %w", err)
	}
	return o, nil
}

// Close closes the underlying DB connection.
func (o *Outbox) Close() error { return o.db.Close() }

// migrate creates the outbox + cursor tables. No foreign keys (avoids the #850
// DSN-pragma footgun). UNIQUE(execution_id, seq) makes Enqueue idempotent so a
// replayed reconstruction can never double-insert the same report.
func (o *Outbox) migrate() error {
	_, err := o.db.Exec(`
		CREATE TABLE IF NOT EXISTS report_outbox (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			execution_id    TEXT    NOT NULL,
			dispatch_id     TEXT    NOT NULL,
			seq             INTEGER NOT NULL,
			status          TEXT    NOT NULL,
			payload         BLOB    NOT NULL,
			attempts        INTEGER NOT NULL DEFAULT 0,
			next_attempt_at INTEGER NOT NULL DEFAULT 0,
			acked           INTEGER NOT NULL DEFAULT 0,
			permanent       INTEGER NOT NULL DEFAULT 0,
			last_error      TEXT    NOT NULL DEFAULT '',
			created_at      INTEGER NOT NULL DEFAULT 0,
			updated_at      INTEGER NOT NULL DEFAULT 0,
			UNIQUE(execution_id, seq)
		);
		CREATE TABLE IF NOT EXISTS report_cursor (
			execution_id TEXT    PRIMARY KEY,
			ack_seq      INTEGER NOT NULL DEFAULT 0,
			updated_at   INTEGER NOT NULL DEFAULT 0
		);
	`)
	return err
}

// Enqueue durably records one report. It is idempotent on (execution_id, seq):
// a second enqueue of the same key is a no-op and returns inserted=false, so a
// reconstructed accepted never clobbers a still-queued one. The row's mutable
// send bookkeeping (attempts/acked/next_attempt_at) is left to the caller-facing
// Mark* methods.
func (o *Outbox) Enqueue(rec OutboxRecord) (inserted bool, err error) {
	if rec.ExecutionID == "" || rec.DispatchID == "" || rec.Seq < 1 || len(rec.Payload) == 0 {
		return false, fmt.Errorf("outbox enqueue: invalid record %+v", rec)
	}
	now := o.now()
	res, err := o.db.Exec(`
		INSERT OR IGNORE INTO report_outbox (
			execution_id, dispatch_id, seq, status, payload,
			attempts, next_attempt_at, acked, permanent, last_error,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 0, 0, 0, 0, '', ?, ?)`,
		rec.ExecutionID, rec.DispatchID, rec.Seq, rec.Status, rec.Payload, now, now,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RecordBySeq returns the queued report for (execID, seq). found is false (no
// error) when absent.
func (o *Outbox) RecordBySeq(execID string, seq int) (OutboxRecord, bool, error) {
	return o.scanOne(`SELECT `+outboxCols+` FROM report_outbox WHERE execution_id = ? AND seq = ?`, execID, seq)
}

// UnackedRecords returns every not-yet-acked, non-permanent report for execID,
// ordered by seq ascending (send order). It ignores next_attempt_at — the sender
// decides which are due — so stale reports (seq ≤ ack cursor) can still be
// reconciled/acked out of a backoff window.
func (o *Outbox) UnackedRecords(execID string) ([]OutboxRecord, error) {
	rows, err := o.db.Query(
		`SELECT `+outboxCols+` FROM report_outbox
		 WHERE execution_id = ? AND acked = 0 AND permanent = 0
		 ORDER BY seq ASC`, execID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxRecord
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DueExecutions returns the distinct execution_ids that have at least one
// unacked, non-permanent report whose next_attempt_at has arrived. This is the
// work list the sender flushes each cycle.
func (o *Outbox) DueExecutions(now int64) ([]string, error) {
	rows, err := o.db.Query(
		`SELECT DISTINCT execution_id FROM report_outbox
		 WHERE acked = 0 AND permanent = 0 AND next_attempt_at <= ?
		 ORDER BY execution_id ASC`, now)
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
}

// MarkAcked flags a report as projected (acked); it is never re-sent.
func (o *Outbox) MarkAcked(id int64) error {
	_, err := o.db.Exec(
		`UPDATE report_outbox SET acked = 1, updated_at = ? WHERE id = ?`, o.now(), id)
	return err
}

// MarkAttempt records a transient (retryable) send failure: bumps attempts,
// pushes next_attempt_at into the future (backoff), and stores the error.
func (o *Outbox) MarkAttempt(id, nextAttemptAt int64, errMsg string) error {
	_, err := o.db.Exec(
		`UPDATE report_outbox
		 SET attempts = attempts + 1, next_attempt_at = ?, last_error = ?, updated_at = ?
		 WHERE id = ?`, nextAttemptAt, errMsg, o.now(), id)
	return err
}

// MarkPermanent flags a report as permanently failed (401/403/schema): it is
// excluded from all future send attempts.
func (o *Outbox) MarkPermanent(id int64, errMsg string) error {
	_, err := o.db.Exec(
		`UPDATE report_outbox
		 SET permanent = 1, attempts = attempts + 1, last_error = ?, updated_at = ?
		 WHERE id = ?`, errMsg, o.now(), id)
	return err
}

// Cursor returns the highest seq Ploom has acked for execID (0 if none). A report
// with seq ≤ cursor is already projected and must not be re-sent.
func (o *Outbox) Cursor(execID string) (int, error) {
	var ackSeq int
	err := o.db.QueryRow(
		`SELECT ack_seq FROM report_cursor WHERE execution_id = ?`, execID).Scan(&ackSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return ackSeq, nil
}

// AdvanceCursor moves the ack cursor forward to max(existing, ackSeq). It never
// moves backwards, so out-of-order/stale acks cannot regress the cursor.
func (o *Outbox) AdvanceCursor(execID string, ackSeq int) error {
	_, err := o.db.Exec(`
		INSERT INTO report_cursor (execution_id, ack_seq, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(execution_id) DO UPDATE SET
			ack_seq = MAX(ack_seq, excluded.ack_seq),
			updated_at = excluded.updated_at`,
		execID, ackSeq, o.now())
	return err
}

// outboxCols is the ordered column list scanRecord expects.
const outboxCols = `id, execution_id, dispatch_id, seq, status, payload,
	attempts, next_attempt_at, acked, permanent, last_error, created_at, updated_at`

func (o *Outbox) scanOne(query string, args ...any) (OutboxRecord, bool, error) {
	rows, err := o.db.Query(query, args...)
	if err != nil {
		return OutboxRecord{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return OutboxRecord{}, false, rows.Err()
	}
	rec, err := scanRecord(rows)
	if err != nil {
		return OutboxRecord{}, false, err
	}
	return rec, true, nil
}

// scanner is satisfied by *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(s scanner) (OutboxRecord, error) {
	var (
		rec         OutboxRecord
		acked, perm int64
	)
	err := s.Scan(
		&rec.ID, &rec.ExecutionID, &rec.DispatchID, &rec.Seq, &rec.Status, &rec.Payload,
		&rec.Attempts, &rec.NextAttemptAt, &acked, &perm, &rec.LastError,
		&rec.CreatedAt, &rec.UpdatedAt,
	)
	if err != nil {
		return OutboxRecord{}, err
	}
	rec.Acked = acked != 0
	rec.Permanent = perm != 0
	return rec, nil
}
