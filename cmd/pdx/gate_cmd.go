package main

// `pdx gate` (host-load-control spec §5): run a heavy command under a host-wide slot that is a local file lock, so the
// control works with the daemon down. `pdx gate status` reads the slot records straight from the gate directory.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/gate/shim"
)

const gateUsage = `usage: pdx gate [--real <binary>] [--tool <name>] [--class heavy|light] [--auto] [--fg] [--wait-max <s>] [--class-source <s>] [--config <path>] -- <command…>
       pdx gate status [--json [--holders]] [--config <path>]
       pdx gate would-wait [--class heavy|light] [--json] [--config <path>]
       pdx gate would-wait --auto [--json] [--config <path>] -- <command…>
       pdx gate would-wait --auto-batch '<[["go","test"],["ls"]]>' [--json] [--config <path>]
       pdx gate free <heavy|light>-<k> [--yes] [--force] [--config <path>]
       pdx gate set [--profile <name>] [--config <path>] <module>.<key>=<value>
       pdx gate profile use <name> | list [--config <path>]
  The gate is a pass-through (WWD-9 S0): the command is run at once, by exec, with its own exit code and streams. Nothing is queued, no slot is
  taken, nothing is refused with 75. What holds a heavy command now is the wwd: pdx test (a declared task, or --adhoc), and the entry of the hook.
  --class, --auto, --fg, --wait-max, --class-source, --config, --assist-host and --assist-ssh are read for the scripts that still pass them, and have no effect
  --real   the binary to run in place of the command's first word (a shim passes the real one)
  --tool   a shim's call: the command is <name> followed by the words after --; the real binary is looked up on PATH
           without the shim directory, at this run
  would-wait  always answers no (the JSON keeps its shape: class by the command's words, ahead 0); it reads no slot and makes no directory
  history  the commands that were offered to an assist host: where they ran, how long, why a send failed (pdx gate history --help)
  hosts    the assist hosts for this session and the repo of this directory: which are on, ready and allowed, and why not (see pdx assist)
  status, free, set, profile, queue and conflict still look at, and change, the old gate's slots and settings until they are removed (9a-3)
`

func runGate(args []string) {
	os.Exit(gateMain(args, os.Stdout, os.Stderr))
}

// gateMain is runGate with its streams, for tests; it returns the exit code.
func gateMain(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return gateStatus(args[1:], stdout, stderr)
		case "would-wait":
			return gateWouldWait(args[1:], stdout, stderr)
		case "queue":
			return gateQueue(args[1:], stdout, stderr)
		case "conflict":
			return gateConflict(args[1:], stdout, stderr)
		case "free":
			return gateFree(args[1:], stdout, stderr)
		case "set":
			return gateSet(args[1:], stdout, stderr)
		case "hosts":
			return gateHosts(args[1:], stdout, stderr)
		case "history":
			return gateHistory(args[1:], stdout, stderr)
		case "profile":
			return gateProfile(args[1:], stdout, stderr)
		}
	}
	flags, command, ok := splitRunArgs(args)
	hasSep := ok || sepIn(args)
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	class := fs.String("class", "", "heavy or light (no effect: the gate holds nothing)")
	fs.Bool("auto", false, "classify the command by its words (no effect)")
	fs.Bool("fg", false, "no background QoS (no effect)")
	real := fs.String("real", "", "binary to run in place of the first word")
	tool := fs.String("tool", "", "the shim that called: find the real binary behind it")
	fs.String("config", "", "config.toml (no effect)")
	waitMax := fs.Float64("wait-max", 0, "no effect: nothing waits for a slot, so nothing is refused with 75")
	classSource := fs.String("class-source", "", "where --class came from (classifier or kind; no effect)")
	fs.String("assist-host", "", "no effect")
	fs.String("assist-ssh", "", "no effect")
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if *waitMax < 0 {
		fmt.Fprintf(stderr, "pdx gate: --wait-max must not be negative, got %v\n", *waitMax)
		return 2
	}
	if *classSource != "" && *classSource != classByClassifier && *classSource != classByKind {
		fmt.Fprintf(stderr, "pdx gate: --class-source must be classifier or kind, got %q\n", *classSource)
		return 2
	}
	if *tool != "" {
		// a shim passes what the person typed after --, which is nothing for a bare `go`
		if !hasSep || fs.NArg() > 0 || strings.ContainsAny(*tool, "/ ") {
			fmt.Fprint(stderr, gateUsage)
			return 2
		}
	} else if !ok || fs.NArg() > 0 {
		fmt.Fprint(stderr, gateUsage)
		return 2
	}
	if *class != "" && *class != "heavy" && *class != "light" {
		fmt.Fprintf(stderr, "pdx gate: --class must be heavy or light, got %q\n", *class)
		return 2
	}
	realBin := *real
	var childEnv []string // nil: the environment as it is
	if *tool != "" {
		command = append([]string{*tool}, command...)
		// the fuse against a shim that keeps coming back (#3671): the depth of each tool rides in the environment of the child
		depthEnv, depth := gateToolDepth(os.Environ(), *tool)
		if depth >= gateToolDepthMax {
			fmt.Fprintf(stderr, "pdx gate: shim recursion: %s resolved to the pdx shim again\n", *tool)
			return 127
		}
		childEnv = depthEnv
		home, err := gate.AccountHome()
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate: %v\n", err)
			return 127
		}
		// looked up at every run from this run's PATH, so an `nvm use` is followed
		realBin, err = shim.Resolve(*tool, os.Getenv("PATH"), shim.DirIn(home))
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate: %v\n", err)
			return 127
		}
	}
	// WWD-9 S0-a: the gate holds nothing any more. Every form (the script form, --auto, a shim's --tool) runs the command by exec, at once, with
	// its exit code and its streams; --class, --class-source, --fg, --wait-max and --assist-* are read (a mistake in them is still a usage error)
	// and have no effect. The wwd's queue and its entry are what hold a heavy command now.
	return gateExecDirect(realBin, command, childEnv, stderr)
}

