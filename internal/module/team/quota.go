package teammod

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

// The relay quota's storage (#2062, RQ-1a; docs/specs/2026-10-09-relay-quota-spec-plan.md §3.1–3.2). A quota is keyed
// by the CHAIN ROOT of a session: a relay never copies or moves it, so every session of the chain reads one row.
// The rule that spends it is RQ-1b and lives behind its own host switch; nothing here spends anything.

// ErrQuotaExhausted is an automatic approval of a self relay that found the chain's self_left at 0: the transaction
// rolls back and the row stays open for a person (RQ-1b).
var ErrQuotaExhausted = errors.New("relay quota exhausted")

// ErrLineageCycle is a session_lineage that loops (impossible by construction, guarded anyway).
var ErrLineageCycle = errors.New("session_lineage has a cycle")

// chainRootIn walks sid's lineage parents on q (a database or a transaction) and returns the root of its chain: a
// session with no predecessor (a session never relayed is its own root). No fixed depth — the lineage contract is
// uncapped — only a cycle is an error. hops is how many parents it walked.
func chainRootIn(q queryer, sid string) (root string, hops int, err error) {
	seen := map[string]struct{}{sid: {}}
	cur := sid
	for {
		var parent string
		err := q.QueryRow(`SELECT predecessor_session_id FROM session_lineage WHERE session_id = ?`, cur).Scan(&parent)
		if errors.Is(err, sql.ErrNoRows) {
			return cur, hops, nil
		}
		if err != nil {
			return "", 0, fmt.Errorf("chain root of %s: %w", sid, err)
		}
		if _, dup := seen[parent]; dup {
			return "", 0, fmt.Errorf("chain root of %s: %w (at %s)", sid, ErrLineageCycle, parent)
		}
		seen[parent] = struct{}{}
		cur = parent
		hops++
	}
}

// queryer is the read half of a database or a transaction.
type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

// quotaRow is a relay_quotas row.
type quotaRow struct {
	team.RelayQuota
	UpdatedAt int64
	UpdatedBy string
}

func quotaOfRootIn(q queryer, root string) (quotaRow, error) {
	var r quotaRow
	err := q.QueryRow(`SELECT self_left, member_pool_left, updated_at, updated_by, rev FROM relay_quotas WHERE root_session_id = ?`, root).
		Scan(&r.SelfLeft, &r.MemberPoolLeft, &r.UpdatedAt, &r.UpdatedBy, &r.Rev)
	if errors.Is(err, sql.ErrNoRows) {
		return quotaRow{}, nil // no row = both 0
	}
	if err != nil {
		return quotaRow{}, fmt.Errorf("read relay quota of %s: %w", root, err)
	}
	return r, nil
}

// RelayQuotaOf is the numbers of sid's chain and the chain's root.
func (s *Store) RelayQuotaOf(sid string) (team.RelayQuota, string, error) {
	root, _, err := chainRootIn(s.db, sid)
	if err != nil {
		return team.RelayQuota{}, "", err
	}
	r, err := quotaOfRootIn(s.db, root)
	return r.RelayQuota, root, err
}

// RelayQuotasOf is RelayQuotaOf for many sessions (the roster's build, every liveness tick): TWO reads in one
// transaction however many sessions and however deep their chains — the lineage once, the quota rows once — and the
// roots resolved in memory with a visited set (a lineage row per relay ever made is small). sid → numbers and
// sid → root; a session whose chain loops is left out of both and the first such error is returned with what was read.
func (s *Store) RelayQuotasOf(sids []string) (map[string]team.RelayQuota, map[string]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	parent := map[string]string{}
	rows, err := tx.Query(`SELECT session_id, predecessor_session_id FROM session_lineage`)
	if err != nil {
		return nil, nil, fmt.Errorf("read lineage: %w", err)
	}
	for rows.Next() {
		var sid, pred string
		if err := rows.Scan(&sid, &pred); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("read lineage: %w", err)
		}
		parent[sid] = pred
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, fmt.Errorf("read lineage: %w", err)
	}
	rows.Close()
	byRoot := map[string]team.RelayQuota{}
	qrows, err := tx.Query(`SELECT root_session_id, self_left, member_pool_left, rev FROM relay_quotas`)
	if err != nil {
		return nil, nil, fmt.Errorf("read relay quotas: %w", err)
	}
	defer qrows.Close()
	for qrows.Next() {
		var root string
		var q team.RelayQuota
		if err := qrows.Scan(&root, &q.SelfLeft, &q.MemberPoolLeft, &q.Rev); err != nil {
			return nil, nil, fmt.Errorf("read relay quotas: %w", err)
		}
		byRoot[root] = q
	}
	if err := qrows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read relay quotas: %w", err)
	}
	quotas := make(map[string]team.RelayQuota, len(sids))
	roots := make(map[string]string, len(sids))
	var firstErr error
	for _, sid := range sids {
		if _, done := roots[sid]; done || sid == "" {
			continue
		}
		seen := map[string]struct{}{sid: {}}
		cur := sid
		for {
			p, ok := parent[cur]
			if !ok {
				break
			}
			if _, dup := seen[p]; dup {
				if firstErr == nil {
					firstErr = fmt.Errorf("chain root of %s: %w (at %s)", sid, ErrLineageCycle, p)
				}
				cur = ""
				break
			}
			seen[p] = struct{}{}
			cur = p
		}
		if cur == "" {
			continue
		}
		quotas[sid], roots[sid] = byRoot[cur], cur
	}
	return quotas, roots, firstErr
}

