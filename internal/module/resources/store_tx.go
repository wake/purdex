package resourcesmod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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
		// The row must be a waiting row of this very position: a stale or
		// mismatched pair must not start one position and grant another's row.
		var attached int
		if err := x.QueryRow(`SELECT COUNT(*) FROM resource_leases WHERE id = ? AND position_id = ? AND state = 'waiting'`,
			rowID, positionID).Scan(&attached); err != nil {
			return err
		}
		if attached == 0 {
			return errLostCAS
		}
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

	// RowGuard is an extra condition on the row's end, with its arguments (the
	// sweeper's vanished check: the tree must still be counted empty).
	RowGuard     string
	RowGuardArgs []any

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
		// With a position, only a held row of that very position ends a
		// command: a waiting row, or a row of another position, must not move
		// this one. Without one, the row must have no live position (R5): a row
		// whose position is live is ended through it.
		where, args := ` AND state != 'ended' AND (position_id = '' OR NOT EXISTS (
			SELECT 1 FROM resource_positions p WHERE p.id = resource_leases.position_id AND p.state != 'ended'))`,
			[]any{now, e.Reason, now, rowID}
		if e.PositionID != "" {
			where, args = ` AND state = 'held' AND position_id = ?`, append(args, e.PositionID)
		}
		where, args = where+e.RowGuard, append(args, e.RowGuardArgs...)
		if err := mustChange(x, `UPDATE resource_leases
			SET state = 'ended', ended_at = ?, end_reason = ?, waited_ms = COALESCE(waited_ms, ? - created_at)
			WHERE id = ?`+where, args...); err != nil {
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
			return mustChange(x, `UPDATE resource_positions SET commands_ended = commands_ended + 1 WHERE id = ? AND state = 'running'`, e.PositionID)
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

// endPosition describes the end of a whole position (P9 – P13, P16).
type endPosition struct {
	Reason string
	// AskedReason, when set, is the end reason of the ask rows that were asked
	// (named); the other rows end with Reason (P10).
	AskedReason string
	// From lists the states the position may be in.
	From []string
	// WindowGuard, when positive, requires window_until to still be this value:
	// a command that started meanwhile has cleared it and wins (P9, P10).
	WindowGuard int64
	// Notice arms the notice slot with this kind ("cancelled", "drained"), or
	// leaves it alone when empty.
	Notice string
}

// EndPositionWithRows ends a position and its waiting rows in one transaction.
func (s *leaseStore) EndPositionWithRows(id string, now int64, o endPosition) (bool, error) {
	if len(o.From) == 0 {
		return false, errors.New("end position: no source states")
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(o.From)), ",")
	args := []any{o.Reason, now, id}
	for _, f := range o.From {
		args = append(args, f)
	}
	where := ` AND state IN (` + marks + `)`
	if o.WindowGuard > 0 {
		where += ` AND window_until = ?`
		args = append(args, o.WindowGuard)
	}
	return s.tx(func(x *sql.Tx) error {
		if err := mustChange(x, `UPDATE resource_positions SET state = 'ended', end_reason = ?, ended_at = ?
			WHERE id = ?`+where, args...); err != nil {
			return err
		}
		if err := s.mid(); err != nil {
			return err
		}
		if o.Notice != "" {
			if err := armSlot(x, id, o.Notice, now); err != nil {
				return err
			}
		}
		if o.AskedReason != "" {
			if _, err := endWaitingRowsOf(x, id, o.AskedReason, now, ` AND mode = 'ask' AND offered_at > 0`); err != nil {
				return err
			}
		}
		_, err := endWaitingRowsOf(x, id, o.Reason, now, "")
		return err
	})
}

