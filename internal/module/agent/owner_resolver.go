package agent

import (
	"context"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// OwnerResolverKey is the service registry key for OwnerResolver.
const OwnerResolverKey = "agent.owner-resolver"

// OwnerResolver answers which agent owns a tmux session, for modules (e.g.
// peers) that need the answer without importing the agent package's
// unexported internals. It is registered under OwnerResolverKey in Init,
// after the session-provider check — so with no session provider it is not
// registered, same as the other services Init exposes.
//
// The error return tells "the lookup failed" (a tmux read error, a resolver
// timeout, a cancelled context) apart from "no owner" (found=false, err=nil):
// a caller that folded both into found=false would report a session that
// merely couldn't be checked the same way it reports one that genuinely has
// no agent (Item 1, #988).
type OwnerResolver interface {
	ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool, error)
}

// ResolveSessionOwner is the exported form of resolveSessionOwnerErr, for
// callers reached through the OwnerResolver service registry entry rather
// than direct access to *Module.
func (m *Module) ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool, error) {
	return m.resolveSessionOwnerErr(ctx, code)
}

// OwnerResult is one session's answer from an OwnerPass, in ResolveSessionOwner's
// three outcomes: Found with Owner, "no owner" (Found false, Err nil), or "the
// lookup failed" (Err non-nil, Found false). Err is never folded into "no
// owner", for the reason OwnerResolver gives (#988).
type OwnerResult struct {
	Owner PaneOwner
	Found bool
	Err   error
}

// ProcessSource hands an OwnerPass the process view it walks. The pass calls it
// at most once, and only when it reaches the first pane it has to walk: a pass
// with nothing to walk never pays for a process table. Its error is the pass's
// answer for every session that needed the view; the pass does not call it
// again to retry.
type ProcessSource func() (agentpkg.ProcessView, error)

// OwnerPass answers ResolveSessionOwner's question for many sessions at once,
// for a caller (the peers inventory) that would otherwise pay one lookup's
// whole cost per session.
//
// One pass is ONE process view and at most TWO pane listings, however many
// sessions it is asked about: the first listing, taken on first use, decides
// which panes each session has; the second, taken by Confirm, re-checks that
// the panes the answers came from are still in those sessions.
//
// Resolve walks one session. What it decides is provisional: an owner is only
// a candidate until Confirm's listing has placed its pane in the same session
// again, so Confirm's map is the pass's only answer. A session Resolve found no
// candidate for is already final, and Confirm reports it unchanged.
//
// A pass is used from one goroutine, Resolve for each session first and then
// Confirm exactly once. It is not safe for concurrent use and must not be kept
// past Confirm: it is a point-in-time reading, and a later caller wants a new
// one.
type OwnerPass interface {
	Resolve(ctx context.Context, code string)
	Confirm(ctx context.Context) map[string]OwnerResult
}

// OwnerPassResolver is an OwnerResolver that can also answer a batch through an
// OwnerPass. NewOwnerPass's src is the process view the pass walks; nil means
// the pass takes its own snapshot of the process table.
type OwnerPassResolver interface {
	OwnerResolver
	NewOwnerPass(src ProcessSource) OwnerPass
}

var _ OwnerPassResolver = (*Module)(nil)
