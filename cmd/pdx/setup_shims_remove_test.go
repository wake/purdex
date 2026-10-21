package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedShims writes the ten shims an old setup made into home's shim directory.
func seedShims(t *testing.T, home string) string {
	t.Helper()
	dir := shimDirIn(home)
	for _, tool := range shimTools {
		writeExe(t, filepath.Join(dir, tool), fakeShim(tool))
	}
	return dir
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// Both paths: the ten shims and their directory go, the rest of ~/.config/pdx stays; a second run changes nothing and says nothing.
func TestSetupCC_RemovesTheShimsOnBothPaths(t *testing.T) {
	for name, port := range map[string]func(*testing.T) int{"the daemon answers": daemonThatAnswers, "the daemon does not answer": closedPort} {
		home := setupEnv(t, port(t))
		dir := seedShims(t, home)

		code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
		require.Equal(t, 0, code, "%s: %s", name, errb)
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "%s: the shim directory is still there: %v", name, err)
		assert.Contains(t, errb, "pdx gate shims removed", name)
		_, err = os.Stat(filepath.Join(home, ".config", "pdx", "config.toml"))
		assert.NoError(t, err, "%s: the rest of the config directory is kept", name)

		code, _, errb = runPdxSetup(t, home, "--agent", "cc", "--force")
		assert.Equal(t, 0, code, name)
		assert.NotContains(t, errb, "shim", "%s: a second setup says something about shims", name)
	}
}

// A home with no shim directory: nothing is made and nothing is said.
func TestSetupCC_FreshHomeHasNoShimDirectoryAndSaysNothing(t *testing.T) {
	home := setupEnv(t, closedPort(t))
	code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--force")
	require.Equal(t, 0, code, errb)
	_, err := os.Stat(shimDirIn(home))
	assert.True(t, os.IsNotExist(err))
	assert.NotContains(t, errb, "shim")
}

// Only files the old setup wrote go: a file of the same name that is not a shim, and any other file, stay with the directory, and the setup
// says so. The shims next to them are still removed.
func TestRemoveGateShims_LeavesWhatIsNotOurs(t *testing.T) {
	home := t.TempDir()
	dir := seedShims(t, home)
	writeExe(t, filepath.Join(dir, "go"), "#!/bin/sh\necho my own go wrapper\n")                         // same name, not ours
	writeExe(t, filepath.Join(dir, "mytool"), "#!/bin/sh\n")                                             // not a shim name at all
	writeExe(t, filepath.Join(dir, "tsc"), fakeShim("npx"))                                              // the header names another tool
	writeExe(t, filepath.Join(dir, "pnpm"), shimMarker+"x")                                              // not even a script: no header on a second line
	writeExe(t, filepath.Join(dir, "npx"), "#!/bin/sh\n"+shimMarker+" npx); mine\nexec /usr/bin/true\n") // the header, but no hand-over to pdx gate
	var out strings.Builder
	removeGateShims(home, &out)

	assert.ElementsMatch(t, []string{"go", "mytool", "tsc", "pnpm", "npx"}, dirNames(t, dir))
	assert.Contains(t, out.String(), "left in "+dir)
	assert.Contains(t, out.String(), "pdx gate shims removed")

	// again: the same two files, the same word, nothing else
	out.Reset()
	removeGateShims(home, &out)
	assert.ElementsMatch(t, []string{"go", "mytool", "tsc", "pnpm", "npx"}, dirNames(t, dir))
	assert.NotContains(t, out.String(), "shims removed")
}

// A shim directory that is a symbolic link, or a shim name that is a directory or a link out of the directory, is never followed or deleted.
func TestRemoveGateShims_DoesNotFollowLinks(t *testing.T) {
	home := t.TempDir()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	writeExe(t, filepath.Join(elsewhere, "go"), fakeShim("go"))
	dir := shimDirIn(home)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.Symlink(elsewhere, dir))

	var out strings.Builder
	removeGateShims(home, &out)
	_, err := os.Stat(filepath.Join(elsewhere, "go"))
	assert.NoError(t, err, "a file behind a linked shim directory was removed")
	assert.NotContains(t, out.String(), "removed")
}

// A directory named like a tool is not a shim and is not removed.
func TestRemoveGateShims_ADirectoryNamedLikeAToolStays(t *testing.T) {
	home := t.TempDir()
	dir := shimDirIn(home)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "go"), 0o755))
	var out strings.Builder
	removeGateShims(home, &out)
	assert.Equal(t, []string{"go"}, dirNames(t, dir))
}

// --remove is the removal of the hooks: it does not look at the shims.
func TestSetupCC_RemoveDoesNotTouchTheShims(t *testing.T) {
	home := setupEnv(t, closedPort(t))
	dir := seedShims(t, home)
	code, _, errb := runPdxSetup(t, home, "--agent", "cc", "--remove", "--force")
	require.Equal(t, 0, code, errb)
	assert.Len(t, dirNames(t, dir), len(shimTools))
}

// --uninstall-gate is gone with the shims it removed: it is no flag now, so the setup asks for --agent.
func TestSetup_UninstallGateIsNoMoreAFlag(t *testing.T) {
	home := setupEnv(t, closedPort(t))
	dir := seedShims(t, home)
	code, _, errb := runPdxSetup(t, home, "--uninstall-gate")
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "--agent flag is required")
	assert.Len(t, dirNames(t, dir), len(shimTools), "nothing is removed by it")
}
