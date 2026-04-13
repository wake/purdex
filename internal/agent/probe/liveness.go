package probe

import "strings"

var defaultShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true,
}

// IsAliveFor checks whether the given tmux target is running an agent of the
// specified type. It checks in order: (1) pane foreground command, (2) child
// processes, (3) optional content fallback.
func (p *Prober) IsAliveFor(agentType, target string) bool {
	p.matcherMu.RLock()
	matcher, ok := p.matchers[agentType]
	contentMatcher := p.content[agentType]
	p.matcherMu.RUnlock()
	if !ok {
		return false
	}

	// Layer 1a: foreground command
	cmd, err := p.tmux.PaneCurrentCommand(target)
	if err != nil {
		return false
	}
	cmd = strings.TrimSpace(cmd)
	if matcher.commands[cmd] {
		return true
	}
	if defaultShells[cmd] {
		return false
	}

	// Layer 1b: child processes
	children, err := p.tmux.PaneChildCommands(target)
	if err == nil {
		for _, child := range children {
			base := child
			if idx := strings.LastIndex(child, "/"); idx >= 0 {
				base = child[idx+1:]
			}
			if matcher.commands[base] {
				return true
			}
		}
	}

	// Layer 1c: content fallback (optional)
	if contentMatcher != nil {
		content, err := p.tmux.CapturePaneContent(target, 5)
		if err == nil && contentMatcher.LooksLikeAgent(content) {
			return true
		}
	}

	return false
}
