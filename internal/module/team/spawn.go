package teammod

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/agent"
	"github.com/wake/purdex/internal/module/hostconfig"
	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// The spawn runner's seams (spec §7.2 steps 3–6), each type-asserted at
// Init on what the daemon already has; tests inject fakes.

// sessionCreator is the session module's create path (session.RegistryKey).
type sessionCreator interface {
	SessionExists(name string) bool
	ValidateCwd(cwd string) error
	CreateSession(name, cwd string) (*session.SessionInfo, error)
	TmuxInstance() string
}

// tmuxOps is the generation-guarded part of the tmux executor (c.Tmux).
type tmuxOps interface {
	ActivePaneMetadata(ctx context.Context, sessionName string) (tmux.TmuxPaneMetadata, error)
	SendKeysIfInstanceTarget(sessionID, window, expectedInstance string, keys ...string) (bool, error)
	KillSessionIfInstance(sessionID, expectedInstance string) (bool, error)
}

// frameReader lists the live agent frames (agent.TerminalSessionsKey).
type frameReader interface {
	LiveSessions(ctx context.Context, agentType string) ([]agent.TerminalSession, error)
}

// TitleSetter sets a session's title: *store.PeerLabelStore, the value
// WithTitles was given. Without it a spawned member keeps no title.
type TitleSetter interface {
	Claim(sessionID, label string, now time.Time) (store.PeerLabel, error)
}

// paneReadTimeout bounds one pane metadata read of the runner.
const paneReadTimeout = 5 * time.Second

// initSpawn resolves the runner's seams. Each is a hard error, as the origin
// resolver is: a daemon without one is wired wrong and no spawn could run.
func (m *Module) initSpawn(c *core.Core) error {
	var err error
	if m.sessions, err = lookup[sessionCreator](c, session.RegistryKey); err != nil {
		return err
	}
	if m.frames, err = lookup[frameReader](c, agent.TerminalSessionsKey); err != nil {
		return err
	}
	if m.teamCfg, err = lookup[hostconfig.TeamSettingsReader](c, hostconfig.TeamSettingsKey); err != nil {
		return err
	}
	if c.Tmux == nil {
		return errors.New("team: the core has no tmux executor")
	}
	m.tmux = c.Tmux
	m.titleSet, _ = m.titles.(TitleSetter)
	return nil
}

func lookup[T any](c *core.Core, key string) (T, error) {
	svc, _ := c.Registry.Get(key)
	v, ok := svc.(T)
	if !ok {
		return v, fmt.Errorf("team: service %q is missing or not a %T (%T)", key, (*T)(nil), svc)
	}
	return v, nil
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

// startSpawn runs op id's remaining steps on a goroutine that Stop joins.
// The caller holds createMu (the POST) or is Start, so the Add never races
// Stop's Wait: Stop cancels stopCtx under createMu before it waits.
func (m *Module) startSpawn(id string, resumed bool) {
	m.spawnWG.Add(1)
	go func() {
		defer m.spawnWG.Done()
		m.runSpawn(id, resumed)
	}()
}

// runSpawn drives a running op from its recorded step to done or failed
// (spec §7.2 steps 3–6, §9.3), one persisted step at a time. Each step is a
// compare-and-set from the step the runner read, so a runner that loses one
// (another runner of the same op) acts no further. When Stop begins, or an
// op cannot be read, it returns and leaves the op running at its step for
// the next boot; a step team.db refuses to record aborts the op instead
// (abortSpawn). resumed is a runner Start began.
func (m *Module) runSpawn(id string, resumed bool) {
	var member *team.Origin // the registry entry the registration step saw
	for first := true; !m.stopping(); first = false {
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
		switch {
		case resumed && first && (op.Step == team.StepSessionCreated || op.Step == team.StepLaunched) &&
			!m.sessions.SessionExists(op.TmuxName):
			m.failSpawn(op.ID, team.SpawnReasonAbandoned) // spec §9.3: nothing left to continue
		case op.Step == team.StepAccepted:
			next = m.spawnCreate(op, resumed)
		case op.Step == team.StepSessionCreated:
			next = m.spawnLaunch(op)
		case op.Step == team.StepLaunched:
			member, next = m.spawnRegister(op)
		case op.Step == team.StepRegistered:
			m.spawnFinish(op, member)
		default:
			m.logf("[team] spawn %s: unknown step %q", id, op.Step)
		}
		if !next {
			return
		}
	}
}

// spawnCreate is accepted → session_created (spec §7.2 steps 3–4): the
// member's tmux session, through the session module's create path, under
// the name the op's id gives. A session of that name on a new op is
// somebody else's (tmux_name_taken). On a resumed op it is this op's,
// created by a daemon that stopped before recording it: it is adopted under
// the tmux generation that holds it now, read on both sides of the pane read.
func (m *Module) spawnCreate(op spawnRow, resumed bool) bool {
	if m.sessions.SessionExists(op.TmuxName) {
		if !resumed {
			m.failSpawn(op.ID, team.SpawnReasonNameTaken)
			return false
		}
		inst := m.sessions.TmuxInstance()
		md, err := m.paneOf(op.TmuxName)
		if err != nil || inst == "" || inst != m.sessions.TmuxInstance() || md.SessionID == "" || md.PaneID == "" {
			m.logf("[team] spawn %s: cannot adopt tmux session %s (generation %q): %v", op.ID, op.TmuxName, inst, err)
			m.failSpawn(op.ID, team.SpawnReasonCreateFailed)
			return false
		}
		return m.recordSession(op, md.SessionID, inst, md.PaneID)
	}
	info, err := m.sessions.CreateSession(op.TmuxName, op.Cwd)
	if errors.Is(err, session.ErrSessionExists) {
		m.failSpawn(op.ID, team.SpawnReasonNameTaken)
		return false
	}
	if err != nil {
		m.logf("[team] spawn %s: create tmux session %s: %v", op.ID, op.TmuxName, err)
		m.failSpawn(op.ID, team.SpawnReasonCreateFailed)
		return false
	}
	md, err := m.paneOf(op.TmuxName)
	if err != nil || md.SessionID != info.TmuxID || md.PaneID == "" || info.TmuxInstance == "" {
		m.logf("[team] spawn %s: tmux session %s (%s, generation %q) unreadable: %v", op.ID, op.TmuxName, info.TmuxID, info.TmuxInstance, err)
		m.killSpawnSession(op.ID, info.TmuxID, info.TmuxInstance)
		m.failSpawn(op.ID, team.SpawnReasonCreateFailed)
		return false
	}
	return m.recordSession(op, info.TmuxID, info.TmuxInstance, md.PaneID)
}

func (m *Module) paneOf(name string) (tmux.TmuxPaneMetadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), paneReadTimeout)
	defer cancel()
	return m.tmux.ActivePaneMetadata(ctx, name)
}

