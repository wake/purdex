package teammod

// The member rows the sweeper keeps (P4-6): the persisted statusline reading
// of members and leads (spec §8.5 "Persist it for teams only") and the mark
// of a member whose conversation ended (§7.3 gone).

import (
	"database/sql"
	"fmt"

	"github.com/wake/purdex/internal/team"
)

const memberUsageCols = `usage_pct, usage_window, usage_model, usage_effort, usage_at`

// usageScan is the Scan destination of memberUsageCols.
type usageScan struct {
	pct           sql.NullFloat64
	window        int
	model, effort string
	at            int64
}

func (u *usageScan) dest() []any { return []any{&u.pct, &u.window, &u.model, &u.effort, &u.at} }

// reading is the stored reading, nil when none was ever stored (at = 0).
func (u usageScan) reading() *team.MemberContext {
	if u.at <= 0 {
		return nil
	}
	c := &team.MemberContext{Window: u.window, ModelID: u.model, Effort: u.effort, At: u.at}
	if u.pct.Valid {
		v := u.pct.Float64
		c.UsedPercentage = &v
	}
	return c
}

// queryMembers runs a SELECT of memberCols then memberUsageCols and scans
// each row with its reading. Never nil.
func (s *Store) queryMembers(what, query string, args ...any) ([]memberRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer rows.Close()
	out := []memberRow{}
	for rows.Next() {
		var m memberRow
		var u usageScan
		if err := rows.Scan(append(m.dest(), u.dest()...)...); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		m.Usage = u.reading()
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	return out, nil
}

// ActiveMembersOfLiveTeams returns every active member row of a live team,
// with its reading, oldest first: what the sweeper looks after. A member of
// an ended team is left as it ended (D4).
func (s *Store) ActiveMembersOfLiveTeams() ([]memberRow, error) {
	return s.queryMembers("active members", `SELECT `+qualify("m", memberCols+", "+memberUsageCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.state = 'active' AND t.ended_at = 0 ORDER BY m.created_at, m.spawn_op`)
}

func pctArg(c team.MemberContext) any {
	if c.UsedPercentage == nil {
		return nil
	}
	return *c.UsedPercentage
}

// SetMemberUsage stores reading c on the member's row when it is newer than
// the stored one and the row still holds sessionID (a relay may have moved
// it since the caller read it). stored says whether it was written.
func (s *Store) SetMemberUsage(spawnOp, sessionID string, c team.MemberContext) (bool, error) {
	res, err := s.db.Exec(`UPDATE team_members SET usage_pct = ?, usage_window = ?, usage_model = ?, usage_effort = ?, usage_at = ?
		WHERE spawn_op = ? AND session_id = ? AND usage_at < ?`,
		pctArg(c), c.Window, c.ModelID, c.Effort, c.At, spawnOp, sessionID, c.At)
	return oneRow(res, err, "store the reading of member "+spawnOp)
}

// SetLeadUsage stores reading c on a live team's row as its lead's, when it
// is newer than the stored one and the team is still led by leadSessionID.
func (s *Store) SetLeadUsage(teamID, leadSessionID string, c team.MemberContext) (bool, error) {
	res, err := s.db.Exec(`UPDATE teams SET lead_usage_pct = ?, lead_usage_window = ?, lead_usage_model = ?,
			lead_usage_effort = ?, lead_usage_at = ?
		WHERE id = ? AND lead_session_id = ? AND ended_at = 0 AND lead_usage_at < ?`,
		pctArg(c), c.Window, c.ModelID, c.Effort, c.At, teamID, leadSessionID, c.At)
	return oneRow(res, err, "store the reading of the lead of team "+teamID)
}

// MarkMemberKilled is pdx kill's mark (spec §7.3), a compare-and-set on the
// row read: it still holds sessionID (a relay's cleared moves the row to a
// new session, never to be marked killed; P4-6 review R1), it is active or
// gone (the sweeper may mark it gone meanwhile), and that session has no
// relay op in flight. killed says whether this call marked it.
func (s *Store) MarkMemberKilled(spawnOp, sessionID string, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE team_members SET state = 'killed', updated_at = ?
		WHERE spawn_op = ? AND session_id = ? AND state IN ('active', 'gone')
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		at, spawnOp, sessionID, sessionID)
	return oneRow(res, err, "mark member "+spawnOp+" killed")
}

// MarkMemberGone marks an active member gone (spec §7.3: its session ended
// without a kill) in one guarded UPDATE that leaves the row as it is when it
// is no longer active, no longer holds sessionID (a relay moved it since the
// caller looked), or its session has a relay op in claimed, writing or
// written: a member mid-relay is never gone (the old session leaves the
// registry about 0.6 s after the relay's /clear while its op is written).
// The guard is in the statement, as EndTeam's is, so a relay claimed after
// the caller looked wins. gone says whether this call marked it.
func (s *Store) MarkMemberGone(spawnOp, sessionID string, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE team_members SET state = 'gone', updated_at = ?
		WHERE spawn_op = ? AND session_id = ? AND state = 'active'
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		at, spawnOp, sessionID, sessionID)
	return oneRow(res, err, "mark member "+spawnOp+" gone")
}
