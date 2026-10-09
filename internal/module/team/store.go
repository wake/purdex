// Package teammod is the daemon's team module (spec §6, §9): it owns
// team.db and the approval_requests table — one state machine for every
// approval kind, closed by compare-and-set — and serves /api/team/*.
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/wake/purdex/internal/team"
)

// ErrNoSuchApproval is returned by the per-id methods for an unknown id.
var ErrNoSuchApproval = errors.New("no such approval")

// Store is the SQLite persistence of approval requests.
type Store struct {
	db *sql.DB

	// opChanged, when set, is called with a relay op's id AFTER a transaction that changed the op committed — the one
	// choke point for waking its long-polls (P6-2b-2, plan v3 §7), whatever path wrote it (report, claim, an approval's
	// close, a create). The module sets it at Init (wake); it must not block and must not call back into the store.
	opChanged func(opID string)

	// beforeReplaceInsert, when set, runs in ReplaceTerminalOnly's
	// transaction after the old row's close and before the new row's
	// insert; a non-nil error fails the replace there. Tests use it to
	// prove the close rolls back with a failed insert. nil in production.
	beforeReplaceInsert func() error
	// beforeMemberCancelOp, when set, runs in CloseSelfRelayApproved's
	// transaction after the row's cancel and before the op's; an error
	// fails the call there (tests). nil in production.
	beforeMemberCancelOp func() error
	// afterApprovedInsert, when set, runs in CreateApproved's and
	// CreateSelfRelayApproved's transaction after the inserts and before
	// the approve, on that transaction; an error fails the create there
	// (tests: nothing outside the transaction sees the open row). nil in
	// production.
	afterApprovedInsert func(tx *sql.Tx) error
	// beforeListAutoApproved, when set, runs as ListAutoApproved starts
	// (since > 0); an error fails the list there (tests). nil in
	// production.
	beforeListAutoApproved func() error
	// afterTaskSeqRead, when set, runs in CreateTask's transaction right
	// after it read MAX(seq) and before it inserts (tests: a barrier that
	// proves two creates never share a seq). nil in production.
	afterTaskSeqRead func()
	// beforeTaskCommit, when set, runs in a task write transaction where
	// COMMIT would run; an error stands for a failed COMMIT, the
	// transaction still open on its connection (tests). nil in production.
	beforeTaskCommit func() error
	// afterReportInsert, when set, runs in InsertReport's transaction after
	// the report row was inserted and its effect applied to the task, before
	// COMMIT; an error fails the call there (tests: neither the row nor the
	// effect may survive). nil in production.
	afterReportInsert func() error
}

