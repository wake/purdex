package teammod

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/team"
)

// Commands for REMOTE rows (cross-host team spec §4.2, §3.1 rule 2; plan X3b-1b). A release or a kill of a remote member
// moves its row and enqueues the command in ONE transaction; the team ending and the lead moving enqueue `end` /
// `lead_moved` for every host with a live remote row inside the transaction that causes them. Nothing here talks to the
// network except the capability check made before the transaction opens (rule 7).

// isRemoteRow says whether mr lives on another host.
func (m *Module) isRemoteRow(mr memberRow) bool { return mr.HostID != "" && mr.HostID != m.hostID() }

// leadTuple is the lead's full origin tuple a command carries (§6.2): the live origin of its session, else what its request
// row recorded (a lead that is gone still has its process identity there).
func (m *Module) leadTuple(t team.Team) team.TeamLead {
	alias, _ := m.selfHost()
	lead := team.TeamLead{SessionID: t.LeadSessionID, Ref: t.LeadRef, Address: alias + "/" + t.LeadRef}
	if o, ok, err := m.origins.ResolveOriginBySession(t.LeadSessionID); err == nil && ok {
		lead.PID, lead.ProcStart, lead.Address = o.PID, o.ProcStart, firstNonEmpty(o.Address, lead.Address)
		return lead
	}
	if req, ok, err := m.store.Get(t.RequestID); err == nil && ok {
		lead.PID, lead.ProcStart = req.Origin.PID, req.Origin.ProcStart
	}
	return lead
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// joinLook sets the team's stored look on a join command (adopt, spawn; #2346), so the member host shows it at once: name,
// label and colour read in ONE statement (a rename lands wholly before or after it). The colour stays absent for "automatic".
// A failed read leaves the command as it was — the optional fields' absence is the older-lead-host case the receiver
// already handles, and the next team.appearance fills them.
func (m *Module) joinLook(t team.Team, tc *team.TeamCommand) {
	var color sql.NullInt64
	var name, label string
	if err := m.store.db.QueryRow(`SELECT team_name, team_label, team_color FROM teams WHERE id = ?`, t.ID).Scan(&name, &label, &color); err != nil {
		m.logf("[team] join look of team %s: %v", t.ID, err)
		return
	}
	tc.TeamName, tc.TeamLabel = name, label
	if color.Valid {
		c := int(color.Int64)
		tc.TeamColor = &c
	}
}

// remoteCommand builds the command to host for a member row (mk != "") or the whole team (mk == "").
func remoteCommand(id, kind, host string, t team.Team, mk string, lead team.TeamLead, extra func(*team.TeamCommand)) (Command, error) {
	tc := team.TeamCommand{ID: id, Kind: kind, ToHostID: host, TeamID: t.ID, TeamName: t.TeamName, MK: mk, Lead: lead}
	if extra != nil {
		extra(&tc)
	}
	body, err := json.Marshal(tc)
	if err != nil {
		return Command{}, err
	}
	return Command{ID: id, Kind: kind, TeamID: t.ID, MK: mk, HostID: host, Body: body}, nil
}

// remoteTransition is a release or a kill of one remote row: the row moves `from` → `to` and the command is enqueued in the
// same transaction. moved false means the CAS found another state (the caller re-reads the row and answers by it).
func (m *Module) remoteTransition(t team.Team, mr memberRow, kind string, from []string, to string) (moved bool, err error) {
	lead := m.leadTuple(t)
	now := m.now()
	tx, err := m.store.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	q := `UPDATE team_members SET state = ?, updated_at = ? WHERE spawn_op = ? AND host_id = ? AND state IN ('` + joinQuoted(from) + `') AND ` + liveTeamOfRow
	r, err := tx.Exec(q, to, now, mr.SpawnOp, mr.HostID)
	if err != nil {
		return false, err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return false, nil
	}
	var mk string
	if err := tx.QueryRow(`SELECT mk FROM team_members WHERE spawn_op = ?`, mr.SpawnOp).Scan(&mk); err != nil {
		return false, err
	}
	cmd, err := remoteCommand(m.newID(), kind, mr.HostID, t, mk, lead, nil)
	if err != nil {
		return false, err
	}
	if err := m.store.EnqueueCommand(tx, cmd, now); err != nil {
		return false, err // the row does not move without its command
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	m.kickCommands()
	m.rosterChanged()
	return true, nil
}

func joinQuoted(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += "', '"
		}
		out += x
	}
	return out
}

