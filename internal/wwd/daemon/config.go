// Package daemon is the wwd process: configuration, pool token, the two HTTP
// surfaces (token-protected TCP and the trusted local socket).
package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Config is ~/.config/wwd/config.toml (spec §2). Every field is optional.
type Config struct {
	Alias       string       `toml:"alias"`
	Port        int          `toml:"port"`
	TailnetAddr string       `toml:"tailnet_addr"`
	DataDir     string       `toml:"data_dir"`
	SocketPath  string       `toml:"socket"`
	TokenPath   string       `toml:"token_file"`
	WorkRoot    string       `toml:"work_root"`
	Worker      WorkerConfig `toml:"worker"`
}

type WorkerConfig struct {
	Enabled bool `toml:"enabled"`
}

// DefaultConfigPath is ~/.config/wwd/config.toml.
func DefaultConfigPath(home string) string {
	return filepath.Join(home, ".config", "wwd", "config.toml")
}

// LoadConfig reads path (a missing file means all defaults) and fills defaults
// relative to home. Unknown keys are an error so typos cannot pass silently.
func LoadConfig(path, home string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		md, err := toml.Decode(string(b), &c)
		if err != nil {
			return c, fmt.Errorf("config %s: %w", path, err)
		}
		if u := md.Undecoded(); len(u) > 0 {
			return c, fmt.Errorf("config %s: unknown key %s", path, u[0])
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return c, err
	}
	if c.Port == 0 {
		c.Port = wire.Port
	}
	if c.DataDir == "" {
		c.DataDir = filepath.Join(home, ".local", "share", "wwd")
	}
	if c.SocketPath == "" {
		c.SocketPath = filepath.Join(c.DataDir, "wwd.sock")
	}
	if c.TokenPath == "" {
		c.TokenPath = filepath.Join(home, ".config", "wwd", "pool.token")
	}
	if c.WorkRoot == "" {
		c.WorkRoot = filepath.Join(c.DataDir, "work")
	}
	return c, nil
}

// EnsureToken returns the pool token, creating a random one (0600) when the
// file does not exist. A token file readable by others, or empty, is refused.
func EnsureToken(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		tok := hex.EncodeToString(raw)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", err
		}
		defer f.Close()
		if _, err := f.WriteString(tok + "\n"); err != nil {
			return "", err
		}
		return tok, nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	// One descriptor for the checks and the read: no second pathname lookup.
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("token file %s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("token file %s: mode %v is readable by others; chmod 600", path, st.Mode().Perm())
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	return tok, nil
}
