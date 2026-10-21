package hostconfig

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// WakeRowStore is the narrow door to the two rows of the watcher's wake settings (spec section 4): a CAS and nothing else.
func TestWakeRows_GetOfANeverWrittenRow(t *testing.T) {
	m := newTestModule(t)
	raw, rev, ok, err := m.Get(KeyWatchWake)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, rev)
	assert.Nil(t, raw)
}

func TestWakeRows_PutCreatesWithBaseZeroThenAdvancesByCAS(t *testing.T) {
	m := newTestModule(t)
	rev, err := m.Put(KeyWatchWake, []byte(`{"a":1}`), 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev)
	raw, got, ok, err := m.Get(KeyWatchWake)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(1), got)
	assert.JSONEq(t, `{"a":1}`, string(raw))

	rev, err = m.Put(KeyWatchWake, []byte(`{"a":2}`), 1)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rev)
}

func TestWakeRows_AStaleBaseIsAConflictAndChangesNothing(t *testing.T) {
	m := newTestModule(t)
	_, err := m.Put(KeyWatchWake, []byte(`{"a":1}`), 0)
	require.NoError(t, err)
	for _, base := range []int64{0, 2, 99, -1} { // the row exists at revision 1: Put(raw, 0) must not overwrite it either
		_, err = m.Put(KeyWatchWake, []byte(`{"a":9}`), base)
		assert.ErrorIs(t, err, ErrConflict, "base %d", base)
	}
	raw, rev, _, _ := m.Get(KeyWatchWake)
	assert.Equal(t, int64(1), rev)
	assert.JSONEq(t, `{"a":1}`, string(raw))
}

func TestWakeRows_OnlyTheTwoFixedKeys(t *testing.T) {
	m := newTestModule(t)
	for _, key := range []string{"", "watch", "relay_quota", "watch_wake_other", "WATCH_WAKE", "watch_wake "} {
		_, _, _, err := m.Get(key)
		assert.ErrorIs(t, err, ErrWakeKey, "Get %q", key)
		_, err = m.Put(key, []byte(`{}`), 0)
		assert.ErrorIs(t, err, ErrWakeKey, "Put %q", key)
	}
	_, err := m.Put(KeyWatchWakeCache, []byte(`{"c":1}`), 0)
	require.NoError(t, err, "the cache row is the second fixed key")
	e, err := m.store.Get("watch")
	require.NoError(t, err)
	assert.Zero(t, e.Revision, "a refused key wrote nothing")
}

func TestWakeRows_TheValueMustBeAJSONObject(t *testing.T) {
	m := newTestModule(t)
	for _, raw := range []string{``, `null`, `[]`, `"x"`, `{`, `1`} {
		_, err := m.Put(KeyWatchWake, []byte(raw), 0)
		assert.Error(t, err, raw)
	}
	_, _, ok, _ := m.Get(KeyWatchWake)
	assert.False(t, ok)
}

// Two writers that both read "no row" and both Put(raw, 0): exactly one wins (the migration against the first PUT).
func TestWakeRows_ConcurrentCreatesHaveOneWinner(t *testing.T) {
	m := newTestModule(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = m.Put(KeyWatchWake, []byte(`{"w":`+string(rune('0'+i))+`}`), 0)
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("%v", err)
		}
	}
	assert.Equal(t, 1, wins)
	assert.Equal(t, n-1, conflicts)
	_, rev, _, _ := m.Get(KeyWatchWake)
	assert.Equal(t, int64(1), rev)
}

// Init publishes the module under WakeRowStoreKey as the WakeRowStore the watch module type-asserts (the handler tests build their Module
// without Init, so this is where dropping the registration turns red).
func TestInit_RegistersWakeRowStore(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(WakeRowStoreKey)
	require.True(t, ok, "Init must register under WakeRowStoreKey")
	store, ok := svc.(WakeRowStore)
	require.True(t, ok, "registry value must be a WakeRowStore, got %T", svc)
	rev, err := store.Put(KeyWatchWake, []byte(`{"v":1}`), 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev)
}

// The generic hostconfig routes do not know these rows: they are not a way to write the settings around the CAS or the validation.
func TestWakeRows_NotReachableThroughTheGenericRoutes(t *testing.T) {
	m := newTestModule(t)
	for _, key := range []string{KeyWatchWake, KeyWatchWakeCache} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/"+key, `{"items":{"x":1},"baseRevision":0}`)
		assert.GreaterOrEqual(t, rr.Code, 400, key)
		assert.Less(t, rr.Code, 500, key)
		raw, _, ok, _ := m.Get(key)
		assert.False(t, ok, key)
		assert.Nil(t, raw)
	}
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "watch_wake")
}
