// Package testcmd is `pdx test` (spec §8.1): it runs a declared task of the repo's manifest, on the local fast lane when the
// task allows it and through the queue otherwise, waits for the result, and prints the summary. The command in cmd/pdx is a
// thin wrapper; everything that decides something lives here so it can be tested against a fake wwd.
package testcmd

import (
	"fmt"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Exit codes (spec §8.1). A job's own exit code is the command's exit code; these are the ones `pdx test` adds.
const (
	ExitOK        = 0
	ExitFailed    = 1   // the job failed without an exit code of its own (infrastructure, lost, oom)
	ExitNoWWD     = 3   // wwd is not reachable
	ExitUsage     = 4   // the manifest, the task or an argument is wrong
	ExitNoHost    = 5   // no host can ever take the job (its labels)
	ExitExpired   = 6   // it waited in the queue past its limit and nothing ran
	ExitTimeout   = 124 // the command ran out of time
	ExitCancelled = 130 // cancelled, superseded, or the wait was interrupted
)

const Usage = `usage: pdx test <task> [args…] [--class dev|merge|release] [--detach] [--json]
       pdx test --adhoc [--json] -- <command> [args…]
       pdx test --list [--json]
  runs a task declared in .wwd/tasks.toml of this repo and prints its summary; the exit code is the job's own exit code.
  124 timeout · 130 cancelled · 3 wwd unreachable · 4 manifest or argument error · 5 no host can take it · 6 expired in the queue
  A command that itself exits with 3–6 or 124+ is reported as 1 (the report's "exit" keeps its real number).
  Ctrl-C ends the wait for a queued job (the job keeps running); it stops a local fast-lane run, which has no life of its own.
  --class   lower the priority (never above the task's own)   --detach   submit and print the job id, do not wait
  --json    print the report as one JSON object              --list     the tasks of this repo
  --adhoc   run a command that is not a declared task: here only, in this working tree, under a heavy slot, never through a
            shell, recorded as undeclared; it is not queued, so --detach and --class are refused
  A task's arguments are checked against the manifest (files must exist, packages must be package patterns); put -- before an
  argument that starts with a dash.
`

// Args is a parsed command line.
type Args struct {
	Task     string
	TaskArgs []string
	Class    wire.JobClass
	Detach   bool
	JSON     bool
	List     bool
	// AdHoc: an undeclared command (spec §8.3); Task and TaskArgs together are its argv.
	AdHoc bool
}

// Argv is the command of an ad-hoc call.
func (a Args) Argv() []string { return append([]string{a.Task}, a.TaskArgs...) }

// ParseArgs reads the arguments after `pdx test`. Flags may stand anywhere before `--`; the first word that is not a flag is the
// task, the rest are its arguments. A word that looks like an unknown flag is an argument (the task's own validation refuses
// it), so a mistyped flag is an error of the task, not a silent change of meaning.
func ParseArgs(argv []string) (Args, error) {
	var a Args
	rest := false
	before := 0 // words before `--`
	for i := 0; i < len(argv); i++ {
		w := argv[i]
		if rest {
			a.add(w)
			continue
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			before++
		}
		switch {
		case w == "--":
			rest = true
		case w == "-h" || w == "--help":
			return a, fmt.Errorf("help")
		case w == "--detach":
			a.Detach = true
		case w == "--json":
			a.JSON = true
		case w == "--list":
			a.List = true
		case w == "--adhoc":
			a.AdHoc = true
		case w == "--class" || strings.HasPrefix(w, "--class="):
			v := strings.TrimPrefix(w, "--class=")
			if w == "--class" {
				if i+1 >= len(argv) {
					return a, fmt.Errorf("--class needs dev, merge or release")
				}
				i++
				v = argv[i]
			}
			a.Class = wire.JobClass(v)
			if !a.Class.Valid() {
				return a, fmt.Errorf("--class must be dev, merge or release, got %q", v)
			}
		default:
			a.add(w)
		}
	}
	switch {
	case a.AdHoc && (a.List || a.Detach || a.Class != "" || before > 0 || !rest || a.Task == ""):
		return a, fmt.Errorf("--adhoc runs one command after --: pdx test --adhoc [--json] -- <command> [args…] (no task, --detach, --class or --list)")
	case a.List && (a.Task != "" || len(a.TaskArgs) > 0 || a.Detach || a.Class != ""):
		return a, fmt.Errorf("--list takes no task and no other option than --json")
	case !a.List && a.Task == "":
		return a, fmt.Errorf("a task name is needed (pdx test --list shows them)")
	}
	return a, nil
}

func (a *Args) add(w string) {
	if a.Task == "" && !a.List {
		a.Task = w
		return
	}
	a.TaskArgs = append(a.TaskArgs, w)
}
