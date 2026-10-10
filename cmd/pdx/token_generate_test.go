package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #2557: `pdx token generate` rewrote a running daemon's config behind its back (the daemon kept the old token in memory, every
// client got 401). With a daemon running on that config it refuses unless --force; the token is shown in plaintext only to a
// terminal.

const oldTokenLine = "purdex_" + "0123456789012345678901234567890123456789"

// setup writes a config whose data_dir is its own temp dir and, when running, holds the pid lock the way a daemon does.
func setupTokenCfg(t *testing.T, running bool) (cfgPath, before string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "config.toml")
	before = fmt.Sprintf("# mine\ndata_dir = %q\ntoken = %q\nport = 7860\n", dir, oldTokenLine)
	require.NoError(t, os.WriteFile(cfgPath, []byte(before), 0o600))
	if running {
		f, err := acquirePidLock(filepath.Join(dir, "pdx.pid"), os.Getpid())
		require.NoError(t, err)
		t.Cleanup(func() { releasePidLock(f, "") })
	}
	return cfgPath, before
}

func TestTokenGenerate_RefusesWhileTheDaemonRunsAndLeavesTheFileAlone(t *testing.T) {
	path, before := setupTokenCfg(t, true)
	var out, errw bytes.Buffer
	code := tokenGenerate(path, false, true, &out, &errw)
	require.NotEqual(t, 0, code)
	after, _ := os.ReadFile(path)
	require.Equal(t, before, string(after), "the file is untouched")
	require.Empty(t, out.String(), "no token is shown")
	require.Contains(t, errw.String(), "--force")
	require.NotContains(t, errw.String()+out.String(), oldTokenLine)
}

func TestTokenGenerate_ForceWritesAndSaysEveryClientMustRepair(t *testing.T) {
	path, _ := setupTokenCfg(t, true)
	var out, errw bytes.Buffer
	require.Equal(t, 0, tokenGenerate(path, true, true, &out, &errw))
	after, _ := os.ReadFile(path)
	require.NotContains(t, string(after), oldTokenLine)
	require.Contains(t, string(after), "# mine", "only the token line changes")
	require.Contains(t, errw.String()+out.String(), "re-pair", "says plainly what it costs")
	require.Contains(t, errw.String()+out.String(), "restart")
}

func TestTokenGenerate_NoDaemonWritesAsToday(t *testing.T) {
	path, _ := setupTokenCfg(t, false)
	var out, errw bytes.Buffer
	require.Equal(t, 0, tokenGenerate(path, false, true, &out, &errw))
	after, _ := os.ReadFile(path)
	require.NotContains(t, string(after), oldTokenLine)
	require.Contains(t, out.String(), "Token: pdx_")
	require.Contains(t, out.String(), "Saved to: "+path)
}

// A stale pid file (no one holds its lock) is not a running daemon.
func TestTokenGenerate_StalePidFileIsNotARunningDaemon(t *testing.T) {
	path, _ := setupTokenCfg(t, false)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "pdx.pid"), []byte("99999"), 0o644))
	var out, errw bytes.Buffer
	require.Equal(t, 0, tokenGenerate(path, false, true, &out, &errw))
}

// Not a terminal (a log, a pipe, a captured stdout): the token is not printed, only a fingerprint and where it is.
func TestTokenGenerate_NonTTYNeverPrintsTheToken(t *testing.T) {
	path, _ := setupTokenCfg(t, false)
	var out, errw bytes.Buffer
	require.Equal(t, 0, tokenGenerate(path, false, false, &out, &errw))
	after, _ := os.ReadFile(path)
	var written string
	for _, l := range strings.Split(string(after), "\n") {
		if strings.HasPrefix(l, "token") {
			written = strings.Trim(strings.SplitN(l, "=", 2)[1], ` "`)
		}
	}
	require.True(t, strings.HasPrefix(written, "pdx_"), written)
	require.NotContains(t, out.String()+errw.String(), written)
	require.Contains(t, out.String(), "fingerprint")
	require.Contains(t, out.String(), path)
}
