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

// The lead's relay of a member that lives on another host (member relay spec §3.4, D5, D10; MR-3a-1). The relay itself runs on
// the member host (M). Here it is a relay op in the state `forwarded`, written in ONE transaction with the `relay` command
// that asks M to run it. It ends only by a compare-and-set FROM `forwarded`, whichever lands first: the command's refusal,
// the `moved` fact that carries the op's id, a `relay_failed` fact. The others find it ended, are logged and change nothing
// (the row's move by `moved` does not depend on the op). The stall timers and the boot reconciliation dispatch on kind and
// state and never see `forwarded` (relay_forwarded_test.go pins it).

// createRemoteMemberRelay is handleRelayCreate for a remote row, under createMu. M must announce `relay` (and have allowed
// this host): that is read BEFORE the gate, so nothing is spent for a host that cannot apply the command. The liveness, mod and
// process checks are M's.
func (m *Module) createRemoteMemberRelay(w http.ResponseWriter, req team.RelayCreateRequest, t team.Team, mr memberRow) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := m.checkRemoteKind(ctx, mr.HostID, CmdRelay); err != nil {
		var ce *CapError
		if errors.As(err, &ce) && ce.Code == team.ErrRemoteUnsupported {
			m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "the host of member "+mr.Ref+" cannot run a relay for this host yet", nil)
			return
		}
		m.writeCapErr(w, err)
		return
	}
	if m.writeRelayOpen(w, mr.SessionID) {
		return
	}
	leadOrigin, lok, lerr := m.origins.ResolveOriginBySession(t.LeadSessionID)
	if lerr != nil || !lok {
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "the lead is not in the registry; retry", nil)
		return
	}
	now := m.now()
	op := team.RelayOp{
		ID: req.ID, Kind: team.RelayKindMember, HostID: mr.HostID, SessionID: mr.SessionID, Ref: mr.Ref, TeamID: t.ID,
		State: team.RelayForwarded, PID: mr.PID, PaneID: mr.PaneID, ProcStart: mr.ProcStart, CreatedAt: now, UpdatedAt: now,
	}
	var spent bool
	var held *team.Approval
	lead := m.leadTuple(t)
	op, _, err := m.store.CreateRemoteMemberRelayOp(op, m.memberRelayGate(leadOrigin, mr, &spent, &held), func(tx *sql.Tx, op team.RelayOp) error {
		var mk string
		if err := tx.QueryRow(`SELECT mk FROM team_members WHERE spawn_op = ? AND host_id = ?`, mr.SpawnOp, mr.HostID).Scan(&mk); err != nil {
			return err
		}
		cmd, err := remoteCommand(m.newID(), CmdRelay, mr.HostID, t, mk, lead, func(tc *team.TeamCommand) { tc.OpID, tc.CreatedAt = op.ID, op.CreatedAt })
		if err != nil {
			return err
		}
		return m.store.EnqueueCommand(tx, cmd, op.CreatedAt)
	})
	switch {
	case errors.Is(err, ErrRemoteRelayHeld):
		m.writeErr(w, http.StatusConflict, team.ErrRelayUnsupported, "the lead's relay pool is spent out; a remote member's relay cannot wait for an approval yet", nil)
		return
	case errors.Is(err, ErrMemberNotActive):
		m.writeErr(w, http.StatusConflict, team.ErrNotYourMember, "member "+mr.Ref+" left the team while the relay was being opened", nil)
		return
	case errors.Is(err, errLeadChanged):
		m.writeErr(w, http.StatusServiceUnavailable, team.ErrNotReady, "the team's lead changed; retry", nil)
		return
	case errors.Is(err, ErrRelayOpOpen):
		if !m.writeRelayOpen(w, mr.SessionID) {
			m.logf("[team] remote member relay %s: %v", req.ID, err)
			m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		}
		return
	case err != nil:
		m.logf("[team] remote member relay %s: %v", req.ID, err)
		m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
		return
	}
	m.logf("[team] relay op %s forwarded: member %s (%s) of team %s on %s", op.ID, mr.Ref, mr.SessionID, t.ID, mr.HostID)
	if spent {
		m.announceSpend(t.LeadSessionID)
	}
	m.kickCommands()
	m.writeJSON(w, http.StatusCreated, team.RelayCreateResponse{Op: op})
}

