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
