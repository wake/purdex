package session

import (
	"strconv"
	"sync"
	"time"
)

// launchClaimTTL is how long a launch holds its claim when nothing releases it sooner (no-agent launch panel spec §2.3): the
// agent's hooks normally end the panel before this, and 15 s keeps a launch that never produced a SessionStart from locking
// the session out for good.
var launchClaimTTL = 15 * time.Second

// launchClaims is the in-memory one-launch-at-a-time table, keyed by (session code, tmux generation). Every claim carries a
// never-reused nonce and every release names it, so the late cleanup of launch A (its timer, a failed send) can never release
// launch B's claim: B holds the key under a different nonce. A root SessionStart releases whatever claim the key holds — the
// launch it belongs to has done its job.
type launchClaims struct {
	mu     sync.Mutex
	next   uint64
	claims map[string]*launchClaim
}

type launchClaim struct {
	nonce uint64
	timer *time.Timer
}

func newLaunchClaims() *launchClaims { return &launchClaims{claims: map[string]*launchClaim{}} }

func launchClaimKey(code, instance string) string {
	return strconv.Itoa(len(code)) + ":" + code + instance
}

// take claims the key, or reports false when a launch already holds it.
func (c *launchClaims) take(key string) (nonce uint64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, held := c.claims[key]; held {
		return 0, false
	}
	c.next++
	n := c.next
	cl := &launchClaim{nonce: n}
	cl.timer = time.AfterFunc(launchClaimTTL, func() { c.release(key, n) })
	c.claims[key] = cl
	return n, true
}

// release drops the claim only when the key still holds THIS nonce.
func (c *launchClaims) release(key string, nonce uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, held := c.claims[key]; held && cl.nonce == nonce {
		cl.timer.Stop()
		delete(c.claims, key)
	}
}

// releaseKey drops whatever claim the key holds (a root SessionStart of the session's current generation).
func (c *launchClaims) releaseKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, held := c.claims[key]; held {
		cl.timer.Stop()
		delete(c.claims, key)
	}
}

// any reports whether any launch holds a claim: a SessionStart with none to release has nothing to look up.
func (c *launchClaims) any() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.claims) > 0
}

// held reports whether the key is claimed (tests).
func (c *launchClaims) held(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, held := c.claims[key]
	return held
}
