package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeShim is a shim of an old `pdx setup`, as isShim and resolveRealTool see it.
func fakeShim(tool string) string { return shimScript(tool, "/nonexistent/pdx") }

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
}

func TestResolveRealTool(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	writeExe(t, filepath.Join(shimDir, "go"), fakeShim("go"))
	realA := filepath.Join(root, "a")
	writeExe(t, filepath.Join(realA, "go"), "#!/bin/sh\n")

	got, err := resolveRealTool("go", shimDir+":"+realA, shimDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(realA, "go"), got)

	// a symlink alias of the shim is refused, whatever its name or directory
	alias := filepath.Join(root, "alias")
	require.NoError(t, os.MkdirAll(alias, 0o755))
	require.NoError(t, os.Symlink(filepath.Join(shimDir, "go"), filepath.Join(alias, "go")))
	got, err = resolveRealTool("go", alias+":"+realA, shimDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(realA, "go"), got)

	// a directory that is a symlink to the shim directory is the shim directory
	link := filepath.Join(root, "linkdir")
	require.NoError(t, os.Symlink(shimDir, link))
	got, err = resolveRealTool("go", link+":"+realA, shimDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(realA, "go"), got)

	// nothing but the shims: an error, never the shim
	got, err = resolveRealTool("go", link+":"+shimDir+":"+alias, shimDir)
	assert.ErrorIs(t, err, errNoRealBinary)
	assert.Empty(t, got)

	// empty entries (= the current directory), directories named like the tool and non-executables are not binaries
	cwd := filepath.Join(root, "cwd")
	writeExe(t, filepath.Join(cwd, "go"), "#!/bin/sh\n")
	t.Chdir(cwd)
	noexec := filepath.Join(root, "noexec")
	require.NoError(t, os.MkdirAll(noexec, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(noexec, "go"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "dirgo", "go"), 0o755))
	got, err = resolveRealTool("go", ":"+noexec+":"+filepath.Join(root, "dirgo")+":"+realA, shimDir)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(realA, "go"), got)
}

// The binary is looked up at every run from that run's PATH: a switch of node versions is followed.
func TestResolveRealTool_FollowsAChangedPath(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	writeExe(t, filepath.Join(shimDir, "pnpm"), fakeShim("pnpm"))
	n20 := filepath.Join(root, "node20", "bin")
	n24 := filepath.Join(root, "node24", "bin")
	writeExe(t, filepath.Join(n20, "pnpm"), "#!/bin/sh\n")
	writeExe(t, filepath.Join(n24, "pnpm"), "#!/bin/sh\n")
	got, _ := resolveRealTool("pnpm", shimDir+":"+n20, shimDir)
	assert.Equal(t, filepath.Join(n20, "pnpm"), got)
	got, _ = resolveRealTool("pnpm", shimDir+":"+n24+":"+n20, shimDir)
	assert.Equal(t, filepath.Join(n24, "pnpm"), got)
}

// A shim is told by its header, not by the directory it lies in: resolveRealTool skips it wherever PATH finds it (#3671: with HOME swapped,
// the directory worked out from $HOME was not the real shim directory and the shim was found again, without end).
func TestResolveRealTool_SkipsAShimInAnyDirectory(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "elsewhere")
	realDir := filepath.Join(root, "real")
	writeExe(t, filepath.Join(other, "go"), fakeShim("go"))
	writeExe(t, filepath.Join(realDir, "go"), "#!/bin/sh\nexit 0\n")
	// the shim directory handed in is a different one on purpose
	got, err := resolveRealTool("go", other+":"+realDir, filepath.Join(root, "unrelated"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(realDir, "go"), got)

	_, err = resolveRealTool("go", other, filepath.Join(root, "unrelated"))
	assert.ErrorIs(t, err, errNoRealBinary)
}

func TestIsShim(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "go")
	writeExe(t, p, fakeShim("go"))
	assert.True(t, isShim(p))
	writeExe(t, p, "#!/bin/sh\necho hi\n")
	assert.False(t, isShim(p))
	assert.False(t, isShim(filepath.Join(d, "missing")))

	// a wrapper that only mentions the marker (not on its second line) is a real binary, not a shim
	writeExe(t, p, "#!/bin/sh\n# real wrapper\n# see: "+shimMarker+" go) in the docs\nexec /real/go \"$@\"\n")
	assert.False(t, isShim(p))
	writeExe(t, p, string([]byte{0, 1, 2, 3}))
	assert.False(t, isShim(p))
}

func TestShimToolsHaveComposerAndNotThePHPRunners(t *testing.T) {
	assert.Contains(t, shimTools, "composer")
	assert.NotContains(t, shimTools, "phpunit")
	assert.NotContains(t, shimTools, "pest")
	assert.Len(t, shimTools, 10)
}

// shimScript must stay byte for byte what the retired generator wrote (setup deletes a file only if it is exactly that). The digests are those of
// internal/gate/shim.Script as of c515fac3f, for a plain path and for one with a space and the four characters sh quoting escapes.
func TestShimScript_IsTheRetiredGeneratorByteForByte(t *testing.T) {
	for _, c := range []struct{ tool, pdx, sum string }{
		{"go", "/p/pdx", "907e62e0e476954cb21a62aba2dcf3901e551c95a52d180f1cd72746ba291325"},
		{"swift-test", "/a b/\"$`x/pdx", "5bfbd5ad4f85d26c4c5ea310844e695ff9471b8968ee21a733eb10f5365a7e2e"},
	} {
		sum := sha256.Sum256([]byte(shimScript(c.tool, c.pdx)))
		assert.Equal(t, c.sum, hex.EncodeToString(sum[:]), c.tool)
		got, ok := shimPdxPath(shimScript(c.tool, c.pdx))
		assert.True(t, ok)
		assert.Equal(t, c.pdx, got)
	}
}