func (m *Module) recordSession(op spawnRow, tmuxID, inst, pane string) bool {
	won, err := m.store.AdvanceSpawnOp(op.ID, team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated,
		TmuxID: tmuxID, TmuxInstance: inst, PaneID: pane, At: m.now()})
	if err != nil {
		m.abortSpawn(op.ID, tmuxID, inst, err)
	}
	return err == nil && won
}

// spawnLaunch is session_created → launched (spec §7.2 step 4): the launch
// line, typed into window 0 under the generation the session was created
// in. The step is recorded BEFORE the keys go out: winning that compare-
// and-set is the right to send, so the line is typed at most once whatever
// races or restarts; a daemon that dies between the record and the send
// leaves a launched op that times out (killed, member_start_timeout).
func (m *Module) spawnLaunch(op spawnRow) bool {
	m.ensurePluginTree()
	line, err := m.memberLaunchLine(op)
	if err != nil {
		m.logf("[team] spawn %s: %v", op.ID, err)
		m.killSpawnSession(op.ID, op.TmuxID, op.TmuxInstance)
		m.failSpawn(op.ID, team.SpawnReasonLaunchFailed)
		return false
	}
	at := m.now()
	won, err := m.store.AdvanceSpawnOp(op.ID, team.StepSessionCreated, spawnUpdate{Step: team.StepLaunched, LaunchedAt: at, At: at})
	if err != nil {
		m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
	}
	if err != nil || !won {
		return false
	}
	sent, err := m.tmux.SendKeysIfInstanceTarget(op.TmuxID, "0", op.TmuxInstance, line+"\n")
	if err != nil || !sent {
		m.logf("[team] spawn %s: launch line not sent to %s (generation %s): %v", op.ID, op.TmuxID, op.TmuxInstance, err)
		m.killSpawnSession(op.ID, op.TmuxID, op.TmuxInstance)
		m.failSpawn(op.ID, team.SpawnReasonLaunchFailed)
		return false
	}
	return true
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

func (m *Module) memberLaunchLine(op spawnRow) (string, error) {
	s, err := m.teamCfg.TeamSettings()
	if err != nil {
		return "", fmt.Errorf("team.member_command: %w", err)
	}
	return launchLine(s.MemberCommand, agentcc.PluginRoot(m.dataDir), op.Model, op.Effort)
}

// spawnRegister is launched → registered (spec §7.2 step 5): until
// launched_at + the budget, a verified root frame on the member's pane with
// a session id whose registry entry is live, so the ref is known. Past the
// budget the session is killed and the op fails member_start_timeout: it no
// longer counts against the limit.
func (m *Module) spawnRegister(op spawnRow) (*team.Origin, bool) {
	for {
		if o, ok := m.memberOnPane(op.PaneID); ok {
			won, err := m.store.AdvanceSpawnOp(op.ID, team.StepLaunched, spawnUpdate{Step: team.StepRegistered, SessionID: o.SessionID, At: m.now()})
			if err != nil {
				m.abortSpawn(op.ID, op.TmuxID, op.TmuxInstance, err)
			}
			return &o, err == nil && won
		}
		if m.now() >= op.LaunchedAt+m.spawnBudget {
			m.killSpawnSession(op.ID, op.TmuxID, op.TmuxInstance)
			m.failSpawn(op.ID, team.SpawnReasonStartTimeout)
			return nil, false
		}
		m.spawnSleep(m.stopCtx, m.spawnPoll)
		if m.stopping() {
			return nil, false
		}
	}
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
// registry entry the registration saw (nil when resumed at registered:
// read again, or the ref alone if gone).
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

// abortSpawn ends an op failed abandoned when team.db refused to record its
// step: a stored row that fails its checks, an update that does not fit, or
// a write error. A retry would meet the same refusal (P4-4 review), and a
// running op holds a place in its team. The tmux session the op holds, if
// any, is killed first (generation-guarded), so nothing of it keeps running.
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
