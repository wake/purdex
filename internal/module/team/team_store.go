package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/team"
)

// ErrLeadHasTeam is returned by CloseLeadApproved when the request's origin
// already leads a live team (spec §6.2 already_lead): the partial unique
// index teams_one_live_per_lead refused the insert, and the close was
// rolled back with it, so the row is still open.
var ErrLeadHasTeam = errors.New("the session already leads a live team")

// ErrNoSuchMember is returned by SetMemberState for an unknown spawn op.
var ErrNoSuchMember = errors.New("no such member")

// ErrMemberCannotLead is returned by CloseLeadApproved when the origin is
// an active member of a live team (spec §6.2 member_cannot_lead): both
// writes rolled back, the row is still open.
var ErrMemberCannotLead = errors.New("the session is an active member of a live team")

// teamSchema is the P4 teams table (spec §7.1). It is run by OpenStore
// after relaySchema; every statement is idempotent, so it is safe on a
// team.db written before it existed. A team's id is the id of the lead
// request that approved it (plan v3 deviation 1), so request_id = id.
// ended_at = 0 while the team is live; one live team per lead session.
// team_members (P4-3, §7.3): one row per spawn op, one active row per session.
const teamSchema = `
	CREATE TABLE IF NOT EXISTS teams (
		id              TEXT PRIMARY KEY,
		host_id         TEXT    NOT NULL,
		lead_session_id TEXT    NOT NULL,
		lead_ref        TEXT    NOT NULL,
		grant_json      TEXT    NOT NULL,
		request_id      TEXT    NOT NULL UNIQUE,
		created_at      INTEGER NOT NULL,
		ended_at        INTEGER NOT NULL DEFAULT 0,
		end_reason      TEXT    NOT NULL DEFAULT ''
	);
	CREATE UNIQUE INDEX IF NOT EXISTS teams_one_live_per_lead ON teams (lead_session_id) WHERE ended_at = 0;
	CREATE TABLE IF NOT EXISTS team_members (
		spawn_op      TEXT PRIMARY KEY,
		team_id       TEXT    NOT NULL,
		host_id       TEXT    NOT NULL,
		session_id    TEXT    NOT NULL,
		ref           TEXT    NOT NULL,
		title         TEXT    NOT NULL DEFAULT '',
		cwd           TEXT    NOT NULL,
		tmux_session  TEXT    NOT NULL,
		tmux_id       TEXT    NOT NULL DEFAULT '',
		tmux_instance TEXT    NOT NULL DEFAULT '',
		pane_id       TEXT    NOT NULL DEFAULT '',
		pid           INTEGER NOT NULL DEFAULT 0,
		proc_start    TEXT    NOT NULL DEFAULT '',
		model         TEXT    NOT NULL DEFAULT '',
		effort        TEXT    NOT NULL DEFAULT '',
		state         TEXT    NOT NULL,
		created_at    INTEGER NOT NULL,
		updated_at    INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS team_members_team ON team_members (team_id, state);
	CREATE UNIQUE INDEX IF NOT EXISTS team_members_one_active ON team_members (session_id) WHERE state = 'active';`

const teamCols = `id, host_id, lead_session_id, lead_ref, grant_json, request_id, created_at, ended_at, end_reason, team_name, team_label`

const memberCols = `spawn_op, team_id, host_id, session_id, ref, title, cwd, tmux_session, tmux_id, tmux_instance, pane_id, pid, proc_start, model, effort, state, created_at, updated_at,
	origin, ended_at, notice_pending, notice_since`

// qualify prefixes every column of cols with alias, for a join.
func qualify(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}

// teamDest is the Scan destination of teamCols; the grant is decoded after.
func teamDest(t *team.Team, grantJSON *string) []any {
	return []any{&t.ID, &t.HostID, &t.LeadSessionID, &t.LeadRef, grantJSON, &t.RequestID,
		&t.CreatedAt, &t.EndedAt, &t.EndReason, &t.TeamName, &t.TeamLabel}
}

