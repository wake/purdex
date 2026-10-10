package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// #1950: a daemon with no token generates one at boot (initPairing, general mode). The plaintext goes to a terminal only; into a
// log or a pipe goes a fingerprint and where the token is. The generated token is written to the config file as the file has it
// (no flag override, no other setting lost), 0600; if it cannot be written the daemon does not start on a token that no client can
// learn.

const tokenPrefix = "purdex_"

func readConfigFile(t *testing.T, path string) config.Config {
	t.Helper()
	cfg, err := config.Load(path)
	require.NoError(t, err)
	return cfg
}

func TestInitPairing_GeneratesATokenAndWritesItTo0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "config.toml")
	cfg := config.Config{Bind: "100.64.0.2", Port: 7860}
	c := newTestCore(&cfg)
	var out bytes.Buffer
	require.NoError(t, initPairingTo(c, &cfg, path, false, &out, false))

	require.True(t, strings.HasPrefix(cfg.Token, tokenPrefix) && len(cfg.Token) == len(tokenPrefix)+40, "token %d bytes", len(cfg.Token))
	require.Equal(t, cfg.Token, c.Cfg.Token, "the runtime config carries it")
	require.Equal(t, core.StatePending, c.Pairing.Get())
	require.Equal(t, cfg.Token, readConfigFile(t, path).Token, "the file carries it")
	st, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

// Not a terminal (the daemon's stdout is its log file, or a pipe): the token is not printed.
func TestInitPairing_NonTTYPrintsAFingerprintAndNeverTheToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Config{Bind: "100.64.0.2", Port: 7860}
	var out bytes.Buffer
	require.NoError(t, initPairingTo(newTestCore(&cfg), &cfg, path, false, &out, false))
	require.NotContains(t, out.String(), cfg.Token)
	require.NotContains(t, out.String(), strings.TrimPrefix(cfg.Token, tokenPrefix))
	require.Contains(t, out.String(), config.TokenFingerprint(cfg.Token))
	require.Contains(t, out.String(), path, "it says where the token is")
	require.Contains(t, out.String(), "0600")
}

// A terminal: the person starting the daemon in the foreground sees it, once (the first pairing of an App needs it).
func TestInitPairing_TTYPrintsTheToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Config{Bind: "100.64.0.2", Port: 7860}
	var out bytes.Buffer
	require.NoError(t, initPairingTo(newTestCore(&cfg), &cfg, path, false, &out, true))
	require.Contains(t, out.String(), "Token: "+cfg.Token)
}

// The file keeps what it had: every other setting stays, and a command-line override (--bind / --port, already applied to the
// in-memory config) is not written into it.
func TestInitPairing_KeepsTheFilesOtherSettingsAndNotTheFlagOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	orig := config.Config{Bind: "100.64.0.2", Port: 7861, Allow: []string{"100.64.0.0/10"}, DataDir: "/data/pdx", AllowedPaths: []string{"/w"},
		Peers: config.PeersConfig{Hosts: []config.PeerHost{{Alias: "air26", URL: "https://a.example", HostID: "h-a", InboundToken: "pdxp_in"}}}}
	require.NoError(t, config.WriteFile(path, orig))
	cfg := orig
	cfg.Bind, cfg.Port = "127.0.0.1", 9999 // --bind / --port
	require.NoError(t, initPairingTo(newTestCore(&cfg), &cfg, path, false, &bytes.Buffer{}, false))

	got := readConfigFile(t, path)
	require.Equal(t, cfg.Token, got.Token)
	require.Equal(t, "100.64.0.2", got.Bind, "the --bind override must not be persisted")
	require.Equal(t, 7861, got.Port, "the --port override must not be persisted")
	require.Equal(t, orig.Allow, got.Allow)
	require.Equal(t, orig.DataDir, got.DataDir)
	require.Equal(t, orig.AllowedPaths, got.AllowedPaths)
	require.Equal(t, orig.Peers.Hosts, got.Peers.Hosts)
}

// A token that is there is left alone: nothing written, nothing printed.
func TestInitPairing_AnExistingTokenIsLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Config{Bind: "100.64.0.2", Port: 7860, Token: "pdx_existing0123456789012345678901234567"}
	require.NoError(t, config.WriteFile(path, cfg))
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	var out bytes.Buffer
	c := newTestCore(&cfg)
	require.NoError(t, initPairingTo(c, &cfg, path, false, &out, true))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Empty(t, out.String())
	require.Equal(t, core.StateNormal, c.Pairing.Get())
	require.Equal(t, "pdx_existing0123456789012345678901234567", cfg.Token)
}

// Cannot write the config: no start. The runtime state is unchanged (no token, not pending), and the error says how to fix it
// without naming the token.
func TestInitPairing_AConfigThatCannotBeWrittenRefusesTheStart(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	path := filepath.Join(blocker, "config.toml") // its parent is a file
	cfg := config.Config{Bind: "100.64.0.2", Port: 7860}
	c := newTestCore(&cfg)
	var out bytes.Buffer
	err := initPairingTo(c, &cfg, path, false, &out, true)
	require.Error(t, err)
	require.Empty(t, cfg.Token)
	require.Empty(t, c.Cfg.Token)
	require.Equal(t, core.StateNormal, c.Pairing.Get())
	require.Empty(t, out.String(), "no token is shown for a daemon that will not run")
	require.Contains(t, err.Error(), path)
	require.Contains(t, err.Error(), "pdx token generate")
	require.NotContains(t, err.Error(), tokenPrefix, "the refusal never names the token it did not keep")
}

// Quick mode (the pairing code) is not touched by this: no token is generated.
func TestInitPairing_QuickModeStillPairsWithoutAToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := config.Config{Bind: "100.64.0.2", Port: 7860}
	c := newTestCore(&cfg)
	var out bytes.Buffer
	require.NoError(t, initPairingTo(c, &cfg, path, true, &out, true))
	require.Empty(t, cfg.Token)
	require.Equal(t, core.StatePairing, c.Pairing.Get())
	require.Contains(t, out.String(), "配對碼")
}
