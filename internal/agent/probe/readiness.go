package probe

// CheckReadiness delegates to the registered ReadinessChecker for the given agent type.
// Returns (result, false) if no checker is registered.
func (p *Prober) CheckReadiness(agentType, target string) (ReadinessResult, bool) {
	p.matcherMu.RLock()
	checker, ok := p.readiness[agentType]
	p.matcherMu.RUnlock()
	if !ok {
		return ReadinessResult{}, false
	}
	return checker.CheckReadiness(target), true
}
