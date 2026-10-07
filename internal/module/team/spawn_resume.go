package teammod

// Boot reconciliation of spawn ops (spec §9.3): the daemon reads what is
// there, a step at a time, and never waits for lost hooks.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/team"
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
	ops, err := m.store.ListRunningSpawnOps(m.now())
	if err != nil {
		m.logf("[team] boot: spawn ops: %v", err)
		return
	}
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
