package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupCodeByName_HitOnFreshCache(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	wantCode, err := EncodeSessionID("$0")
	require.NoError(t, err)

	code, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, wantCode, code)
	assert.Equal(t, 1, fake.ListCallCount())

	code, ok = mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, wantCode, code)
	assert.Equal(t, 1, fake.ListCallCount(), "second lookup within TTL must not re-query tmux")
}

func TestLookupCodeByName_RefreshOnStale(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, 1, fake.ListCallCount())

	// Force the cache stamp into the past so the next call sees a stale cache.
	mod.nameCacheMu.Lock()
	mod.nameCacheAt = time.Now().Add(-2 * nameCacheTTL)
	mod.nameCacheMu.Unlock()

	_, ok = mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, 2, fake.ListCallCount(), "stale cache must trigger a refresh")
}

func TestLookupCodeByName_MissReturnsFalse(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("foo", "/tmp")

	code, ok := mod.LookupCodeByName("bar")
	assert.False(t, ok)
	assert.Equal(t, "", code)
	assert.Equal(t, 1, fake.ListCallCount(), "miss must refresh once, not retry")

	// A repeated miss while cache is fresh must not re-query tmux either.
	code, ok = mod.LookupCodeByName("bar")
	assert.False(t, ok)
	assert.Equal(t, "", code)
	assert.Equal(t, 1, fake.ListCallCount())
}

func TestLookupCodeByName_RefreshAfterInvalidate(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	_, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, 1, fake.ListCallCount())

	mod.invalidateNameCache()

	_, ok = mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, 2, fake.ListCallCount(), "invalidate must force the next lookup to refresh")
}

// TestLookupCodeByName_NameReuseAfterInvalidate documents the cache's
// staleness contract under the name-reuse scenario: an external actor kills
// session "alpha" ($1) and immediately recreates a new session also called
// "alpha" but with a different tmux ID ($2). Until the watcher's wait-for
// path reaches invalidateNameCache, a hook resolving "alpha" must observe
// the stale code-of-$1; once invalidated, the next lookup converges on the
// fresh code-of-$2. The 250ms TTL bounds how long the stale window can
// last in the absence of explicit invalidation.
func TestLookupCodeByName_NameReuseAfterInvalidate(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("alpha", "/tmp")

	staleCode, err := EncodeSessionID("$0")
	require.NoError(t, err)

	freshCode, err := EncodeSessionID("$1")
	require.NoError(t, err)

	got, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	require.Equal(t, staleCode, got)
	require.Equal(t, 1, fake.ListCallCount())

	// External mutation: kill "alpha" and immediately recreate it. The fake's
	// auto-incrementing ID counter guarantees the recreated session takes a
	// different tmux ID, mirroring real tmux behavior.
	require.NoError(t, fake.KillSession("alpha"))
	fake.AddSession("alpha", "/tmp")

	// Cache is still warm — the lookup observes the stale mapping. This is
	// the documented race window.
	stillStale, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, staleCode, stillStale,
		"without invalidation, fresh-cache lookup must return the stale code (race window observable)")
	assert.Equal(t, 1, fake.ListCallCount(),
		"fresh cache must not re-query tmux even when underlying state diverged")

	// Watcher catches up via wait-for and signals invalidation.
	mod.invalidateNameCache()

	healed, ok := mod.LookupCodeByName("alpha")
	require.True(t, ok)
	assert.Equal(t, freshCode, healed,
		"after invalidate, the next lookup must return the fresh code (self-healing)")
	assert.NotEqual(t, staleCode, healed)
	assert.Equal(t, 2, fake.ListCallCount(), "invalidate forces a refresh on the next lookup")
}

func TestLookupCodeByName_RenameInvalidatesCache(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("oldname", "/tmp")

	oldCode, ok := mod.LookupCodeByName("oldname")
	require.True(t, ok)
	require.NotEmpty(t, oldCode)
	assert.Equal(t, 1, fake.ListCallCount())

	// Simulate the side effect of a successful rename: the underlying tmux
	// state changed, and the handler signals invalidation so the next lookup
	// refreshes against the new ground truth.
	require.NoError(t, fake.RenameSession("oldname", "newname"))
	mod.invalidateNameCache()

	newCode, ok := mod.LookupCodeByName("newname")
	require.True(t, ok)
	assert.Equal(t, oldCode, newCode, "tmux session ID survives rename, so code is unchanged")
	assert.Equal(t, 2, fake.ListCallCount(), "lookup after invalidate must re-query tmux")

	missCode, ok := mod.LookupCodeByName("oldname")
	assert.False(t, ok)
	assert.Equal(t, "", missCode, "old name must no longer resolve after rename + invalidate")
}
