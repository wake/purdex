// internal/module/team/spawn_remote_lead.go
package teammod

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/wake/purdex/internal/team"
)

// `pdx spawn --host <alias>` on the lead host (cross-host team spec §4.5, §5.5, §7; plan X4b-2). The member host runs the
// spawn; this host keeps the op (remote_spawns), a seat for it, and the `spawn` command in its commands outbox. What closes the
// op is the member host's `registered` / `spawn_failed` fact (facts_recv_store.go), a refusal of the command, the 10 minute
// void or an unpairing.

// remoteSpawnPoll is how often a held POST re-reads its op: the registered / spawn_failed facts wake it at once, a command
// refusal is applied inside the outbox's transaction and is found at the next tick.
const remoteSpawnPoll = 200 * time.Millisecond

// sameRemoteSpawn is whether op is the very request req made (the replay of an id): same host, lead, cwd, title, model, effort
// and task. The stored columns are the comparison, so no hash is kept.
func sameRemoteSpawn(op remoteSpawnRow, want remoteSpawnRow) bool {
	return op.HostID == want.HostID && op.TeamID == want.TeamID && op.OriginSessionID == want.OriginSessionID && op.Cwd == want.Cwd &&
		op.Title == want.Title && op.Model == want.Model && op.Effort == want.Effort && op.TaskSubject == want.TaskSubject &&
		op.TaskDescription == want.TaskDescription && op.TaskDoneJSON == want.TaskDoneJSON
}

// handleRemoteSpawn creates the forwarded op, or joins the one this id already names, and answers 200 with it once it leaves
// running, or after spawnWait while it still runs (the CLI then posts the same body again).
func (m *Module) handleRemoteSpawn(w http.ResponseWriter, r *http.Request, req team.SpawnRequest, origin team.Origin) {
	if m.cmdCaller == nil {
		m.writeErr(w, http.StatusConflict, team.ErrRemoteUnsupported, "cross-host team is not available on this daemon", nil)
		return
	}
	want := remoteSpawnRow{ID: req.ID, OriginSessionID: origin.SessionID, Cwd: req.Cwd, Title: req.Title, Model: req.Model,
		Effort: req.Effort, TaskSubject: taskSubjectOf(req.Task), TaskDescription: taskDescriptionOf(req.Task), TaskDoneJSON: taskDoneJSONOf(req.Task)}

	m.createMu.Lock()
	op, found, err := m.store.GetRemoteSpawn(req.ID)
	if err == nil && found {
		m.createMu.Unlock()
		// A replay is read back by its id whatever the pairing is NOW (the host may have been unpaired since, which is what
		// ended the op): the stored end must stay readable. The host named in the request is compared when it still resolves;
		// when it no longer does, it cannot be compared and the rest of the request must match.
		want.TeamID, want.HostID = op.TeamID, m.remoteHostID(req.Host)
		if want.HostID == "" {
			want.HostID = op.HostID
		}
		if !sameRemoteSpawn(op, want) {
			m.writeErr(w, http.StatusConflict, team.ErrIDConflict, "id already used by a different spawn", nil)
			return
		}
		m.answerRemoteSpawn(w, r, op.ID, origin)
		return
	}
	if err != nil {
		m.createMu.Unlock()
		m.failRemoteSpawn(w, req.ID, err)
		return
	}
	want.HostID = m.remoteHostID(req.Host)
	if want.HostID == "" || !m.cmdCaller.Paired(want.HostID) {
		m.createMu.Unlock()
		m.writeErr(w, http.StatusBadRequest, team.ErrBadRequest, "no paired host answers to "+req.Host, nil)
		return
	}
	row, ok := m.acceptRemoteSpawn(w, r, req, origin, want) // holds createMu until it returns
	m.createMu.Unlock()
	if !ok {
		return
	}
	m.answerRemoteSpawn(w, r, row.ID, origin)
}

// remoteHostID is the host id the request's --host names: an alias of a configured host, or a host id; "" when neither
// resolves (never paired, or since unpaired).
func (m *Module) remoteHostID(host string) string {
	if id := m.cmdCaller.HostIDOf(host); id != "" {
		return id
	}
	if m.cmdCaller.AliasOf(host) != "" {
		return host // the host part was a host id
	}
	return ""
}

