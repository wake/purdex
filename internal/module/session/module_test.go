package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/store"
	"github.com/wake/purdex/internal/tmux"
)

// newTestModule creates a SessionModule with in-memory MetaStore and FakeExecutor,
// already initialised via Core. Returns the module, meta store, and fake executor.
func newTestModule(t *testing.T) (*SessionModule, *store.MetaStore, *tmux.FakeExecutor) {
	t.Helper()

	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { meta.Close() })

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)

	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))

	return mod, meta, fake
}

func TestSessionModuleName(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer meta.Close()

	mod := NewSessionModule(meta)
	assert.Equal(t, "session", mod.Name())
}

func TestSessionModuleRegistersProvider(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer meta.Close()

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)

	reg := core.NewServiceRegistry()
	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: reg,
	})

	err = mod.Init(c)
	require.NoError(t, err)

	svc, ok := reg.Get(RegistryKey)
	assert.True(t, ok, "SessionProvider should be registered")

	_, isProvider := svc.(SessionProvider)
	assert.True(t, isProvider, "registered service should implement SessionProvider")
}

func TestSessionModuleStartResetsStaleModes(t *testing.T) {
	meta, err := store.OpenMeta(":memory:")
	require.NoError(t, err)
	defer meta.Close()

	// Pre-populate stale meta (includes legacy "term" and "jsonl" values for upgrade path)
	require.NoError(t, meta.SetMeta("$0", store.SessionMeta{Mode: "stream"}))
	require.NoError(t, meta.SetMeta("$1", store.SessionMeta{Mode: "term"}))
	require.NoError(t, meta.SetMeta("$2", store.SessionMeta{Mode: "jsonl"}))

	fake := tmux.NewFakeExecutor()
	mod := NewSessionModule(meta)

	c := core.New(core.CoreDeps{
		Tmux:     fake,
		Registry: core.NewServiceRegistry(),
	})
	require.NoError(t, mod.Init(c))

	// Start should reset stale modes
	err = mod.Start(context.Background())
	require.NoError(t, err)

	m0, _ := meta.GetMeta("$0")
	m1, _ := meta.GetMeta("$1")
	m2, _ := meta.GetMeta("$2")
	assert.Equal(t, "terminal", m0.Mode)
	assert.Equal(t, "terminal", m1.Mode)
	assert.Equal(t, "terminal", m2.Mode)
}
