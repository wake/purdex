package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/gate"
	ipeers "github.com/wake/purdex/internal/peers"
)

// `pdx lease run` is a deprecated alias of `pdx gate` (host-load-control plan G2, point 3): the host pool it used to ask the
// daemon for is the gate's slots now, which need no daemon. The old kinds map to the gate's two pools; the mode flags (--ask,
// --queue, --wait) and the client id and command text mean nothing to a local file lock and are read and ignored.

// leaseKindClass is the pool each built-in kind takes a slot from, or none (the command runs at once, with no slot); a kind it
// does not know (a custom one) is heavy, the side that waits rather than the side that runs unqueued.
var leaseKindClass = map[string]gate.Class{
	"test-full": gate.ClassHeavy,
	"build":     gate.ClassNone, // packaging (the deploy script builds the Purdex App this way) is never held back (user 10-15)
	"test-pkg":  gate.ClassLight,
	"lint-full": gate.ClassLight,
}

// leaseHeavyWeight is the weight from which an explicit --weight counts as heavy (the built-in full test and build are 35,
// a package test 15, a full lint 10).
const leaseHeavyWeight = 20

// leaseGateClass is the gate pool for what `pdx lease run` was asked for.
func leaseGateClass(kind string, weight int) gate.Class {
	if kind != "" {
		if c, ok := leaseKindClass[kind]; ok {
			return c
		}
		return gate.ClassHeavy
	}
	if weight > 0 && weight < leaseHeavyWeight {
		return gate.ClassLight
	}
	return gate.ClassHeavy
}

// Where the class of an aliased run came from, as the decision record says it.
const (
	classByClassifier = "classifier"
	classByKind       = "kind"
)

// leaseRunClass is the pool for a wrapped command: what the gate's own classifier says when it knows the command (a vitest
// run of named files is light, a full test run heavy, as for a command typed without the alias), and only when it does not
// (a script of the project's, a command the gate never holds) what --kind or --weight map to. A kind of build stays the one
// that is never held back, whatever the classifier thinks (user 10-15: packaging is not queued).
func leaseRunClass(kind string, weight int, command []string, cfgPath string) (gate.Class, string) {
	if kind == "build" {
		return leaseGateClass(kind, weight), classByKind
	}
	cfg, _ := gate.Load(gateConfigPath(cfgPath))
	if cfg.Enabled {
		if c := cfg.Intercept.Classify(command, gate.EnvOf(os.Environ(), gateStdinIsTTY())); c.Valid() {
			return c, classByClassifier
		}
	}
	return leaseGateClass(kind, weight), classByKind
}

// The ranges `pdx lease run` always checked its flags against (the daemon lease that defined them is gone).
const (
	leaseMaxExplicitWeight = 200
	leaseMaxWait           = 590 * time.Second
)

// splitRunArgs separates a run's own flags from the command: everything after the first "--" is the command.
func splitRunArgs(args []string) (flags, command []string, ok bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], len(args[i+1:]) > 0
		}
	}
	return args, nil, false
}

// checkLeaseSize checks the flags the alias still reads: exactly one of kind and weight, in range; a wait in range; a client id
// that is a lower-case UUID v4 (all of them ignored afterwards). It returns what is wrong, or "".
func checkLeaseSize(kind string, weight int, wait time.Duration, clientID string) string {
	switch {
	case (kind == "") == (weight == 0):
		return "give exactly one of --kind and --weight"
	case weight != 0 && (weight < 1 || weight > leaseMaxExplicitWeight):
		return fmt.Sprintf("--weight must be between 1 and %d", leaseMaxExplicitWeight)
	case wait < 0 || wait > leaseMaxWait:
		return fmt.Sprintf("--wait must be between 0 and %ds", int(leaseMaxWait/time.Second))
	case clientID != "" && !(ipeers.IsUUID(clientID) && clientID[14] == '4'):
		return "--client-id must be a lower-case UUID v4"
	}
	return ""
}

// leaseStdinIsTTY says whether stdin is a terminal (a seam for tests).
var leaseStdinIsTTY = func() bool {
	_, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil
}

// gateRunMain is gateMain (a seam for tests).
var gateRunMain = gateMain

// runLeaseRunAlias is `pdx lease run (--kind K | --weight N) [--ask|--queue|--wait D] -- <cmd…>` run by the gate.
func runLeaseRunAlias(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	flagArgs, command, hasCmd := splitRunArgs(args)
	fs := flag.NewFlagSet("pdx lease run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfgPath := fs.String("config", "", "")
	kind := fs.String("kind", "", "")
	weight := fs.Int("weight", 0, "")
	var mf modeFlags
	mf.register(fs)
	fs.String("command", "", "")
	clientID := fs.String("client-id", "", "")
	pos, err := parseTeamFlags(fs, normalizeWaitArgs(flagArgs))
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	case len(pos) != 0:
		msg = fmt.Sprintf("unexpected argument %q (the command goes after --)", pos[0])
	case !hasCmd:
		msg = "需要 -- 之後的指令"
	default:
		// the flags are still checked as `pdx lease run` checked them: exactly one of --kind and --weight, in range; a wait in
		// range; at most one mode flag (a call that was a usage error does not start running a command)
		if msg = mf.check(); msg == "" {
			msg = checkLeaseSize(*kind, *weight, mf.wait.d, *clientID)
		}
	}
	if msg != "" {
		fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
		return ExitUsage
	}
	class, source := leaseRunClass(*kind, *weight, command, *cfgPath)
	if !class.Valid() {
		fmt.Fprintf(stderr, "pdx lease run 已改為 pdx gate 的別名：--kind %s（打包）不經過閘門，直接執行；--ask／--queue／--wait 不再有作用\n", *kind)
		return gateExecDirect("", command, nil, stderr)
	}
	fmt.Fprintf(stderr, "pdx lease run 已改為 pdx gate 的別名（%s）：--ask／--queue／--wait 不再有作用；請改用 pdx gate --class %s -- …\n", class, class)
	gateArgs := []string{"--class", string(class), "--class-source", source}
	if *cfgPath != "" {
		gateArgs = append(gateArgs, "--config", *cfgPath)
	}
	gateArgs = append(gateArgs, "--")
	return gateRunMain(append(gateArgs, command...), stdout, stderr)
}
