package session

import (
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/gitbranch"
)

const (
	branchResolveTimeout = time.Second
	branchMinInterval    = time.Second
	// branchCacheMax bounds the cache and the number of cwds refreshed per
	// round, however many sessions exist.
	branchCacheMax = 512
)

// branchEntry is a resolved branch and when its resolve was started.
type branchEntry struct {
	val string
	at  time.Time
}

// branchCache holds cwd -> git branch. Reads are map lookups; every file
// access happens on a background goroutine, so a HEAD on a hung filesystem
// can never stall a session list, a get or the watcher. At most one resolve
// per cwd is ever in flight: a stuck one is not retried until it returns.
type branchCache struct {
	resolve     func(cwd string) string // may block; only ever run in the background
	timeout     time.Duration           // after this a still-running resolve counts as "no branch"
	minInterval time.Duration           // a cwd is not re-resolved more often than this

	mu       sync.Mutex
	entries  map[string]branchEntry
	inflight map[string]struct{}
	wg       sync.WaitGroup
}

func newBranchCache(resolve func(string) string) *branchCache {
	return &branchCache{
		resolve:     resolve,
		timeout:     branchResolveTimeout,
		minInterval: branchMinInterval,
		entries:     map[string]branchEntry{},
		inflight:    map[string]struct{}{},
	}
}

func newGitBranchCache() *branchCache {
	home, _ := os.UserHomeDir()
	return newBranchCache(func(cwd string) string { return gitbranch.Branch(cwd, home) })
}

// get returns the cached branch, "" when unknown. Never touches the disk.
func (c *branchCache) get(cwd string) string {
	if cwd == "" {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries[cwd].val
}

// refresh drops cwds that are no longer listed and starts a background resolve
// for each listed cwd that has none running and was not resolved recently.
func (c *branchCache) refresh(cwds []string) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	live := make(map[string]struct{}, len(cwds))
	for _, cwd := range cwds {
		if cwd == "" {
			continue
		}
		if _, dup := live[cwd]; dup {
			continue
		}
		if len(live) >= branchCacheMax {
			break
		}
		live[cwd] = struct{}{}
	}
	for cwd := range c.entries {
		if _, ok := live[cwd]; !ok {
			delete(c.entries, cwd)
		}
	}
	for cwd := range live {
		if _, busy := c.inflight[cwd]; busy {
			continue
		}
		if e, ok := c.entries[cwd]; ok && now.Sub(e.at) < c.minInterval {
			continue
		}
		c.inflight[cwd] = struct{}{}
		// Mark the attempt now so minInterval holds even while it runs.
		c.entries[cwd] = branchEntry{val: c.entries[cwd].val, at: now}
		c.wg.Add(1)
		go c.run(cwd)
	}
}

func (c *branchCache) run(cwd string) {
	defer c.wg.Done()
	timer := time.AfterFunc(c.timeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, busy := c.inflight[cwd]; busy {
			if e, ok := c.entries[cwd]; ok {
				c.entries[cwd] = branchEntry{at: e.at}
			}
		}
	})
	val := c.resolve(cwd)
	timer.Stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, cwd)
	if e, ok := c.entries[cwd]; ok { // still listed
		c.entries[cwd] = branchEntry{val: val, at: e.at}
	}
}

// wait blocks until every running resolve has returned (tests only: a stuck
// resolve makes it block, which production code never does).
func (c *branchCache) wait() { c.wg.Wait() }
