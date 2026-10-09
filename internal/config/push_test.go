package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/wake/purdex/internal/config"
)

func loadToml(t *testing.T, body string) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Push is off unless config.toml names the APNs directory (spec §3).
func TestPush_AbsentSectionIsOff(t *testing.T) {
	if cfg := loadToml(t, "port = 9000\n"); cfg.PushAPNsDir() != "" {
		t.Fatalf("APNsDir = %q, want off", cfg.PushAPNsDir())
	}
	if cfg := loadToml(t, "[push]\n"); cfg.PushAPNsDir() != "" {
		t.Fatalf("empty section: APNsDir = %q, want off", cfg.PushAPNsDir())
	}
}

func TestPush_APNsDirIsReadAndKeptVerbatim(t *testing.T) {
	cfg := loadToml(t, "[push]\napns_dir = \"~/x\"\n")
	if cfg.PushAPNsDir() != "~/x" { // "~" is expanded by the module at Init, with the daemon user's home
		t.Fatalf("APNsDir = %q", cfg.PushAPNsDir())
	}
}

func TestPush_CloneIsIndependent(t *testing.T) {
	a := loadToml(t, "[push]\napns_dir = \"/a\"\n")
	b := a.Clone()
	b.Push.APNsDir = "/b"
	if a.PushAPNsDir() != "/a" {
		t.Fatalf("clone shares the original: %q", a.PushAPNsDir())
	}
}

// Writing a config that has no [push] section back must not add one (the TOML encoder would emit `[push]` otherwise).
func TestPush_AnUnsetSectionIsNotWrittenBack(t *testing.T) {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(loadToml(t, "port = 9000\n")); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("push")) {
		t.Fatalf("encoded config mentions push:\n%s", buf.String())
	}
	buf.Reset()
	if err := toml.NewEncoder(&buf).Encode(loadToml(t, "[push]\napns_dir = \"/a\"\n")); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("apns_dir")) {
		t.Fatalf("a set section is lost:\n%s", buf.String())
	}
}
