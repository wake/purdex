package main

// The mode flags `pdx lease run` still accepts (lease v2 amendment A, Task 16): --queue, --ask and --wait. The alias reads them
// and ignores them.

import (
	"flag"
	"time"
)

// waitFlag is --wait. It is a bare switch (--wait), and it still takes the old
// duration (--wait=2m, --wait 2m) so scripts and the old skill text keep
// working. The duration is accepted and ignored: the daemon no longer reads it.
type waitFlag struct {
	set bool
	d   time.Duration
}

func (w *waitFlag) String() string { return w.d.String() }

func (w *waitFlag) IsBoolFlag() bool { return true }

func (w *waitFlag) Set(s string) error {
	w.set = true
	if s == "true" || s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	w.d = d
	return nil
}

// normalizeWaitArgs turns "--wait <dur>" into "--wait=<dur>": a boolean flag
// does not take the next token by itself. A token that is not a duration stays
// where it is (a bare --wait followed by something else).
func normalizeWaitArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if (a == "--wait" || a == "-wait") && i+1 < len(args) {
			if _, err := time.ParseDuration(args[i+1]); err == nil {
				out = append(out, "--wait="+args[i+1])
				i++
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// modeFlags are the flags that choose the mode.
type modeFlags struct {
	queue, ask bool
	wait       waitFlag
}

func (m *modeFlags) register(fs *flag.FlagSet) {
	fs.BoolVar(&m.queue, "queue", false, "")
	fs.BoolVar(&m.ask, "ask", false, "")
	fs.Var(&m.wait, "wait", "")
}

// check says whether the mode flags are usable: at most one of --queue, --ask and --wait. They choose nothing any more (the
// alias runs the gate); a call that gives two is still a usage error, as it always was.
func (m *modeFlags) check() string {
	n := 0
	for _, b := range []bool{m.queue, m.ask, m.wait.set} {
		if b {
			n++
		}
	}
	if n > 1 {
		return "give at most one of --queue, --ask and --wait"
	}
	return ""
}
