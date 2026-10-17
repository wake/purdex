package teammod

// What tmux says about the pane a registry entry runs in: the generation of the server and the code of the session. The roster and the
// fresh list carry them (tmux_instance, code) so an App can tell a seat from a same-named session of a restarted tmux.

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/module/session"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
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

// startedUnderServer says whether the agent of o started under the tmux server of the generation instance ("<pid>:<start_time>"): a
// process cannot be in a pane of a server that did not exist yet, so an entry that started BEFORE the server did belongs to an older
// one, and a pane id the restarted server hands out again is not its pane. The registry records the agent's start time when it
// registers (Origin.ProcStart), which survives a tmux session rename, unlike the session name in Origin.Tmux. Either time unreadable
// is "cannot tell" (false): the entry then carries nothing rather than something wrong.
func startedUnderServer(o team.Origin, instance string) bool {
	_, startText, ok := strings.Cut(instance, ":")
	if !ok {
		return false
	}
	serverStart, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || serverStart <= 0 {
		return false
	}
	agentStart, err := ipeers.ParseProcStart(o.ProcStart)
	if err != nil {
		return false
	}
	// Both are creation times cut to the whole second, so an agent that started under the server is never earlier than it; no slack,
	// or an agent that died a second before a quick restart would pass for the new server's.
	return agentStart.Unix() >= serverStart
}

// paneIdentityOf reads the pane an Origin names and checks that the entry belongs to the server that answered (startedUnderServer);
// false when it is not in tmux, tmux is not wired or does not answer, or the entry is older than the server.
func (m *Module) paneIdentityOf(ctx context.Context, o team.Origin) (tmux.PaneIdentity, bool) {
	pane := originPane(o.Tmux)
	if pane == "" || m.tmux == nil {
		return tmux.PaneIdentity{}, false
	}
	id, err := m.tmux.PaneIdentity(ctx, pane, spawnTagOption)
	if err != nil || !startedUnderServer(o, id.Instance) {
		return tmux.PaneIdentity{}, false
	}
	return id, true
}

// sessionCodeOf is the session code of the tmux session an Origin runs in ("" when unknown).
func (m *Module) sessionCodeOf(ctx context.Context, o team.Origin) string {
	id, ok := m.paneIdentityOf(ctx, o)
	if !ok {
		return ""
	}
	code, err := session.EncodeSessionID(id.SessionID)
	if err != nil {
		return ""
	}
	return code
}

// generationMemo reads the tmux generation once for a roster build: the server has one, so the first pane that answers settles it
// (a pane that does not answer is tried again with the next session, at most maxGenerationMisses times). Each entry is then
// checked against it on its own (startedUnderServer).
type generationMemo struct {
	m      *Module
	ctx    context.Context // one budget for the build (originProbeTimeout)
	val    string
	misses int
}

const maxGenerationMisses = 3

func (g *generationMemo) of(o team.Origin) string {
	pane := originPane(o.Tmux)
	if pane == "" {
		return "" // not in tmux: no generation, whatever another session said
	}
	if g.val == "" && g.misses < maxGenerationMisses && g.m.tmux != nil {
		if id, err := g.m.tmux.PaneIdentity(g.ctx, pane, spawnTagOption); err == nil {
			g.val = id.Instance
		} else {
			g.misses++
		}
	}
	if g.val == "" || !startedUnderServer(o, g.val) {
		return ""
	}
	return g.val
}
