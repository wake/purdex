package agent

import (
	"context"
	"strings"
)

// ConfirmedOwners returns the live panes that run the Claude Code session sessionID, each confirmed by the same
// owner resolution every other caller uses (process generation, pane membership, the second pane listing): the
// conversation API's "live pane" source (spec §8.2).
//
// The frames store finds the candidates by session id (cheap, no tmux); with none it stops there. Otherwise ONE
// OwnerPass does the rest — its first pane listing places the candidate panes in their tmux sessions, its second
// confirms them, so the whole query is two listings and one process view. Every confirmed root frame that reports
// sessionID counts, one per pane, even when a sibling pane of the same tmux session runs a newer conversation (the
// pass's session-wide winner is not the question here). Several panes are all returned, unordered: the caller picks
// by LastSeenAt. The deadline (provenanceTimeout) covers the whole call.
//
// The error is returned only when nothing was confirmed and some part of the walk failed (a listing, the process
// view, the deadline): "could not tell", which the caller reports as status "unknown"; a confirmed pane next to a
// failed one is still an answer. What the pass itself folds into "nobody" (an unreadable pane pid, an unverifiable
// process) stays "nobody" here, as for every other caller of the pass.
func (m *Module) ConfirmedOwners(ctx context.Context, sessionID string) ([]PaneOwner, error) {
	if m == nil || m.frames == nil || m.tmux == nil || sessionID == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, provenanceTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frames, err := m.frames.ListRootsBySessionID(sessionID)
	if err != nil {
		return nil, err
	}
	candidate := map[string]bool{}
	for _, f := range frames {
		if f.AgentType != "cc" {
			continue
		}
		if _, ok := liveFrame(f); ok {
			candidate[f.PaneID] = true
		}
	}
	if len(candidate) == 0 {
		return nil, nil
	}

	pass := m.NewOwnerPass(nil).(*ownerPass)
	err = pass.enumerate(ctx)
	if err == nil {
		err = ctx.Err() // a listing that returned as the deadline passed is not an answer
	}
	if err != nil {
		return nil, err
	}
	var codes []string
	for code, panes := range pass.panes {
		for _, p := range panes {
			if candidate[p.id] {
				codes = append(codes, code)
				break
			}
		}
	}
	for _, code := range codes {
		pass.Resolve(ctx, code)
	}
	results := pass.Confirm(ctx)

	byPane := map[string]PaneOwner{}
	var firstErr error
	for _, code := range codes {
		if res := results[code]; res.Err != nil {
			if firstErr == nil {
				firstErr = res.Err
			}
			continue
		}
		for _, o := range pass.ConfirmedOwners(code) {
			if !strings.EqualFold(o.SessionID, sessionID) {
				continue
			}
			if cur, ok := byPane[o.TmuxPaneID]; !ok || betterOwner(o, cur) {
				byPane[o.TmuxPaneID] = o
			}
		}
	}
	var owners []PaneOwner
	for _, o := range byPane {
		o.Status = m.overlayStatus(sessionID, o.FrameID, o.Status) // the mod's light, as LightStatus gives it
		owners = append(owners, o)
	}
	if len(owners) > 0 {
		return owners, nil
	}
	return nil, firstErr
}
