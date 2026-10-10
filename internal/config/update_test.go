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
	old := LockWait
	LockWait = 200 * time.Millisecond
	defer func() { LockWait = old }()

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

// UpdateChanged writes only the fields that differ between before and after: a token another writer set, a host another
// writer added and a setting the file has differently all stay; the changed field goes in. Mutation gate: write all of after
// -> red.
func TestUpdateChanged_WritesOnlyWhatChanged(t *testing.T) {
	p := writeCfg(t, richConfig)
	mem, err := Load(p) // what the daemon holds
	require.NoError(t, err)
	// another writer: a new token, a new host, a different upload_dir
	require.NoError(t, SetToken(p, "pdx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	_, err = Update(p, func(c *Config) error {
		c.UploadDir = "/tmp/file-has-this"
		c.Peers.Hosts = append(c.Peers.Hosts, PeerHost{Alias: "extra", HostID: "x:1", InboundToken: "t"})
		return nil
	})
	require.NoError(t, err)

	after := mem.Clone()
	after.Detect.PollInterval = 9 // the daemon's change
	after.Bind = "9.9.9.9"        // ... and an override that is not a change of this writer's? it IS different, so it is written
	before := mem.Clone()
	_, err = UpdateChanged(p, before, after)
	require.NoError(t, err)
	back, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, 9, back.Detect.PollInterval)
	require.Equal(t, "pdx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", back.Token)
	require.Equal(t, "/tmp/file-has-this", back.UploadDir)
	require.Len(t, back.Peers.Hosts, 2)
	require.Equal(t, "9.9.9.9", back.Bind)
}

// A change inside a nested struct carries only that field: peers.deliver does not drag peers.hosts back to the writer's list.
func TestUpdateChanged_ANestedFieldDoesNotCarryItsSiblings(t *testing.T) {
	p := writeCfg(t, richConfig)
	mem, err := Load(p)
	require.NoError(t, err)
	_, err = Update(p, func(c *Config) error {
		c.Peers.Hosts = append(c.Peers.Hosts, PeerHost{Alias: "extra", HostID: "x:1", InboundToken: "t"})
		return nil
	})
	require.NoError(t, err)
	after := mem.Clone()
	after.Peers.Deliver = true
	_, err = UpdateChanged(p, mem.Clone(), after)
	require.NoError(t, err)
	back, err := Load(p)
	require.NoError(t, err)
	require.True(t, back.Peers.Deliver)
	require.Len(t, back.Peers.Hosts, 2, "peers.hosts was written back from the writer's older list")
}

// Two names of one file (two symlinks to it) share one lock: writers through either lose nothing. Mutation gate: name the lock
// from the path as given -> red.
func TestUpdate_TwoNamesOfOneFileShareOneLock(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real.toml")
	require.NoError(t, os.WriteFile(real, []byte(richConfig), 0o600))
	a, b := filepath.Join(t.TempDir(), "config.toml"), filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.Symlink(real, a))
	require.NoError(t, os.Symlink(real, b))
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := a
			if i%2 == 1 {
				path = b
			}
			_, err := Update(path, func(c *Config) error {
				c.Peers.Hosts = append(c.Peers.Hosts, PeerHost{Alias: fmt.Sprintf("h%d", i), HostID: fmt.Sprintf("id%d", i), InboundToken: "t"})
				return nil
			})
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()
	back, err := Load(real)
	require.NoError(t, err)
	require.Len(t, back.Peers.Hosts, n+1)
}

// A config file that is itself a symlink stays one: the write goes to what it points at.
func TestUpdate_AConfigThatIsASymlinkStaysASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.toml")
	require.NoError(t, os.WriteFile(target, []byte(richConfig), 0o600))
	link := filepath.Join(dir, "config.toml")
	require.NoError(t, os.Symlink(target, link))
	_, err := Update(link, func(c *Config) error { c.UploadDir = "/tmp/through-link"; return nil })
	require.NoError(t, err)
	st, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, st.Mode()&os.ModeSymlink, "the symlink was replaced by a file")
	back, err := Load(target)
	require.NoError(t, err)
	require.Equal(t, "/tmp/through-link", back.UploadDir)
}

// Names that do not exist yet still resolve to one: through a symlinked ancestor over directories that are not there, and
// through a symlink that dangles (the file is created where it points). (codex attack, #2556.)
func TestCanonPath_ResolvesWhatDoesNotExistYet(t *testing.T) {
	real := t.TempDir()
	realEval, _ := filepath.EvalSymlinks(real)
	link := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(real, link))
	require.Equal(t, filepath.Join(realEval, "new", "deeper", "config.toml"), canonPath(filepath.Join(link, "new", "deeper", "config.toml")))

	dangling := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.Symlink(filepath.Join(real, "later.toml"), dangling))
	require.Equal(t, filepath.Join(realEval, "later.toml"), canonPath(dangling))
}

func TestUpdate_ADanglingConfigSymlinkIsFilledNotReplaced(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.Symlink(filepath.Join(real, "later.toml"), link))
	_, err := Update(link, func(c *Config) error { c.HostID = "h:1"; return nil })
	require.NoError(t, err)
	st, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, st.Mode()&os.ModeSymlink, "the symlink was replaced")
	back, err := Load(filepath.Join(real, "later.toml"))
	require.NoError(t, err)
	require.Equal(t, "h:1", back.HostID)
}

// The path that is locked is the path that is written: a config symlink retargeted while a writer waits for the lock does not
// send that writer to the new target under the old target's lock. (codex, #2556.)
func TestWithLock_WritesThePathItLocked(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.toml"), filepath.Join(dir, "b.toml")
	require.NoError(t, os.WriteFile(a, []byte(richConfig), 0o600))
	require.NoError(t, os.WriteFile(b, []byte(richConfig), 0o600))
	link := filepath.Join(dir, "config.toml")
	require.NoError(t, os.Symlink(a, link))

	hold, err := os.OpenFile(a+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	defer hold.Close()
	require.NoError(t, syscall.Flock(int(hold.Fd()), syscall.LOCK_EX))

	done := make(chan error, 1)
	var lockedPath string
	go func() {
		done <- WithLock(link, func(locked string) error {
			lockedPath = locked
			mem, _ := Load(locked)
			after := mem.Clone()
			after.UploadDir = "/tmp/written"
			_, err := UpdateChangedHeld(locked, mem, after)
			return err
		})
	}()
	time.Sleep(100 * time.Millisecond) // the writer is waiting for a.toml.lock
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(b, link)) // retargeted
	require.NoError(t, syscall.Flock(int(hold.Fd()), syscall.LOCK_UN))
	require.NoError(t, <-done)

	wantA, _ := filepath.EvalSymlinks(a)
	require.Equal(t, wantA, lockedPath)
	gotA, _ := Load(a)
	gotB, _ := Load(b)
	require.Equal(t, "/tmp/written", gotA.UploadDir)
	require.NotEqual(t, "/tmp/written", gotB.UploadDir, "wrote the new target while holding the old target's lock")
}
