package config

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// tmuxExecTimeout bounds lightweight tmux metadata queries (display-message, -V).
// Shorter than TmuxAlive's 5s because these are read-only, single-process commands.
const tmuxExecTimeout = 3 * time.Second

// GetTmuxInstance returns the tmux server's "pid:startTime" identifier.
// Returns empty string if tmux is not running or the command times out.
func GetTmuxInstance() string {
	return GetTmuxInstanceContext(context.Background())
}

// GetTmuxInstanceContext is GetTmuxInstance bounded by min(ctx's deadline,
// tmuxExecTimeout), so the probe on the session-list path (#1293) runs
// inside that read's budget.
func GetTmuxInstanceContext(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, tmuxExecTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{pid}:#{start_time}")
	// A killed probe must not keep Output() waiting on a grandchild that
	// inherited the pipe.
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// EnsureHostID generates a stable host ID if not already set, persists it to the
// config file, and updates cfg.HostID in place. Format: "hostname:6-char-code".
// If cfg.HostID is already set, returns it unchanged without writing.
func EnsureHostID(cfg *Config, cfgPath string) (string, error) {
	if cfg.HostID != "" {
		return cfg.HostID, nil
	}

	hostname := shortHostname()
	code := randomCode(6)
	cfg.HostID = hostname + ":" + code

	if err := WriteFile(cfgPath, *cfg); err != nil {
		cfg.HostID = "" // rollback — don't use unstable ID
		return "", fmt.Errorf("persist host_id: %w", err)
	}
	return cfg.HostID, nil
}

func shortHostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	return strings.ToLower(name)
}

func randomCode(n int) string {
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := make([]byte, n)
	rand.Read(buf)
	for i := range buf {
		buf[i] = chars[buf[i]%36]
	}
	return string(buf)
}

// WriteFile serialises the config to TOML and writes it to the given path atomically.
func WriteFile(path string, cfg Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes())
}

// writeFileAtomic writes data to path through a temp file of its own in the same directory (a fixed name would be shared by two
// concurrent writers), mode 0600, flushed before the rename that publishes it.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
