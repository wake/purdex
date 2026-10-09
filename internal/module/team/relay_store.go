package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// ErrNoSuchRelayOp is returned by the per-id relay methods for an unknown id.
var ErrNoSuchRelayOp = errors.New("no such relay op")

// ErrRelayOpOpen is returned by CreateRelayOp when the session already has
// a non-terminal op: the table's partial unique index holds spec §8.7's
// "at most one open relay per session" even if two creators race past the
// handler's check (which runs under createMu and answers 409 relay_open
// with the open op; this is the floor beneath it).
var ErrRelayOpOpen = errors.New("relay op already open for this session")

// ErrBadRelayReport is returned by ReportRelay for a `cleared` report that
// would corrupt the lineage: an empty new session id or ref, a new session
// equal to the old one, a new session that already heads a lineage row of
// another op, or one that is an ancestor of the old session (a cycle). The
// op is left as it was. The HTTP handler (P5a-2b) answers 400 for the empty
// fields before reaching here; this is the floor for every other caller.
var ErrBadRelayReport = errors.New("bad relay report")

// relaySchema holds the P5a tables (and relay_quotas, #2062) (spec §8.1, §8.4, §8.7). It is
// run by OpenStore after approval_requests; every statement is idempotent.
const relaySchema = `
	CREATE TABLE IF NOT EXISTS relay_ops (
		id              TEXT PRIMARY KEY,
		kind            TEXT    NOT NULL,
		host_id         TEXT    NOT NULL,
		session_id      TEXT    NOT NULL,
		new_session_id  TEXT    NOT NULL DEFAULT '',
		ref             TEXT    NOT NULL,
		new_ref         TEXT    NOT NULL DEFAULT '',
		team_id         TEXT    NOT NULL DEFAULT '',
		request_id      TEXT    NOT NULL DEFAULT '',
		state           TEXT    NOT NULL,
		reason          TEXT    NOT NULL DEFAULT '',
		handoff_path    TEXT    NOT NULL,
		pruned          INTEGER NOT NULL DEFAULT 0,
		used_percentage REAL,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS relay_ops_session_state ON relay_ops (session_id, state);
	CREATE UNIQUE INDEX IF NOT EXISTS relay_ops_one_open ON relay_ops (session_id)
		WHERE state NOT IN ('done', 'failed', 'cancelled');
	CREATE TABLE IF NOT EXISTS session_lineage (
		session_id             TEXT PRIMARY KEY,
		predecessor_session_id TEXT    NOT NULL,
		predecessor_ref        TEXT    NOT NULL,
		op_id                  TEXT    NOT NULL,
		at                     INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS relay_quotas (
		root_session_id  TEXT PRIMARY KEY,
		self_left        INTEGER NOT NULL DEFAULT 0,
		member_pool_left INTEGER NOT NULL DEFAULT 0,
		updated_at       INTEGER NOT NULL,
		updated_by       TEXT    NOT NULL DEFAULT '',
		rev              INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS session_prefs (
		session_id        TEXT PRIMARY KEY,
		self_relay_paused INTEGER NOT NULL DEFAULT 0,
		updated_at        INTEGER NOT NULL
	);`

const relayCols = `id, kind, host_id, session_id, new_session_id, ref, new_ref, team_id, request_id,
	state, reason, handoff_path, pruned, used_percentage, created_at, updated_at`

func scanRelayOp(r rowScanner) (team.RelayOp, error) {
	var op team.RelayOp
	var pruned int
	var used sql.NullFloat64
	if err := r.Scan(&op.ID, &op.Kind, &op.HostID, &op.SessionID, &op.NewSessionID, &op.Ref, &op.NewRef, &op.TeamID, &op.RequestID,
		&op.State, &op.Reason, &op.HandoffPath, &pruned, &used, &op.CreatedAt, &op.UpdatedAt); err != nil {
		return team.RelayOp{}, err
	}
	op.Pruned = pruned != 0
	if used.Valid {
		v := used.Float64
		op.UsedPercentage = &v
	}
	return op, nil
}

// CreateRelayOp inserts op as given (the caller sets State and times). A
// duplicate id is an error: op ids are daemon-minted UUIDs, never retried.
func (s *Store) CreateRelayOp(op team.RelayOp) error { return insertRelayOpIn(s.db, op) }

// insertRelayOpIn is CreateRelayOp on ex (the database, or a transaction:
// CreateSelfRelayApproved). ErrRelayOpOpen (wrapped) when the session
// already has a non-terminal op.
func insertRelayOpIn(ex dbtx, op team.RelayOp) error {
	var used any
	if op.UsedPercentage != nil {
		used = *op.UsedPercentage
	}
	if _, err := ex.Exec(`
		INSERT INTO relay_ops (id, kind, host_id, session_id, new_session_id, ref, new_ref, team_id, request_id,
			state, reason, handoff_path, pruned, used_percentage, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		op.ID, string(op.Kind), op.HostID, op.SessionID, op.NewSessionID, op.Ref, op.NewRef, op.TeamID, op.RequestID,
		string(op.State), op.Reason, op.HandoffPath, used, op.CreatedAt, op.UpdatedAt); err != nil {
		if strings.Contains(err.Error(), "relay_ops.session_id") { // the partial unique index relay_ops_one_open
			return fmt.Errorf("insert relay op %s: %w", op.ID, ErrRelayOpOpen)
		}
		return fmt.Errorf("insert relay op %s: %w", op.ID, err)
	}
	return nil
}

// GetRelayOp returns the op with id; ok is false when there is none.
func (s *Store) GetRelayOp(id string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(s.db.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("get relay op %s: %w", id, err)
	}
	return op, true, nil
}

// OpenRelayOpBySession returns the session's non-terminal op, if any
// (spec §8.7: at most one open relay per session).
func (s *Store) OpenRelayOpBySession(sessionID string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(s.db.QueryRow(`SELECT `+relayCols+` FROM relay_ops
		WHERE session_id = ? AND state NOT IN ('done', 'failed', 'cancelled') ORDER BY created_at, id LIMIT 1`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("open relay op by session %s: %w", sessionID, err)
	}
	return op, true, nil
}

// RelayOpByRequest returns the op opened for a self_relay approval row.
func (s *Store) RelayOpByRequest(requestID string) (team.RelayOp, bool, error) {
	op, err := scanRelayOp(s.db.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE request_id = ? ORDER BY created_at, id LIMIT 1`, requestID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, false, nil
	}
	if err != nil {
		return team.RelayOp{}, false, fmt.Errorf("relay op by request %s: %w", requestID, err)
	}
	return op, true, nil
}

// ListActiveRelayOps returns every op not in done/failed/cancelled, oldest first. Never nil.
func (s *Store) ListActiveRelayOps() ([]team.RelayOp, error) {
	rows, err := s.db.Query(`SELECT ` + relayCols + ` FROM relay_ops
		WHERE state NOT IN ('done', 'failed', 'cancelled') ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list active relay ops: %w", err)
	}
	defer rows.Close()
	out := []team.RelayOp{}
	for rows.Next() {
		op, err := scanRelayOp(rows)
		if err != nil {
			return nil, fmt.Errorf("list active relay ops: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
