package probe

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

const recursiveDescendantCacheTTL = 250 * time.Millisecond

var (
	readProcessInfoFn = agentpkg.ReadProcessInfo
	listProcessTreeFn = listProcessTree
)

// IsAliveFor checks whether the tmux target's pane PID tree contains a process
// identified as the given agent type.
func (p *Prober) IsAliveFor(agentType, target string) bool {
	p.registryMu.RLock()
	identify, ok := p.identifiers[agentType]
	p.registryMu.RUnlock()
	if !ok {
		return false
	}
	if p.tmux == nil {
		return false
	}

	panePIDRaw, err := p.tmux.PanePID(target)
	if err != nil {
		return false
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(panePIDRaw))
	if err != nil || panePID <= 0 {
		return false
	}

	p.livenessMu.Lock()
	p.pruneExpiredDescendantCacheLocked(p.now())
	p.livenessMu.Unlock()

	descendants, err := p.cachedDescendants(target, panePID)
	if err == nil {
		candidates := make([]int, 0, len(descendants)+1)
		candidates = append(candidates, panePID)
		candidates = append(candidates, descendants...)
		for _, pid := range candidates {
			info, infoErr := readProcessInfoFn(pid)
			if infoErr != nil {
				continue
			}
			if identify(info) {
				return true
			}
		}
	}
	return false
}

type descendantCacheEntry struct {
	rootPID     int
	descendants []string
	expiresAt   time.Time
}

func (p *Prober) cachedDescendants(target string, rootPID int) ([]int, error) {
	now := p.now()
	p.livenessMu.Lock()
	entry, ok := p.descendantCache[target]
	if ok && entry.rootPID == rootPID && now.Before(entry.expiresAt) {
		descendants := append([]string(nil), entry.descendants...)
		p.livenessMu.Unlock()
		return parsePIDSlice(descendants), nil
	}
	p.livenessMu.Unlock()

	descendants, err := p.queryDescendants(rootPID)
	if err != nil {
		return nil, err
	}
	stringified := make([]string, 0, len(descendants))
	for _, pid := range descendants {
		stringified = append(stringified, strconv.Itoa(pid))
	}

	p.livenessMu.Lock()
	p.descendantCache[target] = descendantCacheEntry{
		rootPID:     rootPID,
		descendants: stringified,
		expiresAt:   now.Add(recursiveDescendantCacheTTL),
	}
	p.livenessMu.Unlock()

	return append([]int(nil), descendants...), nil
}

func (p *Prober) queryDescendants(rootPID int) ([]int, error) {
	childrenByParent, err := listProcessTreeFn()
	if err != nil {
		return nil, err
	}
	var descendants []int
	queue := append([]int(nil), childrenByParent[rootPID]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		descendants = append(descendants, pid)
		queue = append(queue, childrenByParent[pid]...)
	}
	return descendants, nil
}

func (p *Prober) pruneExpiredDescendantCacheLocked(now time.Time) {
	for target, entry := range p.descendantCache {
		if !now.Before(entry.expiresAt) {
			delete(p.descendantCache, target)
		}
	}
}

func parsePIDSlice(values []string) []int {
	out := make([]int, 0, len(values))
	for _, value := range values {
		pid, err := strconv.Atoi(value)
		if err == nil {
			out = append(out, pid)
		}
	}
	return out
}

func IsPidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func ProcessStartTime(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", fmt.Errorf("read start time for pid %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func listProcessTree() (map[int][]int, error) {
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=").Output()
	if err != nil {
		return nil, fmt.Errorf("list process tree: %w", err)
	}
	childrenByParent := make(map[int][]int)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		childrenByParent[ppid] = append(childrenByParent[ppid], pid)
	}
	return childrenByParent, nil
}

func PidAncestorIncludes(pid int, ancestor int) bool {
	current := pid
	for current > 1 {
		if current == ancestor {
			return true
		}
		info, err := agentpkg.ReadProcessInfo(current)
		if err != nil {
			return false
		}
		if info.PPID <= 0 || info.PPID == current {
			return false
		}
		current = info.PPID
	}
	return false
}
