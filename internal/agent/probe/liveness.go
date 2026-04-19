package probe

import (
	"sort"
	"strings"
	"time"
)

var defaultShells = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "dash": true,
}

const recursiveDescendantCacheTTL = 250 * time.Millisecond

type descendantCommandQuerier interface {
	PaneDescendantCommands(target string) ([]string, error)
}

// IsAliveFor checks whether the given tmux target is running an agent of the
// specified type. It checks in order: (1) pane foreground command,
// (2) direct child processes, (3) cached recursive descendants,
// (4) optional content fallback.
func (p *Prober) IsAliveFor(agentType, target string) bool {
	p.matcherMu.RLock()
	matcher, ok := p.matchers[agentType]
	contentMatcher := p.content[agentType]
	p.matcherMu.RUnlock()
	if !ok {
		return false
	}

	p.livenessMu.Lock()
	p.pruneExpiredDescendantCacheLocked(p.now())
	p.livenessMu.Unlock()

	// Layer 1a: foreground command
	cmd, err := p.tmux.PaneCurrentCommand(target)
	if err != nil {
		return false
	}
	cmd = strings.TrimSpace(cmd)
	if matcher.commands[baseCommand(cmd)] {
		return true
	}

	panePID := ""
	panePIDKnown := false
	if pid, err := p.tmux.PanePID(target); err == nil {
		panePID = strings.TrimSpace(pid)
		panePIDKnown = true
	}

	// Layer 1b: child processes
	childrenKnown := false
	var children []string
	children, err = p.tmux.PaneChildCommands(target)
	if err == nil {
		childrenKnown = true
		for _, child := range children {
			if matcher.commands[baseCommand(child)] {
				return true
			}
		}
	}

	// Layer 1c: recursive descendants (cached by target + current snapshot)
	descendants, err := p.cachedDescendants(target, descendantCacheSnapshot{
		panePID:       panePID,
		panePIDKnown:  panePIDKnown,
		paneCommand:   cmd,
		childrenKey:   childSnapshotKey(children),
		childrenKnown: childrenKnown,
	})
	if err == nil {
		for _, descendant := range descendants {
			if matcher.commands[baseCommand(descendant)] {
				return true
			}
		}
	}

	if defaultShells[baseCommand(cmd)] {
		return false
	}

	// Layer 1d: content fallback (optional)
	if contentMatcher != nil {
		content, err := p.tmux.CapturePaneContent(target, 5)
		if err == nil && contentMatcher.LooksLikeAgent(content) {
			return true
		}
	}

	return false
}

type descendantCacheSnapshot struct {
	panePID       string
	panePIDKnown  bool
	paneCommand   string
	childrenKey   string
	childrenKnown bool
}

type descendantCacheEntry struct {
	snapshot    descendantCacheSnapshot
	descendants []string
	expiresAt   time.Time
}

func (p *Prober) cachedDescendants(target string, snapshot descendantCacheSnapshot) ([]string, error) {
	now := p.now()
	p.livenessMu.Lock()
	entry, ok := p.descendantCache[target]
	if ok && entry.snapshot == snapshot && now.Before(entry.expiresAt) {
		descendants := append([]string(nil), entry.descendants...)
		p.livenessMu.Unlock()
		return descendants, nil
	}
	p.livenessMu.Unlock()

	descendants, err := p.queryDescendants(target)
	if err != nil {
		return nil, err
	}
	descendants = append([]string(nil), descendants...)

	p.livenessMu.Lock()
	p.descendantCache[target] = descendantCacheEntry{
		snapshot:    snapshot,
		descendants: descendants,
		expiresAt:   now.Add(recursiveDescendantCacheTTL),
	}
	p.livenessMu.Unlock()

	return append([]string(nil), descendants...), nil
}

func (p *Prober) queryDescendants(target string) ([]string, error) {
	querier, ok := p.tmux.(descendantCommandQuerier)
	if !ok {
		return nil, nil
	}
	return querier.PaneDescendantCommands(target)
}

func (p *Prober) pruneExpiredDescendantCacheLocked(now time.Time) {
	for target, entry := range p.descendantCache {
		if !now.Before(entry.expiresAt) {
			delete(p.descendantCache, target)
		}
	}
}

func childSnapshotKey(children []string) string {
	if len(children) == 0 {
		return ""
	}
	snapshot := append([]string(nil), children...)
	sort.Strings(snapshot)
	return strings.Join(snapshot, "\x00")
}

func baseCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if idx := strings.LastIndex(cmd, "/"); idx >= 0 {
		return cmd[idx+1:]
	}
	return cmd
}
