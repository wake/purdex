package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"runtime"

	"github.com/wake/purdex/internal/wwd/install"
)

type installOpts struct {
	alias, tokenFile  string
	noRestart, dryRun bool
}

func parseInstallArgs(args []string) (installOpts, error) {
	var o installOpts
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.alias, "alias", "", "this host: mlab, air26, mserv or air19")
	fs.StringVar(&o.tokenFile, "token-file", "", "a file whose content becomes pool.token when none exists yet")
	fs.BoolVar(&o.noRestart, "no-restart", false, "install the files but leave the running service alone")
	fs.BoolVar(&o.dryRun, "dry-run", false, "say what would change, change nothing")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.alias == "" {
		return o, fmt.Errorf("--alias is required (mlab, air26, mserv or air19): nothing guesses which host this is")
	}
	if _, err := install.ConfigFor(o.alias); err != nil {
		return o, err
	}
	return o, nil
}

func parseUninstallArgs(args []string) (purge bool, err error) {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&purge, "purge", false, "also remove the config, the pool token and the data (wwd.db, logs)")
	return purge, fs.Parse(args)
}

// installer is the Installer for this process: the running binary is what gets
// installed.
func installer(alias string) (*install.Installer, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return &install.Installer{
		Home: home, GOOS: runtime.GOOS, UID: os.Getuid(), User: name, Alias: alias, Binary: self, Out: os.Stdout,
	}, nil
}

// runInstallCmd handles `wwd install` and `wwd uninstall`; it reports whether
// args named one of them.
func runInstallCmd(args []string) (handled bool, err error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "install":
		o, err := parseInstallArgs(args[1:])
		if err != nil {
			return true, err
		}
		in, err := installer(o.alias)
		if err != nil {
			return true, err
		}
		in.TokenFile, in.NoRestart, in.DryRun = o.tokenFile, o.noRestart, o.dryRun
		return true, in.Install(context.Background())
	case "uninstall":
		purge, err := parseUninstallArgs(args[1:])
		if err != nil {
			return true, err
		}
		in, err := installer("mlab") // the alias only picks a config template; uninstall does not write one
		if err != nil {
			return true, err
		}
		return true, in.Uninstall(context.Background(), purge)
	}
	return false, nil
}
