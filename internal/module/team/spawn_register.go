package teammod

// The spawn runner's last steps (spec §7.2 steps 5–6): the member registers
// on its pane, and is stored.

import (
	"context"
	"fmt"
	"time"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
)

// frameReader lists the live agent frames (agent.TerminalSessionsKey).
type frameReader interface {
	LiveSessions(ctx context.Context, agentType string) ([]agent.TerminalSession, error)
}

// TitleSetter sets a session's title: *store.PeerLabelStore, the value
// WithTitles was given. Without it a spawned member keeps no title.
type TitleSetter interface {
	Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
}

var _ TitleSetter = (*store.PeerLabelStore)(nil) // else members silently get no title

// initRegister resolves the registration's seams: the frames are a hard
// error, as every runner service is (initSpawn); the title store is optional.
func (m *Module) initRegister(c *core.Core) error {
	var err error
	m.frames, err = lookup[frameReader](c, agent.TerminalSessionsKey)
	m.titleSet, _ = m.titles.(TitleSetter)
	return err
}

// sleepCtx is the registration poll's pause: d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// spawnRegister is launched → registered (spec §7.2 step 5). A verified root
// frame on the op's pane with a session id whose registry entry is live is
// the member, once one tmux answer confirms the pane is still the op's: the
// generation it was created in, its session, its tag (review H4: a
// restarted server mints pane ids again, and frames carry no tmux identity).
// A pane that answers but is not the op's means the session died with its
// server: the op is abandoned. An unreadable pane is looked at again.
//
// Every poll judges the deadline first (spec §7.2 step 5: "Wait up to 20 s
// … On timeout: kill"; review R1, ruled again after the critic): past
// launched_at + the budget the op times out (timeOutSpawn), even when the
// member has shown up by then — there is no record of when it registered to
// compare with the deadline.
func (m *Module) spawnRegister(op spawnRow) (*team.Origin, bool) {
	for {
		if m.now() >= op.LaunchedAt+m.spawnBudget {
			m.timeOutSpawn(op)
			return nil, false
		}
		if o, ok := m.memberOnPane(op.PaneID); ok {
			id, err := m.paneIdentity(op.PaneID)
			if err == nil && !ownsPane(op, id) {
				m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, fmt.Errorf("pane %s is no longer this op's: %+v", op.PaneID, id))
				return nil, false
			}
			if err == nil {
				won, err := m.store.AdvanceSpawnOp(op.ID, team.StepLaunched, spawnUpdate{Step: team.StepRegistered, SessionID: o.SessionID, At: m.now()})
				if err != nil {
					m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
				}
				return &o, err == nil && won
			}
		}
		m.spawnSleep(m.stopCtx, m.spawnPoll)
		if m.stopping() {
			return nil, false
		}
	}
}

// timeOutSpawn takes the timeout's decision before anything else (P4-5
// re-review): one compare-and-set from launched to
// failed{member_start_timeout}, which excludes the registration's own CAS
// from launched (the same row and step). Only its winner kills the session,
// under its recorded generation; a runner that lost it, to a registration
// or another timeout, touches nothing. A daemon that dies between the CAS
// and the kill leaves the session of a failed op behind.
func (m *Module) timeOutSpawn(op spawnRow) {
	won, err := m.store.FailSpawnOpAtStep(op.ID, team.StepLaunched, team.SpawnReasonStartTimeout, m.now())
	if err != nil {
		m.logf("[team] spawn %s: %v", op.ID, err)
	}
	if !won {
		return
	}
	m.logf("[team] spawn %s failed: %s", op.ID, team.SpawnReasonStartTimeout)
	m.wake(op.ID)
	m.killSpawnSession(op.ID, op.TmuxID, op.TmuxInstance)
}

func (m *Module) memberOnPane(pane string) (team.Origin, bool) {
	frames, err := m.frames.LiveSessions(m.stopCtx, "cc")
	if pane == "" || err != nil {
		return team.Origin{}, false
	}
	for _, f := range frames {
		if f.PaneID != pane || !f.Verified || f.SessionID == "" {
			continue
		}
		if o, ok, err := m.origins.ResolveOriginBySession(f.SessionID); err == nil && ok {
			return o, true
		}
	}
	return team.Origin{}, false
}

// spawnFinish is registered → done (spec §7.2 step 6): the member row, its
// title, then the op, each idempotent. A registry read error leaves the op
// registered for the next boot; a refused write aborts it. o is the
// registry entry the registration saw (nil when resumed at registered: read
// again, or the ref alone if gone).
func (m *Module) spawnFinish(op spawnRow, o *team.Origin) {
	if o == nil || o.SessionID != op.SessionID {
		r, ok, err := m.origins.ResolveOriginBySession(op.SessionID)
		if err != nil {
			m.logf("[team] spawn %s: %v", op.ID, err)
			return
		}
		if !ok {
			r = team.Origin{SessionID: op.SessionID, Ref: ipeers.RefID(op.SessionID)}
		}
		o = &r
	}
	now := m.now()
	if err := m.store.InsertMember(memberRow{SpawnOp: op.ID, TeamID: op.TeamID, HostID: op.HostID, SessionID: op.SessionID,
		Ref: o.Ref, Title: op.Title, Cwd: op.Cwd, TmuxSession: op.TmuxName, TmuxID: op.TmuxID, TmuxInstance: op.TmuxInstance,
		PaneID: op.PaneID, PID: o.PID, ProcStart: o.ProcStart, Model: op.Model, Effort: op.Effort,
		State: team.MemberActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
		return
	}
	if op.Title != "" && m.titleSet != nil {
		if _, err := m.titleSet.Claim(op.SessionID, op.Title, time.UnixMilli(now)); err != nil {
			m.logf("[team] spawn %s: title %q for %s: %v", op.ID, op.Title, op.SessionID, err)
		}
	}
	won, err := m.store.AdvanceSpawnOp(op.ID, team.StepRegistered, spawnUpdate{Step: team.StepRegistered, State: team.SpawnDone, At: now})
	if err != nil {
		m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
	}
	if won {
		m.logf("[team] spawn %s done: member %s (%s) in %s", op.ID, o.Ref, op.SessionID, op.TmuxName)
		m.wake(op.ID)
	}
}
