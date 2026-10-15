package main

// `pdx gate` (host-load-control spec §5): run a heavy command under a host-wide slot that is a local file lock, so the
// control works with the daemon down. `pdx gate status` reads the slot records straight from the gate directory.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wake/purdex/internal/gate"
)

const gateUsage = `usage: pdx gate [--class heavy|light] [--auto] [--fg] [--real <binary>] [--config <path>] -- <command…>
       pdx gate status [--json] [--config <path>]
  --class  the pool to take a slot from (default heavy); --auto classifies the command by its words instead
  --fg     keep normal priority (no background QoS), for a release gate on an idle host
  --real   the binary to run in place of the command's first word (a shim passes the real one)
`

func runGate(args []string) {
	os.Exit(gateMain(args, os.Stdout, os.Stderr))
}

// gateMain is runGate with its streams, for tests; it returns the exit code.
func gateMain(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "status" {
		return gateStatus(args[1:], stdout, stderr)
	}
	flags, command, ok := splitRunArgs(args)
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	class := fs.String("class", "", "heavy or light")
	auto := fs.Bool("auto", false, "classify the command by its words")
	fg := fs.Bool("fg", false, "no background QoS")
	real := fs.String("real", "", "binary to run in place of the first word")
	cfgPath := fs.String("config", "", "config.toml (default ~/.config/pdx/config.toml)")
	if err := fs.Parse(flags); err != nil {
		return 2
	}
	if !ok || fs.NArg() > 0 {
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
	cfg := loadGateConfig(*cfgPath, stderr)
	g := gate.New(cfg)
	g.Stderr = stderr
	g.Load = gateReloader(*cfgPath, cfg)
	code, _ := g.Run(context.Background(), gate.Options{Argv: command, Real: *real, Class: c, Foreground: *fg, Stdout: stdout, Stderr: stderr})
	return code
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
		}
	}
	printPool("重", cfg.Slots.Heavy, st.Heavy, st.WaitHeavy)
	printPool("輕", cfg.Slots.Light, st.Light, st.WaitLight)
	for _, h := range st.Suspect {
		fmt.Fprintf(stdout, "注意：%s-%d 的紀錄說 pid %d 還在，但鎖是空的（以鎖為準，視為空閒）\n", h.Class, h.Slot, h.PID)
	}
	return 0
}
