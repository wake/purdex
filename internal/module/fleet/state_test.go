package fleetmod

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestState_RoundTripAndTornAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fleet", "state.json")
	require.Equal(t, persisted{Role: "standby"}, loadState(p), "missing file")
	in := persisted{Role: "primary", Term: 7, LastHeard: time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)}
	require.NoError(t, saveState(p, in))
	out := loadState(p)
	require.Equal(t, int64(7), out.Term)
	require.Equal(t, "primary", out.Role)
	_, err := os.Stat(p + ".tmp")
	require.True(t, os.IsNotExist(err), "the temp file is renamed away")

	require.NoError(t, os.WriteFile(p, []byte(`{"role":"primary","ter`), 0o600))
	require.Equal(t, persisted{Role: "standby"}, loadState(p), "torn file")
	require.NoError(t, os.WriteFile(p, []byte(`{"role":"king","term":3}`), 0o600))
	require.Equal(t, "standby", loadState(p).Role, "unknown role")
}

func TestState_WriteFailureIsAnError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))
	require.Error(t, saveState(filepath.Join(blocker, "x", "state.json"), persisted{}))
}
