package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/team"
)

// `pdx relay lock|unlock <op> --session <sid>` (spec §6.6 "Who writes the flag", plan v3 P6-3b): the Purdex mod raises
// the relay lock's flag file for the turn that writes the handoff and lowers it before /clear. Both are LOCAL file
// operations on team.HookLockPath(<data_dir>, "cc", <sid>) with the op id as the content — no daemon call, so they
// work across a daemon restart. unlock is a compare-and-remove by op id: it never lowers a flag another request
// (a lead's, or another op) raised.

// relayOpIDPattern is what an op id may be here: a single path element's worth of plain characters (the id is a UUID).
var relayOpIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`)

// runRelayLock returns 0 when done (an unlock that found no flag, or someone else's, is done), 1 for an I/O error and
// 2 for a usage error.
func runRelayLock(args []string, stdout, stderr io.Writer, lock bool) int {
	if len(args) < 1 {
		return relayUsageErr(stderr, "需要 <op>")
	}
	opID := args[0]
	fs := flag.NewFlagSet("pdx relay lock", flag.ContinueOnError)
	var sid string
	fs.StringVar(&sid, "session", "", "")
	cfgPath, ok := relayFlags(fs, args[1:], stderr)
	if !ok {
		return ExitUsage
	}
	if fs.NArg() != 0 || strings.TrimSpace(sid) == "" {
		return relayUsageErr(stderr, "需要 <op> 與 --session")
	}
	if !relayOpIDPattern.MatchString(opID) {
		return relayUsageErr(stderr, fmt.Sprintf("bad op id %q", opID))
	}
	if strings.ContainsAny(sid, "/\\\x00") || sid == "." || sid == ".." {
		return relayUsageErr(stderr, fmt.Sprintf("bad session id %q", sid))
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "pdx relay: %v\n", err)
		return ExitError
	}
	path := team.HookLockPath(cfg.DataDir, team.HookAgentCC, sid)
	if path == "" {
		fmt.Fprintln(stderr, "pdx relay: no data dir to hold the lock flag")
		return ExitError
	}
	if !lock {
		team.RemoveHookLock(path, opID)
		return ExitOK
	}
	if err := raiseRelayLock(path, opID); err != nil {
		fmt.Fprintf(stderr, "pdx relay: cannot raise the relay lock: %v\n", err)
		return ExitError
	}
	return ExitOK
}

// raiseRelayLock writes opID into the flag at path under the flag's flock, as WriteHookLock does, but reports an error:
// the mod needs to know whether the lock went up.
func raiseRelayLock(path, opID string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := team.OpenHookLockLocked(path, true)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteAt([]byte(opID), 0)
	return err
}
