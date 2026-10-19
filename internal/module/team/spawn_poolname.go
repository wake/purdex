package teammod

import (
	"context"
	"errors"
	"time"

	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The member name of a spawn (name pool v2 spec §5.2). The team module asks the peers name service for it when it accepts the
// op, stores it on the op (spawn_ops.pool_name) and releases it at every end of the op. The name service is optional: a build
// without peers' service, or a call that fails, leaves the spawn with no reserved name and it goes on as before.

// spawnNameTimeout bounds a call to the name service made on the accept path.
const spawnNameTimeout = 3 * time.Second

// nameService is the peers name service, resolved at use time (the start order of the modules does not matter); nil when the
// registry has none.
func (m *Module) nameService() ipeers.NameService {
	if m.names != nil {
		return m.names
	}
	if m.core == nil || m.core.Registry == nil {
		return nil
	}
	v, ok := m.core.Registry.Get(ipeers.NameServiceKey)
	if !ok {
		return nil
	}
	svc, _ := v.(ipeers.NameService)
	return svc
}

// reserveMemberName reserves a name for op opID, led by leadSessionID, for the tmux session tmuxName; "" when there is none to
// give. Call before the op is written: a refused accept releases it (releaseMemberName).
func (m *Module) reserveMemberName(leadSessionID, opID, tmuxName string, at int64) string {
	svc := m.nameService()
	if svc == nil {
		return ""
	}
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, spawnNameTimeout)
	defer cancel()
	code, err := svc.ReserveMember(ctx, leadSessionID, opID, tmuxName, time.UnixMilli(at))
	if err != nil {
		m.logf("[team] spawn %s: no member name reserved: %v", opID, err)
		return ""
	}
	return code
}

// chooseMemberName is the lead host's choice of a name for a member that will live on another host: reserved to pick with this lead's
// pair history, and given back at once, because this host has no session to name and the member host holds its own reservation. ""
// when there is none to give.
func (m *Module) chooseMemberName(leadSessionID, opID string, at int64) string {
	code := m.reserveMemberName(leadSessionID, opID, "", at)
	if code != "" {
		m.releaseMemberName(opID)
	}
	return code
}

// reserveExactName reserves exactly code for op opID on this (the member) host; "" when it is held here, not a pool name, or the name
// service fails: the session is then named by this host's own rules and the spawn goes on.
func (m *Module) reserveExactName(code, opID string, at int64) string {
	svc := m.nameService()
	if svc == nil {
		return ""
	}
	tmuxName, err := team.SpawnTmuxName(opID)
	if err != nil {
		return ""
	}
	parent := m.stopCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, spawnNameTimeout)
	defer cancel()
	if err := svc.ReserveExact(ctx, code, opID, tmuxName, time.UnixMilli(at)); err != nil {
		if !errors.Is(err, ipeers.ErrNameTaken) && !errors.Is(err, ipeers.ErrNameUnknown) {
			m.logf("[team] spawn %s: no member name reserved: %v", opID, err)
		}
		return ""
	}
	return code
}

// releaseUnlessOpen gives the name of op opID back unless the op is there and still open (a replay of a running spawn, or a command
// that clashed with one: the name is that op's).
func (m *Module) releaseUnlessOpen(opID string) {
	if op, found, err := m.store.GetSpawnOp(opID); err == nil && found && (op.State == team.SpawnRunning || op.State == team.SpawnAwaitingResources) {
		return
	}
	m.releaseMemberName(opID)
}

// releaseMemberName gives the name of op opID back. It is idempotent, and does nothing when the op had none.
func (m *Module) releaseMemberName(opID string) {
	if svc := m.nameService(); svc != nil {
		svc.ReleaseReservation(opID)
	}
}

// releaseMemberNames releases the names of several ops (a team end).
func (m *Module) releaseMemberNames(opIDs []string) {
	for _, id := range opIDs {
		m.releaseMemberName(id)
	}
}
