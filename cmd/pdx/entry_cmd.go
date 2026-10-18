package main

// `pdx entry match` (wwd spec §8.2): what the hook asks. It reads the repo's task manifest and says what to do with one test or
// build command; the rules live in internal/wwd/entry, the hook keeps no copy. Not for people: it is not in the help.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/wake/purdex/internal/wwd/entry"
)

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
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "pdx entry match: %s is not a directory\n", cwd)
		return 2
	}
	b, err := json.Marshal(entry.Match(cwd, cmd))
	if err != nil {
		fmt.Fprintf(stderr, "pdx entry match: %v\n", err)
		return 2
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
