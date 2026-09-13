package agent

import "context"

// OwnerResolverKey is the service registry key for OwnerResolver.
const OwnerResolverKey = "agent.owner-resolver"

// OwnerResolver answers which agent owns a tmux session, for modules (e.g.
// peers) that need the answer without importing the agent package's
// unexported internals. It is registered under OwnerResolverKey in Init,
// after the session-provider check — so with no session provider it is not
// registered, same as the other services Init exposes.
type OwnerResolver interface {
	ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool)
}

// ResolveSessionOwner is the exported form of resolveSessionOwner, for
// callers reached through the OwnerResolver service registry entry rather
// than direct access to *Module.
func (m *Module) ResolveSessionOwner(ctx context.Context, code string) (PaneOwner, bool) {
	return m.resolveSessionOwner(ctx, code)
}
