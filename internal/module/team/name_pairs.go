package teammod

import (
	"context"
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
	memberCode := ""
	if p.TargetHostID == "" {
		memberCode = m.sessionCode(p.TargetSessionID)
	}
	if memberCode == "" {
		memberCode = addressCode(p.TargetName)
	}
	m.recordPair(m.sessionCode(p.LeadSessionID), memberCode, "adopt "+approvalID)
}
