package teammod

// Boot reconciliation of spawn ops (spec §9.3): the daemon reads what is
// there, a step at a time, and never waits for lost hooks.

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// resumeSpawns continues every running op from its recorded step. An op
// past session_created whose tmux session (by name) is gone has nothing to
// continue: abandoned. Every other one gets a runner, whose steps decide
// with one tmux answer each: an accepted op adopts a session of its name
// only when it carries the op's tag (spawnCreate), a launched op past its
// budget is killed and fails member_start_timeout (spawnRegister). The
// store fails a corrupt row abandoned instead of listing it. A read error
// is logged: those ops wait for the next boot.
func (m *Module) resumeSpawns() {
	if m.tmux == nil {
		return
	}
	ops, err := m.store.ListRunningSpawnOps(m.now()) // also fails a corrupt running row, so it comes first
	if err != nil {
		m.logf("[team] boot: spawn ops: %v", err)
		return
	}
	m.reapOrphanSpawnSessions() // before any runner starts: a running op's session is then the runner's alone
	for _, op := range ops {
		if (op.Step == team.StepSessionCreated || op.Step == team.StepLaunched) && !m.sessions.SessionExists(op.TmuxName) {
			m.failSpawn(op.ID, team.SpawnReasonAbandoned)
			continue
		}
		m.startSpawn(op.ID)
	}
	if len(ops) > 0 {
		m.logf("[team] boot: resumed %d spawn op(s)", len(ops))
	}
}

// ensurePluginTree extracts the embedded mod into the launch line's
// --plugin-dir when that tree is absent (spec §7.2 step 4). A tree that is
// there is never rewritten (coordinator decision 9): `pdx setup` owns it. A
// failure is logged; the launch goes on.
func (m *Module) ensurePluginTree() {
	if agentcc.PluginSource == nil {
		return
	}
	if _, err := os.Stat(filepath.Join(agentcc.PluginRoot(m.dataDir), "hooks", "register.js")); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	exe, err := os.Executable()
	if err == nil {
		m.core.CfgMu.RLock()
		cfgPath := m.core.Cfg.Path
		m.core.CfgMu.RUnlock()
		_, _, err = agentcc.ExtractPlugin(agentcc.PluginSource, m.dataDir, buildinfo.Version, exe, cfgPath)
	}
	if err != nil {
		m.logf("[team] spawn: extract the plugin tree: %v", err)
	}
}

// reapOrphanSpawnSessions kills the tmux sessions that carry the spawn tag (spawnTagOption) and belong to a spawn that FAILED
// (#2341): the leftover of a crash between a runner's lost record and its kill (recordSession), or between
// CreateSessionTagged and the record. The op the tag names decides:
//   - running  → the runner's (resumed right after this); left alone;
//   - done     → a LIVE member's session — its tag stays for good; left alone;
//   - failed   → nobody will ever use or kill it; killed;
//   - unknown to this daemon → NOT ours to judge: the tag is an ordinary tmux user option (a user can type anything into it,
//     and a tmux server can be shared with another daemon's sessions), so it only earns a log line.
//
// Only a session that carries the tag is looked at. The kill goes through the generation guard (KillSessionIfInstance) after a
// second identity read, taken right before it, still shows the very session, generation AND tag of the first: an owner that
// changed in between (the user cleared the tag) is left alone. What stays is one tmux round trip between that read and the
// kill; closing it needs a kill that also compares the tag inside the one tmux invocation (a tmux executor change, tracked
// apart). Anything tmux cannot answer is skipped and logged, never guessed.
func (m *Module) reapOrphanSpawnSessions() {
	ctx, cancel := context.WithTimeout(m.stopCtx, 30*time.Second)
	defer cancel()
	sessions, err := m.tmux.ListSessions(ctx)
	if err != nil {
		m.logf("[team] boot: orphan spawn sessions: list tmux sessions: %v", err)
		return
	}
	read := func(name string) (tmux.PaneIdentity, error) {
		rctx, rcancel := context.WithTimeout(ctx, tmuxReadTimeout)
		defer rcancel()
		return m.tmux.PaneIdentity(rctx, "="+name+":", spawnTagOption)
	}
	reaped := 0
	for _, s := range sessions {
		id, err := read(s.Name)
		if err != nil {
			m.logf("[team] boot: orphan spawn sessions: read %s: %v", s.Name, err)
			continue
		}
		if id.Tag == "" {
			continue // not a spawn session
		}
		op, found, err := m.store.GetSpawnOp(id.Tag)
		if err != nil {
			m.logf("[team] boot: orphan spawn sessions: op %s: %v", id.Tag, err)
			continue
		}
		if !found {
			m.logf("[team] boot: tmux session %s carries a spawn tag of no op of this daemon; left alone", s.Name)
			continue
		}
		if op.State != team.SpawnFailed {
			continue // running (a runner's) or done (a member's)
		}
		if m.afterOrphanIdentity != nil {
			m.afterOrphanIdentity(s.Name)
		}
		again, err := read(s.Name)
		if err != nil || again.Instance != id.Instance || again.SessionID != id.SessionID || again.Tag != id.Tag { // ownership only: pane and cwd move with ordinary activity
			m.logf("[team] boot: orphan spawn sessions: %s changed before the kill (%v); left alone", s.Name, err)
			continue
		}
		killed, err := m.tmux.KillSessionIfInstance(id.SessionID, id.Instance)
		switch {
		case err != nil && !errors.Is(err, tmux.ErrNoSession):
			m.logf("[team] boot: orphan spawn sessions: kill %s (op %s): %v", s.Name, id.Tag, err)
		case killed:
			reaped++
			m.logf("[team] boot: killed tmux session %s of failed spawn op %s", s.Name, id.Tag)
		}
	}
	if reaped > 0 {
		m.logf("[team] boot: killed %d orphan spawn session(s)", reaped)
	}
}
