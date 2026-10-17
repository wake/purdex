package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/wwd/facts"
)

// RunFunc runs a command (launchctl, sudo systemctl…). The default counts the
// fork through execstat.
type RunFunc func(ctx context.Context, tag, name string, args ...string) ([]byte, error)

func defaultRun(ctx context.Context, tag, name string, args ...string) ([]byte, error) {
	return execstat.CommandContext(ctx, tag, name, args...).CombinedOutput()
}

const systemUnitPath = "/etc/systemd/system/" + UnitName

// Installer installs, upgrades and removes wwd on this host. Every path is under
// Home except the systemd unit. Fields other than Run and Out have no defaults
// that guess: the caller says the host.
type Installer struct {
	Home   string
	GOOS   string // "darwin" or "linux"
	UID    int    // the user's uid, for launchctl's gui/<uid> domain
	User   string // the user the systemd service runs as
	Alias  string // which host's config template to start from
	Binary string // the wwd binary to install (the running one for `wwd install`)

	TokenFile string // a file whose content becomes pool.token, when none exists
	UnitPath  string // the systemd unit file; empty: /etc/systemd/system/wwd.service
	NoRestart bool   // install the files but leave the running service alone
	DryRun    bool   // say what would change, change nothing

	Run RunFunc   // nil: execstat-counted exec
	Out io.Writer // messages; nil: discarded
}

func (i *Installer) run() RunFunc {
	if i.Run != nil {
		return i.Run
	}
	return defaultRun
}

func (i *Installer) say(format string, a ...any) {
	if i.Out != nil {
		fmt.Fprintf(i.Out, format+"\n", a...)
	}
}

// Paths.
func (i *Installer) DataDir() string    { return filepath.Join(i.Home, ".local", "share", "wwd") }
func (i *Installer) BinPath() string    { return filepath.Join(i.DataDir(), "bin", "wwd") }
func (i *Installer) LogPath() string    { return filepath.Join(i.DataDir(), "wwd.log") }
func (i *Installer) ConfigDir() string  { return filepath.Join(i.Home, ".config", "wwd") }
func (i *Installer) ConfigPath() string { return filepath.Join(i.ConfigDir(), "config.toml") }
func (i *Installer) TokenPath() string  { return filepath.Join(i.ConfigDir(), "pool.token") }
func (i *Installer) PlistPath() string {
	return filepath.Join(i.Home, "Library", "LaunchAgents", Label+".plist")
}
func (i *Installer) unitPath() string {
	if i.UnitPath != "" {
		return i.UnitPath
	}
	return systemUnitPath
}
func (i *Installer) unitStamp() string { return filepath.Join(i.DataDir(), "unit.sha256") }

func (i *Installer) domain() string { return "gui/" + strconv.Itoa(i.UID) }

func (i *Installer) toolPath() string {
	return strings.Join(facts.DefaultToolDirs(i.Home, "/usr/bin:/bin:/usr/sbin:/sbin"), ":")
}

func fileHash(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return hashBytes(b), true
}

