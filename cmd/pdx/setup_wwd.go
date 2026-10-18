package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wake/purdex/internal/wwd/install"
)

// wwdSetupOpts are the `pdx setup --wwd…` flags: installing, upgrading or
// removing wwd, the per-host watcher/worker daemon (spec 2026-10-18 §2). It is a
// different job from installing agent hooks and cannot be mixed with it.
type wwdSetupOpts struct {
	alias, binary, tokenFile string
	noRestart, dryRun        bool
	remove, purge            bool
}

// parseSetupWWD reads the wwd flags out of the setup arguments; ok is false when
// there are none (the arguments are for the agent setup).
func parseSetupWWD(args []string) (o wwdSetupOpts, ok bool, err error) {
	hasAgent := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--wwd":
			ok = true
		case "--wwd-remove":
			ok, o.remove = true, true
		case "--wwd-purge":
			ok, o.purge = true, true
		case "--wwd-alias", "--wwd-binary", "--token-file":
			ok = ok || args[i] != "--token-file" // --token-file alone is not enough to be a wwd job
			if i+1 >= len(args) {
				return o, true, fmt.Errorf("%s needs a value", args[i])
			}
			switch args[i] {
			case "--wwd-alias":
				o.alias = args[i+1]
			case "--wwd-binary":
				o.binary = args[i+1]
			default:
				o.tokenFile = args[i+1]
			}
			i++
		case "--no-restart":
			o.noRestart = true
		case "--dry-run":
			o.dryRun = true
		case "--agent":
			hasAgent = true
			i++
		}
	}
	if !ok {
		return wwdSetupOpts{}, false, nil
	}
	if hasAgent {
		return o, true, errors.New("setup: --wwd… and --agent are two different jobs: run them one at a time")
	}
	if !o.remove {
		if o.alias == "" {
			return o, true, errors.New("setup: --wwd needs --wwd-alias (mlab, air26, mserv or air19): nothing guesses which host this is")
		}
		if _, err := install.ConfigFor(o.alias); err != nil {
			return o, true, fmt.Errorf("setup: %w", err)
		}
	}
	return o, true, nil
}

// defaultWWDBinary is the wwd that sits beside this pdx (they are built together).
func defaultWWDBinary(pdxPath string) (string, error) {
	p := filepath.Join(filepath.Dir(pdxPath), "wwd")
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", fmt.Errorf("setup: no wwd beside %s: pass --wwd-binary <path> (build it with `make build-wwd`)", pdxPath)
	}
	return p, nil
}

// runSetupWWD installs (or removes) wwd with the Installer in. in carries the host
// (home, uid, user); the options carry what the command line chose.
func runSetupWWD(ctx context.Context, in *install.Installer, o wwdSetupOpts) error {
	if o.remove {
		return in.Uninstall(ctx, o.purge)
	}
	in.Alias, in.Binary, in.TokenFile, in.NoRestart, in.DryRun = o.alias, o.binary, o.tokenFile, o.noRestart, o.dryRun
	return in.Install(ctx)
}

// setupWWDForUser is the `pdx setup --wwd…` entry: the Installer for this user and host.
func setupWWDForUser(o wwdSetupOpts) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if o.binary == "" && !o.remove {
		pdx, err := thisPdx()
		if err != nil {
			return err
		}
		if o.binary, err = defaultWWDBinary(pdx); err != nil {
			return err
		}
	}
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	in := &install.Installer{Home: home, GOOS: runtime.GOOS, UID: os.Getuid(), User: name, Out: os.Stdout}
	if err := runSetupWWD(context.Background(), in, o); err != nil {
		return fmt.Errorf("setup: %s", strings.TrimPrefix(err.Error(), "setup: "))
	}
	return nil
}
