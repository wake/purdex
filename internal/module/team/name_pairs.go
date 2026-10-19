package teammod

import (
	"context"
	"database/sql"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The lead/member pairs of the name history (name pool v2 spec §6.1): one pair is written, through the name service, when a spawned
// member's op is registered and when an adopt is approved. The name service writes it only when both are persona codes, and every
// failure is only logged: a pair is history for the next pick, never a reason to fail the spawn or the approval.

// recordPair writes one lead/member pair by code; nothing is written for an empty code, or without a name service.
func (m *Module) recordPair(leadCode, memberCode, what string) {
	if leadCode == "" || memberCode == "" {
		return
	}
	svc := m.nameService()
	if svc == nil {
		return
	}
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, leadNameTimeout)
	defer cancel()
	if err := svc.RecordPairCodes(ctx, leadCode, memberCode); err != nil {
		m.logf("[team] %s: record pair %s/%s: %v", what, leadCode, memberCode, err)
	}
}

// sessionCode is the persona code the session's name ends in ("" when it has none, or without a name service).
func (m *Module) sessionCode(sessionID string) string {
	svc := m.nameService()
	if svc == nil || sessionID == "" {
		return ""
	}
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, leadNameTimeout)
	defer cancel()
	code, _ := svc.CodeOf(ctx, sessionID)
	return code
}

// addressCode is the persona code the name of an address ("<alias>/<name>") ends in; "" for an address by ref or a name without one.
func addressCode(address string) string {
	name := address
	if i := strings.LastIndexByte(address, '/'); i >= 0 {
		name = address[i+1:]
	}
	code, _ := ipeers.PoolNameOf(name)
	return code
}

// recordRegisteredPair writes the pair of a spawn op that has registered: the code of the team's lead as it is now (it may have
// relayed since the accept) and the name the op carries. It runs on the LEAD host — for a local op when the op finishes, for a
// forwarded one when the member host's `registered` fact is applied — because that is where the lead's code and the history are. The
// member host of a forwarded op writes nothing.
func (m *Module) recordRegisteredPair(teamID, poolName, opID string) {
	if poolName == "" {
		return
	}
	t, ok, err := m.store.TeamByID(teamID)
	if err != nil || !ok {
		return
	}
	m.recordPair(m.sessionCode(t.LeadSessionID), poolName, "spawn "+opID)
}

// recordAdoptPairAtApproval is recordAdoptPair for the moment an adopt is approved: a target on this host is a member from then on; one
// on another host is not yet (the member host can still refuse it), so its pair waits for noteAdoptPair / afterAdoptPairs.
func (m *Module) recordAdoptPairAtApproval(p team.AdoptPayload, approvalID string) {
	if p.TargetHostID != "" {
		return
	}
	m.recordAdoptPair(p, approvalID)
}

// noteAdoptPair notes, inside the settle transaction tx, that the remote adopt of approval approvalID became active. The pair is
// written by afterAdoptPairs once tx has committed (dropAdoptPairs forgets it when it did not).
func (m *Module) noteAdoptPair(tx *sql.Tx, approvalID string, memberCode *string) {
	m.endedOpsMu.Lock()
	defer m.endedOpsMu.Unlock()
	if m.adoptPairs == nil {
		m.adoptPairs = map[*sql.Tx][]adoptPairNote{}
	}
	m.adoptPairs[tx] = append(m.adoptPairs[tx], adoptPairNote{approvalID, memberCode})
}

// adoptPairNote is one remote adopt that became active: its approval, and the persona code the member host reported ("" = none).
type adoptPairNote struct {
	approvalID string
	memberCode *string
}

func (m *Module) takeAdoptPairs(tx *sql.Tx) []adoptPairNote {
	m.endedOpsMu.Lock()
	defer m.endedOpsMu.Unlock()
	ids := m.adoptPairs[tx]
	delete(m.adoptPairs, tx)
	return ids
}

func (m *Module) dropAdoptPairs(tx *sql.Tx) { m.takeAdoptPairs(tx) }

// afterAdoptPairs writes the pair of each remote adopt the committed settle tx made active, from the approval's payload.
func (m *Module) afterAdoptPairs(tx *sql.Tx) {
	for _, note := range m.takeAdoptPairs(tx) {
		id := note.approvalID
		a, _, err := m.store.getRow(id)
		if err != nil {
			m.logf("[team] adopt %s: pair not recorded, its approval is unreadable: %v", id, err)
			continue
		}
		p, err := team.AdoptPayloadOf(a)
		if err != nil {
			m.logf("[team] adopt %s: pair not recorded: %v", id, err)
			continue
		}
		m.recordAdoptPairAs(p, id, note.memberCode)
	}
}

// recordRemoteSpawnPair writes the pair of a forwarded spawn after its `registered` fact was applied here (not a replay of it): only
// when the op is done — a session_conflict fails the op while the fact still answers applied — and carries a name.
func (m *Module) recordRemoteSpawnPair(fromHost string, fact team.TeamFact) {
	op, found, err := m.store.GetRemoteSpawn(fact.MK)
	if err != nil || !found || op.HostID != fromHost || op.TeamID != fact.TeamID || op.State != remoteSpawnDone {
		return
	}
	m.recordRegisteredPair(op.TeamID, op.PoolName, op.ID)
}

// recordAdoptPair writes the pair of an adopt that was approved: the lead's code and the adopted session's (spec §5.4: it keeps its
// name and counts from now on). A local target's code is read from the session; a remote one has only the name the request carried.
func (m *Module) recordAdoptPair(p team.AdoptPayload, approvalID string) {
	m.recordAdoptPairAs(p, approvalID, nil)
}

// recordAdoptPairAs is recordAdoptPair with the code the member host reported (reported). A reported code, even an empty one, is
// the answer: the session has none now and no pair is written. Not reported (an older member host, or a local target) falls back
// to what this host can tell: the session's code for a local target, the name the request carried for a remote one.
func (m *Module) recordAdoptPairAs(p team.AdoptPayload, approvalID string, reported *string) {
	var memberCode string
	switch {
	case reported != nil:
		memberCode = *reported
	default:
		if p.TargetHostID == "" {
			memberCode = m.sessionCode(p.TargetSessionID)
		}
		if memberCode == "" {
			memberCode = addressCode(p.TargetName)
		}
	}
	m.recordPair(m.sessionCode(p.LeadSessionID), memberCode, "adopt "+approvalID)
}