func decodeTeamGrant(t *team.Team, grantJSON string) error {
	if err := json.Unmarshal([]byte(grantJSON), &t.Grant); err != nil {
		return fmt.Errorf("decode grant of team %s: %w", t.ID, err)
	}
	return nil
}

func scanTeam(r rowScanner) (team.Team, error) {
	var t team.Team
	var grantJSON string
	if err := r.Scan(teamDest(&t, &grantJSON)...); err != nil {
		return team.Team{}, err
	}
	if err := decodeTeamGrant(&t, grantJSON); err != nil {
		return team.Team{}, err
	}
	return t, nil
}

// memberRow is one team_members row: the lead host's record of a member
// (spec §7.2 step 6, §7.3), stored by the spawn runner (P4-5) once the
// member registered.
type memberRow struct {
	SpawnOp, TeamID, HostID, SessionID, Ref, Title, Cwd string
	TmuxSession, TmuxID, TmuxInstance, PaneID           string
	PID                                                 int
	ProcStart, Model, Effort                            string
	State                                               team.MemberState
	CreatedAt, UpdatedAt                                int64
	// Origin is how the member joined (team.MemberOriginSpawned | MemberOriginAdopted; "" is spawned). For an adopted
	// row SpawnOp holds the adoption's request id (the member key), the wire's SpawnOp stays empty. EndedAt is when the
	// row left `active` (0 while it is). NoticePending is the notice the session is owed ("" | team.NoticeAdopted |
	// team.NoticeReleased) since NoticeSince; the outbox (PL-1d1) drains it.
	Origin        string
	EndedAt       int64
	NoticePending string
	NoticeSince   int64
	// Usage is the persisted statusline reading (P4-6, spec §8.5), nil when
	// none was stored. Only MembersOf reads it.
	Usage *team.MemberContext
}

func (m *memberRow) dest() []any {
	return []any{&m.SpawnOp, &m.TeamID, &m.HostID, &m.SessionID, &m.Ref, &m.Title, &m.Cwd, &m.TmuxSession,
		&m.TmuxID, &m.TmuxInstance, &m.PaneID, &m.PID, &m.ProcStart, &m.Model, &m.Effort, &m.State,
		&m.CreatedAt, &m.UpdatedAt, &m.Origin, &m.EndedAt, &m.NoticePending, &m.NoticeSince}
}

func validMemberState(s team.MemberState) bool {
	switch s {
	case team.MemberActive, team.MemberKilled, team.MemberGone, team.MemberReleased:
		return true
	}
	return false
}

// InsertMember stores m, idempotent on the spawn op (a spawn retried after
// a restart stores one row; a stored row is left as it is). A second
// active row for one session (team_members_one_active), or a row missing
// spawn op, team or session or with an unknown state, is an error.
func (s *Store) InsertMember(m memberRow) error {
	if m.SpawnOp == "" || m.TeamID == "" || m.SessionID == "" || !validMemberState(m.State) {
		return fmt.Errorf("insert member: spawn op %q, team %q, session %q and state %q must all be set and the state known",
			m.SpawnOp, m.TeamID, m.SessionID, m.State)
	}
	return insertMemberIn(context.Background(), s.db, m)
}

// execer is what insertMemberIn writes through: the store's pool or a
// connection that holds the write lock.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertMemberIn(ctx context.Context, q execer, m memberRow) error {
	_, err := insertMemberRowIn(ctx, q, m)
	return err
}

