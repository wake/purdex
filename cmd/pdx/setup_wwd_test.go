package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/install"
)

func TestParseSetupWWD(t *testing.T) {
	o, ok, err := parseSetupWWD([]string{"--wwd", "--wwd-alias", "mlab", "--wwd-binary", "/b/wwd", "--token-file", "/t", "--no-restart", "--dry-run"})
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if o.alias != "mlab" || o.binary != "/b/wwd" || o.tokenFile != "/t" || !o.noRestart || !o.dryRun || o.remove {
		t.Fatalf("%+v", o)
	}
	o, ok, err = parseSetupWWD([]string{"--wwd-remove", "--wwd-purge"})
	if err != nil || !ok || !o.remove || !o.purge {
		t.Fatalf("%+v %v %v", o, ok, err)
	}
	// ordinary setup arguments are not ours
	if _, ok, err := parseSetupWWD([]string{"--agent", "cc", "--force"}); ok || err != nil {
		t.Fatalf("an agent setup was taken for a wwd one: %v %v", ok, err)
	}
	// the host must be named for an install
	if _, ok, err := parseSetupWWD([]string{"--wwd"}); !ok || err == nil || !strings.Contains(err.Error(), "--wwd-alias") {
		t.Fatalf("missing alias: ok=%v err=%v", ok, err)
	}
	if _, ok, err := parseSetupWWD([]string{"--wwd", "--wwd-alias", "elsewhere"}); !ok || err == nil {
		t.Fatalf("unknown alias accepted: %v", err)
	}
	// wwd flags next to an agent flag are refused: they are two different jobs
	if _, ok, err := parseSetupWWD([]string{"--wwd", "--wwd-alias", "mlab", "--agent", "cc"}); !ok || err == nil {
		t.Fatalf("mixed jobs accepted: %v", err)
	}
}

func TestWWDBinaryDefaultsToTheOneBesidePdx(t *testing.T) {
	dir := t.TempDir()
	pdx := filepath.Join(dir, "pdx")
	if err := os.WriteFile(pdx, []byte("p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := defaultWWDBinary(pdx); err == nil || !strings.Contains(err.Error(), "--wwd-binary") {
		t.Fatalf("no wwd beside pdx: %v", err)
	}
	w := filepath.Join(dir, "wwd")
	if err := os.WriteFile(w, []byte("w"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := defaultWWDBinary(pdx)
	if err != nil || got != w {
		t.Fatalf("%q %v", got, err)
	}
}

func TestSetupWWDDrivesTheInstaller(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "wwd")
	if err := os.WriteFile(bin, []byte("wwd binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var calls []string
	in := &install.Installer{
		Home: home, GOOS: "darwin", UID: 501, User: "wake",
		Run: func(ctx context.Context, tag, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if name == "launchctl" && len(args) > 0 && args[0] == "print" {
				return nil, os.ErrNotExist // not loaded yet
			}
			return nil, nil
		},
	}
	out := &bytes.Buffer{}
	in.Out = out
	o := wwdSetupOpts{alias: "air26", binary: bin}
	if err := runSetupWWD(context.Background(), in, o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "gs.wake.wwd.plist")); err != nil {
		t.Error("no LaunchAgent")
	}
	cfg, _ := os.ReadFile(filepath.Join(home, ".config", "wwd", "config.toml"))
	if !strings.Contains(string(cfg), `alias = "air26"`) || !strings.Contains(string(cfg), "[owner]") {
		t.Errorf("the air26 template was not used:\n%s", cfg)
	}
	var bootstrapped bool
	for _, c := range calls {
		bootstrapped = bootstrapped || strings.HasPrefix(c, "launchctl bootstrap gui/501")
	}
	if !bootstrapped {
		t.Errorf("calls %v", calls)
	}

	// removal
	calls = nil
	if err := runSetupWWD(context.Background(), in, wwdSetupOpts{remove: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "gs.wake.wwd.plist")); err == nil {
		t.Error("LaunchAgent left")
	}
}
