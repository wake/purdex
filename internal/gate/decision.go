package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Every command that goes through the gate leaves one line in <dir>/decisions.jsonl: what class it was, whether it got
// a slot and how long it waited and why, which parameters were applied and where each came from, and how it ended. A
// later tuner (or a person with jq) reads the file; the gate itself never does. Writing it can fail without the command
// noticing.

const (
	decisionsFile    = "decisions.jsonl"
	decisionsMaxSize = 4 << 20 // beyond this the file becomes decisions.jsonl.1 (the one before that is dropped)
)

// Applied is one parameter that was in force for the command and where it came from.
type Applied struct {
	Value  string `json:"value"`
	Source Source `json:"source"`
}

// Decision is the record of one command.
type Decision struct {
	Time    time.Time `json:"time"`
	Argv    []string  `json:"argv"` // truncated and with the secrets replaced, as in a holder's record
	Cwd     string    `json:"cwd,omitempty"`
	Session string    `json:"session,omitempty"` // the Claude Code session the command ran in
	Nested  bool      `json:"nested,omitempty"`  // it ran below a slot that really is held (outcome nested)
	// LateSlot says a packaging script was put under a slot after it had run for LateAfterS seconds (#2996). Outcome is "packaging"
	// for such a script (whether or not it ran long enough to be put under one), and "none" for an unknown `run` script that ran
	// long enough to be written down.
	LateSlot   bool    `json:"late_slot,omitempty"`
	LateAfterS float64 `json:"late_after_s,omitempty"`
	Class      Class   `json:"class,omitempty"`
	// ClassSource is how the class was had: classifier (the gate's rules by the command's words), kind (the old lease kind
	// of `pdx lease run`, for a command the rules do not know); empty when the person named the class.
	ClassSource string   `json:"class_source,omitempty"`
	Profile     string   `json:"profile"`
	Outcome     string   `json:"outcome"` // slot | no-slot | off | nested | refused | interrupted | orphaned | packaging | none
	Slot        string   `json:"slot,omitempty"`
	WaitS       float64  `json:"wait_s"`
	WaitWhy     []string `json:"wait_reasons,omitempty"` // queue | memory-pressure | pressure-unreadable | paused | held
	// Rank is the ticket's last rank while it waited (front | back | hold; absent for a plain normal ticket) and HeldS the seconds it
	// spent on hold (spec §5.2).
	Rank  string  `json:"rank,omitempty"`
	HeldS float64 `json:"held_s,omitempty"`
	// Conflict is the last conflict rule that kept the command waiting ("<a> × <b>") and ConflictS the seconds it spent blocked by one
	// (spec §5.3); both absent for a command no rule held back.
	Conflict  string             `json:"conflict,omitempty"`
	ConflictS float64            `json:"conflict_s,omitempty"`
	Applied   map[string]Applied `json:"applied,omitempty"`
	Added     []string           `json:"limits_added,omitempty"`
	Left      []string           `json:"left_running,omitempty"`
	Exit      int                `json:"exit"`
	Duration  float64            `json:"duration_s"`
	// The usage of the command tree (setUsage): absent for a command that never started, and for a record of an older gate.
	CPUS        *float64 `json:"cpu_s,omitempty"`        // user + system seconds (a pointer: a started command with 0 still says 0)
	MaxRSSMB    *float64 `json:"max_rss_mb,omitempty"`   // the largest single process
	MajorFaults *int64   `json:"major_faults,omitempty"` // pages read back from disk / swap
	dir         string
}

func (d *Decision) why(reason string) {
	for _, r := range d.WaitWhy {
		if r == reason {
			return
		}
	}
	d.WaitWhy = append(d.WaitWhy, reason)
}

func (d *Decision) apply(key, value string, src Source) {
	if d.Applied == nil {
		d.Applied = map[string]Applied{}
	}
	d.Applied[key] = Applied{Value: value, Source: src}
}

// appliedSettings notes the parameters that shape this command: the pool size, the priority, and the caps.
func (d *Decision) appliedSettings(cfg Config, class Class, foreground bool, lim Limits) {
	size := "slots." + string(class)
	d.apply(size, strconv.Itoa(cfg.Slots.Size(class)), cfg.Sources[size])
	if !foreground {
		d.apply("priority.mode", cfg.Priority.Mode, cfg.Sources["priority.mode"])
		if cfg.Priority.Mode == "nice" {
			d.apply("priority.nice", strconv.Itoa(cfg.Priority.Nice), cfg.Sources["priority.nice"])
		}
	}
	for name, v := range map[string]int{"go_p": lim.GoP, "vitest_workers": lim.VitestWorkers, "pnpm_concurrency": lim.PnpmWorkspace} {
		if src, ok := lim.Src[name]; ok && v > 0 {
			if src == SrcConfig && cfg.Sources["parallelism."+name] == SrcProfile {
				src = SrcProfile
			}
			d.apply("parallelism."+name, strconv.Itoa(v), src)
		}
	}
}

// recordRun writes the decision of a run. A command that took no slot of its own (the gate is off, or it ran below a held slot) is
// written only where the gate directory exists already: it must not make the directory (#3649), so in a fresh one that command leaves
// no line. Every other outcome makes the directory as it always did.
func (g *Gate) recordRun(dir string, d *Decision) {
	g.record(dir, d, d.Outcome != "off" && d.Outcome != "nested")
}

// record appends the decision to the gate directory. Failure is silent: the log is not the command. With create false the directory is
// not made: a directory that is not there (or goes away meanwhile) makes the lock file fail to open and nothing is written, with no
// look-then-create in between.
func (g *Gate) record(dir string, d *Decision, create bool) {
	if dir == "" {
		return
	}
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	lock := lockFileIn
	if create {
		lock = lockFile
	}
	unlock, err := lock(filepath.Join(dir, decisionsFile+".lock"))
	if err != nil {
		return
	}
	defer unlock() // size check, rotation and append are one step: two gates cannot rotate over each other
	path := filepath.Join(dir, decisionsFile)
	if st, err := os.Stat(path); err == nil && st.Size() > decisionsMaxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n')) // one write: lines of two gates do not mix
}