// insertMemberRowIn is insertMemberIn that also says whether a row was inserted (false: the spawn_op was taken
// and the stored row was left as it is).
func insertMemberRowIn(ctx context.Context, q execer, m memberRow) (bool, error) {
	origin := m.Origin
	if origin == "" {
		origin = team.MemberOriginSpawned
	}
	res, err := q.ExecContext(ctx, `INSERT INTO team_members (`+memberCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (spawn_op) DO NOTHING`,
		m.SpawnOp, m.TeamID, m.HostID, m.SessionID, m.Ref, m.Title, m.Cwd, m.TmuxSession, m.TmuxID,
		m.TmuxInstance, m.PaneID, m.PID, m.ProcStart, m.Model, m.Effort, string(m.State), m.CreatedAt, m.UpdatedAt,
		origin, m.EndedAt, m.NoticePending, m.NoticeSince)
	if err != nil {
		return false, fmt.Errorf("insert member %s: %w", m.SpawnOp, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("insert member %s rows affected: %w", m.SpawnOp, err)
	}
	return n == 1, nil
}

// ActiveMemberInLiveTeam returns the session's active member row and its
// team, read in one statement, when that team is live: a killed or gone
// member, or a member of an ended team (D4), is no member (spec §8.7,
// §6.2 member_cannot_lead).
func (s *Store) ActiveMemberInLiveTeam(sessionID string) (memberRow, team.Team, bool, error) {
	var m memberRow
	var t team.Team
	var grantJSON string
	err := s.db.QueryRow(`SELECT `+qualify("m", memberCols)+`, `+qualify("t", teamCols)+`
		FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`, sessionID).
		Scan(append(m.dest(), teamDest(&t, &grantJSON)...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return memberRow{}, team.Team{}, false, nil
	}
	if err == nil {
		err = decodeTeamGrant(&t, grantJSON)
	}
	if err != nil {
		return memberRow{}, team.Team{}, false, fmt.Errorf("active member %s: %w", sessionID, err)
	}
	return m, t, true, nil
}

// CloseSelfRelayApproved is the approve of a self_relay row (spec §8.7 (b))
// in one write transaction (P4-3 review H2). Unless sessionID is an active
// member of a live team it is CloseIfOpen(c). If it is (U13), the row
// closes cancelled instead and its awaiting op becomes
// cancelled{member_relay_is_leads}, both or neither; memberCancelled says
// that committed. The row and won are as CloseIfOpen's.
func (s *Store) CloseSelfRelayApproved(id string, c Close, sessionID string) (a team.Approval, won, memberCancelled bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return team.Approval{}, false, false, fmt.Errorf("approve self relay %s: begin: %w", id, err)
	}
	defer tx.Rollback()
	n, memberCancelled, err := s.closeSelfRelayApprovedIn(tx, id, c, sessionID)
	if err == nil {
		a, _, err = getRowIn(tx, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return team.Approval{}, false, false, fmt.Errorf("approve self relay %s: %w", id, err)
	}
	return a, n == 1, memberCancelled, nil
}

// closeSelfRelayApprovedIn is CloseSelfRelayApproved's statements on the
// caller's transaction — the click's (through CloseSelfRelayApproved) and
// the unattended create's (CreateSelfRelayApproved) — so both run the same
// SQL. n is the CAS's RowsAffected (1: this close won); memberCancelled
// says the row and its awaiting op were cancelled instead (U13).
func (s *Store) closeSelfRelayApprovedIn(tx *sql.Tx, id string, c Close, sessionID string) (n int64, memberCancelled bool, err error) {
	// A write first, so SQLite takes the write lock before the member read.
	_, err = tx.Exec(`UPDATE approval_requests SET id = id WHERE id = ?`, id)
	var member bool
	if err == nil {
		member, err = isLiveMemberIn(tx, sessionID)
	}
	if member {
		c = Close{State: team.StateCancelled, DecidedAt: c.DecidedAt, UnexpiredAt: c.UnexpiredAt}
	}
	if err == nil {
		n, err = closeRowIn(tx, id, c, "", 0)
	}
	// The relay-quota rule (#2062): only a close the daemon makes itself (Auto), of a row that is really approved
	// (not the member's cancel above), and only when the module asked for it, spends — in this transaction.
	if err == nil && !member && n == 1 && c.Auto && c.SpendQuota {
		err = spendSelfQuotaIn(tx, sessionID, c.DecidedAt)
	}
	if err == nil && member && n == 1 && s.beforeMemberCancelOp != nil {
		err = s.beforeMemberCancelOp()
	}
	if err == nil && member && n == 1 {
		_, err = tx.Exec(`UPDATE relay_ops SET state = ?, reason = ?, updated_at = ? WHERE request_id = ? AND state = ?`,
			string(team.RelayCancelled), team.ErrMemberRelayIsLeads, c.DecidedAt, id, string(team.RelayAwaitingApproval))
	}
	if err != nil {
		return 0, false, err
	}
	return n, member && n == 1, nil
}

// isLiveMemberIn reports, on q (a transaction's read under its write lock),
// whether sessionID is an active member of a live team.
func isLiveMemberIn(q dbtx, sessionID string) (bool, error) {
	var one int
	err := q.QueryRow(`SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("member check %s: %w", sessionID, err)
	}
	return true, nil
}