// acceptRemoteSpawn is the create path under createMu: the lead's live team, the member host's capability (rule 7: spawn
// announced and allow_team on), then the op, its seat and the command in one transaction. false means an error was written.
func (m *Module) acceptRemoteSpawn(w http.ResponseWriter, r *http.Request, req team.SpawnRequest, origin team.Origin, want remoteSpawnRow) (remoteSpawnRow, bool) {
	t, found, err := m.store.LiveTeamByLead(origin.SessionID)
	if err != nil {
		m.failRemoteSpawn(w, req.ID, err)
		return remoteSpawnRow{}, false
	}
	if !found {
		m.writeErr(w, http.StatusConflict, team.ErrNotLead, "this session leads no live team", nil)
		return remoteSpawnRow{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := m.checkRemoteKind(ctx, want.HostID, CmdSpawn); err != nil {
		m.writeCapErr(w, err)
		return remoteSpawnRow{}, false
	}
	now := m.now()
	want.TeamID, want.State, want.CreatedAt, want.UpdatedAt = t.ID, remoteSpawnRunning, now, now
	cmd, err := remoteCommand(req.ID, CmdSpawn, want.HostID, t, req.ID, m.leadTuple(t), func(tc *team.TeamCommand) {
		tc.Cwd, tc.Title, tc.Model, tc.Effort = req.Cwd, req.Title, req.Model, req.Effort
	})
	if err != nil {
		m.failRemoteSpawn(w, req.ID, err)
		return remoteSpawnRow{}, false
	}
	row, err := m.store.AcceptRemoteSpawn(want, cmd, now)
	switch {
	case errors.Is(err, ErrSpawnNotLead):
		m.writeErr(w, http.StatusConflict, team.ErrNotLead, "this session no longer leads team "+t.ID, nil)
		return remoteSpawnRow{}, false
	case errors.Is(err, ErrSpawnTeamFull):
		m.writeErr(w, http.StatusConflict, team.ErrTeamFull, fmt.Sprintf("team %s has its %d active or starting members", t.ID, t.Grant.MaxMembers), nil)
		return remoteSpawnRow{}, false
	case err != nil:
		m.failRemoteSpawn(w, req.ID, err)
		return remoteSpawnRow{}, false
	}
	m.logf("[team] remote spawn %s accepted: team %s, host %s, %s", row.ID, t.ID, row.HostID, row.Cwd)
	m.rosterChanged() // the op takes a seat
	m.kickCommands()
	return row, true
}

func (m *Module) failRemoteSpawn(w http.ResponseWriter, id string, err error) {
	m.logf("[team] remote spawn %s: %v", id, err)
	m.writeErr(w, http.StatusInternalServerError, errStorage, "team.db failed; see the daemon log", nil)
}

// answerRemoteSpawn holds the POST while the op runs (up to spawnWait; the facts wake it, a refused command is found by the
// poll), then answers with the op.
func (m *Module) answerRemoteSpawn(w http.ResponseWriter, r *http.Request, id string, origin team.Origin) {
	ch := m.addWaiter(id)
	defer func() { m.removeWaiter(id, ch) }() // the CURRENT channel: a wake takes one and the loop registers the next
	deadline := time.NewTimer(m.spawnWait)
	defer deadline.Stop()
	tick := time.NewTicker(remoteSpawnPoll)
	defer tick.Stop()
	for {
		row, ok, err := m.store.GetRemoteSpawn(id)
		if err == nil && !ok {
			err = fmt.Errorf("remote spawn %s vanished", id)
		}
		if err != nil {
			m.failRemoteSpawn(w, id, err)
			return
		}
		if row.State != remoteSpawnRunning {
			m.writeRemoteSpawn(w, row, origin)
			return
		}
		select {
		case <-ch:
			ch = m.addWaiter(id) // wake takes the channel; wait for the next change on a fresh one
		case <-tick.C:
		case <-deadline.C:
			m.writeRemoteSpawn(w, row, origin)
			return
		case <-r.Context().Done():
			m.writeRemoteSpawn(w, row, origin)
			return
		case <-m.stopCtx.Done():
			m.writeRemoteSpawn(w, row, origin)
			return
		}
	}
}

func (m *Module) writeRemoteSpawn(w http.ResponseWriter, row remoteSpawnRow, origin team.Origin) {
	op, err := m.remoteSpawnView(row, origin.Address)
	if err != nil {
		m.failRemoteSpawn(w, row.ID, err)
		return
	}
	m.writeJSON(w, http.StatusOK, op)
}

// remoteSpawnView is the wire form of a forwarded op: a done op carries its remote member (the alias address) and, when it
// had one, its first task's id.
func (m *Module) remoteSpawnView(r remoteSpawnRow, leadAddress string) (team.SpawnOp, error) {
	op := team.SpawnOp{ID: r.ID, TeamID: r.TeamID, HostID: r.HostID, State: team.SpawnState(r.State), Reason: r.Reason, Cwd: r.Cwd,
		Title: r.Title, Model: r.Model, Effort: r.Effort, LeadAddress: leadAddress, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if r.State != remoteSpawnDone {
		return op, nil
	}
	rows, err := m.store.MembersOf(r.TeamID)
	if err != nil {
		return team.SpawnOp{}, err
	}
	for _, mr := range rows {
		if mr.SpawnOp != r.ID {
			continue
		}
		v := m.memberView(mr)
		op.Member = &v
		if r.TaskSubject != "" {
			task, found, err := m.store.TaskBySpawnOp(r.TeamID, r.ID)
			if err != nil {
				return team.SpawnOp{}, err
			}
			if !found {
				return team.SpawnOp{}, fmt.Errorf("remote spawn %s is done but its task is missing", r.ID)
			}
			op.TaskID = team.TaskDisplayID(task.TeamID, task.Seq)
		}
		return op, nil
	}
	return team.SpawnOp{}, fmt.Errorf("remote spawn %s is done but has no member row", r.ID)
}