func hashBytes(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// writeAtomic writes data to path through a temporary file in the same
// directory and a rename, so a reader never sees half a file.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wwd-install-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Install is idempotent: it changes only what differs, restarts the service only
// when the binary or the service definition changed (or it is not running), and
// never overwrites an existing config or pool token.
func (i *Installer) Install(ctx context.Context) error {
	if i.GOOS != "darwin" && i.GOOS != "linux" {
		return fmt.Errorf("install: no service support for %s", i.GOOS)
	}
	config, err := ConfigFor(i.Alias)
	if err != nil {
		return err
	}
	bin, err := os.ReadFile(i.Binary)
	if err != nil {
		return fmt.Errorf("install: the binary to install: %w", err)
	}
	binChanged := false
	if cur, ok := fileHash(i.BinPath()); !ok || cur != hashBytes(bin) {
		binChanged = true
		i.step("install the binary to %s", i.BinPath())
		if !i.DryRun {
			if err := writeAtomic(i.BinPath(), bin, 0o755); err != nil {
				return err
			}
		}
	}

	if _, err := os.Stat(i.ConfigPath()); errors.Is(err, os.ErrNotExist) {
		i.step("write the starting config %s for %s", i.ConfigPath(), i.Alias)
		if !i.DryRun {
			if err := writeAtomic(i.ConfigPath(), []byte(config), 0o600); err != nil {
				return err
			}
		}
	}

	tokenMissing := false
	if _, err := os.Stat(i.TokenPath()); errors.Is(err, os.ErrNotExist) {
		if i.TokenFile != "" {
			tok, err := os.ReadFile(i.TokenFile)
			if err != nil {
				return fmt.Errorf("install: token file: %w", err)
			}
			t := strings.TrimSpace(string(tok))
			if t == "" {
				return errors.New("install: the token file is empty")
			}
			i.step("copy the pool token into %s (0600)", i.TokenPath())
			if !i.DryRun {
				if err := writeAtomic(i.TokenPath(), []byte(t+"\n"), 0o600); err != nil {
					return err
				}
			}
		} else {
			tokenMissing = true
		}
	}

	var changed, defChanged bool
	switch i.GOOS {
	case "darwin":
		defChanged, err = i.writePlist()
	case "linux":
		defChanged, err = i.writeUnit(ctx)
	}
	if err != nil {
		return err
	}
	changed = binChanged || defChanged

	if i.DryRun {
		return nil
	}
	if i.NoRestart {
		if changed {
			i.say("installed; the running service was not touched (--no-restart). Restart it when it is safe: %s", i.restartHint())
		}
	} else if err := i.activate(ctx, binChanged, defChanged); err != nil {
		return err
	}
	if tokenMissing {
		i.say("note: %s does not exist yet. wwd makes a random token on its first start, but every host of the pool must hold the SAME token: copy one host's pool.token (0600) to the others, or run `wwd install --token-file <file>` before the first start.", i.TokenPath())
	}
	return nil
}

func (i *Installer) step(format string, a ...any) {
	if i.DryRun {
		i.say("would "+format, a...)
		return
	}
	i.say(format, a...)
}

func (i *Installer) restartHint() string {
	if i.GOOS == "darwin" {
		return fmt.Sprintf("launchctl kickstart -k %s/%s", i.domain(), Label)
	}
	return "sudo systemctl restart " + UnitName
}

func (i *Installer) writePlist() (changed bool, err error) {
	plist := Plist(PlistOpts{Label: Label, Binary: i.BinPath(), Config: i.ConfigPath(), Log: i.LogPath(), Path: i.toolPath()})
	if cur, ok := fileHash(i.PlistPath()); ok && cur == hashBytes([]byte(plist)) {
		return false, nil
	}
	i.step("write the LaunchAgent %s", i.PlistPath())
	if i.DryRun {
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(i.LogPath()), 0o755); err != nil {
		return false, err
	}
	return true, writeAtomic(i.PlistPath(), []byte(plist), 0o644)
}

func (i *Installer) writeUnit(ctx context.Context) (changed bool, err error) {
	unit := Unit(UnitOpts{User: i.User, Binary: i.BinPath(), Config: i.ConfigPath(), Path: i.toolPath()})
	h := hashBytes([]byte(unit))
	// the stamp alone is not proof: the unit may have been edited or removed since
	if b, err := os.ReadFile(i.unitPath()); err == nil && hashBytes(b) == h {
		return false, nil
	}
	i.step("install the systemd unit %s (sudo)", i.unitPath())
	if i.DryRun {
		return true, nil
	}
	tmp := filepath.Join(i.DataDir(), "wwd.service.new")
	if err := writeAtomic(tmp, []byte(unit), 0o644); err != nil {
		return false, err
	}
	defer os.Remove(tmp)
	if out, err := i.run()(ctx, "wwd.install.unit", "sudo", "install", "-m", "0644", tmp, i.unitPath()); err != nil {
		return false, fmt.Errorf("install the unit: %w: %s", err, bytes.TrimSpace(out))
	}
	// the stamp says which unit text is installed; written only after sudo succeeded
	return true, writeAtomic(i.unitStamp(), []byte(h+"\n"), 0o644)
}

// activate starts or restarts the service as far as the change needs it.
func (i *Installer) activate(ctx context.Context, binChanged, defChanged bool) error {
	run := i.run()
	do := func(tag, name string, args ...string) error {
		if out, err := run(ctx, tag, name, args...); err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out))
		}
		return nil
	}
	if i.GOOS == "darwin" {
		target := i.domain() + "/" + Label
		_, perr := run(ctx, "wwd.install.print", "launchctl", "print", target)
		loaded := perr == nil
		switch {
		case !loaded:
			i.say("load the LaunchAgent")
			return do("wwd.install.bootstrap", "launchctl", "bootstrap", i.domain(), i.PlistPath())
		case defChanged:
			i.say("reload the LaunchAgent")
			_ = do("wwd.install.bootout", "launchctl", "bootout", target) // already gone is fine
			return do("wwd.install.bootstrap", "launchctl", "bootstrap", i.domain(), i.PlistPath())
		case binChanged:
			i.say("restart wwd on the new binary")
			return do("wwd.install.kickstart", "launchctl", "kickstart", "-k", target)
		}
		return nil
	}
	switch {
	case defChanged:
		i.say("enable and start the service")
		if err := do("wwd.install.reload", "sudo", "systemctl", "daemon-reload"); err != nil {
			return err
		}
		if err := do("wwd.install.enable", "sudo", "systemctl", "enable", UnitName); err != nil {
			return err
		}
		return do("wwd.install.restart", "sudo", "systemctl", "restart", UnitName)
	case binChanged:
		i.say("restart wwd on the new binary")
		return do("wwd.install.restart", "sudo", "systemctl", "restart", UnitName)
	}
	if _, err := run(ctx, "wwd.install.active", "systemctl", "is-active", "--quiet", UnitName); err != nil {
		i.say("start the service (it was not running)")
		return do("wwd.install.start", "sudo", "systemctl", "start", UnitName)
	}
	return nil
}

