package install

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/daemon"
)

// fakeRun records every command and answers from a script.
type fakeRun struct {
	calls  []string
	failOn map[string]bool // a command line prefix that exits non-zero
}

func (f *fakeRun) run(ctx context.Context, tag, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	for p, bad := range f.failOn {
		if bad && strings.HasPrefix(line, p) {
			return nil, os.ErrNotExist
		}
	}
	// the two sudo commands that touch the unit file act on it, as the real ones do
	if name == "sudo" && len(args) == 5 && args[0] == "install" {
		b, err := os.ReadFile(args[3])
		if err != nil {
			return nil, err
		}
		return nil, os.WriteFile(args[4], b, 0o644)
	}
	if name == "sudo" && len(args) == 3 && args[0] == "rm" {
		return nil, os.Remove(args[2])
	}
	return nil, nil
}

func (f *fakeRun) has(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func writeBin(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "wwd-src")
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func newInstaller(t *testing.T, goos, alias, bin string) (*Installer, *fakeRun, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	f := &fakeRun{failOn: map[string]bool{}}
	out := &bytes.Buffer{}
	return &Installer{Home: home, GOOS: goos, UID: 501, User: "wake", UnitPath: filepath.Join(home, "wwd.service"), Alias: alias, Binary: bin, Run: f.run, Out: out}, f, out
}

func TestEveryProfileIsAValidConfig(t *testing.T) {
	for _, h := range Hosts {
		text, err := ConfigFor(h.Alias)
		if err != nil {
			t.Fatal(err)
		}
		home := t.TempDir()
		p := filepath.Join(home, "config.toml")
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := daemon.LoadConfig(p, home) // strict: unknown keys, bad peers and bad slots all fail here
		if err != nil {
			t.Fatalf("%s: %v\n%s", h.Alias, err, text)
		}
		if cfg.Alias != h.Alias || cfg.TailnetAddr != h.Addr {
			t.Errorf("%s: alias %q addr %q", h.Alias, cfg.Alias, cfg.TailnetAddr)
		}
		if len(cfg.Peers) != len(Hosts)-1 {
			t.Errorf("%s: %d peers, want the other %d hosts", h.Alias, len(cfg.Peers), len(Hosts)-1)
		}
		for _, peer := range cfg.Peers {
			if peer.Alias == h.Alias {
				t.Errorf("%s lists itself as a peer", h.Alias)
			}
		}
		if cfg.Worker.Enabled != h.WorkerEnabled || cfg.Owner.Enabled != h.OwnerRule {
			t.Errorf("%s: worker=%v owner=%v", h.Alias, cfg.Worker.Enabled, cfg.Owner.Enabled)
		}
	}
	// the facts the spec fixes (§2): air19 runs no worker, air26 has the owner rule
	byAlias := map[string]Host{}
	for _, h := range Hosts {
		byAlias[h.Alias] = h
	}
	if byAlias["air19"].WorkerEnabled || !byAlias["air26"].OwnerRule || byAlias["mlab"].OwnerRule || !byAlias["mlab"].WorkerEnabled {
		t.Errorf("profiles disagree with the spec: %+v", byAlias)
	}
	if _, err := ConfigFor("nowhere"); err == nil {
		t.Error("an unknown alias must be refused")
	}
	// no secret in a template
	for _, h := range Hosts {
		text, _ := ConfigFor(h.Alias)
		if strings.Contains(strings.ToLower(text), "token =") {
			t.Errorf("%s: a token in the config template", h.Alias)
		}
	}
}

func TestPlist(t *testing.T) {
	p := Plist(PlistOpts{Label: Label, Binary: "/Users/a&b/.local/share/wwd/bin/wwd", Config: "/Users/a&b/.config/wwd/config.toml", Log: "/Users/a&b/.local/share/wwd/wwd.log", Path: "/opt/homebrew/bin:/usr/bin"})
	for _, want := range []string{
		"<key>Label</key>", "<string>gs.wake.wwd</string>", "<key>KeepAlive</key>", "<true/>", "<key>RunAtLoad</key>",
		"<string>-config</string>", "/Users/a&amp;b/.config/wwd/config.toml", "<key>StandardErrorPath</key>", "/opt/homebrew/bin:/usr/bin",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "a&b") {
		t.Error("an unescaped ampersand makes the plist invalid XML")
	}
	if !strings.HasPrefix(p, "<?xml") {
		t.Error("not a plist document")
	}
}

// macOS's own parser has the last word on whether the plist is well formed.
func TestPlistPassesPlutilLint(t *testing.T) {
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Skip("no plutil on this host")
	}
	p := filepath.Join(t.TempDir(), "gs.wake.wwd.plist")
	text := Plist(PlistOpts{Label: Label, Binary: "/Users/a&b/bin/wwd", Config: "/Users/a&b/c.toml", Log: "/Users/a&b/l.log", Path: "/opt/homebrew/bin:/usr/bin"})
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(plutil, "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("plutil -lint: %v: %s", err, out)
	}
}

