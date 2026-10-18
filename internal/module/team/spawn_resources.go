package teammod

// RT-5a, spec §4.6: before a new local spawn launches anything the daemon reads the host zone. Normal or unknown goes ahead as
// before. Warning or danger opens a spawn_resources card for the person (the op waits in awaiting_resources, holding its seat),
// or, with the unattended switch on, refuses an agent's spawn; the person's own /team spawn always gets the card.

import (
	"encoding/json"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/team"
)

// hostDetail is the latest host reading with the lease lists, from the one reader the resources module registers
// (resources.HostReaderKey). ok is false without a reader or a usable sample: the caller treats that as an unknown zone.
func (m *Module) hostDetail() (resources.HostDetail, bool) {
	hr := m.hostReader
	if hr == nil && m.core != nil && m.core.Registry != nil { // looked up here: the resources module may Init after this one
		if svc, ok := m.core.Registry.Get(resources.HostReaderKey); ok {
			hr, _ = svc.(resources.HostReader)
		}
	}
	if hr == nil {
		return resources.HostDetail{}, false
	}
	if d, ok := hr.(resources.HostDetailReader); ok {
		return d.LatestHostDetail()
	}
	h, ok := hr.LatestHost()
	return resources.HostDetail{Host: h}, ok
}

// spawnResourceCheckOn is the host switch relay.spawn_resource_check (RT-5a): off by default, and off whenever it cannot be read,
// so a spawn then goes ahead exactly as it did before the card existed.
func (m *Module) spawnResourceCheckOn() bool {
	if m.switches == nil {
		return false
	}
	sw, err := m.switches.RelaySwitches()
	return err == nil && sw.SpawnResourceCheck
}

// spawnResourcesPayload is the card a spawn needs, or nil when the zone lets it through (normal, unknown, no reader, no sample).
func (m *Module) spawnResourcesPayload(req team.SpawnRequest, cwd string, person bool, teamID string, lead team.Origin) *team.SpawnResourcesPayload {
	d, ok := m.hostDetail()
	if !ok || (d.Host.Zone != "warning" && d.Host.Zone != "danger") {
		return nil
	}
	p := &team.SpawnResourcesPayload{
		SpawnID: req.ID, TeamID: teamID, LeadRef: lead.Ref, LeadTitle: lead.Title, Cwd: cwd,
		Title: req.Title, Model: req.Model, Effort: req.Effort, TaskSubject: taskSubjectOf(req.Task), Person: person,
		Host: team.SpawnResourcesHost{Zone: d.Host.Zone, Trend: d.Host.Trend, Mem: d.Host.Mem, Pressure: d.Host.Pressure,
			Load1: d.Host.Load1, NCPU: d.Host.NCPU, SampledAt: d.SampledAt},
		Heavy: []team.SpawnResourcesHeavy{}, Queue: []team.SpawnResourcesQueued{},
	}
	// heavy and queue stay empty: the held leases and the queue they were read from are gone (WWD-0a); the gate's holders
	// take their place when the wwd has them.
	return p
}

// spawnCardRow is the approval row of a spawn's resource card: its id is the spawn's, so the spawn client, the card and the op
// are one thing to follow.
func (m *Module) spawnCardRow(id string, origin team.Origin, p *team.SpawnResourcesPayload, now int64) (team.Approval, string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return team.Approval{}, "", err
	}
	row := team.Approval{
		ID: id, Kind: team.KindSpawnResources, HostID: m.hostID(), Origin: origin, Payload: raw, State: team.StateOpen,
		CreatedAt: now, DeadlineAt: now + team.SpawnResourcesDeadlineS*1000, LeaseUntil: now + team.LeaseS*1000,
	}
	return row, requestHash(team.KindSpawnResources, origin.SessionID, team.SpawnResourcesDeadlineS, raw), nil
}

// afterSpawnCard follows the close of a spawn_resources card: the op leaves awaiting_resources with it. Approved → running and
// its runner starts; denied → failed spawn_denied; timeout → spawn_timeout; closed unattended_refused → that; any other end
// (cancelled, abandoned: the CLI stopped polling, or the lead went) → spawn_abandoned. The seat is free on every failure. Like
// afterClose's relay follow-up it is a second write: a failure is logged and settleAwaitingSpawns re-derives it (at boot, and on the sweeper's liveness tick for a daemon that stays up).
func (m *Module) afterSpawnCard(a team.Approval) { m.settleSpawnCard(a, true) }

// settleSpawnCard is afterSpawnCard; start says whether an approved op's runner starts here (boot leaves that to the resume).
func (m *Module) settleSpawnCard(a team.Approval, start bool) {
	approve := a.State == team.StateApproved
	reason := ""
	if !approve {
		switch {
		case a.State == team.StateDenied:
			reason = team.SpawnReasonDenied
		case a.State == team.StateTimeout:
			reason = team.SpawnReasonTimeout
		case a.CloseReason == team.ErrUnattendedRefused:
			reason = team.ErrUnattendedRefused
		default:
			reason = team.SpawnReasonCLIGone
		}
	}
	won, err := m.store.SettleAwaitingSpawn(a.ID, approve, reason, m.now())
	if err != nil {
		m.logf("[team] spawn %s: settle after its resource card %s: %v", a.ID, a.State, err)
		return
	}
	if !won {
		return
	}
	if approve {
		m.logf("[team] spawn %s approved on the resource card", a.ID)
		if start {
			m.startSpawn(a.ID)
		}
	} else {
		m.logf("[team] spawn %s failed: %s (resource card %s)", a.ID, reason, a.State)
		m.rosterChanged() // the seat is free again
	}
	m.wake(a.ID) // the spawn client waits on the op's id
}

// closeOrphanSpawnCards cancels a resource card whose op was failed under it (the team ended): the person is not asked to let
// through a spawn that no longer exists, and an approve of it would report a success that starts nothing.
func (m *Module) closeOrphanSpawnCards() {
	ids, err := m.store.OrphanSpawnCards()
	if err != nil {
		m.logf("[team] sweep: orphan spawn cards: %v", err)
		return
	}
	for _, id := range ids {
		if _, won, err := m.closeAs(id, Close{State: team.StateCancelled, DecidedAt: m.now()}); err != nil {
			m.logf("[team] sweep: close orphan spawn card %s: %v", id, err)
		} else if won {
			m.logf("[team] spawn card %s cancelled: its spawn is no longer waiting", id)
		}
	}
}

// settleAwaitingSpawns settles ops whose card closed without the follow-up (a crash, or a store error between the two writes), and fails an op
// that has no open card at all. An op whose card is still open keeps waiting; the sweeper owns its deadline.
func (m *Module) settleAwaitingSpawns(start bool) {
	ops, err := m.store.AwaitingSpawnOps()
	if err != nil {
		m.logf("[team] boot: awaiting spawn ops: %v", err)
		return
	}
	for _, op := range ops {
		a, ok, err := m.store.Get(op.ID)
		switch {
		case err != nil:
			m.logf("[team] boot: spawn %s card: %v", op.ID, err)
		case !ok:
			m.settleSpawnCard(team.Approval{ID: op.ID, State: team.StateAbandoned}, start)
		case a.State != team.StateOpen:
			m.settleSpawnCard(a, start)
		}
	}
}