// EndRowMaybePosition is P14: a waiting row ends (client cancel, abandoned ask
// row), and a queued position left with no waiting row ends with the same
// reason, in one transaction.
func (s *leaseStore) EndRowMaybePosition(rowID, reason string, now int64, waitReasons string) (bool, error) {
	return s.tx(func(x *sql.Tx) error {
		var positionID string
		if err := x.QueryRow(`SELECT position_id FROM resource_leases WHERE id = ? AND state = 'waiting'`, rowID).Scan(&positionID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errLostCAS
			}
			return err
		}
		if err := mustChange(x, `UPDATE resource_leases
			SET state = 'ended', ended_at = ?, end_reason = ?, waited_ms = COALESCE(waited_ms, ? - created_at),
				wait_reasons = COALESCE(NULLIF(?, ''), wait_reasons)
			WHERE id = ? AND state = 'waiting'`, now, reason, now, waitReasons, rowID); err != nil {
			return err
		}
		if positionID == "" {
			return nil
		}
		if err := s.mid(); err != nil {
			return err
		}
		_, err := x.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = ?, ended_at = ?
			WHERE id = ? AND state = 'queued'
			AND NOT EXISTS (SELECT 1 FROM resource_leases WHERE position_id = ? AND state = 'waiting')`,
			reason, now, positionID, positionID)
		return err
	})
}

// PassOffer is P15: an offer nobody was told about for 3 minutes passes. The
// position is queued again (it keeps its place), its rows are un-named, and the
// notice slot is cleared. offeredAt is the offer being passed: a newer offer
// is left alone.
func (s *leaseStore) PassOffer(id string, offeredAt, now int64) (bool, error) {
	return s.tx(func(x *sql.Tx) error {
		if err := mustChange(x, `UPDATE resource_positions
			SET state = 'queued', passed_at = ?, offer_passes = offer_passes + 1, window_until = 0,
				notice_kind = '', notice_sent_at = 0, notice_due_at = 0, notice_tries = 0, notice_result = ''
			WHERE id = ? AND state = 'offered' AND offered_at = ? AND delivered_at = 0`, now, id, offeredAt); err != nil {
			return err
		}
		if err := s.mid(); err != nil {
			return err
		}
		_, err := x.Exec(`UPDATE resource_leases SET offered_at = 0 WHERE position_id = ? AND state = 'waiting'`, id)
		return err
	})
}

// NameRows is R7: waiting rows of an offered or idle position that were never
// named are named now, and the slot of the given kind is armed. It returns how
// many rows it named.
func (s *leaseStore) NameRows(positionID string, now int64, kind string) (int64, error) {
	var named int64
	_, err := s.tx(func(x *sql.Tx) error {
		n, err := run(x, `UPDATE resource_leases SET offered_at = ?
			WHERE position_id = ? AND state = 'waiting' AND offered_at = 0
			AND EXISTS (SELECT 1 FROM resource_positions WHERE id = ? AND state IN ('offered','idle'))`,
			now, positionID, positionID)
		if err != nil {
			return err
		}
		named = n
		if n == 0 {
			return nil
		}
		if err := s.mid(); err != nil {
			return err
		}
		return armSlot(x, positionID, kind, now)
	})
	return named, err
}

// ReconcilePositions repairs the pairs of rows that disagree, in one
// transaction (amendment A §2.4a, cases a–d; case e is the drain). With
// transactions they cannot come from a crash; they come from an older daemon
// after a rollback, or from a bug. It returns one line per repair.
func (s *leaseStore) ReconcilePositions(now int64) ([]string, error) {
	var repairs []string
	_, err := s.tx(func(x *sql.Tx) error {
		ids := func(query string, args ...any) ([]string, error) {
			rows, err := x.Query(query, args...)
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
		const heldOf = `EXISTS (SELECT 1 FROM resource_leases l WHERE l.position_id = resource_positions.id AND l.state = 'held')`
		// (a) running with no held row: idle, or done when it was asked to be.
		aIDs, err := ids(`SELECT id FROM resource_positions WHERE state = 'running' AND NOT ` + heldOf)
		if err != nil {
			return err
		}
		for _, id := range aIDs {
			var done int
			if err := x.QueryRow(`SELECT done_requested FROM resource_positions WHERE id = ?`, id).Scan(&done); err != nil {
				return err
			}
			if done != 0 {
				if _, err := x.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = ?, ended_at = ? WHERE id = ?`,
					resources.EndDone, now, id); err != nil {
					return err
				}
			} else if _, err := x.Exec(`UPDATE resource_positions SET state = 'idle', window_until = ?, commands_ended = commands_started
				WHERE id = ?`, now+int64(resources.PositionWindow.Milliseconds()), id); err != nil {
				return err
			}
			repairs = append(repairs, "running position "+id+" had no held row")
		}
		if err := s.mid(); err != nil {
			return err
		}
		// (b) queued / offered / idle with a held row: the held row is the truth.
		bIDs, err := ids(`SELECT id FROM resource_positions WHERE state IN ('queued','offered','idle') AND ` + heldOf)
		if err != nil {
			return err
		}
		for _, id := range bIDs {
			if _, err := x.Exec(`UPDATE resource_positions SET state = 'running', window_until = 0,
				first_started_at = COALESCE(NULLIF(first_started_at, 0), ?) WHERE id = ?`, now, id); err != nil {
				return err
			}
			repairs = append(repairs, "position "+id+" had a held row but was not running")
		}
		// (c) waiting rows whose position ended or is missing.
		cIDs, err := ids(`SELECT l.id FROM resource_leases l WHERE l.state = 'waiting' AND l.position_id != ''
			AND (NOT EXISTS (SELECT 1 FROM resource_positions p WHERE p.id = l.position_id)
			  OR EXISTS (SELECT 1 FROM resource_positions p WHERE p.id = l.position_id AND p.state = 'ended'))`)
		if err != nil {
			return err
		}
		if len(cIDs) > 0 {
			if _, err := x.Exec(`UPDATE resource_leases
				SET state = 'ended', ended_at = ?, waited_ms = COALESCE(waited_ms, ? - created_at),
					end_reason = COALESCE(NULLIF((SELECT p.end_reason FROM resource_positions p WHERE p.id = resource_leases.position_id), ''), ?)
				WHERE state = 'waiting' AND position_id != ''
				AND (NOT EXISTS (SELECT 1 FROM resource_positions p WHERE p.id = resource_leases.position_id)
				  OR EXISTS (SELECT 1 FROM resource_positions p WHERE p.id = resource_leases.position_id AND p.state = 'ended'))`,
				now, now, resources.EndAbandoned); err != nil {
				return err
			}
			for _, id := range cIDs {
				repairs = append(repairs, "waiting row "+id+" belonged to an ended or missing position")
			}
		}
		// (d) queued with no waiting row.
		dIDs, err := ids(`SELECT id FROM resource_positions WHERE state = 'queued'
			AND NOT EXISTS (SELECT 1 FROM resource_leases l WHERE l.position_id = resource_positions.id AND l.state = 'waiting')`)
		if err != nil {
			return err
		}
		for _, id := range dIDs {
			if _, err := x.Exec(`UPDATE resource_positions SET state = 'ended', end_reason = ?, ended_at = ? WHERE id = ?`,
				resources.EndAbandoned, now, id); err != nil {
				return err
			}
			repairs = append(repairs, "queued position "+id+" had no waiting row")
		}
		return nil
	})
	return repairs, err
}