// OpenStore opens (or creates) team.db at path. ":memory:" is for tests.
func OpenStore(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		// busy_timeout: a concurrent writer waits instead of failing with SQLITE_BUSY.
		dsn = path + "?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open team db: %w", err)
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS approval_requests (
			id                TEXT PRIMARY KEY,
			kind              TEXT    NOT NULL,
			host_id           TEXT    NOT NULL,
			origin_session_id TEXT    NOT NULL,
			origin_json       TEXT    NOT NULL,
			payload_json      TEXT    NOT NULL,
			request_hash      TEXT    NOT NULL,
			state             TEXT    NOT NULL,
			created_at        INTEGER NOT NULL,
			deadline_at       INTEGER NOT NULL,
			lease_until       INTEGER NOT NULL,
			decided_by_json   TEXT,
			decided_at        INTEGER NOT NULL DEFAULT 0,
			grant_json        TEXT
		);
		CREATE INDEX IF NOT EXISTS approval_requests_state_created
			ON approval_requests (state, created_at);
		CREATE INDEX IF NOT EXISTS approval_requests_state_decided
			ON approval_requests (state, decided_at);`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db: %w", err)
	}
	if _, err := db.Exec(relaySchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (relay): %w", err)
	}
	if _, err := db.Exec(teamSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (teams): %w", err)
	}
	if _, err := db.Exec(spawnSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (spawn ops): %w", err)
	}
	if _, err := db.Exec(taskSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (tasks): %w", err)
	}
	if _, err := db.Exec(reportSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (reports): %w", err)
	}
	if err := migrateReportsPK(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (reports key): %w", err)
	}
	if err := migrateUsage(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (usage): %w", err)
	}
	if err := migrateTeamName(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (team name): %w", err)
	}
	if err := migrateTeamLabel(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (team label): %w", err)
	}
	if err := migrateAdopt(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (adopt): %w", err)
	}
	if err := migrateMemberLastTurn(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (member last turn): %w", err)
	}
	if err := migrateRelayQuotaRev(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (relay quota rev): %w", err)
	}
	if err := migrateSpawnTask(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (spawn task): %w", err)
	}
	if err := migrateRelayOpBinding(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (relay op binding): %w", err)
	}
	if _, err := db.Exec(modHelloSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate team db (mod hello): %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const selectCols = `id, kind, host_id, origin_json, payload_json, request_hash, state,
	created_at, deadline_at, lease_until, decided_by_json, decided_at, grant_json, close_reason`

type rowScanner interface{ Scan(dest ...any) error }

// dbtx is what *sql.DB and *sql.Tx share: the single-statement helpers
// below run on either, so a transaction (ReplaceTerminalOnly) reuses them.
type dbtx interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// scanRow decodes one approval_requests row and its request hash.
func scanRow(r rowScanner) (team.Approval, string, error) {
	var a team.Approval
	var hash, originJSON, payloadJSON string
	var decidedBy, grant sql.NullString
	if err := r.Scan(&a.ID, &a.Kind, &a.HostID, &originJSON, &payloadJSON, &hash, &a.State,
		&a.CreatedAt, &a.DeadlineAt, &a.LeaseUntil, &decidedBy, &a.DecidedAt, &grant, &a.CloseReason); err != nil {
		return team.Approval{}, "", err
	}
	if err := json.Unmarshal([]byte(originJSON), &a.Origin); err != nil {
		return team.Approval{}, "", fmt.Errorf("decode origin of %s: %w", a.ID, err)
	}
	a.Payload = json.RawMessage(payloadJSON)
	if decidedBy.Valid {
		a.DecidedBy = new(team.Client)
		if err := json.Unmarshal([]byte(decidedBy.String), a.DecidedBy); err != nil {
			return team.Approval{}, "", fmt.Errorf("decode decided_by of %s: %w", a.ID, err)
		}
	}
	if grant.Valid {
		// grant_json holds the Grant of a lead row and, in its place, the
		// HookDecision of a hook row (the wire says the decision "rides in
		// Grant's place"); the kind says which (P8a).
		if team.IsHookKind(a.Kind) {
			a.Hook = new(team.HookDecision)
			if err := json.Unmarshal([]byte(grant.String), a.Hook); err != nil {
				return team.Approval{}, "", fmt.Errorf("decode hook decision of %s: %w", a.ID, err)
			}
		} else {
			a.Grant = new(team.Grant)
			if err := json.Unmarshal([]byte(grant.String), a.Grant); err != nil {
				return team.Approval{}, "", fmt.Errorf("decode grant of %s: %w", a.ID, err)
			}
		}
	}
	return a, hash, nil
}

// getRow reads one row with its hash; ErrNoSuchApproval when absent.
func (s *Store) getRow(id string) (team.Approval, string, error) { return getRowIn(s.db, id) }

// getRowIn is getRow on q (the database, or a transaction).
func getRowIn(q dbtx, id string) (team.Approval, string, error) {
	a, hash, err := scanRow(q.QueryRow(`SELECT `+selectCols+` FROM approval_requests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, "", ErrNoSuchApproval
	}
	if err != nil {
		return team.Approval{}, "", fmt.Errorf("get approval %s: %w", id, err)
	}
	return a, hash, nil
}

// Get returns the approval with id; ok is false when there is none.
func (s *Store) Get(id string) (team.Approval, bool, error) {
	a, _, err := s.getRow(id)
	if errors.Is(err, ErrNoSuchApproval) {
		return team.Approval{}, false, nil
	}
	return a, err == nil, err
}

