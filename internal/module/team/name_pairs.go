package teammod

import (
	"context"
	"strings"

	ipeers "github.com/wake/purdex/internal/peers"
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

// recordSpawnPair is the pair of a spawned member whose op reached registered: the lead's code and the op's reserved name. An op
// without a name writes nothing. A local op's lead is the team's lead now (it may have relayed since the accept).
func (m *Module) recordSpawnPair(leadCode, poolName, opID string) {
	if poolName == "" {
		return
	}
	m.recordPair(leadCode, poolName, "spawn "+opID)
}
