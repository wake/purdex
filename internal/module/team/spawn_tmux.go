package teammod

// The spawn runner's tmux steps: the member's session, tagged with its op
// id at birth, and the launch line typed into it.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	agentcc "github.com/wake/purdex/internal/agent/cc"
	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/team"
	"github.com/wake/purdex/internal/tmux"
)

// spawnTagOption is the session user option that holds the op id: the
// session's ownership token (P4-5 review H3). The full UUID, which the tmux
// name (10 hex digits of it) does not give away.
const spawnTagOption = "@pdx_spawn_op"

// tmuxReadTimeout bounds one identity read of the runner.
const tmuxReadTimeout = 5 * time.Second

// spawnCreate is accepted → session_created (spec §7.2 steps 3–4). The cwd
// the POST checked is resolved and checked against the team's roots again
// (review H2 (a): the path may have changed since). The session is created
// through the session module's create path, tagged with the op id at birth,
// and one identity read confirms its id, generation and tag and gives its
// pane. A session of the op's name that exists already is adopted only when
// that one read shows it carries the op's tag (review H3: a daemon that
// died between the create and its record); untagged, or another op's, it
// is somebody else's (tmux_name_taken), left alone.
func (m *Module) spawnCreate(op spawnRow) bool {
	if m.sessions.SessionExists(op.TmuxName) {
		if id, err := m.paneIdentity("=" + op.TmuxName + ":"); err == nil && id.Tag == op.ID {
			m.logf("[team] spawn %s: adopting its tmux session %s (%s, generation %s)", op.ID, op.TmuxName, id.SessionID, id.Instance)
			return m.recordSession(op, id)
		}
		m.failSpawn(op.ID, team.SpawnReasonNameTaken)
		return false
	}
	cwd, ok := m.underOpRoots(op, op.Cwd)
	if !ok {
		m.logf("[team] spawn %s: cwd %s is no longer under the team's roots", op.ID, op.Cwd)
		m.failSpawn(op.ID, team.SpawnReasonCreateFailed)
		return false
	}
	info, err := m.sessions.CreateSessionTagged(op.TmuxName, cwd, session.SessionTag{Option: spawnTagOption, Value: op.ID})
	if errors.Is(err, session.ErrSessionExists) {
		m.failSpawn(op.ID, team.SpawnReasonNameTaken)
		return false
	}
	if err != nil {
		m.logf("[team] spawn %s: create tmux session %s: %v", op.ID, op.TmuxName, err)
		m.failSpawn(op.ID, team.SpawnReasonCreateFailed)
		return false
	}
	id, err := m.paneIdentity("=" + op.TmuxName + ":")
	if err != nil || id.Instance != info.TmuxInstance || id.SessionID != info.TmuxID || id.Tag != op.ID {
		m.logf("[team] spawn %s: tmux session %s (%s, generation %q) is not confirmed as ours: %+v %v", op.ID, op.TmuxName, info.TmuxID, info.TmuxInstance, id, err)
		m.killSpawnSession(op.ID, info.TmuxID, info.TmuxInstance)
		m.failSpawn(op.ID, team.SpawnReasonCreateFailed)
		return false
	}
	return m.recordSession(op, id)
}

func (m *Module) recordSession(op spawnRow, id tmux.PaneIdentity) bool {
	won, err := m.store.AdvanceSpawnOp(op.ID, team.StepAccepted, spawnUpdate{Step: team.StepSessionCreated,
		TmuxID: id.SessionID, TmuxInstance: id.Instance, PaneID: id.PaneID, At: m.now()})
	if err != nil {
		m.abortSpawn(op.ID, id.SessionID, id.Instance, err)
	}
	if err == nil && !won {
		// the op ended while its session was being created (a team's `end`, #2327): nothing recorded this session, so
		// nobody else will kill it. A runner that merely lost to another runner of the same op leaves it alone.
		if cur, ok, gerr := m.store.GetSpawnOp(op.ID); gerr == nil && (!ok || cur.State != team.SpawnRunning) {
			m.killSpawnSession(op.ID, id.SessionID, id.Instance)
		}
	}
	return err == nil && won
}

// spawnLaunch is session_created → launched (spec §7.2 step 4). Before any
// key: the pane is still the op's (same generation, session and tag) and
// its real directory, as tmux reports it, is under the team's roots (review
// H2 (b)); else the session is killed and nothing is sent. The step is
// recorded BEFORE the keys go out: winning that compare-and-set is the
// right to send, so the line is typed at most once whatever races or
// restarts; a daemon that dies between the record and the send leaves a
// launched op that times out (killed, member_start_timeout).
func (m *Module) spawnLaunch(op spawnRow) bool {
	m.ensurePluginTree()
	line, err := m.memberLaunchLine(op)
	if err == nil {
		err = m.checkLaunchPane(op)
	}
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

func (m *Module) memberLaunchLine(op spawnRow) (string, error) {
	s, err := m.teamCfg.TeamSettings()
	if err != nil {
		return "", fmt.Errorf("team.member_command: %w", err)
	}
	return launchLine(s.MemberCommand, agentcc.PluginRoot(m.dataDir), op.Model, op.Effort)
}

func (m *Module) checkLaunchPane(op spawnRow) error {
	id, err := m.paneIdentity(op.PaneID)
	switch {
	case err != nil:
		return fmt.Errorf("pane %s unreadable: %w", op.PaneID, err)
	case !ownsPane(op, id):
		return fmt.Errorf("pane %s is no longer this op's: %+v", op.PaneID, id)
	}
	if _, ok := m.underOpRoots(op, id.Cwd); !ok {
		return fmt.Errorf("pane %s is in %q, outside the team's roots", op.PaneID, id.Cwd)
	}
	return nil
}

// ownsPane reports whether id, one tmux answer, is the op's pane on the
// server generation it was created in, in its session, carrying its tag.
func ownsPane(op spawnRow, id tmux.PaneIdentity) bool {
	return id.Instance == op.TmuxInstance && id.SessionID == op.TmuxID && id.PaneID == op.PaneID && id.Tag == op.ID
}

func (m *Module) paneIdentity(target string) (tmux.PaneIdentity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxReadTimeout)
	defer cancel()
	return m.tmux.PaneIdentity(ctx, target, spawnTagOption)
}

// underTeamRoots resolves dir (symlinks evaluated) and reports whether it is
// one of the team's granted roots or below one.
func (m *Module) underTeamRoots(teamID, dir string) (string, bool) {
	t, found, err := m.store.TeamByID(teamID)
	if err != nil {
		m.logf("[team] team %s: %v", teamID, err)
	}
	resolved, rerr := filepath.EvalSymlinks(dir)
	if dir == "" || rerr != nil || !found {
		return "", false
	}
	return resolved, underGrant(resolved, t.Grant)
}

// underRoots reports whether dir (symlinks evaluated) is a root or below
// one, each root's symlinks evaluated too, so a link inside a root that
// points outside it is outside (spec §15 "symlink escape").
func underRoots(dir string, roots []string) bool {
	for _, root := range roots {
		if r, err := filepath.EvalSymlinks(root); err == nil && within(dir, r) {
			return true
		}
	}
	return false
}

// within reports whether dir is root or below it, by path components; both are already resolved.
func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