// Create inserts a (state open) if its id is new and returns it with
// inserted=true. When the id exists it inserts nothing and returns the
// stored row and the hash it was stored with, so the caller can tell an
// idempotent retry (same hash) from a conflicting reuse of the id.
func (s *Store) Create(a team.Approval, hash string) (stored team.Approval, storedHash string, inserted bool, err error) {
	n, err := insertRowIn(s.db, a, hash, "\n\t\tON CONFLICT(id) DO NOTHING")
	if err != nil {
		return team.Approval{}, "", false, err
	}
	stored, storedHash, err = s.getRow(a.ID)
	if err != nil {
		return team.Approval{}, "", false, err
	}
	return stored, storedHash, n == 1, nil
}

// CreateApproved is the create of a request the daemon approves itself
// (U23 unattended, D-U23-1): in one write transaction it inserts a (state
// open; an id in use is an error — the caller answered replays under
// createMu before calling) and runs approveIn, the kind's own approve
// statements (closeLeadApprovedIn, …) on that transaction, so the row's
// first committed state is approved and no reader outside the transaction
// — a snapshot, a list, a poll — ever sees it open. A refusal or an error
// from approveIn, or an approveIn that leaves the row anything but
// approved, rolls the insert back: nothing is written and the error says
// why (errors.Is finds a refusal such as ErrMemberCannotLead). It returns
// the row as committed.
func (s *Store) CreateApproved(a team.Approval, hash string, approveIn func(tx *sql.Tx) error) (team.Approval, error) {
	fail := func(err error) (team.Approval, error) {
		return team.Approval{}, fmt.Errorf("create approved %s: %w", a.ID, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at once.
	if _, err := insertRowIn(tx, a, hash, ""); err != nil {
		return fail(err)
	}
	if err := s.atApprovedInsert(tx); err != nil {
		return fail(err)
	}
	if err := approveIn(tx); err != nil {
		return fail(err)
	}
	after, err := approvedRowIn(tx, a.ID)
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return after, nil
}

// atApprovedInsert runs the afterApprovedInsert seam, if set.
func (s *Store) atApprovedInsert(tx *sql.Tx) error {
	if s.afterApprovedInsert == nil {
		return nil
	}
	return s.afterApprovedInsert(tx)
}

// approvedRowIn reads the row a create-time approve just closed and refuses
// anything but approved: the commit that follows must never be the row's
// first state other than approved (D-U23-6).
func approvedRowIn(tx *sql.Tx, id string) (team.Approval, error) {
	a, _, err := getRowIn(tx, id)
	if err != nil {
		return team.Approval{}, err
	}
	if a.State != team.StateApproved {
		return team.Approval{}, fmt.Errorf("the approve left the row %s, not approved", a.State)
	}
	return a, nil
}

// insertRowIn inserts a (state open) on ex with the conflict clause
// appended ("" makes an existing id an error) and returns how many rows it
// inserted.
func insertRowIn(ex dbtx, a team.Approval, hash, conflict string) (int64, error) {
	originJSON, err := json.Marshal(a.Origin)
	if err != nil {
		return 0, fmt.Errorf("encode origin: %w", err)
	}
	res, err := ex.Exec(`
		INSERT INTO approval_requests
			(id, kind, host_id, origin_session_id, origin_json, payload_json, request_hash, state, created_at, deadline_at, lease_until)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`+conflict,
		a.ID, string(a.Kind), a.HostID, a.Origin.SessionID, string(originJSON), string(a.Payload), hash,
		string(team.StateOpen), a.CreatedAt, a.DeadlineAt, a.LeaseUntil)
	if err != nil {
		return 0, fmt.Errorf("insert approval %s: %w", a.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("insert approval %s rows affected: %w", a.ID, err)
	}
	return n, nil
}

// Close is how a request leaves the open state. DecidedBy is set for
// approved/denied only, Grant for approved only.
type Close struct {
	State     team.State
	DecidedAt int64
	DecidedBy *team.Client
	Grant     *team.Grant
	Hook      *team.HookDecision // hook kinds: stored in grant_json in Grant's place
	// UnexpiredAt, when non-zero, makes the CAS also require that the row is
	// not overdue at it (deadline_at and lease_until both after it): the
	// daemon's own approve (U23 autoApprove) leaves an overdue row to the
	// sweeper's timeout or abandonment, in the same statement. A click
	// leaves it 0 and runs today's SQL.
	UnexpiredAt int64
	// Reason is the code an adopt request cancelled at approve carries (close_reason); empty for every other close.
	Reason string
	// Auto marks a close the DAEMON makes itself (U23: autoApprove, beginApproved, the create-time approves). It is an
	// internal flag, set only by daemonClose — nothing an HTTP body carries can produce it — and it is the truth of
	// "automatic": the stored decided_by of an Auto close is always UnattendedClient, and a close that is not Auto
	// is refused if it names that kind (RQ-0, #2062 plan review: a decide request posing as `unattended` must
	// neither pass for the daemon in the audit nor, later, spend a quota).
	Auto bool
	// SpendQuota asks an Auto close of a self_relay row to spend one of the chain's self_left in the same transaction
	// (the relay-quota rule, #2062; set by the module only while the hostconfig switch relay_quota is on). Never honoured
	// for a close that is not Auto: a person's click spends nothing.
	SpendQuota bool
	// SpentOut, when non-nil, is set true by the store when the close won AND spent a unit in its transaction: the
	// module publishes the new numbers only for a close that really spent, not by re-reading a switch that may have
	// changed since.
	SpentOut *bool
}

// ErrReservedDecider is a non-Auto close that names the daemon's own decider kind.
var ErrReservedDecider = errors.New("decided_by kind \"unattended\" is reserved for the daemon's own approvals")

// decidedByOf is the decided_by_json of c (nil: NULL). The Auto flag decides: an Auto close is recorded as the
// unattended decider whatever DecidedBy says; any other close naming that kind is refused.
func decidedByOf(c Close) (any, error) {
	by := c.DecidedBy
	if c.Auto {
		u := team.UnattendedClient()
		by = &u
	} else if by != nil && strings.EqualFold(strings.TrimSpace(by.Kind), team.ClientKindUnattended) {
		return nil, ErrReservedDecider
	}
	if by == nil {
		return nil, nil
	}
	b, err := json.Marshal(by)
	if err != nil {
		return nil, fmt.Errorf("encode decided_by: %w", err)
	}
	return string(b), nil
}

// CloseIfOpen is the compare-and-set every close goes through: the UPDATE
// is guarded by state='open', so of any number of concurrent closes
// exactly one sees RowsAffected()==1 and won. It returns the row as it is
// after the attempt (the winner's close, for a loser too) and
// ErrNoSuchApproval for an unknown id.
func (s *Store) CloseIfOpen(id string, c Close) (team.Approval, bool, error) {
	return s.closeWhere(id, c, "", 0)
}

// CloseIfExpired is the sweeper's close (spec §9.2): CloseIfOpen whose
// UPDATE also requires the row to still be overdue at now, in the same
// statement — deadline_at <= now for a timeout, lease_until <= now for an
// abandonment. A lease renewed between the sweeper's read and its close
// therefore makes the close lose, and the row stays open. Any other
// state is an error: it has no expiry to guard on.
func (s *Store) CloseIfExpired(id string, now int64, c Close) (team.Approval, bool, error) {
	switch c.State {
	case team.StateTimeout:
		return s.closeWhere(id, c, " AND deadline_at <= ?", now)
	case team.StateAbandoned:
		return s.closeWhere(id, c, " AND lease_until <= ?", now)
	default:
		return team.Approval{}, false, fmt.Errorf("close approval %s: state %q has no expiry guard", id, c.State)
	}
}

// closeWhere runs the close UPDATE guarded by state='open' and, when guard
// is non-empty, that extra SQL condition (one ? bound to guardArg).
func (s *Store) closeWhere(id string, c Close, guard string, guardArg int64) (team.Approval, bool, error) {
	n, err := closeRowIn(s.db, id, c, guard, guardArg)
	if err != nil {
		return team.Approval{}, false, err
	}
	a, _, err := s.getRow(id)
	if err != nil {
		return team.Approval{}, false, err
	}
	return a, n == 1, nil
}

// closeRowIn runs closeWhere's guarded UPDATE on ex and returns how many
// rows it changed (1: this close won).
func closeRowIn(ex dbtx, id string, c Close, guard string, guardArg int64) (int64, error) {
	var grant any // NULL unless set
	decidedBy, err := decidedByOf(c)
	if err != nil {
		return 0, err
	}
	if c.Grant != nil {
		b, err := json.Marshal(c.Grant)
		if err != nil {
			return 0, fmt.Errorf("encode grant: %w", err)
		}
		grant = string(b)
	}
	if c.Hook != nil {
		b, err := json.Marshal(c.Hook)
		if err != nil {
			return 0, fmt.Errorf("encode hook decision: %w", err)
		}
		grant = string(b)
	}
	args := []any{string(c.State), c.DecidedAt, decidedBy, grant, c.Reason, id}
	if guard != "" {
		args = append(args, guardArg)
	}
	if c.UnexpiredAt != 0 {
		guard += " AND deadline_at > ? AND lease_until > ?"
		args = append(args, c.UnexpiredAt, c.UnexpiredAt)
	}
	res, err := ex.Exec(`
		UPDATE approval_requests
		SET state = ?, decided_at = ?, decided_by_json = ?, grant_json = ?, close_reason = ?
		WHERE id = ? AND state = 'open'`+guard, args...)
	if err != nil {
		return 0, fmt.Errorf("close approval %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("close approval %s rows affected: %w", id, err)
	}
	return n, nil
}

// RenewLease moves an open request's lease forward to until (never back).
// A closed or unknown id is left alone and is not an error.
func (s *Store) RenewLease(id string, until int64) error {
	if _, err := s.db.Exec(`
		UPDATE approval_requests SET lease_until = MAX(lease_until, ?)
		WHERE id = ? AND state = 'open'`, until, id); err != nil {
		return fmt.Errorf("renew lease %s: %w", id, err)
	}
	return nil
}

// ExtendOpenLeases is the boot grace (spec §9.2): every open request's
// lease becomes max(lease_until, until). It returns how many rows changed.
func (s *Store) ExtendOpenLeases(until int64) (int64, error) {
	res, err := s.db.Exec(`
		UPDATE approval_requests SET lease_until = ?
		WHERE state = 'open' AND lease_until < ?`, until, until)
	if err != nil {
		return 0, fmt.Errorf("extend open leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("extend open leases rows affected: %w", err)
	}
	return n, nil
}

// ListOpen returns every open request, oldest first (created_at, id).
func (s *Store) ListOpen() ([]team.Approval, error) {
	rows, err := s.db.Query(`SELECT ` + selectCols + ` FROM approval_requests WHERE state = 'open' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list open approvals: %w", err)
	}
	defer rows.Close()
	out := []team.Approval{}
	for rows.Next() {
		a, _, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("list open approvals: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list open approvals: %w", err)
	}
	return out, nil
}

// OpenByOrigin returns the open request of kind for the origin session, if any.
func (s *Store) OpenByOrigin(sessionID string, kind team.Kind) (team.Approval, bool, error) {
	a, _, err := scanRow(s.db.QueryRow(`SELECT `+selectCols+` FROM approval_requests
		WHERE origin_session_id = ? AND kind = ? AND state = 'open' ORDER BY created_at, id LIMIT 1`, sessionID, string(kind)))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Approval{}, false, nil
	}
	if err != nil {
		return team.Approval{}, false, fmt.Errorf("open approval by origin %s: %w", sessionID, err)
	}
	return a, true, nil
}
