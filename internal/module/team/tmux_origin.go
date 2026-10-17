package teammod

// What tmux says about the pane a registry entry runs in: the generation of the server and the code of the session. The roster and the
// fresh list carry them (tmux_instance, code) so an App can tell a seat from a same-named session of a restarted tmux.

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/wake/purdex/internal/module/session"
	"github.com/wake/purdex/internal/tmux"
)

// originProbeTimeout bounds ALL the pane reads of one roster build or fresh list: they must not wait on a tmux that does not answer,
// whatever the number of sessions (a read that finds the budget spent answers at once).
const originProbeTimeout = 2 * time.Second

var originPanePattern = regexp.MustCompile(`^%[0-9]+$`)

// originPane is the pane id of an Origin.Tmux ("<session>:@<win>.%<pane>"); "" when the session is not in tmux.
func originPane(origin string) string {
	i := strings.LastIndex(origin, ".%")
	if i < 0 {
		return ""
	}
	if pane := origin[i+1:]; originPanePattern.MatchString(pane) {
		return pane
	}
	return ""
}

// The pane id alone names the pane, not the session name in the Origin: the registry keeps the name the agent started under, and a
// renamed tmux session is still the same pane (TestBuild_SessionRowTracksLiveTmuxRename). A pane id a restarted tmux hands out again
// cannot reach a LIVE entry: the agent process died with the old server.
//
// paneIdentityOf reads the pane an Origin.Tmux names; false when it is not in tmux, tmux is not wired, or tmux does not answer.
func (m *Module) paneIdentityOf(ctx context.Context, origin string) (tmux.PaneIdentity, bool) {
	pane := originPane(origin)
	if pane == "" || m.tmux == nil {
		return tmux.PaneIdentity{}, false
	}
	id, err := m.tmux.PaneIdentity(ctx, pane, spawnTagOption)
	if err != nil {
		return tmux.PaneIdentity{}, false
	}
	return id, true
}

// sessionCodeOf is the session code of the tmux session an Origin.Tmux runs in ("" when unknown).
func (m *Module) sessionCodeOf(ctx context.Context, origin string) string {
	id, ok := m.paneIdentityOf(ctx, origin)
	if !ok {
		return ""
	}
	code, err := session.EncodeSessionID(id.SessionID)
	if err != nil {
		return ""
	}
	return code
}

// generationMemo reads the tmux generation once for a roster build: the server has one, so the first pane that answers settles it.
// A pane that does not answer is tried again with the next session, at most maxGenerationMisses times.
type generationMemo struct {
	m      *Module
	ctx    context.Context // one budget for the build (originProbeTimeout)
	val    string
	misses int
}

const maxGenerationMisses = 3

func (g *generationMemo) of(origin string) string {
	if originPane(origin) == "" {
		return "" // not in tmux: no generation, whatever another session said
	}
	if g.val != "" || g.misses >= maxGenerationMisses {
		return g.val
	}
	if id, ok := g.m.paneIdentityOf(g.ctx, origin); ok {
		g.val = id.Instance
	} else {
		g.misses++
	}
	return g.val
}