// SetRelayQuota writes the given fields (nil: unchanged) of sid's chain root in one transaction and returns the row.
// updatedBy is the audit label. The values are the caller's to have checked (0..team.MaxRelayQuota).
func (s *Store) SetRelayQuota(sid string, self, pool *int, at int64, updatedBy string) (root string, row quotaRow, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", quotaRow{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	// A write first, so SQLite takes the write lock before the reads below (as closeSelfRelayApprovedIn does).
	if _, err = tx.Exec(`UPDATE relay_quotas SET updated_at = updated_at WHERE root_session_id = ''`); err != nil {
		return "", quotaRow{}, fmt.Errorf("lock relay quotas: %w", err)
	}
	if root, _, err = chainRootIn(tx, sid); err != nil {
		return "", quotaRow{}, err
	}
	if row, err = quotaOfRootIn(tx, root); err != nil {
		return "", quotaRow{}, err
	}
	if self != nil {
		row.SelfLeft = *self
	}
	if pool != nil {
		row.MemberPoolLeft = *pool
	}
	row.UpdatedAt, row.UpdatedBy = at, updatedBy
	row.Rev++ // read under the write lock taken above: the chain's versions are strictly increasing
	if _, err = tx.Exec(`INSERT INTO relay_quotas (root_session_id, self_left, member_pool_left, updated_at, updated_by, rev)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(root_session_id) DO UPDATE SET self_left = excluded.self_left, member_pool_left = excluded.member_pool_left,
			updated_at = excluded.updated_at, updated_by = excluded.updated_by, rev = excluded.rev`,
		root, row.SelfLeft, row.MemberPoolLeft, row.UpdatedAt, row.UpdatedBy, row.Rev); err != nil {
		return "", quotaRow{}, fmt.Errorf("write relay quota of %s: %w", root, err)
	}
	if err = tx.Commit(); err != nil {
		return "", quotaRow{}, fmt.Errorf("commit: %w", err)
	}
	return root, row, nil
}

