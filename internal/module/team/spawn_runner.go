package teammod

// The spawn runner (spec §7.2 steps 3–6, §9.3): a state machine persisted in
// spawn_ops, one compare-and-set per step. This file holds its seams, the
// loop and the ways an op ends; the tmux steps are in spawn_tmux.go, the
// registration and the member in spawn_register.go.

import (
	"context"
	"errors"
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
	ListSessions(ctx context.Context) ([]tmux.TmuxSession, error)
	SendKeysIfInstanceTarget(sessionID, window, expectedInstance string, keys ...string) (bool, error)
	KillSessionIfInstance(sessionID, expectedInstance string) (bool, error)
	KillSessionIfTagged(sessionID, expectedInstance, option, value string) (bool, error)
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
			m.reapFailedSpawn(op, ok)
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
			cur, ok, _ := m.store.GetSpawnOp(id)
			m.reapFailedSpawn(cur, ok)
			return
		}
	}
}

// reapFailedSpawn kills the session of an op that was failed abandoned by someone else — a team that ended (#2384), an
// abort of another runner — when this runner stops on it: the one that failed the op may not have known the session
// (it was created after), or is gone. Generation-guarded; a session already gone is not an error. Other failure
// reasons kill their own session before they fail the op.
func (m *Module) reapFailedSpawn(op spawnRow, ok bool) {
	if ok && op.State == team.SpawnFailed {
		m.wake(op.ID) // whoever failed it (a team end writes no wake of its own) leaves the requests waiting on it to this runner
		m.rosterChanged()
	}
	if _, mine := m.abortKilled.LoadAndDelete(op.ID); mine {
		return // this runner's own abort killed it already
	}
	if !ok || op.State != team.SpawnFailed || op.Reason != team.SpawnReasonAbandoned || op.TmuxID == "" {
		return
	}
	if _, err := m.tmux.KillSessionIfInstance(op.TmuxID, op.TmuxInstance); err != nil && !errors.Is(err, tmux.ErrNoSession) {
		m.logf("[team] spawn %s: session %s of the failed op not killed: %v", op.ID, op.TmuxID, err)
	}
}

// failSpawn ends a running op as failed and wakes its waiting POSTs.
func (m *Module) failSpawn(id, reason string) { m.failSpawnWon(id, reason) }

// failSpawnWon is failSpawn that says whether this call is the one that ended the op.
func (m *Module) failSpawnWon(id, reason string) bool {
	won, err := m.store.FailSpawnOp(id, reason, m.now())
	if err != nil {
		m.logf("[team] spawn %s: %v", id, err)
	}
	if won {
		m.logf("[team] spawn %s failed: %s", id, reason)
		m.rosterChanged() // the failure is committed: the seat is free again (in_use)
		m.kickFacts()     // a forwarded op's spawn_failed fact is committed with it
		m.wake(id)
	}
	return won
}

// abortSpawn ends an op failed abandoned: team.db refused to record its step
// (a stored row that fails its checks, an update that does not fit, a write
// error; a retry would meet the same refusal, P4-4 review), or its tmux
// session is no longer the one it created. The op is failed FIRST and only the call that wins that
// compare-and-set kills the session (generation-guarded): another runner of the op that finished it, or someone who
// already ended it, owns the session from there (#2384). A crash between the two leaves the session of a failed op,
// which the boot sweep reaps.
func (m *Module) abortSpawn(id, tmuxID, inst string, err error) {
	m.logf("[team] spawn %s: %v; abandoning it", id, err)
	if m.failSpawnWon(id, team.SpawnReasonAbandoned) && tmuxID != "" {
		m.abortKilled.Store(id, true)
		m.killSpawnSession(id, tmuxID, inst)
	}
}

// killSpawnSession kills the member's tmux session by id, only under the
// generation it was created in: a server that restarted since declines,
// and whatever holds that id now is left alone (I3).
func (m *Module) killSpawnSession(opID, tmuxID, inst string) {
	if killed, err := m.tmux.KillSessionIfInstance(tmuxID, inst); err != nil || !killed {
		m.logf("[team] spawn %s: tmux session %s not killed (generation moved or gone): %v", opID, tmuxID, err)
	}
}
