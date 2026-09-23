package hosttransfer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/core"
)

func TestModuleImplementsCoreModule(t *testing.T) {
	var _ core.Module = New()
}

func TestModuleNameAndDependencies(t *testing.T) {
	m := New()
	assert.Equal(t, "hosttransfer", m.Name())
	assert.Nil(t, m.Dependencies())
}

func TestInitGivesAnEmptyStore(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(&core.Core{}))
	require.NotNil(t, m.store)
	_, _, err := m.store.Create([]byte(`[{}]`))
	assert.NoError(t, err)
}

func TestStopDropsEveryPayload(t *testing.T) {
	m := New()
	require.NoError(t, m.Init(&core.Core{}))
	require.NoError(t, m.Start(context.Background()))
	code, _, err := m.store.Create([]byte(`[{"token":"t"}]`))
	require.NoError(t, err)
	store := m.store

	require.NoError(t, m.Stop(context.Background()))
	_, _, err = store.Redeem(code)
	assert.ErrorIs(t, err, ErrInvalidCode, "Stop clears the store it was serving")

	// A new Init starts from nothing.
	require.NoError(t, m.Init(&core.Core{}))
	_, _, err = m.store.Redeem(code)
	assert.ErrorIs(t, err, ErrInvalidCode)
}

func TestStopBeforeInitIsSafe(t *testing.T) {
	assert.NoError(t, New().Stop(context.Background()))
}