// KnownSession says whether team.db has ever heard of sid: a lead, a member or either end of a lineage row.
func (s *Store) KnownSession(sid string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 WHERE EXISTS (SELECT 1 FROM teams WHERE lead_session_id = ?)
		OR EXISTS (SELECT 1 FROM team_members WHERE session_id = ?)
		OR EXISTS (SELECT 1 FROM session_lineage WHERE session_id = ? OR predecessor_session_id = ?)`, sid, sid, sid, sid).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("known session %s: %w", sid, err)
	}
	return true, nil
}

// ProcessOfSession is the process team.db recorded for a session that is a member (its row) or leads a team (its lead
// request's origin): pid and start time, 0 and "" when it knows none. pendingLineage uses it to tie a new session id to
// a relay in flight; the start time guards against a pid the OS has reused.
func (s *Store) ProcessOfSession(sid string) (pid int, procStart string, err error) {
	var p sql.NullInt64
	var ps sql.NullString
	err = s.db.QueryRow(`SELECT pid, proc_start FROM team_members WHERE session_id = ? AND pid > 0 ORDER BY created_at DESC LIMIT 1`, sid).Scan(&p, &ps)
	if err == nil && p.Valid {
		return int(p.Int64), ps.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("process of %s: %w", sid, err)
	}
	err = s.db.QueryRow(`SELECT CASE WHEN json_valid(a.origin_json) THEN json_extract(a.origin_json, '$.pid') END,
		CASE WHEN json_valid(a.origin_json) THEN json_extract(a.origin_json, '$.proc_start') END
		FROM teams t JOIN approval_requests a ON a.id = t.request_id WHERE t.lead_session_id = ? LIMIT 1`, sid).Scan(&p, &ps)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !p.Valid) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("process of %s: %w", sid, err)
	}
	return int(p.Int64), ps.String, nil
}

// spendSelfQuotaIn spends one of the self_left of sid's chain on tx (the approve's own transaction, write lock already
// taken). The chain root is walked on the same transaction; the UPDATE is guarded by self_left >= 1, so of two
// automatic approvals racing for the last one exactly one succeeds. Zero rows is ErrQuotaExhausted: the caller returns
// it and the transaction rolls back with the row still open.
func spendSelfQuotaIn(tx *sql.Tx, sid string, at int64) error {
	root, _, err := chainRootIn(tx, sid)
	if err != nil {
		return err
	}
	// rev + 1 in the same statement: a spend is a write of the chain's row like a PUT, and the App orders by rev.
	res, err := tx.Exec(`UPDATE relay_quotas SET self_left = self_left - 1, updated_at = ?, rev = rev + 1
		WHERE root_session_id = ? AND self_left >= 1`, at, root)
	if err != nil {
		return fmt.Errorf("spend relay quota of %s: %w", root, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("spend relay quota of %s: %w", root, err)
	}
	if n == 0 {
		return ErrQuotaExhausted
	}
	return nil
}

// quotaRuleOn reads the rule's switch, once per decision (flipping it needs no restart). Off when the module has no
// reader, and off — logged once per distinct error — when the stored value does not read: the host then behaves as it
// did before the rule existed.
func (m *Module) quotaRuleOn() bool {
	if m.quotaRule == nil {
		return false
	}
	on, err := m.quotaRule.RelayQuotaRule()
	if err != nil {
		if err.Error() != m.quotaRuleErr {
			m.quotaRuleErr = err.Error()
			m.logf("[team] relay_quota switch unreadable, treated as off: %v", err)
		}
		return false
	}
	m.quotaRuleErr = ""
	return on
}

// withQuotaRule marks c to spend a self relay's quota when the rule is on. Only the daemon's own closes (Auto) are
// ever honoured by the store, so a click never reaches the rule.
func (m *Module) withQuotaRule(c Close, kind team.Kind) Close {
	if (kind == team.KindSelfRelay || kind == team.KindMemberRelay) && c.Auto && m.quotaRuleOn() {
		c.SpendQuota = true
		c.SpentOut = new(bool)
	}
	return c
}

// holdForQuota / unhold keep the set of open rows waiting for quota. hold returns whether the row is newly held.
func (m *Module) holdForQuota(id string) bool {
	m.heldMu.Lock()
	defer m.heldMu.Unlock()
	if _, was := m.heldQuota[id]; was {
		return false
	}
	if m.heldQuota == nil {
		m.heldQuota = map[string]struct{}{}
	}
	m.heldQuota[id] = struct{}{}
	return true
}

func (m *Module) unhold(id string) {
	m.heldMu.Lock()
	delete(m.heldQuota, id)
	m.heldMu.Unlock()
}

// fillHeld fills v.Held: the open self_relay rows waiting for quota (the panel says "額度用完，等你核准"). [] when
// none; left nil — null on the wire — when the open rows could not be read.
func (m *Module) fillHeld(v *team.UnattendedView) {
	m.heldMu.Lock()
	ids := make(map[string]struct{}, len(m.heldQuota))
	for id := range m.heldQuota {
		ids[id] = struct{}{}
	}
	m.heldMu.Unlock()
	held := []team.Approval{}
	if v.On && len(ids) > 0 { // with the switch off nothing is "held for quota": every request waits for a person anyway
		open, err := m.store.ListOpen()
		if err != nil {
			m.logf("[team] unattended held: %v", err)
			return
		}
		for _, a := range open {
			if _, ok := ids[a.ID]; ok && a.Kind == team.KindSelfRelay {
				held = append(held, a)
			}
		}
	}
	v.Held = held
}

// markSpent / takeSpent carry "this approval's transaction spent a unit" from the approve (which knows) to the winner
// point afterApproved (which publishes), without re-reading a mutable switch.
func (m *Module) markSpent(approvalID string) {
	m.heldMu.Lock()
	defer m.heldMu.Unlock()
	if m.spent == nil {
		m.spent = map[string]struct{}{}
	}
	m.spent[approvalID] = struct{}{}
}

func (m *Module) takeSpent(approvalID string) bool {
	m.heldMu.Lock()
	defer m.heldMu.Unlock()
	_, ok := m.spent[approvalID]
	delete(m.spent, approvalID)
	return ok
}

// announceSpend publishes the new numbers of sid's chain after a spend. It runs in the PUT's section (quotaMu) and
// reads the CURRENT row there, so the events of spends and PUTs leave in lock order carrying nondecreasing revs — an
// older snapshot can never follow a newer PUT.
func (m *Module) announceSpend(sid string) {
	m.quotaMu.Lock()
	defer m.quotaMu.Unlock()
	if q, root, err := m.store.RelayQuotaOf(sid); err == nil {
		m.broadcastRelayQuota(root, q)
	} else {
		m.logf("[team] relay quota event after a spend (%s): %v", sid, err)
	}
}
