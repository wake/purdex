package resourcesmod

import (
	"database/sql"
	"errors"
	"fmt"
)

// positionRow is one row of resource_positions (amendment A §2.1): one
// session's place in the queue and, from its turn on, its hold on the host.
// Times are unix milliseconds.
type positionRow struct {
	ID         string
	SessionID  string
	AgentPID   int
	AgentStart string
	State      string // queued | offered | running | idle | ended

	CreatedAt      int64
	RequeuedFrom   string
	OfferedAt      int64
	DeliveredAt    int64
	PassedAt       int64
	OfferPasses    int
	FirstStartedAt int64
	WindowUntil    int64

	CommandsStarted int
	CommandsEnded   int
	Warned          bool
	Extensions      int
	DoneRequested   bool

	NoticeKind   string
	NoticeDueAt  int64
	NoticeSentAt int64
	NoticeTries  int
	NoticeResult string

	OfferWaitMS int64
	IdleWaitMS  int64
	OfferZone   string
	OfferTrend  string
	WaitReasons string

	EndReason string
	EndedAt   int64
}

// positionsSchema is the amendment A table. One live position per session is
// enforced by the unique partial index (V15), not by code.
const positionsSchema = `
	CREATE TABLE IF NOT EXISTS resource_positions (
		id               TEXT PRIMARY KEY,
		session_id       TEXT    NOT NULL,
		agent_pid        INTEGER NOT NULL DEFAULT 0,
		agent_start      TEXT    NOT NULL DEFAULT '',
		state            TEXT    NOT NULL CHECK (state IN ('queued','offered','running','idle','ended')),
		created_at       INTEGER NOT NULL,
		requeued_from    TEXT    NOT NULL DEFAULT '',
		offered_at       INTEGER NOT NULL DEFAULT 0,
		delivered_at     INTEGER NOT NULL DEFAULT 0,
		passed_at        INTEGER NOT NULL DEFAULT 0,
		offer_passes     INTEGER NOT NULL DEFAULT 0,
		first_started_at INTEGER NOT NULL DEFAULT 0,
		window_until     INTEGER NOT NULL DEFAULT 0,
		commands_started INTEGER NOT NULL DEFAULT 0,
		commands_ended   INTEGER NOT NULL DEFAULT 0,
		warned           INTEGER NOT NULL DEFAULT 0,
		extensions       INTEGER NOT NULL DEFAULT 0,
		done_requested   INTEGER NOT NULL DEFAULT 0,
		notice_kind      TEXT    NOT NULL DEFAULT '',
		notice_due_at    INTEGER NOT NULL DEFAULT 0,
		notice_sent_at   INTEGER NOT NULL DEFAULT 0,
		notice_tries     INTEGER NOT NULL DEFAULT 0,
		notice_result    TEXT    NOT NULL DEFAULT '',
		offer_wait_ms    INTEGER NOT NULL DEFAULT 0,
		idle_wait_ms     INTEGER NOT NULL DEFAULT 0,
		offer_zone       TEXT    NOT NULL DEFAULT '',
		offer_trend      TEXT    NOT NULL DEFAULT '',
		wait_reasons     TEXT    NOT NULL DEFAULT '',
		end_reason       TEXT    NOT NULL DEFAULT '',
		ended_at         INTEGER NOT NULL DEFAULT 0
	);
	CREATE UNIQUE INDEX IF NOT EXISTS resource_positions_live ON resource_positions (session_id) WHERE state != 'ended';
	CREATE INDEX IF NOT EXISTS resource_positions_state ON resource_positions (state);`

const positionCols = `id, session_id, agent_pid, agent_start, state, created_at, requeued_from, offered_at, delivered_at,
	passed_at, offer_passes, first_started_at, window_until, commands_started, commands_ended, warned, extensions,
	done_requested, notice_kind, notice_due_at, notice_sent_at, notice_tries, notice_result, offer_wait_ms,
	idle_wait_ms, offer_zone, offer_trend, wait_reasons, end_reason, ended_at`