func TestSystemdUnit(t *testing.T) {
	u := Unit(UnitOpts{User: "wake", Binary: "/home/wake/.local/share/wwd/bin/wwd", Config: "/home/wake/.config/wwd/config.toml", Path: "/usr/local/bin:/usr/bin"})
	for _, want := range []string{
		"[Unit]", "After=network-online.target tailscaled.service", "[Service]", "User=wake",
		"ExecStart=/home/wake/.local/share/wwd/bin/wwd -config /home/wake/.config/wwd/config.toml",
		"Restart=always", "RestartSec=", "MemoryMax=", "TasksMax=", "NoNewPrivileges=yes", "[Install]", "WantedBy=multi-user.target",
		"Environment=PATH=/usr/local/bin:/usr/bin",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit lacks %q:\n%s", want, u)
		}
	}
}

func TestInstallDarwinIsIdempotentAndUpgrades(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "binary v1")
	in, f, out := newInstaller(t, "darwin", "mlab", bin)
	ctx := context.Background()

	f.failOn["launchctl print"] = true // not loaded yet
	if err := in.Install(ctx); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		in.BinPath(), in.ConfigPath(), in.PlistPath(),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if b, _ := os.ReadFile(in.BinPath()); string(b) != "binary v1" {
		t.Error("binary not copied")
	}
	st, _ := os.Stat(in.BinPath())
	if st.Mode().Perm()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}
	if !f.has("launchctl bootstrap gui/501 "+in.PlistPath()) || f.has("launchctl kickstart") {
		t.Errorf("a first install must bootstrap, not restart: %v", f.calls)
	}
	if !strings.Contains(out.String(), "pool.token") {
		t.Error("the install must say what to do about the pool token")
	}

	// the same again: nothing changes, nothing is restarted
	f.calls = nil
	f.failOn["launchctl print"] = false
	if err := in.Install(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "launchctl bootstrap") || strings.HasPrefix(c, "launchctl kickstart") || strings.HasPrefix(c, "launchctl bootout") {
			t.Errorf("an unchanged install touched the service: %s", c)
		}
	}

	// a new binary: copied, and the loaded service restarted
	if err := os.WriteFile(bin, []byte("binary v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := in.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(in.BinPath()); string(b) != "binary v2" {
		t.Error("binary not upgraded")
	}
	if !f.has("launchctl kickstart -k gui/501/gs.wake.wwd") {
		t.Errorf("an upgrade must restart the loaded service: %v", f.calls)
	}

	// --no-restart copies but leaves the running service alone
	if err := os.WriteFile(bin, []byte("binary v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	in.NoRestart = true
	if err := in.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if f.has("launchctl kickstart") || f.has("launchctl bootstrap") {
		t.Errorf("NoRestart restarted: %v", f.calls)
	}
	if b, _ := os.ReadFile(in.BinPath()); string(b) != "binary v3" {
		t.Error("NoRestart must still install the binary")
	}
}

func TestInstallNeverOverwritesConfigOrToken(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	in, _, _ := newInstaller(t, "darwin", "mlab", bin)
	if err := os.MkdirAll(filepath.Dir(in.ConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.ConfigPath(), []byte("# mine\nalias = \"mlab\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.TokenPath(), []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(src, []byte("from-file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in.TokenFile = src
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(in.ConfigPath()); !strings.Contains(string(b), "# mine") {
		t.Error("the config was overwritten")
	}
	if b, _ := os.ReadFile(in.TokenPath()); string(b) != "existing\n" {
		t.Errorf("an existing pool token was replaced: %q", b)
	}
}

func TestTokenIsCopiedWith0600FromAFile(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	in, _, out := newInstaller(t, "darwin", "air26", bin)
	src := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(src, []byte("  tok-value-123 \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in.TokenFile = src
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(in.TokenPath())
	if err != nil || strings.TrimSpace(string(b)) != "tok-value-123" {
		t.Fatalf("%q %v", b, err)
	}
	st, _ := os.Stat(in.TokenPath())
	if st.Mode().Perm() != 0o600 {
		t.Errorf("token mode %v", st.Mode().Perm())
	}
	if strings.Contains(out.String(), "tok-value-123") {
		t.Error("the token value must never be printed")
	}
	// an empty token file is refused
	in2, _, _ := newInstaller(t, "darwin", "air26", bin)
	empty := filepath.Join(t.TempDir(), "e")
	_ = os.WriteFile(empty, []byte("\n"), 0o600)
	in2.TokenFile = empty
	if err := in2.Install(context.Background()); err == nil {
		t.Error("an empty token file was accepted")
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	in, f, out := newInstaller(t, "darwin", "mlab", bin)
	in.DryRun = true
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Errorf("dry run ran %v", f.calls)
	}
	if _, err := os.Stat(in.BinPath()); err == nil {
		t.Error("dry run wrote the binary")
	}
	if !strings.Contains(out.String(), "would") {
		t.Errorf("dry run should say what it would do:\n%s", out)
	}
}

func TestUninstallDarwin(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	in, f, _ := newInstaller(t, "darwin", "mlab", bin)
	ctx := context.Background()
	if err := in.Install(ctx); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if err := in.Uninstall(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !f.has("launchctl bootout gui/501/gs.wake.wwd") {
		t.Errorf("not unloaded: %v", f.calls)
	}
	if _, err := os.Stat(in.PlistPath()); err == nil {
		t.Error("plist left behind")
	}
	if _, err := os.Stat(in.BinPath()); err == nil {
		t.Error("binary left behind")
	}
	// config, token and data stay unless asked
	if _, err := os.Stat(in.ConfigPath()); err != nil {
		t.Error("the config was removed")
	}
	if err := in.Uninstall(ctx, false); err != nil { // twice is fine
		t.Fatal(err)
	}
	if err := in.Uninstall(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(in.ConfigPath()); err == nil {
		t.Error("purge left the config")
	}
}

func TestInstallLinuxUsesSudoSystemd(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	in, f, _ := newInstaller(t, "linux", "mserv", bin)
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range f.calls {
		switch {
		case strings.HasPrefix(c, "sudo install"):
			order = append(order, "unit")
		case strings.HasPrefix(c, "sudo systemctl daemon-reload"):
			order = append(order, "reload")
		case strings.HasPrefix(c, "sudo systemctl enable"):
			order = append(order, "enable")
		case strings.HasPrefix(c, "sudo systemctl restart"), strings.HasPrefix(c, "sudo systemctl start"):
			order = append(order, "start")
		}
	}
	if strings.Join(order, ",") != "unit,reload,enable,start" {
		t.Errorf("order %v in %v", order, f.calls)
	}
	if !f.has("sudo install -m 0644") {
		t.Errorf("the unit is installed 0644 by sudo install: %v", f.calls)
	}
	// nothing macOS about it
	for _, c := range f.calls {
		if strings.Contains(c, "launchctl") {
			t.Errorf("launchctl on Linux: %s", c)
		}
	}
	if _, err := os.Stat(in.PlistPath()); err == nil {
		t.Error("a plist was written on Linux")
	}

	// unchanged: the unit is compared with what is installed and nothing restarts
	f.calls = nil
	if err := in.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.has("sudo systemctl restart") || f.has("sudo install") {
		t.Errorf("an unchanged install touched the service: %v", f.calls)
	}
}

func TestMservNeedsNoTailscaleInterfaceTricks(t *testing.T) {
	// the config only names the addresses; binding is wwd's own job
	text, _ := ConfigFor("mserv")
	if !strings.Contains(text, "100.64.0.6") || !strings.Contains(text, "tailnet_addr") {
		t.Errorf("%s", text)
	}
}

func TestInstallRepairsAnEditedOrMissingUnit(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	in, f, _ := newInstaller(t, "linux", "mserv", bin)
	ctx := context.Background()
	if err := in.Install(ctx); err != nil {
		t.Fatal(err)
	}
	for name, damage := range map[string]func() error{
		"edited":  func() error { return os.WriteFile(in.UnitPath, []byte("[Unit]\n"), 0o644) },
		"removed": func() error { return os.Remove(in.UnitPath) },
	} {
		if err := damage(); err != nil {
			t.Fatal(err)
		}
		f.calls = nil
		if err := in.Install(ctx); err != nil {
			t.Fatal(err)
		}
		if !f.has("sudo install") || !f.has("sudo systemctl daemon-reload") {
			t.Errorf("%s unit was not repaired: %v", name, f.calls)
		}
	}
}

func TestUninstallReportsAFailedStop(t *testing.T) {
	bin := writeBin(t, t.TempDir(), "x")
	for _, goos := range []string{"darwin", "linux"} {
		in, f, _ := newInstaller(t, goos, "mserv", bin)
		ctx := context.Background()
		if err := in.Install(ctx); err != nil {
			t.Fatal(err)
		}
		f.failOn["launchctl bootout"] = true
		f.failOn["sudo systemctl disable"] = true
		if err := in.Uninstall(ctx, false); err == nil {
			t.Errorf("%s: a failed stop was reported as success", goos)
		}
		if _, err := os.Stat(in.BinPath()); err != nil {
			t.Errorf("%s: the binary was deleted under a service that is still there", goos)
		}
	}
}
