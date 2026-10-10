// internal/module/team/relay_moved.go
package teammod

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/wake/purdex/internal/team"
)

// Member relay MR-2 (docs/specs/2026-10-10-member-relay-manual-and-cross-host-spec-plan.md D4, D7, §3.2): a person's /relay in a
// member whose lead is on another host. The relay runs on the member host (M) like any other; when it clears, M moves its
// remote_members row to the new session and queues a `moved` fact for the lead host (L) in that same transaction. L applies the
// fact to its row, keeps the previous ref (so the ref a lead typed yesterday still names the member), and tells the lead.

// remoteRefSchema is L's table of the refs a remote member had before a relay moved it (D7). A new table: nothing to migrate.
// (team_id, host_id, ref) is the key: a ref names one session of one host, and the same session may have been a member of
// another team of this host before. mk is the member the ref belonged to; the row it names is
// found through (team, host, mk), never by the ref again, so a stale entry can only ever point at the member it was kept for.
const remoteRefSchema = `
	CREATE TABLE IF NOT EXISTS remote_member_refs (
		team_id TEXT    NOT NULL,
		host_id TEXT    NOT NULL,
		ref     TEXT    NOT NULL,
		mk      TEXT    NOT NULL,
		at      INTEGER NOT NULL,
		PRIMARY KEY (team_id, host_id, ref)
	);`

// moveRemoteMemberIn is the member host's half of a cleared whose old session is a remote member (D4, §3.2): the active
// remote_members row follows the new session and ref, and a `moved` fact for the lead host is queued, in the caller's
// transaction — either both commit or neither does. op is the relay op as reported (its old session is op.SessionID). A session
// that is not a remote member is not this function's: (false, nil). A new session that already holds a role fails the whole
// cleared (ErrClearedTargetHasRole), as it does for a local member.
//
// Only a person's own /relay reaches here in this version (MR-2): the fact says manual and carries no op id. The lead's relay of
// a remote member (MR-3) sends its op id.
func (s *Store) moveRemoteMemberIn(tx *sql.Tx, op team.RelayOp, r RelayReport) (queued bool, err error) {
	var (
		mk, teamID, leadHost, procStart, pane, title string
		pid                                          int
	)
	err = tx.QueryRow(`SELECT mk, team_id, lead_host_id, pid, proc_start, pane_id, title FROM remote_members
		WHERE member_session_id = ? AND state = ?`, op.SessionID, remoteActive).Scan(&mk, &teamID, &leadHost, &pid, &procStart, &pane, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("remote member of %s: %w", op.SessionID, err)
	}
	if taken, err := hasLiveRoleIn(tx, r.NewSessionID); err != nil {
		return false, err
	} else if taken {
		return false, fmt.Errorf("%w (%s)", ErrClearedTargetHasRole, r.NewSessionID)
	}
	if role, err := memberRoleIn(tx, r.NewSessionID); err != nil {
		return false, err
	} else if role != sessionRoleNone {
		return false, fmt.Errorf("%w (%s)", ErrClearedTargetHasRole, r.NewSessionID)
	}
	if _, err := tx.Exec(`UPDATE remote_members SET member_session_id = ?, ref = ?, updated_at = ? WHERE mk = ? AND state = ?`,
		r.NewSessionID, r.NewRef, r.At, mk, remoteActive); err != nil {
		return false, fmt.Errorf("move remote member %s: %w", mk, err)
	}
	if s.newID == nil {
		return false, errors.New("no id source for the moved fact")
	}
	if err := writeFactIn(tx, team.TeamFact{ID: s.newID(), Kind: team.FactMoved, ToHostID: leadHost, TeamID: teamID, MK: mk,
		NewSession: r.NewSessionID, NewRef: r.NewRef, PID: pid, ProcStart: procStart, Pane: pane, Title: title, Manual: true}, r.At); err != nil {
		return false, err
	}
	if s.failAfterMovedFact != nil {
		if err := s.failAfterMovedFact(); err != nil {
			return false, err
		}
	}
	return true, nil
}

// dropIfUnannounced are the fact kinds that are dropped, not held, when the lead host does not announce them (§3.6, D8): a
// person's /relay cannot be refused (U-M1), so its `moved` cannot be checked before it happens, and a fact held at the head of
// the host's FIFO would hold that host's `ended` facts with it. Unreachable is not "unannounced": only a capabilities answer
// that lacks the kind drops it. (A kind table, not a column: it is a property of the kind, and team_facts needs no migration.)
// errMemberNotSettled: a moved fact met a row that is still joining; it is retried, not answered.
var errMemberNotSettled = errors.New("member row still joining")

func dropIfUnannounced(kind string) bool { return kind == team.FactMoved }