func scanPosition(sc rowScanner) (positionRow, error) {
	var p positionRow
	var warned, done int
	err := sc.Scan(&p.ID, &p.SessionID, &p.AgentPID, &p.AgentStart, &p.State, &p.CreatedAt, &p.RequeuedFrom,
		&p.OfferedAt, &p.DeliveredAt, &p.PassedAt, &p.OfferPasses, &p.FirstStartedAt, &p.WindowUntil,
		&p.CommandsStarted, &p.CommandsEnded, &warned, &p.Extensions, &done, &p.NoticeKind, &p.NoticeDueAt,
		&p.NoticeSentAt, &p.NoticeTries, &p.NoticeResult, &p.OfferWaitMS, &p.IdleWaitMS, &p.OfferZone,
		&p.OfferTrend, &p.WaitReasons, &p.EndReason, &p.EndedAt)
	p.Warned, p.DoneRequested = warned != 0, done != 0
	return p, err
}

// insertPosition inserts a position row; a second live position for the same
// session fails on the unique index. Nothing calls it outside tests until
// the transitions of P2b′-1b.
func (s *leaseStore) insertPosition(p positionRow) error {
	_, err := s.db.Exec(`
		INSERT INTO resource_positions (id, session_id, agent_pid, agent_start, state, created_at, requeued_from, offered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.SessionID, p.AgentPID, p.AgentStart, p.State, p.CreatedAt, p.RequeuedFrom, p.OfferedAt)
	if err != nil {
		return fmt.Errorf("insert position: %w", err)
	}
	return nil
}

// LivePositionOf is the session's position that has not ended, if any.
func (s *leaseStore) LivePositionOf(session string) (positionRow, bool, error) {
	return s.onePosition(`SELECT `+positionCols+` FROM resource_positions WHERE session_id = ? AND state != 'ended'`, session)
}

// PositionByID is the position with the id, whatever state it is in.
func (s *leaseStore) PositionByID(id string) (positionRow, bool, error) {
	return s.onePosition(`SELECT `+positionCols+` FROM resource_positions WHERE id = ?`, id)
}

func (s *leaseStore) onePosition(query string, arg any) (positionRow, bool, error) {
	p, err := scanPosition(s.db.QueryRow(query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return positionRow{}, false, nil
	}
	if err != nil {
		return positionRow{}, false, fmt.Errorf("read position: %w", err)
	}
	return p, true, nil
}

// LivePositions are all positions that have not ended, oldest first.
func (s *leaseStore) LivePositions() ([]positionRow, error) {
	rows, err := s.db.Query(`SELECT ` + positionCols + ` FROM resource_positions WHERE state != 'ended' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	defer rows.Close()
	var out []positionRow
	for rows.Next() {
		p, err := scanPosition(rows)
		if err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RowsOf are the lease rows attached to a position that have not ended,
// oldest first.
func (s *leaseStore) RowsOf(positionID string) ([]leaseRow, error) {
	return s.many(`SELECT `+leaseCols+` FROM resource_leases
		WHERE position_id = ? AND state != 'ended' ORDER BY created_at, id`, positionID)
}

// MaxOfferedAt is the newest offer of any position (0 when none): with
// MaxGrantedAt it is where the gap is counted from after a restart.
func (s *leaseStore) MaxOfferedAt() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(offered_at), 0) FROM resource_positions`).Scan(&n); err != nil {
		return 0, fmt.Errorf("max offered_at: %w", err)
	}
	return n, nil
}

// SetQueuedReason records why a queue-mode row did not run (queue, own_running).
func (s *leaseStore) SetQueuedReason(id, reason string) error {
	_, err := s.db.Exec(`UPDATE resource_leases SET queued_reason = ? WHERE id = ? AND state = 'waiting'`, reason, id)
	return err
}

// ConfirmRow records that the session said yes to a background command whose turn
// came.
func (s *leaseStore) ConfirmRow(id string, now int64) error {
	_, err := s.db.Exec(`UPDATE resource_leases SET confirmed_at = ? WHERE id = ? AND state = 'waiting' AND mode = 'ask'`, now, id)
	return err
}