// MarkDelivered is D1: the offer's notice reached the session. Only for the
// same offer (offered_at); a delivery for an older offer changes nothing. The
// 3-minute window starts at `at`.
func (s *leaseStore) MarkDelivered(id string, offeredAt, at int64) (bool, error) {
	return s.changed(`UPDATE resource_positions SET delivered_at = ?, window_until = ?
		WHERE id = ? AND state = 'offered' AND offered_at = ? AND delivered_at = 0`,
		at, at+resources.PositionWindow.Milliseconds(), id, offeredAt)
}

// MarkDone records `pdx lease done` on a running position: it ends when its
// last command does.
func (s *leaseStore) MarkDone(id string) (bool, error) {
	return s.changed(`UPDATE resource_positions SET done_requested = 1 WHERE id = ? AND state = 'running'`, id)
}

// SetNoticeResult records the outcome of one delivery try of a notice slot.
// The slot is named by what armed it (kind and due_at): a late answer for a
// slot that has since been cleared, passed or armed again changes nothing. It
// reports whether the answer was for the current slot.
func (s *leaseStore) SetNoticeResult(id, kind string, dueAt int64, result string, sentAt int64) (bool, error) {
	return s.changed(`UPDATE resource_positions SET notice_result = ?, notice_sent_at = MAX(notice_sent_at, ?)
		WHERE id = ? AND notice_kind = ? AND notice_due_at = ?`, result, sentAt, id, kind, dueAt)
}

// AddWindowWait adds time the position held the host while someone waited
// (the cost of V16 / V17).
func (s *leaseStore) AddWindowWait(id string, offerMS, idleMS int64) error {
	_, err := s.db.Exec(`UPDATE resource_positions SET offer_wait_ms = offer_wait_ms + ?, idle_wait_ms = idle_wait_ms + ? WHERE id = ?`,
		offerMS, idleMS, id)
	return err
}

// AdoptQueued gives a waiting queue row to a new call of the same session
// that repeats its command and cwd (R2): the new client id, holder, tool use
// and scope replace the old ones and the lease starts again. created_at stays,
// so the place does. One compare-and-set on the old client id, the position,
// the mode, the command and the cwd.
func (s *leaseStore) AdoptQueued(old, n leaseRow, leaseUntil int64) (bool, error) {
	return s.changed(`UPDATE resource_leases
		SET client_id = ?, holder_pid = ?, holder_start = ?, tool_use_id = ?, scope = ?, lease_until = ?
		WHERE id = ? AND state = 'waiting' AND client_id = ? AND position_id = ? AND mode = 'queue'
		AND command = ? AND cwd = ?`,
		n.ClientID, n.HolderPID, n.HolderStart, n.ToolUseID, n.Scope, leaseUntil,
		old.ID, old.ClientID, old.PositionID, old.Command, old.Cwd)
}

// EndParkedAtBoot ends every waiting row that is still parked: the parking
// code is gone and nobody polls such a row.
func (s *leaseStore) EndParkedAtBoot(now int64) (int64, error) {
	return run(s.db, `UPDATE resource_leases
		SET state = 'ended', ended_at = ?, end_reason = ?, waited_ms = COALESCE(waited_ms, ? - created_at)
		WHERE state = 'waiting' AND parked_until > 0`, now, resources.EndAbandoned, now)
}
