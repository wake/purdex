package resourcesmod

import (
	"bufio"
	"bytes"
	"context"
	"github.com/wake/purdex/internal/execstat"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	iagent "github.com/wake/purdex/internal/agent"
	"github.com/wake/purdex/internal/gate"
	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// The load watcher runs on the sampler's tick (host-load-control spec §12, plan W1b-3): after a good sample it hands the
// process table, the host reading, the gate's held slots and the session roots to the detector, and appends what it finds to the
// event log. It reads nothing new in the steady state (no fork, no argv read): command lines, working directories and lock
// holders are asked for only about a tree that crossed a threshold or a slot that stays orphaned.

// watchEnv are the reads the watcher makes outside the sampler's own, each a seam for tests.
type watchEnv struct {
	loadGate func(path string) (gate.Config, error) // path: the daemon's resolved config.toml ("" = the default)
	held     func(cfg gate.Config) (heavy, light []gate.Holder)
	lockPIDs func(cfg gate.Config, h gate.Holder) []int
	// isGate says whether pid is a genuine `pdx gate` of this daemon's pdx; argv is a process's command line (nil: unknown).
	isGate func(ctx context.Context, pid int) bool
	argv   func(ctx context.Context, pid int) []string
	cwd    func(ctx context.Context, pid int) string
}

func defaultWatchEnv() watchEnv {
	return watchEnv{
		loadGate: func(path string) (gate.Config, error) {
			if path == "" {
				path = gate.DefaultConfigPath()
			}
			return gate.Load(path)
		},
		held:     gate.HeldSlots,
		lockPIDs: gate.LockPIDs,
		isGate:   isOwnGate,
		argv: func(ctx context.Context, pid int) []string {
			snap, err := iagent.SharedProcessSnapshot(ctx)
			if err != nil {
				return nil
			}
			info, err := snap.ReadFresh(pid)
			if err != nil {
				return nil
			}
			return info.Argv
		},
		cwd: lsofCwd,
	}
}

// isOwnGate: the process runs this daemon's own pdx executable with `gate` as its first argument (not just a name a process can
// give itself).
func isOwnGate(ctx context.Context, pid int) bool {
	snap, err := iagent.SharedProcessSnapshot(ctx)
	if err != nil {
		return false
	}
	info, err := snap.ReadFresh(pid)
	if err != nil || len(info.Argv) < 2 || info.Argv[1] != "gate" {
		return false
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	a, errA := os.Stat(self)
	b, errB := os.Stat(info.ExePath)
	return errA == nil && errB == nil && os.SameFile(a, b)
}

// lsofCwd is the working directory of pid, from one `lsof -a -p <pid> -d cwd -Fn`; "" when it cannot be read.
func lsofCwd(ctx context.Context, pid int) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := execstat.CommandContext(ctx, "resources.lsof-cwd", "lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil && len(out) == 0 {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if l := sc.Text(); strings.HasPrefix(l, "n") {
			return l[1:]
		}
	}
	return ""
}

// watchRuntime is the detector with its files, made at the first enabled pass.
type watchRuntime struct {
	det   *watch.Detector
	rec   *watch.Recorder
	state *watch.StateStore
}

// watchState is the module's watcher state: touched only by the sampler goroutine (or a test calling tick).
type watchState struct {
	src  watchsettings.Reader // nil: the watcher stays off
	env  watchEnv
	rt   *watchRuntime
	note map[string]string // the last problem logged per source, so a standing one logs once
	mu   sync.Mutex
}

// watchSettings reads the setting every tick (one indexed SQLite read). The watcher runs only on a setting that reads and says
// enabled; a reader error is logged once per run and leaves it off.
func (m *Module) watchSettings() (watchsettings.Settings, bool) {
	if m.watch.src == nil {
		return watchsettings.Settings{}, false
	}
	s, err := m.watch.src.WatchSettings()
	if err != nil {
		m.watchNote("settings", "cannot read the watch setting, the watcher stays off: "+err.Error())
		return watchsettings.Settings{}, false
	}
	m.watchNote("settings", "")
	return s, s.Enabled
}

