package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wake/purdex/internal/gate"
)

// `pdx gate conflict` (spec §5.3, #3058): two kinds of command that do not run together. A rule belongs to one class and names two
// patterns, the start of a command's words; a waiting command that would run beside a holder (or a command ahead of it) the rule keeps
// apart waits until that one ends. Default: no rules. The rules live in the gate directory of the host the command runs on.

const gateConflictUsage = `usage: pdx gate conflict [--json] [--config <file>]
       pdx gate conflict add --class heavy|light "<pattern a>" "<pattern b>" --reason "<one line>" [--config <file>]
       pdx gate conflict remove <n> [--config <file>]
       pdx gate conflict remove --class heavy|light "<pattern a>" "<pattern b>" [--config <file>]
  A pattern is the start of a command's words: "tsc -b" matches tsc -b --force, not tsc --force -b; "vitest related" matches
  vitest related --run a.ts. A rule may pair a pattern with itself. A running command is never touched. The reason is required.
`

func gateConflict(args []string, stdout, stderr io.Writer) int {
	var (
		sub                    string
		pos                    []string
		class, reason, cfgPath string
		asJSON                 bool
		haveClass, haveReason  bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasVal := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-"), "=")
		isFlag := strings.HasPrefix(a, "-") && len(a) > 1 && !isNumber(a)
		value := func() (string, bool) {
			if hasVal {
				return val, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case !isFlag && sub == "" && (a == "add" || a == "remove"):
			sub = a
		case !isFlag:
			pos = append(pos, a)
		case name == "json":
			asJSON = true
		case name == "class" || name == "reason" || name == "config":
			v, ok := value()
			if !ok {
				fmt.Fprintf(stderr, "pdx gate conflict: --%s needs a value\n", name)
				return 2
			}
			switch name {
			case "class":
				class, haveClass = v, true
			case "reason":
				reason, haveReason = v, true
			default:
				cfgPath = v
			}
		default:
			fmt.Fprint(stderr, gateConflictUsage)
			return 2
		}
	}
	g := gate.New(loadGateConfig(cfgPath, stderr))
	switch sub {
	case "":
		if len(pos) > 0 || haveClass || haveReason {
			fmt.Fprint(stderr, gateConflictUsage)
			return 2
		}
		return gateConflictList(g, asJSON, stdout)
	case "add":
		if asJSON || len(pos) != 2 || !haveClass || !haveReason {
			fmt.Fprint(stderr, gateConflictUsage)
			return 2
		}
		msg, _, err := g.AddConflict(gate.Class(class), pos[0], pos[1], reason)
		return conflictResult(msg, err, stdout, stderr)
	default: // remove
		if asJSON || haveReason {
			fmt.Fprint(stderr, gateConflictUsage)
			return 2
		}
		switch {
		case len(pos) == 1 && !haveClass:
			n, err := strconv.Atoi(pos[0])
			if err != nil {
				fmt.Fprint(stderr, gateConflictUsage)
				return 2
			}
			msg, _, err := g.RemoveConflictAt(n)
			return conflictResult(msg, err, stdout, stderr)
		case len(pos) == 2 && haveClass:
			msg, _, err := g.RemoveConflictPair(gate.Class(class), pos[0], pos[1])
			return conflictResult(msg, err, stdout, stderr)
		}
		fmt.Fprint(stderr, gateConflictUsage)
		return 2
	}
}

func isNumber(s string) bool { _, err := strconv.Atoi(s); return err == nil }

// conflictResult prints what an edit found and left; an edit that did not write because there was nothing to do is not a failure
// (it says so); a refused one (a bad rule, a broken file) is an error.
func conflictResult(msg string, err error, stdout, stderr io.Writer) int {
	switch {
	case errors.Is(err, gate.ErrConflictsBroken):
		fmt.Fprintf(stderr, "pdx gate conflict: %v\n", err)
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "pdx gate conflict: %v\n", err)
		return 2
	}
	fmt.Fprintln(stdout, msg)
	return 0
}

func gateConflictList(g *gate.Gate, asJSON bool, stdout io.Writer) int {
	rules, problem := g.Conflicts()
	if rules == nil {
		rules = []gate.Conflict{}
	}
	if asJSON {
		b, _ := json.MarshalIndent(map[string]any{"rules": rules, "error": problem}, "", "  ")
		fmt.Fprintln(stdout, string(b))
		if problem != "" {
			return 1
		}
		return 0
	}
	if problem != "" {
		fmt.Fprintln(stdout, "conflicts.json cannot be used (no rule is in force): "+problem)
		return 1
	}
	if len(rules) == 0 {
		fmt.Fprintln(stdout, "no conflict rules")
		return 0
	}
	for i, r := range rules {
		fmt.Fprintln(stdout, gate.ConflictLine(i+1, r))
	}
	return 0
}
