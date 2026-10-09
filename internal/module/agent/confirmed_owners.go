package agent

import (
	"context"
	"strings"

	"github.com/wake/purdex/internal/module/session"
)

// ConfirmedOwners returns the live panes that run the Claude Code session sessionID, each confirmed by the same
// owner resolution every other caller uses (process generation, pane membership, the second pane listing): the
// conversation API's "live pane" source (spec §8.2).
//
// The frames store finds the candidates by session id (cheap, no tmux); with none it stops there. Otherwise one pane
// listing maps those panes to their tmux sessions and one OwnerPass confirms each session. Every confirmed pane whose
// root frame reports sessionID counts, even when a sibling pane of the same tmux session runs a newer conversation (the
// pass's session-wide winner is not the question here). Several confirmed panes are all returned, unordered: the
// caller picks by LastSeenAt.
//
// The error is returned only when nothing was confirmed and some part of the walk failed (a listing, the process
// view, the deadline): "could not tell", which the caller reports as status "unknown"; a confirmed pane next to a
// failed one is still an answer.
func (m *Module) ConfirmedOwners(ctx context.Context, sessionID string) ([]PaneOwner, error) {
	if m == nil || m.frames == nil || m.tmux == nil || sessionID == "" {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	frames, err := m.frames.ListRootsBySessionID(sessionID)
	if err != nil {
		return nil, err
	}
	panes := map[string]bool{}
	for _, f := range frames {
		if f.AgentType != "cc" {
			continue
		}
		if _, ok := liveFrame(f); ok {
			panes[f.PaneID] = true
		}
	}
	if len(panes) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, provenanceTimeout)
	defer cancel()
	listing, err := m.tmux.ListAllPanes(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	codes := map[string]bool{}
	for _, row := range listing {
		if !panes[row.PaneID] {
			continue
		}
		if code, err := session.EncodeSessionID(row.SessionID); err == nil {
			codes[code] = true
		}
	}
	if len(codes) == 0 {
		return nil, nil
	}

	pass := m.NewOwnerPass(nil).(*ownerPass)
	for code := range codes {
		pass.Resolve(ctx, code)
	}
	results := pass.Confirm(ctx)
	// One owner per pane: the pane's own winner across every conversation its root frames report (an older frame of
	// another conversation must not make this one look live, nor the reverse); then the pane counts when that winner
	// is this conversation.
	byPane := map[string]PaneOwner{}
	var firstErr error
	for code, res := range results {
		if res.Err != nil {
			if firstErr == nil {
				firstErr = res.Err
			}
			continue
		}
		for _, o := range pass.ConfirmedOwners(code) {
			if cur, ok := byPane[o.TmuxPaneID]; !ok || betterOwner(o, cur) {
				byPane[o.TmuxPaneID] = o
			}
		}
	}
	var owners []PaneOwner
	for _, o := range byPane {
		if strings.EqualFold(o.SessionID, sessionID) {
			owners = append(owners, o)
		}
	}
	if len(owners) > 0 {
		return owners, nil
	}
	return nil, firstErr
}
