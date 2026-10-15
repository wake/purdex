package main

// `pdx gate` (host-load-control spec §5): run a heavy command under a host-wide slot that is a local file lock, so the
// control works with the daemon down. `pdx gate status` reads the slot records straight from the gate directory.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/gate/shim"
)

const gateUsage = `usage: pdx gate [--class heavy|light] [--auto] [--tool <name>] [--fg] [--real <binary>] [--config <path>] -- <command…>
       pdx gate status [--json] [--config <path>]
       pdx gate set [--profile <name>] [--config <path>] <module>.<key>=<value>
       pdx gate profile use <name> | list [--config <path>]
  --class  the pool to take a slot from (default heavy); --auto classifies the command by its words instead
  --fg     keep normal priority (no background QoS), for a release gate on an idle host
  --real   the binary to run in place of the command's first word (a shim passes the real one)
  --tool   a shim's call: the command is <name> followed by the words after --; the real binary is looked up on PATH
           without the shim directory, at this run; a command that is not heavy or light is run at once, unqueued
  set      write one setting into [gate] of config.toml (or into [gate.profiles.<name>] with --profile); the gate reads it
           again at the next command and at the next look of every waiting one. Values: 2, true, "text", auto
  profile  use: make a profile the active one (its settings win over [gate]); list: the profiles the file defines
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
		case "set":
			return gateSet(args[1:], stdout, stderr)
		case "profile":
			return gateProfile(args[1:], stdout, stderr)
		}
	}
	flags, command, ok := splitRunArgs(args)
	hasSep := ok || sepIn(args)
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	class := fs.String("class", "", "heavy or light")
	auto := fs.Bool("auto", false, "classify the command by its words")
	fg := fs.Bool("fg", false, "no background QoS")
	real := fs.String("real", "", "binary to run in place of the first word")
	tool := fs.String("tool", "", "the shim that called: find the real binary behind it")
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	if err := fs.Parse(flags); err != nil {
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
	var c gate.Class
	switch *class {
	case "":
		if !*auto {
			c = gate.ClassHeavy
		}
	case "heavy":
		c = gate.ClassHeavy
	case "light":
		c = gate.ClassLight
	default:
		fmt.Fprintf(stderr, "pdx gate: --class must be heavy or light, got %q\n", *class)
		return 2
	}
	realBin := *real
	if *tool != "" {
		command = append([]string{*tool}, command...)
		home, err := os.UserHomeDir()
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
	// The hot path of a shim (go env, pnpm install, …): a command the gate does not take in is run at once, by exec, before any
	// gate state is made. Only the settings file is read (the intercept rules of it still apply), nothing else of the gate.
	if *tool != "" && *class == "" {
		cfg, _ := gate.Load(gateConfigPath(*cfgPath))
		cls := cfg.Intercept.Classify(command, gate.EnvOf(os.Environ(), gateStdinIsTTY()))
		if !cfg.Enabled || !cls.Valid() {
			return gateExecDirect(realBin, command, stderr)
		}
		c = cls
	}
	cfg := loadGateConfig(*cfgPath, stderr)
	g := gate.New(cfg)
	g.Stderr = stderr
	g.Load = gateReloader(*cfgPath, cfg)
	code, _ := g.Run(context.Background(), gate.Options{Argv: command, Real: realBin, Class: c, Foreground: *fg, Stdout: stdout, Stderr: stderr})
	return code
}

// gateStdinIsTTY says whether stdin is a terminal (a seam for tests): vitest without a subcommand runs once, or watches, by it.
var gateStdinIsTTY = func() bool { return leaseStdinIsTTY() }

// gateExec replaces this process with the command (a seam for tests).
var gateExec = syscall.Exec

// gateExecDirect runs argv by exec, as the shell would have run it without the gate: argv[0] is the name as typed, the binary
// is realBin (looked up on PATH when empty). It returns only when the command could not be started.
func gateExecDirect(realBin string, argv []string, stderr io.Writer) int {
	if realBin == "" {
		p, err := exec.LookPath(argv[0])
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate: %v\n", err)
			return 127
		}
		realBin = p
	}
	if err := gateExec(realBin, argv, os.Environ()); err != nil {
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
	cfgPath := fs.String("config", "", "config.toml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := loadGateConfig(*cfgPath, stderr)
	g := gate.New(cfg)
	st := g.Status()
	if *asJSON {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "閘門 %s（%s）\n", map[bool]string{true: "啟用", false: "停用"}[cfg.Enabled], g.DirOf())
	printPool := func(name string, size int, held []gate.Holder, waiting int) {
		fmt.Fprintf(stdout, "%s %d/%d 使用中，%d 個在排隊\n", name, len(held), size, waiting)
		for _, h := range held {
			age := ""
			if !h.Started.IsZero() {
				age = time.Since(h.Started).Round(time.Second).String()
			}
			gone := ""
			if h.Orphaned {
				gone = "  （gate 已不在，樹還活著）"
			}
			fmt.Fprintf(stdout, "  %s-%d  pid %d  %s  %s  %s%s\n", h.Class, h.Slot, h.PID, age, strings.Join(h.Argv, " "), h.Cwd, gone)
			if len(h.Locked) > 0 {
				parts := make([]string, len(h.Locked))
				for i, p := range h.Locked {
					parts[i] = fmt.Sprintf("%d %s", p.PID, p.Cmd)
				}
				fmt.Fprintf(stdout, "      拿著鎖的行程：%s\n", strings.Join(parts, "、"))
			}
		}
	}
	printPool("重", cfg.Slots.Heavy, st.Heavy, st.WaitHeavy)
	printPool("輕", cfg.Slots.Light, st.Light, st.WaitLight)
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

func gateSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "write into this profile")
	cfgPath := fs.String("config", "", "config.toml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	key, value, ok := strings.Cut(strings.Join(fs.Args(), " "), "=")
	if !ok || fs.NArg() == 0 {
		fmt.Fprint(stderr, "usage: pdx gate set [--profile <name>] <module>.<key>=<value>\n")
		return 2
	}
	path := *cfgPath
	if path == "" {
		path = gate.DefaultConfigPath()
	}
	res, err := gate.Set(path, *profile, strings.TrimSpace(key), value)
	if err != nil {
		fmt.Fprintf(stderr, "pdx gate set: %v\n", err)
		return 1
	}
	where := "[gate]"
	if res.Profile != "" {
		where = "profile " + res.Profile
	}
	fmt.Fprintf(stdout, "%s（%s）：%s → %s（%s）\n", res.Key, where, orDash(res.Old), res.New, res.NewSrc)
	return 0
}

func gateProfile(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gate profile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "", "config.toml")
	if len(args) == 0 {
		fmt.Fprint(stderr, "usage: pdx gate profile use <name> | list\n")
		return 2
	}
	sub := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	path := *cfgPath
	if path == "" {
		path = gate.DefaultConfigPath()
	}
	switch {
	case sub == "use" && fs.NArg() == 1:
		known, err := gate.UseProfile(path, fs.Arg(0))
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate profile: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "目前的 profile：%s\n", fs.Arg(0))
		if !known {
			fmt.Fprintf(stdout, "（設定檔裡還沒有 [gate.profiles.%s]，所以目前沒有任何設定被改變；用 pdx gate set --profile %s <module>.<key>=<value> 加入）\n", fs.Arg(0), fs.Arg(0))
		}
		return 0
	case sub == "list" && fs.NArg() == 0:
		cfg, err := gate.Load(path)
		if err != nil {
			fmt.Fprintf(stderr, "pdx gate profile: %v\n", err)
			return 1
		}
		for _, name := range append([]string{"default"}, cfg.Profiles...) {
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
