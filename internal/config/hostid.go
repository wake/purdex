package config

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// GetTmuxInstance returns the tmux server's "pid:startTime" identifier.
// Returns empty string if tmux is not running.
func GetTmuxInstance() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{pid}:#{start_time}").Output()
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

	if err := persistConfig(cfgPath, *cfg); err != nil {
		return cfg.HostID, fmt.Errorf("persist host_id: %w", err)
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

func persistConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
