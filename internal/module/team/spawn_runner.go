package teammod

// The spawn runner (spec §7.2 steps 3–6, §9.3): a state machine persisted in
// spawn_ops, one compare-and-set per step. This file holds its seams, the
// loop and the ways an op ends; the tmux steps are in spawn_tmux.go, the
// registration and the member in spawn_register.go.

import (
	"context"
	"fmt"

	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// sessionCreator is the session module's create path (session.RegistryKey).
type sessionCreator interface {
	SessionExists(name string) bool
	ValidateCwd(cwd string) error
	CreateSessionTagged(name, cwd string, tag session.SessionTag) (*session.SessionInfo, error)
}

// tmuxOps is the generation-guarded part of the tmux executor (c.Tmux).
type tmuxOps interface {
	PaneIdentity(ctx context.Context, target, option string) (tmux.PaneIdentity, error)
	SendKeysIfInstanceTarget(sessionID, window, expectedInstance string, keys ...string) (bool, error)
	KillSessionIfInstance(sessionID, expectedInstance string) (bool, error)
}

// initSpawn resolves the runner's seams. Each service is a hard error, as
// the origin resolver is: a daemon without one is wired wrong. A core
// without a tmux executor (module wiring tests) only disables spawn: the
// POST answers 503 and boot resumes nothing.
func (m *Module) initSpawn(c *core.Core) error {
	var err error
	if m.sessions, err = lookup[sessionCreator](c, session.RegistryKey); err != nil {
		return err
	}
	if m.teamCfg, err = lookup[hostconfig.TeamSettingsReader](c, hostconfig.TeamSettingsKey); err != nil {
		return err
	}
	if c.Tmux != nil {
		m.tmux = c.Tmux
	}
	return m.initRegister(c)
}

func lookup[T any](c *core.Core, key string) (T, error) {
	svc, _ := c.Registry.Get(key)
	v, ok := svc.(T)
	if !ok {
		return v, fmt.Errorf("team: service %q is missing or not a %T (%T)", key, (*T)(nil), svc)
	}
	return v, nil
}

// startSpawn runs op id's remaining steps on a goroutine that Stop joins.
// The caller holds createMu (the POST) or is Start, so the Add never races
// Stop's Wait: Stop cancels stopCtx under createMu before it waits.
func (m *Module) startSpawn(id string) {
	m.spawnWG.Add(1)
	go func() {
		defer m.spawnWG.Done()
		m.runSpawn(id)
	}()
}

// runSpawn drives a running op from its recorded step to done or failed,
// one persisted step at a time. Each step is a compare-and-set from the step
// the runner read, so a runner that loses one (another runner of the same
// op) acts no further. When Stop begins, or the op cannot be read, it
// returns and leaves the op running at its step for the next boot; a step
// team.db refuses to record aborts the op instead (abortSpawn).
func (m *Module) runSpawn(id string) {
	var member *team.Origin // the registry entry the registration step saw
	for !m.stopping() {
		op, ok, err := m.store.GetSpawnOp(id)
		if err != nil {
			m.logf("[team] spawn %s: %v; left running for the next boot", id, err)
			return
		}
		if !ok || op.State != team.SpawnRunning {
			return
		}
		if m.beforeSpawnStep != nil {
			m.beforeSpawnStep(op)
		}
		next := false
		switch op.Step {
		case team.StepAccepted:
			next = m.spawnCreate(op)
		case team.StepSessionCreated:
			next = m.spawnLaunch(op)
		case team.StepLaunched:
			member, next = m.spawnRegister(op)
		case team.StepRegistered:
			m.spawnFinish(op, member)
		default:
			m.logf("[team] spawn %s: no runner step for %q", id, op.Step)
		}
		if !next {
			return
		}
	}
}

// failSpawn ends a running op as failed and wakes its waiting POSTs.
func (m *Module) failSpawn(id, reason string) {
	won, err := m.store.FailSpawnOp(id, reason, m.now())
	if err != nil {
		m.logf("[team] spawn %s: %v", id, err)
	}
	if won {
		m.logf("[team] spawn %s failed: %s", id, reason)
		m.wake(id)
	}
}

// abortSpawn ends an op failed abandoned: team.db refused to record its step
// (a stored row that fails its checks, an update that does not fit, a write
// error; a retry would meet the same refusal, P4-4 review), or its tmux
// session is no longer the one it created. Its session, if any, is killed
// first (generation-guarded), so nothing of a failed op keeps running.
func (m *Module) abortSpawn(id, tmuxID, inst string, err error) {
	m.logf("[team] spawn %s: %v; abandoning it", id, err)
	if tmuxID != "" {
		m.killSpawnSession(id, tmuxID, inst)
	}
	m.failSpawn(id, team.SpawnReasonAbandoned)
}

// killSpawnSession kills the member's tmux session by id, only under the
// generation it was created in: a server that restarted since declines,
// and whatever holds that id now is left alone (I3).
func (m *Module) killSpawnSession(opID, tmuxID, inst string) {
	if killed, err := m.tmux.KillSessionIfInstance(tmuxID, inst); err != nil || !killed {
		m.logf("[team] spawn %s: tmux session %s not killed (generation moved or gone): %v", opID, tmuxID, err)
	}
}
