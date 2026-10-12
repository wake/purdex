// internal/module/team/pause_store.go — the pause state (unattended quota pause spec 2026-10-12 §3.1, UP-1a).
package teammod

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// pauseSchema is the pause state: session_pauses (one row per (session, cause); a session is paused iff it has any row),
// the outbox of flow notices, reask controls and push requests (written in the transaction that causes them), the bounced
// messages a paused session gets listed when it resumes, and the one-time release tokens. UP-1a creates and reads them;
// entering and leaving a pause, the outbox loop and the refusal of deliveries are later phases. Idempotent, like every schema
// here: a team.db written before it gets the tables.
//
// pause_bounces.from_addr is the spec's `from` column (a reserved word in SQL).
const pauseSchema = `
	CREATE TABLE IF NOT EXISTS session_pauses (
		session_id      TEXT    NOT NULL,
		cause           TEXT    NOT NULL CHECK (cause IN ('quota', 'lead')),
		role            TEXT    NOT NULL CHECK (role IN ('solo', 'lead', 'member')),
		team_id         TEXT    NOT NULL DEFAULT '',
		lead_session_id TEXT    NOT NULL DEFAULT '',
		ref             TEXT    NOT NULL DEFAULT '',
		title           TEXT    NOT NULL DEFAULT '',
		on_since        INTEGER NOT NULL,
		since           INTEGER NOT NULL,
		PRIMARY KEY (session_id, cause)
	);
	CREATE TABLE IF NOT EXISTS pause_outbox (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT    NOT NULL,
		kind       TEXT    NOT NULL,
		text       TEXT    NOT NULL DEFAULT '',
		reask_id   TEXT    NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		attempts   INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS pause_bounces (
		session_id TEXT    NOT NULL,
		from_addr  TEXT    NOT NULL,
		text_hash  TEXT    NOT NULL,
		at         INTEGER NOT NULL,
		first_line TEXT    NOT NULL DEFAULT '',
		PRIMARY KEY (session_id, from_addr, text_hash)
	);
	CREATE TABLE IF NOT EXISTS pause_release_tokens (
		token      TEXT PRIMARY KEY,
		session_id TEXT    NOT NULL,
		created_at INTEGER NOT NULL
	);`

