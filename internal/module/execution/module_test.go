package execution

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func TestModule_NameAndDependencies(t *testing.T) {
	m := New()
	require.Equal(t, "execution", m.Name())
	require.Nil(t, m.Dependencies())
}

func TestModule_InitStartStop(t *testing.T) {
	dir := t.TempDir()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}})

	m := New()
	require.NoError(t, m.Init(c))
	require.NotNil(t, m.store)
	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Stop(context.Background()))

	// db file created under DataDir
	require.FileExists(t, filepath.Join(dir, "execution.db"))
}
