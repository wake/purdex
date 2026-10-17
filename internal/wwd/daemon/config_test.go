package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	home := t.TempDir()
	c, err := LoadConfig(filepath.Join(home, "none.toml"), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 7871 || c.DataDir != filepath.Join(home, ".local/share/wwd") ||
		c.SocketPath != filepath.Join(home, ".local/share/wwd/wwd.sock") ||
		c.TokenPath != filepath.Join(home, ".config/wwd/pool.token") {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestLoadConfigFile(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	os.WriteFile(p, []byte("alias = \"air26\"\nport = 7999\ntailnet_addr = \"100.64.0.4\"\n[worker]\nenabled = true\n"), 0o600)
	c, err := LoadConfig(p, home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Alias != "air26" || c.Port != 7999 || c.TailnetAddr != "100.64.0.4" || !c.Worker.Enabled {
		t.Fatalf("%+v", c)
	}
	// unknown keys are an error (typos must not pass silently)
	os.WriteFile(p, []byte("alais = \"x\"\n"), 0o600)
	if _, err := LoadConfig(p, home); err == nil {
		t.Fatal("unknown key must fail")
	}
}

func TestEnsureTokenCreatesAndReuses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "pool.token")
	tok, err := EnsureToken(p)
	if err != nil || len(tok) < 32 {
		t.Fatalf("%q %v", tok, err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	tok2, err := EnsureToken(p)
	if err != nil || tok2 != tok {
		t.Fatalf("reuse: %q %v", tok2, err)
	}
	// loose permissions are refused
	os.Chmod(p, 0o644)
	if _, err := EnsureToken(p); err == nil {
		t.Fatal("0644 token must be refused")
	}
	// empty file is refused
	os.Chmod(p, 0o600)
	os.WriteFile(p, nil, 0o600)
	if _, err := EnsureToken(p); err == nil {
		t.Fatal("empty token must be refused")
	}
}