// endForwardedOpIn ends the forwarded op opID — of THIS host's member — as a compare-and-set from `forwarded`. The op is bound
// to the member by the `relay` command this host queued for it (host, team, mk and the op id in its body), never by the session
// the row has now: a person's own /relay may have moved the row between the command's send and the op's answer. ended says
// whether it changed: false means the op was not forwarded any more (another cause got there first), or is not this member's,
// and nothing was written.
func endForwardedOpIn(tx dbtx, hostID, teamID, mk, opID string, to team.RelayState, reason, newSession, newRef string, now int64) (ended bool, err error) {
	if opID == "" {
		return false, nil
	}
	r, err := tx.Exec(`UPDATE relay_ops SET state = ?, reason = ?, new_session_id = ?, new_ref = ?, updated_at = ?
		WHERE id = ? AND kind = 'member' AND state = 'forwarded' AND host_id = ? AND team_id = ?
		AND EXISTS (SELECT 1 FROM team_commands WHERE kind = 'relay' AND host_id = ? AND team_id = ? AND mk = ?
			AND json_extract(body_json, '$.op_id') = relay_ops.id)`,
		string(to), reason, newSession, newRef, now, opID, hostID, teamID, hostID, teamID, mk)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}

// relayCommandRefused is the `relay` command's refusal: the op ends failed with the refusal's code. An accepted command
// changes nothing (the result comes as a fact).
func (o remoteOutcomes) relayCommandRefused(tx *sql.Tx, c commandRow, code string, now int64) error {
	var body team.TeamCommand
	if err := json.Unmarshal(c.Body, &body); err != nil || body.OpID == "" {
		return fmt.Errorf("relay command %s carries no op id", c.ID)
	}
	ended, err := endForwardedOpIn(tx, c.HostID, c.TeamID, c.MK, body.OpID, team.RelayFailed, code, "", "", now)
	if err != nil {
		return err
	}
	if !ended {
		o.m.logf("[team] relay command %s was refused (%s); op %s is not forwarded any more", c.ID, code, body.OpID)
		return nil
	}
	o.m.noteOpEnded(tx, body.OpID)
	return nil
}

// noteOpEnded remembers an op the settle transaction tx ended, for the wake and the notice once THAT transaction has committed
// (dropRelayOpsEnded forgets it when it did not: a notice for a change that never happened would be a false one).
func (m *Module) noteOpEnded(tx *sql.Tx, opID string) {
	m.endedOpsMu.Lock()
	if m.endedOps == nil {
		m.endedOps = map[*sql.Tx][]string{}
	}
	m.endedOps[tx] = append(m.endedOps[tx], opID)
	m.endedOpsMu.Unlock()
}

func (m *Module) takeEndedOps(tx *sql.Tx) []string {
	m.endedOpsMu.Lock()
	defer m.endedOpsMu.Unlock()
	ids := m.endedOps[tx]
	delete(m.endedOps, tx)
	return ids
}

func (m *Module) dropRelayOpsEnded(tx *sql.Tx) { m.takeEndedOps(tx) }

// afterRelayOpsEnded wakes the long-polls of, and tells the lead about, the ops the committed settle tx ended.
func (m *Module) afterRelayOpsEnded(tx *sql.Tx) {
	for _, id := range m.takeEndedOps(tx) {
		m.store.notifyOp(id)
		if op, ok, err := m.store.GetRelayOp(id); err == nil && ok && op.State.Terminal() {
			m.outcomeNoticeAsync(op)
		}
	}
}

// applyRelayFailedIn is `relay_failed` on the lead host (spec §3.2): the relay the lead started ended failed or cancelled on the
// member host. It binds to the member row (principal host, team, mk) like every member fact, and the op to that member: an op
// of another host, team or member is never touched. The op leaves `forwarded` with the member host's state and reason; an op
// that is not forwarded any more (another cause ended it first) stays as it is: "ignored".
func (s *Store) applyRelayFailedIn(tx dbtx, p FactPlan) (CommandResult, error) {
	f := p.fact
	var one int
	err := tx.QueryRow(`SELECT 1 FROM team_members WHERE host_id = ? AND host_id <> ? AND mk = ? AND team_id = ?`,
		p.FromHostID, s.localHostID, f.MK, f.TeamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return refusal(http.StatusConflict, team.ErrCommandNotYourMember, "no such membership of that host in that team"), nil
	}
	if err != nil {
		return CommandResult{}, err
	}
	ended, err := endForwardedOpIn(tx, p.FromHostID, f.TeamID, f.MK, f.OpID, team.RelayState(f.State), f.Reason, "", "", p.Now)
	if err != nil {
		return CommandResult{}, err
	}
	if !ended {
		return okResult(map[string]string{"state": team.FactIgnored})
	}
	return okResult(map[string]string{"state": team.FactApplied})
}

func relayFailedApplied(outcome []byte) bool {
	var o struct {
		State string `json:"state"`
	}
	return json.Unmarshal(outcome, &o) == nil && o.State == team.FactApplied
}

// noticeForwardedOpEnded tells the lead how a forwarded op ended (after the commit that ended it, once: a replay never gets here).
func (m *Module) noticeForwardedOpEnded(opID string) {
	if op, ok, err := m.store.GetRelayOp(opID); err == nil && ok && op.State.Terminal() {
		m.outcomeNoticeAsync(op)
	}
}