// newGate is the gate of this host for a call that takes a slot or answers about one: it asks the local wwd for its holds (spec §13.4),
// and a wwd that cannot answer holds nothing. Every entry that takes a slot builds its gate here, so they cannot differ.
func newGate(cfg gate.Config) *gate.Gate {
	g := gate.New(cfg)
	g.HoldFetch = gateHoldFetch
	return g
}

// gateHoldFetch asks the local wwd for its holds (a seam for tests: no test may depend on the wwd of the host it runs on).
var gateHoldFetch gate.HoldFetch = gate.WwdHoldFetch

// gateStdinIsTTY says whether stdin is a terminal (a seam for tests): vitest without a subcommand runs once, or watches, by it.
var gateStdinIsTTY = func() bool { return leaseStdinIsTTY() }

// leaseStdinIsTTY says whether stdin is a terminal (a seam for tests).
var leaseStdinIsTTY = func() bool {
	_, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil
}

// Where the class of an aliased run came from, as the decision record says it.
const (
	classByClassifier = "classifier"
	classByKind       = "kind"
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

// gateExec replaces this process with the command (a seam for tests).
var gateExec = syscall.Exec

// gateExecDirect runs argv by exec, as the shell would have run it without the gate: argv[0] is the name as typed, the binary
// is realBin (looked up on PATH when empty). It returns only when the command could not be started.
func gateExecDirect(realBin string, argv, env []string, stderr io.Writer) int {
	if realBin == "" {
		p, err := exec.LookPath(argv[0])
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate: %v\n", err)
			return 127
		}
		realBin = p
	}
	if err := gateExec(realBin, argv, envOrCurrent(env)); err != nil {
		fmt.Fprintf(stderr, "pdx gate: %s: %v\n", realBin, err)
		if errors.Is(err, os.ErrNotExist) {
			return 127
		}
		return 126
	}
	return 0
}

func gateConfigPath(path string) string {
	if path == "" {
		return gate.DefaultConfigPath()
	}
	return path
}

// sepIn reports whether args holds the "--" that ends the flags, with or without anything after it.
func sepIn(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return true
		}
	}
	return false
}

// gateReloader reads the config again each time the gate asks (a command starting, a waiting gate looking at the queue),
// so a changed setting is in force for the next command; a file that cannot be read keeps the last good settings.
func gateReloader(path string, last gate.Config) func() gate.Config {
	if path == "" {
		path = gate.DefaultConfigPath()
	}
	return func() gate.Config {
		cfg, err := gate.Load(path)
		if err != nil {
			return last
		}
		last = cfg
		return cfg
	}
}