// MembersOf returns every member row of the team, in any state, with its
// persisted reading, oldest first. Never nil.
func (s *Store) MembersOf(teamID string) ([]memberRow, error) {
	return s.queryMembers("members of "+teamID, `SELECT `+memberCols+`, `+memberUsageCols+`
		FROM team_members WHERE team_id = ? ORDER BY created_at, spawn_op`, teamID)
}

// SetMemberState sets the member's state at at (spec §7.3: killed by pdx
// kill, gone when its session ended). ErrNoSuchMember for an unknown spawn op.
func (s *Store) SetMemberState(spawnOp string, state team.MemberState, at int64) error {
	if !validMemberState(state) {
		return fmt.Errorf("set member %s: unknown state %q", spawnOp, state)
	}
	res, err := s.db.Exec(`UPDATE team_members SET state = ?, updated_at = ? WHERE spawn_op = ?`, string(state), at, spawnOp)
	if err != nil {
		return fmt.Errorf("set member %s %s: %w", spawnOp, state, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set member %s rows affected: %w", spawnOp, err)
	}
	if n == 0 {
		return fmt.Errorf("set member %s: %w", spawnOp, ErrNoSuchMember)
	}
	return nil
}

// CloseLeadApproved is the approve of a lead request (spec §6.2: "Approval
// creates the team (§7.1) in the same transaction"). In one transaction it
// runs the open CAS (closeRowIn) and, only when that changed the row,
// inserts t. Either both land or neither does: when the origin already
// leads a live team the insert violates teams_one_live_per_lead, both roll
// back and the error is ErrLeadHasTeam — the row stays open. Otherwise the
// returned row and won are what closeWhere returns: the row after the
// attempt (the winner's close, for a loser too) and whether this call won.
// c must be an approval with a grant, and t's id and request id must be
// the request's id; anything else is an error and writes nothing.
func (s *Store) CloseLeadApproved(id string, c Close, t team.Team) (team.Approval, bool, error) {
	fail := func(err error) (team.Approval, bool, error) {
		return team.Approval{}, false, fmt.Errorf("approve lead %s: %w", id, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(fmt.Errorf("begin: %w", err))
	}
	defer tx.Rollback()
	n, err := closeLeadApprovedIn(tx, id, c, t)
	if err != nil {
		return fail(err)
	}
	a, _, err := getRowIn(tx, id) // ErrNoSuchApproval (wrapped) for an unknown id
	if err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(fmt.Errorf("commit: %w", err))
	}
	return a, n == 1, nil
}

// closeLeadApprovedIn is CloseLeadApproved's statements on the caller's
// transaction — the click's (through CloseLeadApproved) and the unattended
// create's (CreateApproved) — so both run the same SQL: the open CAS and,
// only when it changed the row, the member re-check and the team insert.
// n is the CAS's RowsAffected (1: this close won). A misuse (see
// CloseLeadApproved), ErrLeadHasTeam and ErrMemberCannotLead are errors;
// the caller rolls back on any error, so nothing is written.
func closeLeadApprovedIn(tx *sql.Tx, id string, c Close, t team.Team) (int64, error) {
	if c.State != team.StateApproved || c.Grant == nil {
		return 0, fmt.Errorf("a team needs an approval with a grant (state %q, grant set %v)", c.State, c.Grant != nil)
	}
	if t.ID != id || t.RequestID != id {
		return 0, fmt.Errorf("team id %q and request id %q must both be the request's id", t.ID, t.RequestID)
	}
	grantJSON, err := json.Marshal(t.Grant)
	if err != nil {
		return 0, fmt.Errorf("encode grant: %w", err)
	}
	// In CloseLeadApproved this is the first statement, a write, so SQLite
	// takes the write lock at once.
	n, err := closeRowIn(tx, id, c, "", 0)
	if err != nil {
		return 0, err
	}
	if n == 1 {
		// No nested teams, re-checked under the write lock (P4-3 review H1):
		// the origin may have become a member since its request was created.
		if member, err := isLiveMemberIn(tx, t.LeadSessionID); err != nil {
			return 0, err
		} else if member {
			return 0, ErrMemberCannotLead
		}
		if _, err := tx.Exec(`INSERT INTO teams (`+teamCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?)`,
			t.ID, t.HostID, t.LeadSessionID, t.LeadRef, string(grantJSON), t.RequestID, t.CreatedAt, t.TeamName, t.TeamLabel); err != nil {
			if strings.Contains(err.Error(), "teams.lead_session_id") { // the partial unique index teams_one_live_per_lead
				return 0, ErrLeadHasTeam
			}
			return 0, fmt.Errorf("insert team: %w", err)
		}
	}
	return n, nil
}

// LiveTeamByLead returns the live team (ended_at = 0) the session leads, if
// any; an ended team does not count (spec §6.2 already_lead).
func (s *Store) LiveTeamByLead(sessionID string) (team.Team, bool, error) {
	t, err := scanTeam(s.db.QueryRow(`SELECT `+teamCols+` FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Team{}, false, nil
	}
	if err != nil {
		return team.Team{}, false, fmt.Errorf("live team by lead %s: %w", sessionID, err)
	}
	return t, true, nil
}

// TeamByID returns the team with id, live or ended.
func (s *Store) TeamByID(id string) (team.Team, bool, error) {
	t, err := scanTeam(s.db.QueryRow(`SELECT `+teamCols+` FROM teams WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return team.Team{}, false, nil
	}
	if err != nil {
		return team.Team{}, false, fmt.Errorf("team %s: %w", id, err)
	}
	return t, true, nil
}

// ListLiveTeams returns every live team, oldest first. Never nil.
func (s *Store) ListLiveTeams() ([]team.Team, error) {
	rows, err := s.db.Query(`SELECT ` + teamCols + ` FROM teams WHERE ended_at = 0 ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list live teams: %w", err)
	}
	defer rows.Close()
	out := []team.Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("list live teams: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list live teams: %w", err)
	}
	return out, nil
}

// EndTeam ends a live team (spec §7.1) with reason at at, in one guarded
// UPDATE that leaves the team as it is when it already ended; when its
// lead is no longer leadSessionID, the lead the caller saw (a relay moved
// it since: P4-3's cleared transaction); or when that lead has a relay op
// in claimed, writing or written — a team never ends mid-relay (the old
// session id leaves the registry about 0.6 s after a relay's /clear while
// its op is still written). The relay guard is in the statement itself, so
// a relay claimed after the caller looked still wins. ended says whether
// this call ended it. Members are not touched (D4).
func (s *Store) EndTeam(id, leadSessionID, reason string, at int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE teams SET ended_at = ?, end_reason = ?
		WHERE id = ? AND lead_session_id = ? AND ended_at = 0
		  AND NOT EXISTS (SELECT 1 FROM relay_ops
			WHERE session_id = ? AND state IN ('claimed', 'writing', 'written'))`,
		at, reason, id, leadSessionID, leadSessionID)
	if err != nil {
		return false, fmt.Errorf("end team %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("end team %s rows affected: %w", id, err)
	}
	return n == 1, nil
}
