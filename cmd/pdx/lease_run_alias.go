package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/wake/purdex/internal/gate"
)

// `pdx lease run` is a deprecated alias of `pdx gate` (host-load-control plan G2, point 3): the host pool it used to ask the
// daemon for is the gate's slots now, which need no daemon. The old kinds map to the gate's two pools; the mode flags (--ask,
// --queue, --wait) and the client id and command text mean nothing to a local file lock and are read and ignored. The
// daemon-side lease and runLeaseRun go away with L2; until then nothing calls runLeaseRun.

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
		if _, msg = mf.mode(getenv); msg == "" {
			_, msg = checkLeaseSize(*kind, *weight, mf.wait.d, *clientID)
		}
	}
	if msg != "" {
		fmt.Fprintf(stderr, "pdx lease: %s\n%s\n", msg, leaseUsage)
		return ExitUsage
	}
	class := leaseGateClass(*kind, *weight)
	if !class.Valid() {
		fmt.Fprintf(stderr, "pdx lease run 已改為 pdx gate 的別名：--kind %s（打包）不經過閘門，直接執行；--ask／--queue／--wait 不再有作用\n", *kind)
		return gateExecDirect("", command, stderr)
	}
	fmt.Fprintf(stderr, "pdx lease run 已改為 pdx gate 的別名（%s）：--ask／--queue／--wait 不再有作用；請改用 pdx gate --class %s -- …\n", class, class)
	gateArgs := []string{"--class", string(class)}
	if *cfgPath != "" {
		gateArgs = append(gateArgs, "--config", *cfgPath)
	}
	gateArgs = append(gateArgs, "--")
	return gateRunMain(append(gateArgs, command...), stdout, stderr)
}