func loadGateConfig(path string, stderr io.Writer) gate.Config {
	if path == "" {
		path = gate.DefaultConfigPath()
	}
	cfg, err := gate.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "pdx gate: %v; using the defaults\n", err)
	}
	return cfg
}

func gateStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	holders := fs.Bool("holders", false, "with --json: also look up the processes that hold each lock (slow: an lsof over the whole host)")
	cfgPath := fs.String("config", "", "config.toml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := loadGateConfig(*cfgPath, stderr)
	g := newGate(cfg)
	// the JSON is read by machines, often (the fleet view polls it): the lock holders cost an lsof over the whole host each,
	// so it carries them only for a slot whose gate is gone, unless asked
	mode := gate.HoldersAll
	if *asJSON && !*holders {
		mode = gate.HoldersOrphaned
	}
	st := g.StatusWith(mode)
	if *asJSON {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "閘門 %s（%s）\n", map[bool]string{true: "啟用", false: "停用"}[cfg.Enabled], g.DirOf())
	printPool := func(name string, size int, held []gate.Holder, waiting int, queue []gate.Waiter) {
		fmt.Fprintf(stdout, "%s %d/%d 使用中，%d 個在排隊\n", name, len(held), size, waiting)
		if size == 0 && cfg.Enabled { // a disabled gate lets everything through, so nothing waits
			fmt.Fprintln(stdout, "  已暫停（名額 0）：新的指令排隊等候，原因 paused；名額調回後自動開始，已在跑的不受影響")
		}
		for _, h := range held {
			age := ""
			if !h.Started.IsZero() {
				age = time.Since(h.Started).Round(time.Second).String()
			}
			gone := ""
			if h.Orphaned {
				gone = "  （gate 已不在，樹還活著）"
			}
			mark := ""
			switch {
			case h.NoRecord:
				mark = "  （沒有紀錄：鎖被還沒結束的子行程占著）"
			case h.Late && h.Over:
				mark = "  （晚占、超額）"
			case h.Late:
				mark = "  （晚占）"
			case h.Over:
				mark = "  （超額）"
			}
			fmt.Fprintf(stdout, "  %s-%d  pid %d  %s  %s  %s%s%s\n", h.Class, h.Slot, h.PID, age, strings.Join(h.Argv, " "), h.Cwd, gone, mark)
			if len(h.Locked) > 0 {
				parts := make([]string, len(h.Locked))
				for i, p := range h.Locked {
					parts[i] = fmt.Sprintf("%d %s", p.PID, p.Cmd)
				}
				fmt.Fprintf(stdout, "      拿著鎖的行程：%s\n", strings.Join(parts, "、"))
			}
		}
		for _, w := range queue {
			waited := ""
			if !w.Since.IsZero() {
				waited = "等 " + time.Since(w.Since).Round(time.Second).String()
			}
			fmt.Fprintf(stdout, "  排隊 #%d  pid %d  %s  %s  %s\n", w.Place, w.PID, waited, strings.Join(w.Argv, " "), w.Cwd)
			if w.ConflictWith != "" {
				fmt.Fprintf(stdout, "    等 %s：%s（衝突規則：%s）\n", w.ConflictWith, w.ConflictRule, w.ConflictReason)
			}
			if w.Rank != "" && w.Rank != gate.RankNormal || w.RankError != "" {
				fmt.Fprintf(stdout, "    順序：%s%s\n", w.Rank, rankNote(w)) // moved in the queue (pdx gate queue): who, why, until
			}
		}
	}
	printPool("重", st.SlotsHeavy, st.Heavy, st.WaitHeavy, st.QueueHeavy)
	printPool("輕", st.SlotsLight, st.Light, st.WaitLight, st.QueueLight)
	if len(st.Holds) > 0 { // a hold of the local wwd: heavy commands wait for it, and a release hold leaves one light slot
		fmt.Fprintf(stdout, "wwd 的 hold：%s（重指令等它結束；有 release 旗標時輕名額上限 1）\n", strings.Join(st.Holds, "、"))
	}
	if st.ConflictsError != "" {
		fmt.Fprintf(stdout, "衝突規則：conflicts.json 不能用，目前沒有規則生效（%s）\n", st.ConflictsError)
	}
	for i, r := range st.Conflicts {
		fmt.Fprintln(stdout, "衝突規則 "+gate.ConflictLine(i+1, r))
	}
	if st.Admission.Held {
		fmt.Fprintf(stdout, "放行：被主機狀況擋住（%s）\n", st.Admission.Reason)
	} else {
		fmt.Fprintln(stdout, "放行：正常")
	}
	for _, h := range st.Suspect {
		fmt.Fprintf(stdout, "注意：%s-%d 的紀錄說 pid %d 還在，但鎖是空的（以鎖為準，視為空閒）\n", h.Class, h.Slot, h.PID)
	}
	fmt.Fprintf(stdout, "生效的設定（profile：%s；來源 default＝內建、config＝設定檔、profile＝profile、auto＝依主機現況）\n", st.Profile)
	for _, e := range st.Settings {
		fmt.Fprintf(stdout, "  %-34s %-24s %s\n", e.Key, e.Value, e.Source)
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(stdout, "設定警告：%s\n", w)
	}
	return 0
}

