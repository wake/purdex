package main

// `pdx entry match` (wwd spec §8.2): what the hook asks. It reads the repo's task manifest and says what to do with one test or
// build command; the rules live in internal/wwd/entry, the hook keeps no copy. Not for people: it is not in the help.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/wake/purdex/internal/wwd/client"
	"github.com/wake/purdex/internal/wwd/entry"
	"github.com/wake/purdex/internal/wwd/wire"
)

// entryPoolTimeout bounds the read of the local wwd's pool (the hook's own wait for this command is longer; a wwd that does not answer in
// time is "cannot be read" and the command runs as typed).
const entryPoolTimeout = 2 * time.Second

// entryPool reads the pool from the local wwd; a variable so tests can stand in for it.
var entryPool = func(ctx context.Context) (wire.Pool, error) {
	return client.New(client.DefaultSocket()).Pool(ctx)
}

const entryUsage = "usage: pdx entry match --json [--cwd <dir>] -- <command> [args…]\n"

func runEntry(args []string) {
	os.Exit(entryMain(args, os.Stdout, os.Stderr))
}

// entryMain is runEntry with its streams, for tests; it returns the exit code: 0 with an answer (whatever it says), 2 for a bad
// command line.
func entryMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "match" {
		fmt.Fprint(stderr, "pdx entry: needs a subcommand (match)\n"+entryUsage)
		return 2
	}
	var cwd string
	asJSON := false
	rest := args[1:]
	var cmd []string
loop:
	for len(rest) > 0 {
		switch w := rest[0]; {
		case w == "--":
			cmd = rest[1:]
			break loop
		case w == "--json":
			asJSON = true
			rest = rest[1:]
		case w == "--cwd" && len(rest) > 1:
			cwd = rest[1]
			rest = rest[2:]
		default:
			fmt.Fprintf(stderr, "pdx entry match: unexpected %q before --\n%s", w, entryUsage)
			return 2
		}
	}
	if !asJSON || len(cmd) == 0 {
		fmt.Fprint(stderr, "pdx entry match: needs --json and a command after --\n"+entryUsage)
		return 2
	}
	if cwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "pdx entry match: %v\n", err)
			return 2
		}
		cwd = wd
	}
	if abs, err := filepath.Abs(cwd); err == nil { // the manifest is looked for above the directory: a relative path would stop at "."
		cwd = abs
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "pdx entry match: %s is not a directory\n", cwd)
		return 2
	}
	res := entry.Match(cwd, cmd)
	if entry.NeedsPool(res) { // a rewrite through the queue needs a host that can take it (the local fast lane and an ad-hoc job do not)
		ctx, cancel := context.WithTimeout(context.Background(), entryPoolTimeout)
		pool, perr := entryPool(ctx)
		cancel()
		res = entry.GateOnPool(res, pool, perr)
	}
	b, err := json.Marshal(res)
	if err != nil {
		fmt.Fprintf(stderr, "pdx entry match: %v\n", err)
		return 2
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
