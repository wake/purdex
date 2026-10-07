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

// The store is the team.LineageReader the module publishes (P5a-1b).
var _ team.LineageReader = (*Store)(nil)

// relaySchema holds the three P5a tables (spec §8.1, §8.4, §8.7). It is
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
	CREATE TABLE IF NOT EXISTS session_prefs (
		session_id        TEXT PRIMARY KEY,
		self_relay_paused INTEGER NOT NULL DEFAULT 0,
		updated_at        INTEGER NOT NULL
	);`

// relayTransitions is the state machine of spec §8.1: from → the states a
// report may move the op to. A report of the op's current state is a
// no-op (idempotent per (op, state)); anything not listed is bad_transition.
var relayTransitions = map[team.RelayState]map[team.RelayState]bool{
	team.RelayAwaitingApproval: {team.RelayClaimed: true, team.RelayCancelled: true},
	team.RelayRequested:        {team.RelayClaimed: true, team.RelayCancelled: true, team.RelayFailed: true},
	team.RelayClaimed:          {team.RelayWriting: true, team.RelayWritten: true, team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayWriting:          {team.RelayWritten: true, team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayWritten:          {team.RelayCleared: true, team.RelayFailed: true, team.RelayCancelled: true},
	team.RelayCleared:          {team.RelayDone: true, team.RelayFailed: true},
}

// checkLineage guards the `cleared` transition inside ReportRelay's
// transaction (spec §8.4): the new session id and ref are set, the new
// session is not the old one, no other op has already cleared into the
// new session, and the new session is not an ancestor of the old one.
func checkLineage(tx *sql.Tx, cur team.RelayOp, r RelayReport) error {
	if r.NewSessionID == "" || r.NewRef == "" {
		return fmt.Errorf("%w: cleared needs new_session_id and new_ref", ErrBadRelayReport)
	}
	if r.NewSessionID == cur.SessionID {
		return fmt.Errorf("%w: new session equals the old one", ErrBadRelayReport)
	}
	var otherOp string
	err := tx.QueryRow(`SELECT op_id FROM session_lineage WHERE session_id = ?`, r.NewSessionID).Scan(&otherOp)
	if err == nil {
		return fmt.Errorf("%w: session %s already heads the lineage of op %s", ErrBadRelayReport, r.NewSessionID, otherOp)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Walk the old session's ancestors; the chain is finite because every
	// accepted row passed this check, so no cycle exists yet.
	for sid := cur.SessionID; sid != ""; {
		var pred string
		err := tx.QueryRow(`SELECT predecessor_session_id FROM session_lineage WHERE session_id = ?`, sid).Scan(&pred)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if pred == r.NewSessionID {
			return fmt.Errorf("%w: session %s is an ancestor of %s (cycle)", ErrBadRelayReport, r.NewSessionID, cur.SessionID)
		}
		sid = pred
	}
	return nil
}

// RelayReport is one transition: the target state, the new session id and
// ref (cleared only), the reason (failed / cancelled) and the time.
type RelayReport struct {
	State        team.RelayState
	NewSessionID string
	NewRef       string
	Reason       string
	At           int64
}

// ReportResult says what ReportRelay did.
type ReportResult int

const (
	ReportApplied       ReportResult = iota // the op moved to the reported state
	ReportNoop                              // the op was already in that state
	ReportBadTransition                     // the op's state does not lead to the reported one
)

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
func (s *Store) CreateRelayOp(op team.RelayOp) error {
	var used any
	if op.UsedPercentage != nil {
		used = *op.UsedPercentage
	}
	if _, err := s.db.Exec(`
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

