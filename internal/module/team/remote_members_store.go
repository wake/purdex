// internal/module/team/remote_members_store.go
package teammod

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// remoteMemberSchema is M's table of sessions on THIS host that a lead on
// ANOTHER host has made its members (cross-host team spec §5.1). mk is the
// lead host's member key; one active row per session. No route writes it
// yet (X2b). Idempotent, like every schema here: a team.db written before it
// gets the table; any later change to it is a migration.
const remoteMemberSchema = `
	CREATE TABLE IF NOT EXISTS remote_members (
		mk                TEXT PRIMARY KEY,
		member_session_id TEXT    NOT NULL,
		ref               TEXT    NOT NULL,
		team_id           TEXT    NOT NULL,
		team_name         TEXT    NOT NULL DEFAULT '',
		lead_host_id      TEXT    NOT NULL,
		lead_session_id   TEXT    NOT NULL,
		lead_ref          TEXT    NOT NULL,
		lead_address      TEXT    NOT NULL DEFAULT '',
		lead_title        TEXT    NOT NULL DEFAULT '',
		lead_pid          INTEGER NOT NULL DEFAULT 0,
		lead_proc_start   TEXT    NOT NULL DEFAULT '',
		origin            TEXT    NOT NULL,
		state             TEXT    NOT NULL,
		pid               INTEGER NOT NULL DEFAULT 0,
		proc_start        TEXT    NOT NULL DEFAULT '',
		pane_id           TEXT    NOT NULL DEFAULT '',
		tmux_session      TEXT    NOT NULL DEFAULT '',
		cwd               TEXT    NOT NULL DEFAULT '',
		title             TEXT    NOT NULL DEFAULT '',
		model             TEXT    NOT NULL DEFAULT '',
		effort            TEXT    NOT NULL DEFAULT '',
		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL
	);
	CREATE UNIQUE INDEX IF NOT EXISTS remote_members_one_active ON remote_members (member_session_id) WHERE state = 'active';
	CREATE INDEX IF NOT EXISTS remote_members_lead_host ON remote_members (lead_host_id, state);`

// Remote member states (spec §5.2). A row only moves out of active, by CAS.
const (
	remoteActive   = "active"
	remoteReleased = "released"
	remoteKilled   = "killed"
	remoteGone     = "gone"
	remoteEnded    = "ended"
)

func validRemoteState(s string) bool {
	switch s {
	case remoteActive, remoteReleased, remoteKilled, remoteGone, remoteEnded:
		return true
	}
	return false
}

// remoteMemberRow is one remote_members row. The lead fields come from the
// lead host's commands, so the notice pump can rebuild the reply-capable
// sender after a restart.
type remoteMemberRow struct {
	MK              string
	MemberSessionID string
	Ref             string
	TeamID          string
	TeamName        string
	LeadHostID      string
	LeadSessionID   string
	LeadRef         string
	LeadAddress     string
	LeadTitle       string
	LeadPID         int
	LeadProcStart   string
	Origin          string // adopted | spawned
	State           string
	PID             int
	ProcStart       string
	PaneID          string
	TmuxSession     string
	Cwd             string
	Title           string
	Model           string
	Effort          string
	CreatedAt       int64
	UpdatedAt       int64
}

const remoteMemberCols = `mk, member_session_id, ref, team_id, team_name, lead_host_id, lead_session_id, lead_ref,
	lead_address, lead_title, lead_pid, lead_proc_start, origin, state, pid, proc_start, pane_id, tmux_session,
	cwd, title, model, effort, created_at, updated_at`

func (r *remoteMemberRow) dest() []any {
	return []any{&r.MK, &r.MemberSessionID, &r.Ref, &r.TeamID, &r.TeamName, &r.LeadHostID, &r.LeadSessionID, &r.LeadRef,
		&r.LeadAddress, &r.LeadTitle, &r.LeadPID, &r.LeadProcStart, &r.Origin, &r.State, &r.PID, &r.ProcStart, &r.PaneID,
		&r.TmuxSession, &r.Cwd, &r.Title, &r.Model, &r.Effort, &r.CreatedAt, &r.UpdatedAt}
}

