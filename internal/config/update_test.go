package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// #2556: every writer of config.toml takes "<path>.lock", re-reads the file inside it, applies only what it changes and writes
// atomically — so no writer's update is undone by another's.

const richConfig = `# my notes
bind = "100.64.0.2"
port = 7860
token = "purdex_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
host_id = "mlab:abc123"
upload_dir = "/tmp/up"

[detect]
poll_interval = 7

[[peers.hosts]]
alias = "air26"
inbound_token = "inbound-1"
`

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

// The bytes Update writes for a no-op mutate are the bytes WriteFile writes for the same file: nothing about the file changes
// when the base moves from memory to disk, and a second no-op changes nothing more.
func TestUpdate_NoOpWritesExactlyWhatWriteFileWrites(t *testing.T) {
	p := writeCfg(t, richConfig)
	cfg, err := Load(p)
	require.NoError(t, err)
	ref := filepath.Join(t.TempDir(), "ref.toml")
	require.NoError(t, WriteFile(ref, cfg))
	want, _ := os.ReadFile(ref)

	_, err = Update(p, func(*Config) error { return nil })
	require.NoError(t, err)
	got, _ := os.ReadFile(p)
	require.True(t, bytes.Equal(want, got), "first no-op differs from WriteFile:\n%s\nvs\n%s", got, want)
	_, err = Update(p, func(*Config) error { return nil })
	require.NoError(t, err)
	again, _ := os.ReadFile(p)
	require.True(t, bytes.Equal(got, again), "a second no-op changed the file")
}

// The base is the file: a token another writer set is kept when this one changes something else. Mutation gate: base the write on
// anything but the file -> red.
func TestUpdate_KeepsWhatAnotherWriterWrote(t *testing.T) {
	p := writeCfg(t, richConfig)
	require.NoError(t, SetToken(p, "pdx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	got, err := Update(p, func(c *Config) error { c.UploadDir = "/tmp/other"; return nil })
	require.NoError(t, err)
	require.Equal(t, "pdx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", got.Token)
	back, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, "pdx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", back.Token)
	require.Equal(t, "/tmp/other", back.UploadDir)
	require.Equal(t, "air26", back.Peers.Hosts[0].Alias)
}

func TestUpdate_AMutateErrorLeavesTheFileAlone(t *testing.T) {
	p := writeCfg(t, richConfig)
	_, err := Update(p, func(c *Config) error { c.UploadDir = "/x"; return fmt.Errorf("no") })
	require.Error(t, err)
	got, _ := os.ReadFile(p)
	require.Equal(t, richConfig, string(got))
}

func TestUpdate_AMissingFileStartsFromDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	_, err := Update(p, func(c *Config) error { c.HostID = "h:1"; return nil })
	require.NoError(t, err)
	back, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, "h:1", back.HostID)
	st, _ := os.Stat(p)
	require.Equal(t, os.FileMode(0o600), st.Mode().Perm())
}

// Many writers at once, each adding its own host: none is lost. Mutation gate: no lock around read-modify-write -> red.
func TestUpdate_ConcurrentWritersLoseNothing(t *testing.T) {
	p := writeCfg(t, richConfig)
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := Update(p, func(c *Config) error {
				c.Peers.Hosts = append(c.Peers.Hosts, PeerHost{Alias: fmt.Sprintf("h%d", i), HostID: fmt.Sprintf("id%d", i), InboundToken: "t"})
				return nil
			})
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()
	back, err := Load(p)
	require.NoError(t, err)
	require.Len(t, back.Peers.Hosts, n+1)
}

// A process that holds the lock and does not let go: Update gives up after its wait, with an error, and writes nothing.
func TestUpdate_GivesUpWhenTheLockIsHeld(t *testing.T) {
	p := writeCfg(t, richConfig)
	lock, err := os.OpenFile(p+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer lock.Close()
	require.NoError(t, syscall.Flock(int(lock.Fd()), syscall.LOCK_EX))
	old := lockWait
	lockWait = 200 * time.Millisecond
	defer func() { lockWait = old }()

	start := time.Now()
	_, err = Update(p, func(c *Config) error { c.UploadDir = "/x"; return nil })
	require.Error(t, err)
	require.Contains(t, err.Error(), "lock")
	require.Less(t, time.Since(start), 3*time.Second)
	got, _ := os.ReadFile(p)
	require.Equal(t, richConfig, string(got))
}

// EnsureHostID writes only host_id: a --bind override sitting in the in-memory config is not made permanent, and a host_id
// another process already wrote is adopted rather than replaced.
func TestEnsureHostID_PersistsOnlyTheHostID(t *testing.T) {
	p := writeCfg(t, strings.Replace(richConfig, `host_id = "mlab:abc123"`+"\n", "", 1))
	cfg, err := Load(p)
	require.NoError(t, err)
	cfg.Bind = "9.9.9.9" // the flag override, in memory only
	id, err := EnsureHostID(&cfg, p)
	require.NoError(t, err)
	back, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, id, back.HostID)
	require.Equal(t, "100.64.0.2", back.Bind, "the override must not reach the file")
}

func TestEnsureHostID_AdoptsOneAnotherProcessWrote(t *testing.T) {
	p := writeCfg(t, strings.Replace(richConfig, `host_id = "mlab:abc123"`+"\n", "", 1))
	cfg, err := Load(p) // this process saw no host_id ...
	require.NoError(t, err)
	_, err = Update(p, func(c *Config) error { c.HostID = "other:zzzzzz"; return nil }) // ... and another wrote one
	require.NoError(t, err)
	id, err := EnsureHostID(&cfg, p)
	require.NoError(t, err)
	require.Equal(t, "other:zzzzzz", id)
	require.Equal(t, "other:zzzzzz", cfg.HostID)
}