// parseMixed parses fs over args wherever the flags stand: Go's flag package stops at the first word that is not a flag,
// so `profile use night --config X` would otherwise be a usage error. It returns the words that are not flags, in order.
func parseMixed(fs *flag.FlagSet, args []string) ([]string, error) {
	// a `--` ends the flags for good: what follows it is words, whatever it looks like
	var after []string
	for i, a := range args {
		if a == "--" {
			args, after = args[:i], args[i+1:]
			break
		}
	}
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return append(words, after...), nil
		}
		words = append(words, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func gateSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "write into this profile")
	cfgPath := fs.String("config", "", "config.toml")
	expect := fs.String("expect", "", "write only if the value now is this one (the value you read); refused with exit 3 otherwise")
	words, err := parseMixed(fs, args)
	if err != nil {
		return 2
	}
	var want *string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "expect" {
			want = expect
		}
	})
	key, value, ok := strings.Cut(strings.Join(words, " "), "=")
	if !ok || len(words) == 0 {
		fmt.Fprint(stderr, "usage: pdx gate set [--profile <name>] [--expect <value now>] <module>.<key>=<value>\n")
		return 2
	}
	path := *cfgPath
	if path == "" {
		path = gate.DefaultConfigPath()
	}
	res, err := gate.SetExpect(path, *profile, strings.TrimSpace(key), value, want)
	if err != nil {
		fmt.Fprintf(stderr, "pdx gate set: %v\n", err)
		var ee *gate.ExpectError
		if errors.As(err, &ee) {
			return gateExitExpect
		}
		return 1
	}
	where := "[gate]"
	if res.Profile != "" {
		where = "profile " + res.Profile
	}
	fmt.Fprintf(stdout, "%s（%s）：%s → %s（%s）\n", res.Key, where, orDash(res.Old), res.New, res.NewSrc)
	if res.ReadBack != "" {
		fmt.Fprintf(stdout, "讀回：%s = %s\n", res.Key, res.ReadBack)
	}
	return 0
}

// gateExitExpect is the exit code of a write that --expect refused.
const gateExitExpect = 3

func gateProfile(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate profile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config.toml")
	if len(args) == 0 {
		fmt.Fprint(stderr, "usage: pdx gate profile use <name> | list\n")
		return 2
	}
	sub := args[0]
	words, err := parseMixed(fs, args[1:])
	if err != nil {
		return 2
	}
	path := *cfgPath
	if path == "" {
		path = gate.DefaultConfigPath()
	}
	switch {
	case sub == "use" && len(words) == 1:
		known, err := gate.UseProfile(path, words[0])
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate profile: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "目前的 profile：%s\n", words[0])
		if words[0] == gate.ReleaseProfile {
			fmt.Fprintln(stdout, "（release 是內建的：指令照原本的優先權執行、go test 的 -count=1 保留，不需要在設定檔裡寫 [gate.profiles.release]；要再改別的設定，用 pdx gate set --profile release <module>.<key>=<value>）")
		} else if !known {
			fmt.Fprintf(stdout, "（設定檔裡還沒有 [gate.profiles.%s]，所以目前沒有任何設定被改變；用 pdx gate set --profile %s <module>.<key>=<value> 加入）\n", words[0], words[0])
		}
		return 0
	case sub == "list" && len(words) == 0:
		cfg, err := gate.Load(path)
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate profile: %v\n", err)
			return 1
		}
		names := cfg.Profiles
		if !slices.Contains(names, gate.ReleaseProfile) {
			names = append(names, gate.ReleaseProfile) // built in
		}
		if !slices.Contains(names, "default") {
			names = append([]string{"default"}, names...)
		}
		for _, name := range names {
			mark := " "
			if name == cfg.Profile {
				mark = "*"
			}
			fmt.Fprintf(stdout, "%s %s\n", mark, name)
		}
		return 0
	}
	fmt.Fprint(stderr, "usage: pdx gate profile use <name> | list\n")
	return 2
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

