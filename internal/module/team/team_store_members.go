package teammod

// The member rows the sweeper keeps (P4-6): the persisted statusline reading
// of members and leads (spec §8.5 "Persist it for teams only") and the mark
// of a member whose conversation ended (§7.3 gone).

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

// local is the SQL (and its two args) that keeps a statement to the rows that live on THIS host (cross-host spec §4.2): a
// remote member's session, usage and relays are its own host's. localHostID "" (a bare test store) filters nothing.
func (s *Store) local(col string) (string, []any) {
	return `(? = '' OR ` + col + ` = ?)`, []any{s.localHostID, s.localHostID}
}

// ActiveMembersOfLiveTeams returns every active member row of a live team,
// with its reading, oldest first: what the sweeper looks after. A member of
// an ended team is left as it ended (D4). Local rows only: a remote member's liveness, usage and notices are its own host's
// (cross-host spec §4.2); localHostID "" (tests that open a bare store) filters nothing.
func (s *Store) ActiveMembersOfLiveTeams() ([]memberRow, error) {
	return s.queryMembers("active members", `SELECT `+qualify("m", memberCols+", "+memberUsageCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.state = 'active' AND t.ended_at = 0 AND (? = '' OR m.host_id = ?) ORDER BY m.created_at, m.spawn_op`, s.localHostID, s.localHostID)
}

// liveTeam is a live team with the reading its lead's statusline stored on
// the team row (teams.lead_usage_*), nil when none was ever stored. The team
// scan (teamCols) does not read those columns: team.Team is a wire type, and
// only the roster wants the reading.
type liveTeam struct {
	team.Team
	leadUsage *team.MemberContext
}

// ListLiveTeamsWithLeadUsage returns every live team, oldest first, each
// with its persisted lead reading, in ONE statement: a team that ends
// between two reads cannot be listed without its reading (PL-1f'3 review
// A-2). Never nil.
func (s *Store) ListLiveTeamsWithLeadUsage() ([]liveTeam, error) {
	rows, err := s.db.Query(`SELECT ` + teamCols + `, lead_usage_pct, lead_usage_window, lead_usage_model, lead_usage_effort, lead_usage_at
		FROM teams WHERE ended_at = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list live teams with lead readings: %w", err)
	}
	defer rows.Close()
	out := []liveTeam{}
	for rows.Next() {
		var lt liveTeam
		var grantJSON string
		var u usageScan
		if err := rows.Scan(append(teamDest(&lt.Team, &grantJSON), u.dest()...)...); err != nil {
			return nil, fmt.Errorf("list live teams with lead readings: %w", err)
		}
		if err := decodeTeamGrant(&lt.Team, grantJSON); err != nil {
			return nil, fmt.Errorf("list live teams with lead readings: %w", err)
		}
		lt.leadUsage = u.reading()
		out = append(out, lt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list live teams with lead readings: %w", err)
	}
	return out, nil
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

// ClaimMemberKilling is the first step of pdx kill (#2152 point 2): active → killing, in one compare-and-set on the row read
// — it still holds sessionID, is a local row of an active member, and its session has no relay op in flight. The kill signals
// only if it holds the claim; a release or a relay claim finds a row that is not active and refuses by its usual rules.
// claimed says whether this call took it.
func (s *Store) ClaimMemberKilling(spawnOp, sessionID string, at int64) (bool, error) {
	loc, locArgs := s.local("host_id")
	res, err := s.db.Exec(`UPDATE team_members SET state = 'killing', updated_at = ?
		WHERE spawn_op = ? AND session_id = ? AND state = 'active' AND `+loc+`
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		append([]any{at, spawnOp, sessionID}, append(locArgs, sessionID)...)...)
	return oneRow(res, err, "claim member "+spawnOp+" for a kill")
}

// GiveBackMemberKilling returns a claim whose signal could not be sent: killing → active. given says whether this call did
// (false: the row is no longer killing — a boot, the sweeper or another path ended it). While a row is killing the unique
// index on active rows does not cover it, so a competing membership could have taken the session meanwhile (every creation
// path refuses a session that is killing, but the index is the last word): the give-back then violates it, and the row ends
// gone instead of staying killing for good — the session belongs to the newer membership.
func (s *Store) GiveBackMemberKilling(spawnOp, sessionID string, at int64) (bool, error) {
	loc, locArgs := s.local("host_id")
	res, err := s.db.Exec(`UPDATE team_members SET state = 'active', updated_at = ?
		WHERE spawn_op = ? AND session_id = ? AND state = 'killing' AND `+loc,
		append([]any{at, spawnOp, sessionID}, locArgs...)...)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		_, gerr := s.MarkMemberGone(spawnOp, sessionID, at)
		return false, gerr
	}
	return oneRow(res, err, "give back the kill claim of member "+spawnOp)
}

// MarkMemberKilled is pdx kill's mark (spec §7.3), a compare-and-set on the
// row read: it still holds sessionID (a relay's cleared moves the row to a
// new session, never to be marked killed; P4-6 review R1), it is killing (the
// kill's own claim), active or gone (the sweeper may mark it gone meanwhile),
// and that session has no relay op in flight. killed says whether this call
// marked it.
func (s *Store) MarkMemberKilled(spawnOp, sessionID string, at int64) (bool, error) {
	// ended_at is when the row first left `active`: a row that went gone keeps its time.
	loc, locArgs := s.local("host_id")
	res, err := s.db.Exec(`UPDATE team_members SET state = 'killed', updated_at = ?, ended_at = CASE WHEN ended_at = 0 THEN ? ELSE ended_at END
		WHERE spawn_op = ? AND session_id = ? AND state IN ('active', 'gone', 'killing') AND `+loc+`
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		append([]any{at, at, spawnOp, sessionID}, append(locArgs, sessionID)...)...)
	return oneRow(res, err, "mark member "+spawnOp+" killed")
}

// MarkMemberGone marks an active (or killing: the kill found nothing left to signal) member gone (spec §7.3: its session ended
// without a kill) in one guarded UPDATE that leaves the row as it is when it
// is no longer active, no longer holds sessionID (a relay moved it since the
// caller looked), or its session has a relay op in claimed, writing or
// written: a member mid-relay is never gone (the old session leaves the
// registry about 0.6 s after the relay's /clear while its op is written).
// The guard is in the statement, as EndTeam's is, so a relay claimed after
// the caller looked wins. gone says whether this call marked it.
func (s *Store) MarkMemberGone(spawnOp, sessionID string, at int64) (bool, error) {
	loc, locArgs := s.local("host_id")
	res, err := s.db.Exec(`UPDATE team_members SET state = 'gone', updated_at = ?, ended_at = ?
		WHERE spawn_op = ? AND session_id = ? AND state IN ('active', 'killing') AND `+loc+`
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		append([]any{at, at, spawnOp, sessionID}, append(locArgs, sessionID)...)...)
	return oneRow(res, err, "mark member "+spawnOp+" gone")
}

// LocalKillingMembers lists the local member rows a kill claimed and never ended (the daemon died between the claim and the
// end): the boot settles them (recoverKillingMembers).
func (s *Store) LocalKillingMembers() ([]memberRow, error) {
	loc, locArgs := s.local("host_id")
	rows, err := s.db.Query(`SELECT `+memberCols+` FROM team_members WHERE state = 'killing' AND `+loc+` ORDER BY created_at, spawn_op`, locArgs...)
	if err != nil {
		return nil, fmt.Errorf("list killing members: %w", err)
	}
	defer rows.Close()
	var out []memberRow
	for rows.Next() {
		var r memberRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("list killing members: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StaleKillingMembers lists the local rows of live teams that have been killing since before cutoff (ms): a kill claims and ends
// within moments, so one this old lost its end — a store error after the signal, or a daemon that died — and the sweeper settles it.
func (s *Store) StaleKillingMembers(cutoff int64) ([]memberRow, error) {
	return s.queryMembers("stale killing members", `SELECT `+qualify("m", memberCols+", "+memberUsageCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.state = 'killing' AND m.updated_at < ? AND t.ended_at = 0 AND (? = '' OR m.host_id = ?) ORDER BY m.created_at, m.spawn_op`,
		cutoff, s.localHostID, s.localHostID)
}

// LiveMemberSeat is the live team a local session holds a seat in — active, or killing (its kill may still be given back) —
// for the checks that must refuse to put the session in a second one.
func (s *Store) LiveMemberSeat(sessionID string) (team.Team, bool, error) {
	var t team.Team
	var grantJSON string
	loc, locArgs := s.local("m.host_id")
	err := s.db.QueryRow(`SELECT `+qualify("t", teamCols)+` FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state IN ('active', 'killing') AND t.ended_at = 0 AND `+loc+` LIMIT 1`,
		append([]any{sessionID}, locArgs...)...).Scan(teamDest(&t, &grantJSON)...)
	if errors.Is(err, sql.ErrNoRows) {
		return team.Team{}, false, nil
	}
	if err == nil {
		err = decodeTeamGrant(&t, grantJSON)
	}
	if err != nil {
		return team.Team{}, false, fmt.Errorf("live member seat of %s: %w", sessionID, err)
	}
	return t, true, nil
}