// watchNote logs a problem of one source when it changes, and the return to normal.
func (m *Module) watchNote(source, problem string) {
	m.watch.mu.Lock()
	defer m.watch.mu.Unlock()
	if m.watch.note == nil {
		m.watch.note = map[string]string{}
	}
	if m.watch.note[source] == problem {
		return
	}
	m.watch.note[source] = problem
	if problem == "" {
		m.logf("[watch] %s: recovered", source)
	} else {
		m.logf("[watch] %s: %s", source, problem)
	}
}

// watchRuntimeFor makes the detector, recorder and state store at the first call; a state file that cannot be read is set aside
// and the watcher goes on with an empty one. Without a data dir the watcher cannot keep anything and stays off.
func (m *Module) watchRuntimeFor() *watchRuntime {
	if m.watch.rt != nil {
		return m.watch.rt
	}
	if m.core == nil || m.core.Cfg == nil || m.core.Cfg.DataDir == "" {
		m.watchNote("files", "no data dir: the watcher stays off")
		return nil
	}
	dir := filepath.Join(m.core.Cfg.DataDir, "watch")
	st, err := watch.OpenState(filepath.Join(dir, "state.json"))
	if err != nil {
		m.watchNote("state", err.Error())
	}
	host := m.core.Cfg.HostID
	if i := strings.IndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	if host == "" {
		host, _ = os.Hostname()
	}
	det := watch.New(st)
	det.Diag = m.watchNote
	m.watch.rt = &watchRuntime{det: det, rec: watch.NewRecorder(dir, host), state: st}
	return m.watch.rt
}

// watchPass is one detector pass over a good sample. It never fails the tick: a gate config, record or state problem is logged
// once per run and the pass goes on with what it has.
func (m *Module) watchPass(ctx context.Context, raw resources.HostRaw, procs []resources.Proc, roots []resources.Root, ws watchsettings.Settings) {
	rt := m.watchRuntimeFor()
	if rt == nil {
		return
	}
	env := m.watch.env
	cfgPath := ""
	if m.core != nil && m.core.Cfg != nil {
		cfgPath = m.core.Cfg.Path // the file this daemon was started with: the gate and the daemon read the same config.toml
	}
	gcfg, gerr := env.loadGate(cfgPath) // gate.Load answers the defaults with its error
	if gerr != nil {
		m.watchNote("gate config", gerr.Error()+"; using the defaults")
	} else {
		m.watchNote("gate config", "")
	}
	heavy, light := env.held(gcfg)
	in := watch.Input{
		Now: m.now(), Procs: procs, Raw: raw, Roots: roots, Interval: m.interval,
		Holders:  append(heavy, light...),
		IsGate:   func(pid int) bool { return env.isGate(ctx, pid) },
		Argv:     func(pid int) []string { return env.argv(ctx, pid) },
		Cwd:      func(pid int) string { return env.cwd(ctx, pid) },
		LockPIDs: func(h gate.Holder) []int { return env.lockPIDs(gcfg, h) },
		Set: watch.Settings{
			XCores: ws.XCores, YMB: ws.YMB, TS: watchsettings.Seconds(ws.TSeconds),
			PressureLevel: ws.PressureLevel, PressureTS: watchsettings.Seconds(ws.PressureTSeconds),
			PageRate: ws.PageRate, PageTS: watchsettings.Seconds(ws.PageTSeconds),
			OrphanAfter: watch.DefaultSettings().OrphanAfter, SlotLongAfter: gcfg.Lifetime.AlertAfter,
		},
	}
	events := rt.det.Pass(in)
	recErr := ""
	for _, ev := range events {
		if _, err := rt.rec.Record(ev); err != nil && recErr == "" {
			recErr = err.Error()
		}
	}
	m.watchNote("record", recErr)
	flushErr := ""
	if err := rt.state.Flush(); err != nil {
		flushErr = err.Error()
	}
	m.watchNote("state", flushErr)
}