// Uninstall stops the service and removes the service definition and the
// binary. The config, the pool token and the data (wwd.db, logs) stay unless
// purge is set. Running it twice is fine.
func (i *Installer) Uninstall(ctx context.Context, purge bool) error {
	run := i.run()
	switch i.GOOS {
	case "darwin":
		target := i.domain() + "/" + Label
		// only a loaded agent is booted out, so a bootout failure is a real one
		if _, perr := run(ctx, "wwd.uninstall.print", "launchctl", "print", target); perr == nil {
			if out, err := run(ctx, "wwd.uninstall.bootout", "launchctl", "bootout", target); err != nil {
				return fmt.Errorf("launchctl bootout %s: %w: %s", target, err, bytes.TrimSpace(out))
			}
		}
		if err := os.Remove(i.PlistPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	case "linux":
		if _, err := os.Stat(i.unitPath()); err == nil {
			for _, c := range [][]string{
				{"wwd.uninstall.disable", "sudo", "systemctl", "disable", "--now", UnitName},
				{"wwd.uninstall.rm", "sudo", "rm", "-f", i.unitPath()},
				{"wwd.uninstall.reload", "sudo", "systemctl", "daemon-reload"},
			} {
				if out, err := run(ctx, c[0], c[1], c[2:]...); err != nil {
					return fmt.Errorf("%s: %w: %s", strings.Join(c[1:], " "), err, bytes.TrimSpace(out))
				}
			}
		}
		_ = os.Remove(i.unitStamp())
	default:
		return fmt.Errorf("uninstall: no service support for %s", i.GOOS)
	}
	if err := os.RemoveAll(filepath.Dir(i.BinPath())); err != nil {
		return err
	}
	if purge {
		for _, d := range []string{i.ConfigDir(), i.DataDir()} {
			if err := os.RemoveAll(d); err != nil {
				return err
			}
		}
	}
	return nil
}
