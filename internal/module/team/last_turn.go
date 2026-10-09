package teammod

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/module/agent"
)

// The member's last turn (plan T-3a2, spec D-4): the agent module publishes
// every accepted main-turn Stop (agent.TurnEndEvent); this module keeps, for a
// team member, the first sentence of the turn's last assistant message — its
// own words, cut, no model call — on the member's in_progress task, else on
// the member row. Lead and solo sessions are nothing here.

// lastTurnMaxRunes is the longest the stored summary is before its "…".
const lastTurnMaxRunes = 200

// lastTurnSummary is the text rule (spec D-4): the first non-empty line with
// runs of whitespace collapsed; if a sentence ends within 200 runes (。！？, or
// .!? followed by a space or the end) it is cut there, first sentence
// preferred; else a line over 200 runes is cut at 200 and gets "…". Empty text
// gives "".
func lastTurnSummary(text string) string {
	var line string
	for _, l := range strings.Split(text, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			line = strings.Join(f, " ")
			break
		}
	}
	runes := []rune(line)
	limit := min(len(runes), lastTurnMaxRunes)
	for i := 0; i < limit; i++ {
		switch runes[i] {
		case '。', '！', '？':
			return string(runes[:i+1])
		case '.', '!', '?':
			if i+1 == len(runes) || runes[i+1] == ' ' {
				return string(runes[:i+1])
			}
		}
	}
	if len(runes) > lastTurnMaxRunes {
		return string(runes[:lastTurnMaxRunes]) + "…"
	}
	return line
}

// lastTurnWrite says where a turn end was written.
type lastTurnWrite int

const (
	lastTurnNone   lastTurnWrite = iota // not a member, or an older turn than the stored one
	lastTurnTask                        // the member's in_progress task
	lastTurnMember                      // the member row
)

// SetLastTurn records a turn end for the session, in one write transaction:
// for an active member of a live team, on its in_progress task (newest
// updated_at, then the highest seq), else on its row. The write is guarded on
// (last_turn_at, last_turn_seq): an older or equal stamp never replaces a newer
// one, so a delivery out of order or repeated (the feed is at-least-once)
// changes nothing. updated_at is not touched: it ranks the tasks, and a turn is
// not an edit of the task.
func (s *Store) SetLastTurn(sessionID, summary string, at, seq int64) (lastTurnWrite, error) {
	out := lastTurnNone
	err := s.immediateTx(func(ctx context.Context, conn *sql.Conn) error {
		var key, teamID string
		err := conn.QueryRowContext(ctx, `SELECT m.spawn_op, m.team_id FROM team_members m JOIN teams t ON t.id = m.team_id
			WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`, sessionID).Scan(&key, &teamID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("last turn: member of %s: %w", sessionID, err)
		}
		var taskSeq int
		err = conn.QueryRowContext(ctx, `SELECT seq FROM tasks WHERE team_id = ? AND owner_key = ? AND status = 'in_progress'
			ORDER BY updated_at DESC, seq DESC LIMIT 1`, teamID, key).Scan(&taskSeq)
		switch {
		case err == nil:
			res, err := conn.ExecContext(ctx, `UPDATE tasks SET last_turn_summary = ?, last_turn_at = ?, last_turn_seq = ?
				WHERE team_id = ? AND seq = ? AND (last_turn_at < ? OR (last_turn_at = ? AND last_turn_seq < ?))`,
				summary, at, seq, teamID, taskSeq, at, at, seq)
			if err != nil {
				return fmt.Errorf("last turn: task: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				out = lastTurnTask
			}
		case errors.Is(err, sql.ErrNoRows):
			res, err := conn.ExecContext(ctx, `UPDATE team_members SET last_turn_summary = ?, last_turn_at = ?, last_turn_seq = ?
				WHERE spawn_op = ? AND (last_turn_at < ? OR (last_turn_at = ? AND last_turn_seq < ?))`,
				summary, at, seq, key, at, at, seq)
			if err != nil {
				return fmt.Errorf("last turn: member: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				out = lastTurnMember
			}
		default:
			return fmt.Errorf("last turn: current task: %w", err)
		}
		return nil
	})
	if err != nil {
		return lastTurnNone, err
	}
	return out, nil
}

// MemberLastTurnAts is the last_turn_at of each of the team's members that has
// one, by member key (spawn_op): the display's LAST for a member with no task.
func (s *Store) MemberLastTurnAts(teamID string) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT spawn_op, last_turn_at FROM team_members WHERE team_id = ? AND last_turn_at > 0`, teamID)
	if err != nil {
		return nil, fmt.Errorf("member last turns of %s: %w", teamID, err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var key string
		var at int64
		if err := rows.Scan(&key, &at); err != nil {
			return nil, err
		}
		out[key] = at
	}
	return out, rows.Err()
}

// turnEndSubscriber is the part of the agent module's TerminalSessions this
// module uses; asserted on the registry service so a daemon (or test) without
// the agent module simply records no last turns.
type turnEndSubscriber interface {
	SubscribeTurnEnd(fn func(agent.TurnEndEvent)) (unsubscribe func())
}

// subscribeTurnEnd starts the feed. Called from Start, after every Init.
func (m *Module) subscribeTurnEnd(svc any) {
	if s, ok := svc.(turnEndSubscriber); ok {
		m.unsubTurnEnd = s.SubscribeTurnEnd(m.onTurnEnd)
	}
}

// onTurnEnd runs on the subscriber's own goroutine, under none of the agent
// module's locks. A non-member (or a lead, or a solo session) writes nothing and logs
// nothing: this fires for every turn of every session on the host.
func (m *Module) onTurnEnd(ev agent.TurnEndEvent) {
	summary := lastTurnSummary(ev.Text)
	if summary == "" || m.store == nil || m.stopping() {
		return
	}
	where, err := m.store.SetLastTurn(ev.SessionID, summary, ev.At, ev.Seq)
	if err != nil {
		m.logf("[team] %v", err)
		return
	}
	if where == lastTurnTask {
		m.rosterChanged()
	}
}
