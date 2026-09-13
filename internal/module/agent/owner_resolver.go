package agent

import "context"

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