// applyMovedIn is `moved` on the lead host (spec §3.2, §4.5): the member's session moved on its host. The row is the one of THIS
// host, in this team, with this mk — never another host's, never a local row. An active row of a live team takes the new session,
// ref and process, and its previous ref is kept (D7); anything else is "ignored". No such row at all is not_your_member. A new
// session that is already an active member here is ignored: moving onto it could never commit.
func (s *Store) applyMovedIn(tx dbtx, p FactPlan) (CommandResult, error) {
	f := p.fact
	var oldRef, state string
	err := tx.QueryRow(`SELECT ref, state FROM team_members WHERE host_id = ? AND host_id <> ? AND mk = ? AND team_id = ?`,
		p.FromHostID, s.localHostID, f.MK, f.TeamID).Scan(&oldRef, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no such membership of that host in that team"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	if state == rowJoining {
		// The adopt answer and the facts travel on separate queues, so this can arrive first. Not a verdict: nothing is logged and
		// the member host sends the fact again (a 5xx), by which time the row is active.
		return CommandResult{}, errMemberNotSettled
	}
	if state != string(team.MemberActive) {
		return okResult(map[string]string{"state": team.FactIgnored})
	}
	var one int
	switch err := tx.QueryRow(`SELECT 1 FROM team_members WHERE session_id = ? AND state IN ('active', 'killing')`, f.NewSession).Scan(&one); {
	case err == nil:
		return okResult(map[string]string{"state": team.FactIgnored})
	case !errors.Is(err, sql.ErrNoRows):
		return CommandResult{}, err
	}
	r, err := tx.Exec(`UPDATE team_members SET session_id = ?, ref = ?, pid = ?, proc_start = ?, pane_id = ?, title = ?, updated_at = ?, `+resetMemberUsage+`
		WHERE host_id = ? AND mk = ? AND team_id = ? AND state = 'active' AND `+liveTeamOfRow,
		f.NewSession, f.NewRef, f.PID, f.ProcStart, f.Pane, f.Title, p.Now, p.FromHostID, f.MK, f.TeamID)
	if err != nil {
		return CommandResult{}, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return okResult(map[string]string{"state": team.FactIgnored}) // the team ended
	}
	if oldRef != "" && oldRef != f.NewRef {
		if _, err := tx.Exec(`INSERT INTO remote_member_refs (team_id, host_id, ref, mk, at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(team_id, host_id, ref) DO UPDATE SET mk = excluded.mk, at = excluded.at`, f.TeamID, p.FromHostID, oldRef, f.MK, p.Now); err != nil {
			return CommandResult{}, err
		}
	}
	return okResult(map[string]string{"state": team.FactApplied, "old_ref": oldRef}) // the old ref is for the lead's notice
}

// formerRefMatch is matchRemoteMember's second look (D7): the rows of host that were once called ref. It is consulted only when
// no row of that host holds ref now, and it names a member of THIS team of THAT host by mk — a stale entry cannot point at
// anyone else's row.
func (m *Module) formerRefMatch(rows []memberRow, teamID, hostID, ref string) []memberRow {
	var spawnOp string
	if err := m.store.db.QueryRow(`SELECT m.spawn_op FROM remote_member_refs r JOIN team_members m ON m.mk = r.mk AND m.host_id = r.host_id
		WHERE r.team_id = ? AND r.host_id = ? AND r.ref = ? AND m.team_id = r.team_id AND m.mk <> '' ORDER BY m.created_at DESC LIMIT 1`, teamID, hostID, ref).Scan(&spawnOp); err != nil {
		return nil
	}
	var hits []memberRow
	for _, r := range rows {
		if r.HostID == hostID && r.SpawnOp == spawnOp {
			hits = append(hits, r)
		}
	}
	return hits
}

// announceMovedAfter tells the lead that a person's /relay moved one of its remote members (D3), when the fact just applied
// moved a row: after the commit, once (a replay never gets here), from a goroutine so the answer to the member host is not held by
// a lead that is slow to read. Refs are written as the lead can address them: <alias>/_<ref>.
func (m *Module) announceMovedAfter(hostID string, f team.TeamFact, outcome []byte) {
	var o struct {
		State  string `json:"state"`
		OldRef string `json:"old_ref"`
	}
	if json.Unmarshal(outcome, &o) != nil || o.State != team.FactApplied || o.OldRef == "" {
		return
	}
	if !m.goTracked(func() { m.announceMoved(hostID, f, o.OldRef) }) {
		m.logf("[team] moved notice of %s not sent: stopping", f.MK)
	}
}

func (m *Module) announceMoved(hostID string, f team.TeamFact, oldRef string) {
	t, ok, err := m.store.TeamByID(f.TeamID)
	if err != nil || !ok || t.EndedAt != 0 {
		return
	}
	alias := m.remoteAlias(hostID)
	if alias == "" {
		return
	}
	mr := memberRow{HostID: hostID, SessionID: f.NewSession, Ref: f.NewRef}
	text := fmt.Sprintf(team.RelayManualNoticeFmt, alias+"/"+oldRef, alias+"/"+f.NewRef)
	m.noticeToLead(mr, t, text, "moved notice")
}