const wouldWaitUsage = "usage: pdx gate would-wait [--class heavy|light] [--json] [--config <path>]\n       pdx gate would-wait --auto [--json] [--config <path>] -- <command…>\n       pdx gate would-wait --auto-batch '<[[\"go\",\"test\"],[\"ls\"]]>' [--json] [--config <path>]\n"

// wouldWaitBatch answers for several commands in one process: each is classified by its words as --auto does, and none waits. The JSON is an
// array in the order of the commands.
func wouldWaitBatch(cfg gate.Config, env gate.Env, argvs [][]string, asJSON bool, stdout io.Writer) int {
	answers := make([]gate.WouldWait, 0, len(argvs))
	for _, argv := range argvs {
		answers = append(answers, gate.WouldWait{Class: cfg.Intercept.Classify(argv, env)})
	}
	if asJSON {
		b, _ := json.Marshal(answers)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for range answers {
		fmt.Fprintln(stdout, "no")
	}
	return 0
}

// gateWouldWait answers whether a command of a class would wait now: never (S0-a), and without reading the gate. It exits 0 (the answer
// is in the output); 2 is a usage error.
func gateWouldWait(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate would-wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags, command, hasCmd := splitRunArgs(args)
	class := fs.String("class", "heavy", "heavy or light")
	auto := fs.Bool("auto", false, "classify the command after -- by its words")
	batch := fs.String("auto-batch", "", "several commands at once: a JSON array of argv arrays, each classified by its words")
	asJSON := fs.Bool("json", false, "print JSON")
	cfgPath := fs.String("config", "", "config.toml")
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	batchGiven := false
	fs.Visit(func(f *flag.Flag) { batchGiven = batchGiven || f.Name == "auto-batch" })
	if fs.NArg() > 0 || (*class != "heavy" && *class != "light") || *auto != hasCmd || (batchGiven && (*auto || hasCmd)) {
		fmt.Fprint(stderr, wouldWaitUsage)
		return 2
	}
	var argvs [][]string
	if batchGiven {
		if err := json.Unmarshal([]byte(*batch), &argvs); err != nil || len(argvs) == 0 {
			fmt.Fprint(stderr, wouldWaitUsage)
			return 2
		}
		for _, a := range argvs {
			if len(a) == 0 {
				fmt.Fprint(stderr, wouldWaitUsage)
				return 2
			}
		}
	}
	// WWD-9 S0-a: nothing waits for a slot any more, so the answer is always no. It keeps its shape (class, ahead 0) for the readers of the JSON,
	// reads no slot, no queue and no pressure, and makes no gate directory.
	cfg := loadGateConfig(*cfgPath, stderr)
	env := gate.EnvOf(os.Environ(), false) // the same view of the environment as a shim's: no terminal, the caller's variables
	if batchGiven {
		return wouldWaitBatch(cfg, env, argvs, *asJSON, stdout)
	}
	cl := gate.Class(*class)
	if *auto {
		cl = cfg.Intercept.Classify(command, env)
	}
	w := gate.WouldWait{Class: cl}
	if *asJSON {
		b, _ := json.Marshal(w)
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if !w.Wait {
		fmt.Fprintln(stdout, "no")
		return 0
	}
	fmt.Fprintf(stdout, "yes (%s): %s\n", w.Reason, w.Note)
	return 0
}
