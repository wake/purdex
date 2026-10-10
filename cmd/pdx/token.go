package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wake/purdex/internal/config"
)

func runToken(args []string) {
	// Extract subcommand ("generate") from args, pass the rest to flag parsing.
	// This allows both `pdx token generate --config X` and `pdx token --config X generate`.
	var subcmd string
	var flagArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "generate" {
			subcmd = args[i]
		} else {
			flagArgs = append(flagArgs, args[i])
		}
	}

	if subcmd != "generate" {
		fmt.Fprintf(os.Stderr, "Usage: pdx token generate [--config <path>] [--force]\n")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("token", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config.toml (default: ~/.config/pdx/config.toml)")
	force := fs.Bool("force", false, "write the token even though a daemon is running on this config")
	fs.Parse(flagArgs)

	// Resolve config path
	path := *cfgPath
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "token: cannot determine home directory: %v\n", err)
			os.Exit(1)
		}
		path = filepath.Join(home, ".config", "pdx", "config.toml")
	}

	if code := tokenGenerate(path, *force, isTerminal(os.Stdout), os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// tokenGenerate writes a new token into the config at path and returns the exit code. A daemon running on that config keeps its
// token in memory, so rewriting the file behind it leaves every client that holds the old token — and every pdx call on the host
// — answering 401 (incident 2026-10-11, #2557): without force it is refused and the file is not touched. Whether a daemon runs
// on THIS file is read from the file's run lock (#2562), which no daemon of another path, data_dir or spelling holds, inside the
// lock a starting daemon also takes before it reads the file — so one cannot start between the check and the write. A daemon
// older than the run lock is still found by its pid file. The plaintext token goes to a terminal only; to a log or a pipe goes
// its fingerprint (#1950).
func tokenGenerate(path string, force, tty bool, out, errw io.Writer) int {
	var (
		running bool
		pid     int
		token   string
	)
	refused := errors.New("refused")
	err := config.WithLock(path, func(locked string) error {
		cfg, err := config.Load(locked)
		if err != nil {
			return err
		}
		held, herr := config.RunLockHeld(locked)
		if herr != nil {
			fmt.Fprintf(errw, "token: cannot tell whether a daemon uses %s (%v); continuing on its pid file\n", locked, herr)
		}
		oldRunning, oldPid := isDaemonRunning(filepath.Join(cfg.DataDir, "pdx.pid"))
		running, pid = held || oldRunning, oldPid
		if running && !force {
			who := "a daemon"
			if pid > 0 {
				who = fmt.Sprintf("a daemon (pid %d)", pid)
			}
			fmt.Fprintf(errw, "token: %s is running on %s and keeps its token in memory; a new token in the file would "+
				"lock out the daemon's own clients (every pdx call would answer 401). Nothing was changed. "+
				"Pass --force to write it anyway.\n", who, path)
			return refused
		}

		// Generate 20-byte random token → "pdx_" + 40 hex chars (160-bit entropy)
		raw := make([]byte, 20)
		if _, err := rand.Read(raw); err != nil {
			return fmt.Errorf("failed to generate random bytes: %w", err)
		}
		token = "pdx_" + hex.EncodeToString(raw)
		// One line of the file changes (config.SetTokenHeld); every other setting, comment and table stays.
		return config.SetTokenHeld(locked, token)
	})
	switch {
	case errors.Is(err, refused):
		return 1
	case err != nil:
		fmt.Fprintf(errw, "token: %v\n", err)
		return 1
	}

	if tty {
		fmt.Fprintf(out, "Token: %s\n", token)
	} else {
		fmt.Fprintf(out, "Token generated (fingerprint %s)\n", config.TokenFingerprint(token))
	}
	fmt.Fprintf(out, "Saved to: %s\n", path)
	if running {
		who := "the daemon"
		if pid > 0 {
			who = fmt.Sprintf("the daemon (pid %d)", pid)
		}
		fmt.Fprintf(errw, "token: --force: %s still holds the old token until you restart it, and every client "+
			"(the App, phones, other hosts) must re-pair with the new token; they are refused with 401 until both are done.\n", who)
	}
	return 0
}