// InsertRemoteMember stores r, idempotent on mk (a command replayed stores
// one row; a stored row is left as it is). A second active row for one
// session (remote_members_one_active), or a row missing mk, session, team or
// lead host or with an unknown state, is an error.
func (s *Store) InsertRemoteMember(r remoteMemberRow) error {
	return insertRemoteMemberIn(s.db, r)
}

func insertRemoteMemberIn(q dbtx, r remoteMemberRow) error {
	if r.MK == "" || r.MemberSessionID == "" || r.TeamID == "" || r.LeadHostID == "" || !validRemoteState(r.State) {
		return fmt.Errorf("insert remote member: mk %q, session %q, team %q, lead host %q and state %q must all be set and the state known",
			r.MK, r.MemberSessionID, r.TeamID, r.LeadHostID, r.State)
	}
	res, err := q.Exec(`INSERT INTO remote_members (`+remoteMemberCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (mk) DO NOTHING`,
		r.MK, r.MemberSessionID, r.Ref, r.TeamID, r.TeamName, r.LeadHostID, r.LeadSessionID, r.LeadRef,
		r.LeadAddress, r.LeadTitle, r.LeadPID, r.LeadProcStart, r.Origin, r.State, r.PID, r.ProcStart, r.PaneID,
		r.TmuxSession, r.Cwd, r.Title, r.Model, r.Effort, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert remote member %s: %w", r.MK, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("insert remote member %s rows affected: %w", r.MK, err)
	} else if n == 1 {
		return nil
	}
	// The mk is taken. Only a replay of the same membership is a success; the
	// lead fields, state and times may have moved on since (lead_moved, a
	// later state), the identity may not.
	var have remoteMemberRow
	if err := q.QueryRow(`SELECT member_session_id, team_id, lead_host_id, origin FROM remote_members WHERE mk = ?`, r.MK).
		Scan(&have.MemberSessionID, &have.TeamID, &have.LeadHostID, &have.Origin); err != nil {
		return fmt.Errorf("insert remote member %s: read the stored row: %w", r.MK, err)
	}
	if have.MemberSessionID != r.MemberSessionID || have.TeamID != r.TeamID || have.LeadHostID != r.LeadHostID || have.Origin != r.Origin {
		return fmt.Errorf("%w (%s)", ErrRemoteMemberConflict, r.MK)
	}
	return nil
}

// ErrRemoteMemberConflict: the member key is stored for another membership
// (session, team, lead host or origin differ) — key reuse, never an
// idempotent replay (spec §3.1 rule 3: id_conflict).
var ErrRemoteMemberConflict = errors.New("the member key is stored for a different membership")

// RemoteMember returns the row with member key mk, in any state.
func (s *Store) RemoteMember(mk string) (remoteMemberRow, bool, error) {
	var r remoteMemberRow
	err := s.db.QueryRow(`SELECT `+remoteMemberCols+` FROM remote_members WHERE mk = ?`, mk).Scan(r.dest()...)
	if errors.Is(err, sql.ErrNoRows) {
		return remoteMemberRow{}, false, nil
	}
	if err != nil {
		return remoteMemberRow{}, false, fmt.Errorf("remote member %s: %w", mk, err)
	}
	return r, true, nil
}

// SetRemoteMemberState is casRemoteMemberStateIn on its own statement.
func (s *Store) SetRemoteMemberState(mk string, from []string, to string, at int64) (bool, error) {
	return casRemoteMemberStateIn(s.db, mk, from, to, at)
}

// casRemoteMemberStateIn moves the row mk to `to` only if it is now in one
// of the `from` states (spec §3.1 rule 5, §5.2): a change whose CAS finds
// another state changes nothing and reports false — the caller ignores it,
// it never forces. Every move in §5.2 leaves active for a terminal state, so
// that is all this accepts: from must be {active} and to a terminal state;
// terminal → active, or one terminal over another, is an error.
func casRemoteMemberStateIn(q dbtx, mk string, from []string, to string, at int64) (bool, error) {
	if !validRemoteState(to) || to == remoteActive || len(from) == 0 || slices.ContainsFunc(from, func(f string) bool { return f != remoteActive }) {
		return false, fmt.Errorf("set remote member %s: %v → %q is not a move of §5.2 (only active → a terminal state)", mk, from, to)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(from)), ", ")
	args := []any{to, at, mk}
	for _, f := range from {
		args = append(args, f)
	}
	res, err := q.Exec(`UPDATE remote_members SET state = ?, updated_at = ? WHERE mk = ? AND state IN (`+marks+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("set remote member %s %s: %w", mk, to, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("set remote member %s rows affected: %w", mk, err)
	}
	return n == 1, nil
}

// LiveRemoteMembersOutsideHosts is §3.2's judgement: the active rows whose
// lead host is not among pairedHostIDs (the host ids of the live peer
// entries). It only reads; ending those rows is X2c's.
func (s *Store) LiveRemoteMembersOutsideHosts(pairedHostIDs []string) ([]remoteMemberRow, error) {
	rows, err := s.db.Query(`SELECT `+remoteMemberCols+` FROM remote_members WHERE state = ? ORDER BY created_at, mk`, remoteActive)
	if err != nil {
		return nil, fmt.Errorf("live remote members: %w", err)
	}
	defer rows.Close()
	out := []remoteMemberRow{}
	for rows.Next() {
		var r remoteMemberRow
		if err := rows.Scan(r.dest()...); err != nil {
			return nil, fmt.Errorf("live remote members: %w", err)
		}
		if !slices.Contains(pairedHostIDs, r.LeadHostID) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// sessionRole is the one role a session has on this host (spec §5.3).
type sessionRole string

const (
	sessionRoleNone         sessionRole = "none"
	sessionRoleLead         sessionRole = "lead"
	sessionRoleMemberLocal  sessionRole = "member_local"  // active member of a live team led on this host
	sessionRoleMemberRemote sessionRole = "member_remote" // active remote member of a team led on another host
)

// isMember: a member of either kind.
func (r sessionRole) isMember() bool {
	return r == sessionRoleMemberLocal || r == sessionRoleMemberRemote
}

// sessionRoleIn is the single role gate (spec §5.3), read on q (a
// transaction's read under its write lock): lead of a live team, active
// member of a live local team, active remote member, else none. Every gate
// that asks "may this session take a role" calls it inside its own
// transaction. A store error is an error, never none.
func sessionRoleIn(q dbtx, sessionID string) (sessionRole, error) {
	return firstRoleIn(q, sessionID, roleChecks)
}

// memberRoleIn is sessionRoleIn without the lead check: whether the session
// is a member, whatever else it is (what isLiveMemberIn always asked).
func memberRoleIn(q dbtx, sessionID string) (sessionRole, error) {
	return firstRoleIn(q, sessionID, roleChecks[1:])
}

type roleCheck struct {
	role  sessionRole
	query string
}

// roleChecks are in priority order: lead first, as relayRole always read it.
var roleChecks = []roleCheck{
	{sessionRoleLead, `SELECT 1 FROM teams WHERE lead_session_id = ? AND ended_at = 0`},
	{sessionRoleMemberLocal, `SELECT 1 FROM team_members m JOIN teams t ON t.id = m.team_id
		WHERE m.session_id = ? AND m.state = 'active' AND t.ended_at = 0`},
	{sessionRoleMemberRemote, `SELECT 1 FROM remote_members WHERE member_session_id = ? AND state = 'active'`},
}

func firstRoleIn(q dbtx, sessionID string, checks []roleCheck) (sessionRole, error) {
	for _, c := range checks {
		var one int
		err := q.QueryRow(c.query, sessionID).Scan(&one)
		if err == nil {
			return c.role, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("role check %s: %w", sessionID, err)
		}
	}
	return sessionRoleNone, nil
}

// SessionRole is sessionRoleIn on the store's pool, for callers outside a
// transaction (the hello's role, the create pre-check).
func (s *Store) SessionRole(sessionID string) (sessionRole, error) {
	return sessionRoleIn(s.db, sessionID)
}
