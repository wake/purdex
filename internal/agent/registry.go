package agent

import "sync"

// Registry manages registered agent providers.
type Registry struct {
	mu        sync.RWMutex
	providers []AgentProvider
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a provider. Registration order determines Claim priority.
func (r *Registry) Register(p AgentProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, p)
}

// Get returns the provider matching the given agent type.
func (r *Registry) Get(agentType string) (AgentProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Type() == agentType {
			return p, true
		}
	}
	return nil, false
}

// Claim asks each provider (in registration order) whether it claims
// the session described by ctx. Used only for process detection path
// (when no hook event with agent_type is available).
func (r *Registry) Claim(ctx ClaimContext) (AgentProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.providers {
		if p.Claim(ctx) {
			return p, true
		}
	}
	return nil, false
}

// All returns all registered providers.
func (r *Registry) All() []AgentProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AgentProvider, len(r.providers))
	copy(out, r.providers)
	return out
}
