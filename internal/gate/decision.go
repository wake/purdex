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
	Time     time.Time          `json:"time"`
	Argv     []string           `json:"argv"`
	Class    Class              `json:"class,omitempty"`
	Profile  string             `json:"profile"`
	Outcome  string             `json:"outcome"` // slot | no-slot | off | nested | refused | interrupted
	Slot     string             `json:"slot,omitempty"`
	WaitS    float64            `json:"wait_s"`
	WaitWhy  []string           `json:"wait_reasons,omitempty"` // queue | memory-pressure | pressure-unreadable
	Applied  map[string]Applied `json:"applied,omitempty"`
	Added    []string           `json:"limits_added,omitempty"`
	Left     []string           `json:"left_running,omitempty"`
	Exit     int                `json:"exit"`
	Duration float64            `json:"duration_s"`
	dir      string
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

// record appends the decision to the gate directory. Failure is silent: the log is not the command.
func (g *Gate) record(dir string, d *Decision) {
	if dir == "" {
		return
	}
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
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