// migratePauseRefTitle gives a session_pauses written before ref and title existed (an earlier build of the pause phase) the two
// columns; the CREATE above already has them for a new team.db.
func migratePauseRefTitle(db *sql.DB) error {
	for _, c := range []string{"ref", "title"} {
		if err := ensureColumn(db, "session_pauses", c, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	return nil
}

// PauseRow is one row of session_pauses. OnSince is the unattended switch's Since at the time of the pause (the host-config
// value changes on every off → on); Since is when this row was written. Ref and Title are the session's own at that moment,
// what GET /api/team/paused shows when the registry no longer lists the session (a solo session has no team row to fall back on).
type PauseRow struct {
	SessionID, Cause, Role, TeamID, LeadSessionID string
	Ref, Title                                    string
	OnSince, Since                                int64
}

// PausedAgg is one paused session as the queries show it: its causes (sorted), the earliest Since of its rows, the role and
// team of its earliest row, and how many bounced messages wait for it.
type PausedAgg struct {
	SessionID, Role, TeamID, LeadSessionID string
	Ref, Title                             string
	Causes                                 []string
	Since                                  int64
	Bounced                                int
}

// AddPause stores p unless a row for (session, cause) already exists, which makes it a no-op (created is false and the
// existing row is left as it is): a cause is written once, however often the thing that causes it is seen.
func (s *Store) AddPause(p PauseRow) (created bool, err error) {
	if p.SessionID == "" {
		return false, fmt.Errorf("add pause: no session")
	}
	res, err := s.db.Exec(`INSERT INTO session_pauses (session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (session_id, cause) DO NOTHING`,
		p.SessionID, p.Cause, p.Role, p.TeamID, p.LeadSessionID, p.Ref, p.Title, p.OnSince, p.Since)
	if err != nil {
		return false, fmt.Errorf("add pause %s/%s: %w", p.SessionID, p.Cause, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("add pause %s/%s rows affected: %w", p.SessionID, p.Cause, err)
	}
	return n == 1, nil
}

// Pauses lists every pause row, oldest first. Never nil.
func (s *Store) Pauses() ([]PauseRow, error) {
	rows, err := s.db.Query(`SELECT session_id, cause, role, team_id, lead_session_id, ref, title, on_since, since FROM session_pauses ORDER BY since, session_id, cause`)
	if err != nil {
		return nil, fmt.Errorf("list pauses: %w", err)
	}
	defer rows.Close()
	out := []PauseRow{}
	for rows.Next() {
		var p PauseRow
		if err := rows.Scan(&p.SessionID, &p.Cause, &p.Role, &p.TeamID, &p.LeadSessionID, &p.Ref, &p.Title, &p.OnSince, &p.Since); err != nil {
			return nil, fmt.Errorf("list pauses: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pauses: %w", err)
	}
	return out, nil
}

// PausedSessions is the paused sessions, earliest pause first (then by session id), each with its causes and its bounce
// count: the answer of GET /api/team/paused. Never nil.
func (s *Store) PausedSessions() ([]PausedAgg, error) {
	rows, err := s.Pauses()
	if err != nil {
		return nil, err
	}
	out := []PausedAgg{}
	idx := map[string]int{}
	for _, r := range rows { // rows are by since: the first row of a session is its earliest
		i, ok := idx[r.SessionID]
		if !ok {
			idx[r.SessionID] = len(out)
			out = append(out, PausedAgg{SessionID: r.SessionID, Role: r.Role, TeamID: r.TeamID, LeadSessionID: r.LeadSessionID, Ref: r.Ref, Title: r.Title, Since: r.Since})
			i = len(out) - 1
		}
		if out[i].Ref == "" {
			out[i].Ref = r.Ref // a later row of the session may have recorded what the earliest did not
		}
		if out[i].Title == "" {
			out[i].Title = r.Title
		}
		out[i].Causes = append(out[i].Causes, r.Cause)
	}
	if len(out) == 0 {
		return out, nil
	}
	counts, err := s.bounceCounts()
	if err != nil {
		return nil, err
	}
	for i := range out {
		slices.Sort(out[i].Causes)
		out[i].Bounced = counts[out[i].SessionID]
	}
	return out, nil
}

func (s *Store) bounceCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT session_id, COUNT(*) FROM pause_bounces GROUP BY session_id`)
	if err != nil {
		return nil, fmt.Errorf("count pause bounces: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var sid string
		var n int
		if err := rows.Scan(&sid, &n); err != nil {
			return nil, fmt.Errorf("count pause bounces: %w", err)
		}
		out[sid] = n
	}
	return out, rows.Err()
}

// PausesAmong maps each of ids that is paused to its causes (sorted) and earliest since; a session that is not paused is
// absent. The roster reads it once for every session it lists. It reads the pause rows (few: only paused sessions have any)
// and keeps the ones asked for, so the number of ids never meets SQLite's limit on bound variables.
func (s *Store) PausesAmong(ids []string) (map[string]team.RosterPause, error) {
	out := map[string]team.RosterPause{}
	if len(ids) == 0 {
		return out, nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	rows, err := s.Pauses()
	if err != nil {
		return nil, err
	}
	for _, r := range rows { // by since: the first row of a session is its earliest
		if _, ok := want[r.SessionID]; !ok {
			continue
		}
		p, seen := out[r.SessionID]
		if !seen {
			p.Since = r.Since
		}
		p.Causes = append(p.Causes, r.Cause)
		out[r.SessionID] = p
	}
	for sid, p := range out {
		slices.Sort(p.Causes)
		out[sid] = p
	}
	return out, nil
}

// LiveLocalMembersOfLead is the members a lead's pause takes with it: the rows of the lead's live team that are `active` and
// live on THIS host — the persisted predicate of ActiveMembersOfLiveTeams. killing, releasing, joining, gone and killed rows,
// a remote member, and the members of an ended team are not live local members.
func (s *Store) LiveLocalMembersOfLead(leadSessionID string) ([]memberRow, error) {
	return s.queryMembers("live local members of a lead", `SELECT `+qualify("m", memberCols+", "+memberUsageCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE t.lead_session_id = ? AND m.state = 'active' AND t.ended_at = 0 AND (? = '' OR m.host_id = ?) ORDER BY m.created_at, m.spawn_op`,
		leadSessionID, s.localHostID, s.localHostID)
}

// What a bounce keeps is bounded, and so is how many a paused session holds: the sender's address and the first line are cut,
// and past maxBounces rows a new (sender, text) is refused all the same but not listed — a flood of different texts cannot grow
// team.db without bound while the session stays paused.
const (
	maxBounceFirstLine = 120
	maxBounceFrom      = 120
	maxBounces         = 100
)

// PauseVerdict is the one question both delivery points ask before a message reaches a session (unattended quota pause spec
// §3.2): is the session paused? If it has any pause row, the message is bounced — a bounce row (session, from, text hash) is
// upserted, so the same sender repeating the same text is one row with the latest time, and a session holds at most maxBounces
// of them — and paused is true; else false and nothing is written.
//
// The common case (not paused) is one read. A paused session takes the write lock first (BEGIN IMMEDIATE) and looks again, so
// the check and the bounce serialise with the resume, which deletes the pause rows and the bounces in one transaction: a
// message is either bounced-and-listed or delivered, never bounced with no one left to list it. A store error is an error, not
// "not paused": the caller holds the message.
func (s *Store) PauseVerdict(sessionID, from, text string, at int64) (paused bool, err error) {
	var one int
	switch err := s.db.QueryRow(`SELECT 1 FROM session_pauses WHERE session_id = ? LIMIT 1`, sessionID).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("pause verdict %s: %w", sessionID, err)
	}
	sum := sha256.Sum256([]byte(text))
	hash := hex.EncodeToString(sum[:])
	from = cutRunes(from, maxBounceFrom)
	err = s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		if err := conn.QueryRowContext(ctx, `SELECT 1 FROM session_pauses WHERE session_id = ? LIMIT 1`, sessionID).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			paused = false // resumed since the read above: the message is delivered
			return nil
		} else if err != nil {
			return err
		}
		paused = true
		var n int
		var have int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(from_addr = ? AND text_hash = ?), 0) FROM pause_bounces WHERE session_id = ?`, from, hash, sessionID).Scan(&n, &have); err != nil {
			return err
		}
		if n >= maxBounces && have == 0 {
			return nil // refused all the same, not listed
		}
		_, err := conn.ExecContext(ctx, `INSERT INTO pause_bounces (session_id, from_addr, text_hash, at, first_line) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (session_id, from_addr, text_hash) DO UPDATE SET at = excluded.at`,
			sessionID, from, hash, at, firstLineOf(text, maxBounceFirstLine))
		return err
	})
	if err != nil {
		return false, fmt.Errorf("pause verdict %s: %w", sessionID, err)
	}
	return paused, nil
}

// cutRunes cuts s to max characters.
func cutRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}

// firstLineOf is the first non-blank line of text, trimmed, cut to max characters.
func firstLineOf(text string, max int) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return cutRunes(line, max)
		}
	}
	return ""
}

// pauseGate is the team module as peers' PauseGate (registered under peersmod.PauseGateKey in Init).
type pauseGate struct{ m *Module }

func (g pauseGate) PauseVerdict(sessionID, from, text string, at int64) (bool, error) {
	return g.m.store.PauseVerdict(sessionID, from, text, at)
}