// ReportRelay applies one transition under a write transaction: a report of
// the current state is ReportNoop (idempotent per (op, state)); a state
// relayTransitions does not allow is ReportBadTransition; otherwise the row
// is updated with a CAS on its state. For cleared, the lineage row is
// written in the same transaction (spec §8.4): session_lineage{new →
// old, old ref, op}. The row after the attempt is returned in every case;
// ErrNoSuchRelayOp for an unknown id.
func (s *Store) ReportRelay(id string, r RelayReport) (team.RelayOp, ReportResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: begin: %w", id, err)
	}
	defer tx.Rollback()
	// The first statement is a write, so SQLite takes the write lock at
	// once (same reasoning as peer_label.go Release): two concurrent
	// reports cannot both read the same state and both pass the CAS.
	if _, err := tx.Exec(`UPDATE relay_ops SET updated_at = updated_at WHERE id = ?`, id); err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: lock: %w", id, err)
	}
	cur, err := scanRelayOp(tx.QueryRow(`SELECT `+relayCols+` FROM relay_ops WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.RelayOp{}, ReportBadTransition, ErrNoSuchRelayOp
	}
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
	}
	if cur.State == r.State {
		return cur, ReportNoop, nil
	}
	if !relayTransitions[cur.State][r.State] {
		return cur, ReportBadTransition, nil
	}
	if r.State == team.RelayCleared {
		if err := checkLineage(tx, cur, r); err != nil {
			return cur, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
		}
	}
	next := cur
	next.State, next.Reason, next.UpdatedAt = r.State, r.Reason, r.At
	if r.State == team.RelayCleared {
		next.NewSessionID, next.NewRef = r.NewSessionID, r.NewRef
	}
	res, err := tx.Exec(`UPDATE relay_ops SET state = ?, reason = ?, new_session_id = ?, new_ref = ?, updated_at = ?
		WHERE id = ? AND state = ?`, string(next.State), next.Reason, next.NewSessionID, next.NewRef, next.UpdatedAt, id, string(cur.State))
	if err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: state changed under the transaction", id)
	}
	if r.State == team.RelayCleared {
		// A plain INSERT: checkLineage has already proven the key is free,
		// so a conflict here is a real error and rolls the op back with it.
		if _, err := tx.Exec(`INSERT INTO session_lineage (session_id, predecessor_session_id, predecessor_ref, op_id, at)
			VALUES (?, ?, ?, ?, ?)`, r.NewSessionID, cur.SessionID, cur.Ref, id, r.At); err != nil {
			return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: lineage: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return team.RelayOp{}, ReportBadTransition, fmt.Errorf("report relay %s: commit: %w", id, err)
	}
	return next, ReportApplied, nil
}

// lineageRow is one session_lineage row.
type lineageRow struct {
	predecessorSessionID, predecessorRef string
}

// PreviousRefs implements team.LineageReader: for every session id that
// appears as the head of a lineage row, the predecessor refs walking back
// the whole chain, newest first, uncapped (spec §8.4). A cycle (impossible
// by construction, guarded anyway) stops the walk.
func (s *Store) PreviousRefs() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT session_id, predecessor_session_id, predecessor_ref FROM session_lineage`)
	if err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	defer rows.Close()
	back := map[string]lineageRow{}
	for rows.Next() {
		var sid string
		var lr lineageRow
		if err := rows.Scan(&sid, &lr.predecessorSessionID, &lr.predecessorRef); err != nil {
			return nil, fmt.Errorf("read lineage: %w", err)
		}
		back[sid] = lr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	// Each session's chain is its predecessor's ref followed by the
	// predecessor's own chain, so chains are memoised: every row's
	// predecessor is looked up once. The output itself is Θ(sum of chain
	// lengths) — a single chain of N relays yields N chains of 1..N refs —
	// because the contract hands every head its whole chain (U3: uncapped)
	// without knowing which heads are live; N is the number of relays one
	// conversation has been through, tens at most, 7 bytes a ref. The
	// lineage is acyclic (checkLineage), and `visiting` makes a cycle in a
	// hand-edited database terminate instead of recursing forever.
	out := make(map[string][]string, len(back))
	visiting := map[string]bool{}
	var chain func(sid string) []string
	chain = func(sid string) []string {
		if refs, done := out[sid]; done {
			return refs
		}
		lr, ok := back[sid]
		if !ok || visiting[sid] {
			return nil
		}
		visiting[sid] = true
		refs := append([]string{lr.predecessorRef}, chain(lr.predecessorSessionID)...)
		visiting[sid] = false
		out[sid] = refs
		return refs
	}
	for head := range back {
		chain(head)
	}
	return out, nil
}

// SetSelfRelayPaused records the per-session pause (spec §8.7 "a pause").
func (s *Store) SetSelfRelayPaused(sessionID string, paused bool, now int64) error {
	v := 0
	if paused {
		v = 1
	}
	if _, err := s.db.Exec(`INSERT INTO session_prefs (session_id, self_relay_paused, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET self_relay_paused = excluded.self_relay_paused, updated_at = excluded.updated_at`,
		sessionID, v, now); err != nil {
		return fmt.Errorf("set self relay paused %s: %w", sessionID, err)
	}
	return nil
}

// SelfRelayPaused reads the pause; a session without a row is not paused.
func (s *Store) SelfRelayPaused(sessionID string) (bool, error) {
	var v int
	err := s.db.QueryRow(`SELECT self_relay_paused FROM session_prefs WHERE session_id = ?`, sessionID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("self relay paused %s: %w", sessionID, err)
	}
	return v != 0, nil
}

// ListUnprunedRelayOps returns every op whose file has not been pruned, in
// any state, oldest first. The retention sweeper's input.
func (s *Store) ListUnprunedRelayOps() ([]team.RelayOp, error) {
	rows, err := s.db.Query(`SELECT ` + relayCols + ` FROM relay_ops WHERE pruned = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list unpruned relay ops: %w", err)
	}
	defer rows.Close()
	out := []team.RelayOp{}
	for rows.Next() {
		op, err := scanRelayOp(rows)
		if err != nil {
			return nil, fmt.Errorf("list unpruned relay ops: %w", err)
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// MarkRelayPruned records that op's handoff file is gone (spec §8.3
// retention: "the row keeps the path, marked pruned").
func (s *Store) MarkRelayPruned(id string) error {
	if _, err := s.db.Exec(`UPDATE relay_ops SET pruned = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("mark relay op %s pruned: %w", id, err)
	}
	return nil
}

// ChainRoots maps every session id that appears in session_lineage (as a
// head or a predecessor) to the root of its chain — the one session with
// no predecessor. Two ops whose sessions share a root are in one chain.
func (s *Store) ChainRoots() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT session_id, predecessor_session_id FROM session_lineage`)
	if err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	defer rows.Close()
	pred := map[string]string{}
	for rows.Next() {
		var sid, p string
		if err := rows.Scan(&sid, &p); err != nil {
			return nil, fmt.Errorf("read lineage: %w", err)
		}
		pred[sid] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read lineage: %w", err)
	}
	roots := make(map[string]string, len(pred)*2)
	rootOf := func(sid string) string {
		seen := map[string]bool{sid: true}
		for {
			p, ok := pred[sid]
			if !ok || seen[p] {
				return sid
			}
			seen[p] = true
			sid = p
		}
	}
	for sid, p := range pred {
		roots[sid] = rootOf(sid)
		roots[p] = rootOf(p)
	}
	return roots, nil
}
