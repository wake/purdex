package resourcesmod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/resources"
)

// dbx is what a statement runs on: the database or a transaction.
type dbx interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// errLostCAS aborts a combined operation whose compare-and-set lost: the
// transaction rolls back and the operation reports "not done" without an error.
var errLostCAS = errors.New("lost compare-and-set")

// tx runs fn in one transaction. It commits when fn returns nil, rolls back on
// any error, and reports false with a nil error for errLostCAS. stateMu still
// serialises every writer in the daemon; the transaction makes a pair of
// statements atomic on disk, so a crash between them leaves neither.
func (s *leaseStore) tx(fn func(x *sql.Tx) error) (bool, error) {
	t, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	if err := fn(t); err != nil {
		_ = t.Rollback()
		if errors.Is(err, errLostCAS) {
			return false, nil
		}
		return false, err
	}
	if err := t.Commit(); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// mid is the test seam between the statements of a combined operation.
func (s *leaseStore) mid() error {
	if s.afterFirstStmt != nil {
		return s.afterFirstStmt()
	}
	return nil
}

// run executes one statement and returns the rows it changed.
func run(x dbx, query string, args ...any) (int64, error) {
	res, err := x.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// mustChange is run for a compare-and-set that has to win: no change is errLostCAS.
func mustChange(x dbx, query string, args ...any) error {
	n, err := run(x, query, args...)
	if err != nil {
		return err
	}
	if n == 0 {
		return errLostCAS
	}
	return nil
}

// endWaitingRowsOf ends the waiting rows of a position with the reason; where
// is an extra condition on the rows ("" for all of them).
func endWaitingRowsOf(x dbx, positionID, reason string, now int64, where string) (int64, error) {
	return run(x, `UPDATE resource_leases
		SET state = 'ended', ended_at = ?, end_reason = ?, waited_ms = COALESCE(waited_ms, ? - created_at)
		WHERE position_id = ? AND state = 'waiting'`+where, now, reason, now, positionID)
}

// armSlot arms the notice slot of a position with a fresh pending notice.
func armSlot(x dbx, positionID, kind string, now int64) error {
	_, err := x.Exec(`UPDATE resource_positions
		SET notice_kind = ?, notice_due_at = ?, notice_sent_at = 0, notice_tries = 0, notice_result = ''
		WHERE id = ?`, kind, now, positionID)
	return err
}

// CreateAttached is P1: the session's live position is created, or the one
// that already exists is taken, and the row is inserted attached to it, in one
// transaction. p is the position to create when the session has none. A row
// whose client id exists already is not inserted (a replay): created is false
// and no position is left behind.
func (s *leaseStore) CreateAttached(p positionRow, r leaseRow) (row leaseRow, pos positionRow, created bool, err error) {
	committed, err := s.tx(func(x *sql.Tx) error {
		n, err := run(x, `INSERT INTO resource_positions (id, session_id, agent_pid, agent_start, state, created_at, requeued_from)
			VALUES (?, ?, ?, ?, 'queued', ?, ?) ON CONFLICT DO NOTHING`,
			p.ID, p.SessionID, p.AgentPID, p.AgentStart, p.CreatedAt, p.RequeuedFrom)
		if err != nil {
			return fmt.Errorf("create position: %w", err)
		}
		fresh := n == 1
		if pos, err = scanPosition(x.QueryRow(`SELECT `+positionCols+` FROM resource_positions
			WHERE session_id = ? AND state != 'ended'`, p.SessionID)); err != nil {
			return fmt.Errorf("read the live position: %w", err)
		}
		if err := s.mid(); err != nil {
			return err
		}
		r.PositionID = pos.ID
		if created, err = insertLeaseRow(x, r); err != nil {
			return err
		}
		if !created && fresh {
			return errLostCAS // a replay must not leave an empty queued position
		}
		return nil
	})
	if err != nil {
		return leaseRow{}, positionRow{}, false, err
	}
	if !committed {
		created = false
	}
	row, ok, err := s.GetByClientID(r.ClientID)
	if err != nil {
		return leaseRow{}, positionRow{}, false, err
	}
	if !ok {
		return leaseRow{}, positionRow{}, false, errors.New("create attached: row vanished")
	}
	if pos, ok, err = s.PositionByID(row.PositionID); err != nil || !ok {
		if err == nil {
			err = errors.New("create attached: position vanished")
		}
		return leaseRow{}, positionRow{}, false, err
	}
	return row, pos, created, nil
}

// OfferPosition is P2: a queued position becomes offered (the grant decision:
// the gap starts at now), its waiting rows are named and the notice slot
// `offer` is armed, in one transaction. It reports whether this call did it.
func (s *leaseStore) OfferPosition(id string, now int64, zone, trend, waitReasons string) (bool, error) {
	return s.tx(func(x *sql.Tx) error {
		if err := mustChange(x, `UPDATE resource_positions
			SET state = 'offered', offered_at = ?, delivered_at = 0, window_until = 0, passed_at = 0,
				offer_zone = ?, offer_trend = ?, wait_reasons = COALESCE(NULLIF(?, ''), wait_reasons),
				notice_kind = 'offer', notice_due_at = ?, notice_sent_at = 0, notice_tries = 0, notice_result = ''
			WHERE id = ? AND state = 'queued'`, now, zone, trend, waitReasons, now, id); err != nil {
			return err
		}
		if err := s.mid(); err != nil {
			return err
		}
		_, err := x.Exec(`UPDATE resource_leases SET offered_at = ? WHERE position_id = ? AND state = 'waiting'`, now, id)
		return err
	})
}

// GrantInPosition is P3 / P4 / P5: a command of the position starts. from is the
// state the position must be in (offered, idle or running); the row is granted
// with its decision record in the same transaction. A running position only
// counts the command; the others become running (first_started_at keeps the
// first start).
func (s *leaseStore) GrantInPosition(positionID, from, rowID string, now int64, wouldWait bool,
	d resources.Decision, v2 resources.V2Record, waitReasons, baseline string) (bool, error) {
	return s.tx(func(x *sql.Tx) error {
		switch from {
		case resources.PosRunning:
			if err := mustChange(x, `UPDATE resource_positions SET commands_started = commands_started + 1
				WHERE id = ? AND state = 'running'`, positionID); err != nil {
				return err
			}
		case resources.PosOffered, resources.PosIdle:
			if err := mustChange(x, `UPDATE resource_positions
				SET state = 'running', first_started_at = COALESCE(NULLIF(first_started_at, 0), ?), window_until = 0,
					commands_started = commands_started + 1
				WHERE id = ? AND state = ?`, now, positionID, from); err != nil {
				return err
			}
		default:
			return fmt.Errorf("grant in position: cannot start a command from %q", from)
		}
		if err := s.mid(); err != nil {
			return err
		}
		ok, err := grantDecidedOn(x, rowID, now, wouldWait, d, v2, waitReasons, baseline)
		if err != nil {
			return err
		}
		if !ok {
			return errLostCAS
		}
		return nil
	})
}

// commandOutcome is what the end of a command does to its position.
type commandOutcome int

const (
	// outcomeCount: another held row remains, only commands_ended counts.
	outcomeCount commandOutcome = iota
	// outcomeIdle: P6, the position stays in a window.
	outcomeIdle
	// outcomeRelease: P7, the position ends after its limit.
	outcomeRelease
	// outcomeEnd: P8, the position ends with PosReason (done, abandoned).
	outcomeEnd
)

// commandEnd describes the end of one command of a position. The module
// decides the outcome (AfterCommandEnd) and passes it in.
type commandEnd struct {
	Reason     string // how the row ends
	Outcome    commandOutcome
	PositionID string // "" ends the row alone (a row with no position, R5)

	// outcomeIdle
	WindowUntil int64
	Warned      bool
	NewWarning  bool   // an extension was given: extensions += 1
	Notice      string // the final notice text, stored on the row

	// outcomeEnd
	PosReason string
	// outcomeRelease: the id of the position the ask rows move to (used only
	// when there are any).
	NewPositionID string
}

// commandEndResult reports an EndCommand.
type commandEndResult struct {
	Ended         bool
	NewPositionID string // the position the ask rows were moved to (P7)
}

// EndCommand ends a held or waiting row and applies the outcome to its
// position (P6 / P7 / P8), in one transaction.
func (s *leaseStore) EndCommand(rowID string, now int64, e commandEnd) (commandEndResult, error) {
	var out commandEndResult
	committed, err := s.tx(func(x *sql.Tx) error {
		if err := mustChange(x, `UPDATE resource_leases
			SET state = 'ended', ended_at = ?, end_reason = ?, waited_ms = COALESCE(waited_ms, ? - created_at)
			WHERE id = ? AND state != 'ended'`, now, e.Reason, now, rowID); err != nil {
			return err
		}
		if e.PositionID == "" {
			return nil
		}
		if err := s.mid(); err != nil {
			return err
		}
		switch e.Outcome {
		case outcomeCount:
			_, err := x.Exec(`UPDATE resource_positions SET commands_ended = commands_ended + 1 WHERE id = ? AND state = 'running'`, e.PositionID)
			return err
		case outcomeIdle:
			if err := mustChange(x, `UPDATE resource_positions
				SET state = 'idle', commands_ended = commands_ended + 1, window_until = ?, warned = ?, extensions = extensions + ?
				WHERE id = ? AND state = 'running'`, e.WindowUntil, b2i(e.Warned), b2i(e.NewWarning), e.PositionID); err != nil {
				return err
			}
			if e.Notice != "" {
				if _, err := x.Exec(`UPDATE resource_leases SET notice = ? WHERE id = ?`, e.Notice, rowID); err != nil {
					return err
				}
			}
			named, err := run(x, `UPDATE resource_leases SET offered_at = ?
				WHERE position_id = ? AND state = 'waiting' AND offered_at = 0`, now, e.PositionID)
			if err != nil {
				return err
			}
			if named > 0 {
				return armSlot(x, e.PositionID, "idle", now)
			}
			return nil
		case outcomeRelease:
			var session, agentStart string
			var agentPID int
			if err := x.QueryRow(`SELECT session_id, agent_pid, agent_start FROM resource_positions WHERE id = ?`,
				e.PositionID).Scan(&session, &agentPID, &agentStart); err != nil {
				return err
			}
			if err := mustChange(x, `UPDATE resource_positions
				SET state = 'ended', commands_ended = commands_ended + 1, end_reason = ?, ended_at = ?
				WHERE id = ? AND state = 'running'`, resources.EndReleasedAfterLimit, now, e.PositionID); err != nil {
				return err
			}
			if _, err := endWaitingRowsOf(x, e.PositionID, resources.EndReleasedAfterLimit, now, ` AND mode != 'ask'`); err != nil {
				return err
			}
			var asks int
			if err := x.QueryRow(`SELECT COUNT(*) FROM resource_leases WHERE position_id = ? AND state = 'waiting' AND mode = 'ask'`,
				e.PositionID).Scan(&asks); err != nil {
				return err
			}
			if asks == 0 {
				return nil
			}
			if _, err := x.Exec(`INSERT INTO resource_positions (id, session_id, agent_pid, agent_start, state, created_at, requeued_from)
				VALUES (?, ?, ?, ?, 'queued', ?, ?)`, e.NewPositionID, session, agentPID, agentStart, now, e.PositionID); err != nil {
				return err
			}
			if _, err := x.Exec(`UPDATE resource_leases SET position_id = ?, offered_at = 0, confirmed_at = 0
				WHERE position_id = ? AND state = 'waiting' AND mode = 'ask'`, e.NewPositionID, e.PositionID); err != nil {
				return err
			}
			out.NewPositionID = e.NewPositionID
			return nil
		case outcomeEnd:
			if err := mustChange(x, `UPDATE resource_positions
				SET state = 'ended', commands_ended = commands_ended + 1, end_reason = ?, ended_at = ?
				WHERE id = ? AND state = 'running'`, e.PosReason, now, e.PositionID); err != nil {
				return err
			}
			_, err := endWaitingRowsOf(x, e.PositionID, e.PosReason, now, "")
			return err
		}
		return fmt.Errorf("end command: unknown outcome %d", e.Outcome)
	})
	if err != nil {
		return commandEndResult{}, err
	}
	out.Ended = committed
	if !committed {
		out.NewPositionID = ""
	}
	return out, nil
}
