package shim

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shim is told by its header, not by the directory it lies in: Resolve skips it wherever PATH finds it (#3671: with HOME
// swapped, the directory worked out from $HOME was not the real shim directory and the shim was found again, without end).
func TestResolve_SkipsAShimInAnyDirectory(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "elsewhere")
	realDir := filepath.Join(root, "real")
	for _, d := range []string{other, realDir} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(other, "go"), []byte(Script("go", "/nonexistent/pdx")), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// the shim directory handed in is a different one on purpose
	got, err := Resolve("go", other+":"+realDir, filepath.Join(root, "unrelated"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(realDir, "go"), got)

	_, err = Resolve("go", other, filepath.Join(root, "unrelated"))
	assert.ErrorIs(t, err, ErrNoRealBinary)
}

func TestIsShim(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "go")
	require.NoError(t, os.WriteFile(p, []byte(Script("go", "/x/pdx")), 0o755))
	assert.True(t, IsShim(p))
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho hi\n"), 0o755))
	assert.False(t, IsShim(p))
	assert.False(t, IsShim(filepath.Join(d, "missing")))
}