// writeCapErr answers a refused capability check.
func (m *Module) writeCapErr(w http.ResponseWriter, err error) {
	var ce *CapError
	if errors.As(err, &ce) {
		status := http.StatusConflict
		if ce.Code == "remote_unreachable" {
			status = http.StatusServiceUnavailable
		}
		m.writeErr(w, status, ce.Code, ce.Detail, nil)
		return
	}
	m.writeErr(w, http.StatusInternalServerError, errStorage, err.Error(), nil)
}

// releaseRemote is handleRelease for a remote row: active / joining → releasing + the release command; an answer in flight or
// a finished row answers 200 as it is (idempotent); a kill in flight is 409 command_pending.
func (m *Module) releaseRemote(w http.ResponseWriter, t team.Team, mr memberRow) {
	switch mr.State {
	case team.MemberActive, team.MemberJoining:
	case team.MemberKilling:
		m.writeErr(w, http.StatusConflict, team.ErrCommandPending, "member "+mr.Ref+" has a kill in flight; release it when that has landed", nil)
		return
	default:
		m.writeJSON(w, http.StatusOK, m.memberView(mr))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := m.checkRemoteKind(ctx, mr.HostID, CmdRelease); err != nil {
		m.writeCapErr(w, err)
		return
	}
	m.finishRemote(w, t, mr, CmdRelease, []string{string(team.MemberActive), string(team.MemberJoining)}, string(team.MemberReleasing))
}

// killRemote is handleKill for a remote row: only from active (while joining or releasing a kill is 409 command_pending —
// a refusal can then only ever return the row to active, never re-open a state whose outcome was consumed).
func (m *Module) killRemote(w http.ResponseWriter, t team.Team, mr memberRow) {
	switch mr.State {
	case team.MemberActive:
	case team.MemberJoining, team.MemberReleasing:
		m.writeErr(w, http.StatusConflict, team.ErrCommandPending, "member "+mr.Ref+" has a "+string(mr.State)+" in flight; kill it when that has landed", nil)
		return
	case team.MemberReleased:
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, fmt.Sprintf("%q was released from team %s", mr.Ref, t.ID), nil)
		return
	default: // killing: idempotent; killed, gone, failed: as it is
		m.writeJSON(w, http.StatusOK, m.memberView(mr))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := m.checkRemoteKind(ctx, mr.HostID, CmdKill); err != nil {
		m.writeCapErr(w, err)
		return
	}
	m.finishRemote(w, t, mr, CmdKill, []string{string(team.MemberActive)}, string(team.MemberKilling))
}

func (m *Module) finishRemote(w http.ResponseWriter, t team.Team, mr memberRow, kind string, from []string, to string) {
	moved, err := m.remoteTransition(t, mr, kind, from, to)
	if err != nil {
		m.logf("[team] %s %s: %v", kind, mr.SpawnOp, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	if !moved { // another call, an outcome or an ended fact got there first: answer by what the row is now
		rows, err := m.store.MembersOf(t.ID)
		if err == nil {
			for _, now := range rows {
				if now.SpawnOp == mr.SpawnOp {
					mr = now
				}
			}
		}
		m.writeJSON(w, http.StatusOK, m.memberView(mr))
		return
	}
	mr.State = team.MemberState(to)
	m.logf("[team] remote member %s (%s on %s) of team %s: %s sent", mr.Ref, mr.SessionID, mr.HostID, t.ID, kind)
	m.writeJSON(w, http.StatusOK, m.memberView(mr))
}

// liveRemoteHostsTx are the hosts that hold a live remote row of the team (§4.2), read in tx.
// withSpawns adds the hosts that hold only a running forwarded spawn of the team (#2327): `end` has to reach them too, to
// abort the op before it registers a member nobody leads.
func (s *Store) liveRemoteHostsTx(tx dbtxq, teamID string, withSpawns bool) ([]string, error) {
	q, args := `SELECT host_id FROM team_members WHERE team_id = ? AND host_id <> ? AND state IN `+liveRemoteStates, []any{teamID, s.localHostID}
	if withSpawns {
		q, args = q+` UNION SELECT host_id FROM remote_spawns WHERE team_id = ? AND host_id <> ? AND state = 'running'`, append(args, teamID, s.localHostID)
	}
	rows, err := tx.Query(`SELECT DISTINCT host_id FROM (`+q+`) ORDER BY host_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hosts []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

// LiveRemoteHosts are the member hosts that hold a live remote row of the team (and, with withSpawns, a running forwarded
// spawn), outside a transaction: the hosts a caller asks about before it opens the one that writes.
func (s *Store) LiveRemoteHosts(teamID string, withSpawns bool) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return s.liveRemoteHostsTx(tx, teamID, withSpawns)
}

// enqueueTeamLevelTx enqueues one team-level command (no mk) per host with a live remote row of the team, in tx.
func (s *Store) enqueueTeamLevelTx(tx dbtxq, t team.Team, kind string, lead team.TeamLead, extra func(*team.TeamCommand), newID func() string, now int64, only map[string]bool) (int, error) {
	hosts, err := s.liveRemoteHostsTx(tx, t.ID, kind == CmdEnd || kind == CmdAppearance)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, h := range hosts {
		if only != nil && !only[h] {
			continue
		}
		cmd, err := remoteCommand(newID(), kind, h, t, "", lead, extra)
		if err != nil {
			return 0, err
		}
		if err := s.EnqueueCommand(tx, cmd, now); err != nil {
			return 0, err
		}
		n++
	}
	return n, nil
}

// EndTeamWithCommands is EndTeam that also enqueues `end` for every host with a live remote row, inside the same
// transaction (rule 2): the team is not ended without its commands, and the commands do not exist without the team ending.
func (s *Store) EndTeamWithCommands(t team.Team, reason string, at int64, lead team.TeamLead, newID func() string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if ended, err := endTeamTx(tx, t.ID, t.LeadSessionID, reason, at); err != nil || !ended {
		return false, err
	}
	// the rows keep their states (D4); the commands read them, so they are enqueued from the rows as they are
	if _, err := s.enqueueTeamLevelTx(tx, t, CmdEnd, lead, nil, newID, at, nil); err != nil {
		return false, err
	}
	// the forwarded spawns still running are over with the team (the member host aborts them on the end above): close them
	// here too, after the hosts were read, so a replayed spawn request does not keep answering `running` (#2327)
	if _, err := tx.Exec(`UPDATE remote_spawns SET state = 'failed', reason = ?, updated_at = ? WHERE team_id = ? AND state = 'running'`,
		team.SpawnReasonAbandoned, at, t.ID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// ledTeamsTx are the live teams the session leads, read in tx (before a cleared moves them).
func ledTeamsTx(tx *sql.Tx, sessionID string) ([]string, error) {
	rows, err := tx.Query(`SELECT id FROM teams WHERE lead_session_id = ? AND ended_at = 0`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// enqueueLeadMovedTx tells every host with a live remote row of the given teams — the ones THIS cleared moved, read before
// it ran — that the lead is now newLead (§4.2): in the cleared's own transaction, after moveTeamRoles.
func (s *Store) enqueueLeadMovedTx(tx *sql.Tx, teamIDs []string, newLead team.TeamLead, newID func() string, now int64) error {
	for _, id := range teamIDs {
		var t team.Team
		var grant string
		if err := tx.QueryRow(`SELECT `+teamCols+` FROM teams WHERE id = ? AND lead_session_id = ? AND ended_at = 0`, id, newLead.SessionID).Scan(teamDest(&t, &grant)...); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // it did not follow the lead after all
			}
			return err
		}
		if _, err := s.enqueueTeamLevelTx(tx, t, CmdLeadMoved, newLead, func(c *team.TeamCommand) {
			c.LeadSessionID, c.LeadRef = newLead.SessionID, newLead.Ref
		}, newID, now, nil); err != nil {
			return err
		}
	}
	return nil
}

// remoteAlias is the peer alias of a remote row's host ("" when it is no longer paired).
func (m *Module) remoteAlias(hostID string) string {
	if m.cmdCaller == nil {
		return ""
	}
	return m.cmdCaller.AliasOf(hostID)
}
