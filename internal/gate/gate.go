package gate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	mrand "math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wake/purdex/internal/redact"
)

// HeldEnv names the slot a command runs under: <class>-<k>:<token>, e.g. heavy-1:9f3c…. A gate that finds it passes
// the command through only when the claim is true (see nested): the token is the one in the slot's record, the slot's
// lock is held, and the caller is a descendant of the holder. A variable someone set by hand, an old one from a job
// that is over, or a light slot claimed for a heavy command takes no one past the gate.
const HeldEnv = "PDX_GATE_HELD"

// LateRootEnv marks the gate pid of a packaging command that may take a late slot for its whole tree: a packaging gate below
// it (its pid is an ancestor of the inner gate) does not take one of its own.
const LateRootEnv = "PDX_GATE_LATE_ROOT"

// ExitRefused is the exit code of a command the gate did not run: the disk is below the floor, or the gate directory is
// broken (sysexits EX_TEMPFAIL).
const ExitRefused = 75

// ExitOrphaned is the exit code of a command that waited for a slot and whose caller went away meanwhile: nobody is left to
// read its output, so it was not run (129, as for a hangup).
const ExitOrphaned = 129

// TagEnv names what the run is for: an assist run sent here by another host's gate sets assist:<job>, and the slot's record and the
// waiting ticket carry it, so `pdx gate status` shows the run as an assist holder.
const TagEnv = "PDX_GATE_TAG"

// AdmittedMarkEnv, when set, makes the gate print the admitted mark on the command's stderr right before it starts the command:
// whoever reads the output (a gate on another host that sent the run) knows that nothing ran before this line, and that the command
// did after. Its value is a nonce the sender chose ("1" for none): it is in the mark lines, and the gate takes the variable out of the
// command's environment, so a command cannot print a mark of its own that the sender would believe.
const AdmittedMarkEnv = "PDX_GATE_ADMITTED_MARK"

// AdmittedMark is the line the gate prints for AdmittedMarkEnv=nonce: a record separator (0x1e), so it is not a line a command prints by
// accident, and the nonce.
func AdmittedMark(nonce string) string { return "\x1epdx-gate-admitted:" + nonce + "\n" }

// NotStartedMark follows AdmittedMark when the command could not be started after all (a binary that cannot be executed): the run was
// admitted but nothing ran, which is a failure before the start for whoever sent it.
func NotStartedMark(nonce string) string { return "\x1epdx-gate-not-started:" + nonce + "\n" }

// KeepOrphanEnv set to anything but "" and "0" lets a command that waits go on waiting when its caller is gone (a detached job
// on purpose: `nohup pdx gate … &` that outlives the terminal).
const KeepOrphanEnv = "PDX_GATE_KEEP_ORPHAN"

// Options is one run.
type Options struct {
	Argv  []string // the command as the person typed it; argv[0] is its name
	Real  string   // the binary to run in place of argv[0] (a shim passes the real one: its own name finds the shim)
	Class Class    // "" classifies Argv
	// ClassSource says where a given Class came from, for the decision record ("classifier", "kind"); "" for one the person chose.
	ClassSource string
	// Foreground keeps the command at normal priority and untouched by the cache module, for a release gate on an idle host.
	Foreground bool
	Env        []string
	Dir        string
	// WaitMax, when above 0, is how long the command may wait for its slot: past it the gate ends the wait, runs nothing and exits with
	// ExitRefused (a run sent from another host uses it to know that nothing started there).
	WaitMax time.Duration
	// WaitedOut, when set, is written when Run returns: true when the wait for a slot ran past WaitMax and that is why nothing
	// ran, false for every other end (including a refusal for another cause, which also exits ExitRefused).
	WaitedOut *bool
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

// state is what copies of a Gate share: the config as last read.
type state struct {
	mu  sync.RWMutex
	cfg Config
	// the last reading of the wwd's holds (holdview.go)
	holdMu   sync.Mutex
	holdMemo HoldView
	holdAt   time.Time
}

// Gate runs commands under slots.
type Gate struct {
	st *state
	// Load reads the settings again; nil keeps the config the gate was made with. It is called when a command starts and
	// at every look of a waiting gate at the queue, so a change to the file is in force for the next command.
	Load     func() Config
	Pressure PressureReader
	Now      func() time.Time
	// Signals delivers the interrupts to pass on to the command; nil listens for SIGINT, SIGTERM and SIGHUP.
	Signals <-chan os.Signal
	Stderr  io.Writer
	// Poll is how long a waiting gate sleeps between looks (1–3 s with jitter); KillGrace, when set, replaces the reap
	// grace of the config; TreeEvery is how often the process tree of a terminal job is looked at.
	Poll      func() time.Duration
	KillGrace time.Duration
	TreeEvery time.Duration
	// Tools are the scheduling helpers of this host.
	Tools   Tools
	Session string
	// Seams: the process table, whether stdin is a terminal, the free bytes of a path, the host's cores and memory, the
	// gate's own pid, and the token minted for a slot.
	Procs    Procs
	IsTTY    func() bool
	DiskFree func(path string) (int64, error)
	Facts    func() HostFacts
	// Holders names the other processes that hold a slot's lock file when the slot is released.
	Holders func(path string) []Straggler
	PID     func() int
	// Getppid is the parent of this process now (os.Getppid); a seam so a test can reparent it.
	Getppid func() int
	Token   func() string
	// HoldFetch asks the local wwd for its holds (holdview.go); nil asks nothing, and there are no holds.
	HoldFetch HoldFetch
}

// SessionFromEnv is the Claude Code session a command runs in: CLAUDE_CODE_SESSION_ID (what Claude Code sets), else
// CLAUDE_SESSION_ID (an older name some callers still set); "" when neither is. It is the one reading of those two: the gate records its
// slots, queue entries and decisions by it, and `pdx test` and `pdx jobs` start from it to name the submitter's session of a wwd job
// (#3477). The gate itself reads nothing else: a PDX_SESSION_ID left in a shell must not take the place of the real session.
func SessionFromEnv(get func(string) string) string {
	if v := get("CLAUDE_CODE_SESSION_ID"); v != "" {
		return v
	}
	return get("CLAUDE_SESSION_ID")
}

// New is the gate of this host with the given config.
func New(cfg Config) *Gate {
	g := &Gate{
		st: &state{cfg: cfg}, Now: time.Now, Stderr: os.Stderr, TreeEvery: 500 * time.Millisecond,
		Poll:    func() time.Duration { return time.Second + time.Duration(mrand.Int63n(int64(2*time.Second))) },
		Session: SessionFromEnv(os.Getenv),
		Procs:   procTable, IsTTY: stdinIsTTY, DiskFree: freeBytes, Facts: ReadHostFacts, PID: os.Getpid, Getppid: os.Getppid, Token: newToken, Holders: lsofHolders,
	}
	g.Pressure = NewHostReader(cfg.Dir)
	g.Tools = hostTools()
	return g
}

func hostTools() Tools {
	var t Tools
	for _, p := range []string{"/usr/bin/nice", "/bin/nice"} {
		if _, err := os.Stat(p); err == nil {
			t.Nice = p
			break
		}
	}
	if p, err := exec.LookPath("taskpolicy"); err == nil {
		t.Taskpolicy = p
	} else if _, err := os.Stat("/usr/sbin/taskpolicy"); err == nil {
		t.Taskpolicy = "/usr/sbin/taskpolicy"
	}
	return t
}

// Config is the settings as last read.
func (g *Gate) Config() Config {
	g.st.mu.RLock()
	defer g.st.mu.RUnlock()
	return g.st.cfg
}

// SetConfig changes the settings the gate holds (what Load would otherwise bring).
func (g *Gate) SetConfig(f func(*Config)) {
	g.st.mu.Lock()
	defer g.st.mu.Unlock()
	f(&g.st.cfg)
}

// refresh reads the settings again.
func (g *Gate) refresh() Config {
	if g.Load != nil {
		c := g.Load()
		g.st.mu.Lock()
		g.st.cfg = c
		g.st.mu.Unlock()
		return c
	}
	return g.Config()
}

func newToken() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

func (g *Gate) say(format string, a ...any) {
	if g.Stderr != nil {
		fmt.Fprintf(g.Stderr, "pdx gate: "+format+"\n", a...)
	}
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return kv[len(name)+1:]
		}
	}
	return ""
}

// withoutEnv is env without name.
func withoutEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return out
}

func withEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, name+"=") {
			out = append(out, kv)
		}
	}
	return append(out, name+"="+value)
}

func (g *Gate) pool(cfg Config, c Class) *Pool {
	return &Pool{Dir: cfg.Dir, Class: c, Size: cfg.Slots.Size(c), Now: g.Now}
}

// nested says whether the claim in PDX_GATE_HELD is true for a command of the wanted class. All of it must hold: the
// token is the one in that slot's record; the slot's lock is held right now; and this process descends from the gate
// that took the slot (or from the command it started, which outlives the gate if that was killed). A heavy slot covers
// a light command below it; a light slot never covers a heavy one, which takes a heavy slot of its own.
func (g *Gate) nested(cfg Config, env []string, want Class) bool {
	v := envValue(env, HeldEnv)
	label, token, ok := strings.Cut(v, ":")
	if !ok || token == "" {
		return false
	}
	cls, ks, ok := strings.Cut(label, "-")
	if !ok {
		return false
	}
	held := Class(cls)
	k, err := strconv.Atoi(ks)
	if !held.Valid() || err != nil || k < 1 {
		return false
	}
	if held == ClassLight && want == ClassHeavy {
		return false
	}
	h, locked, ok := g.pool(cfg, held).Record(k)
	if !ok || !locked || h.Token == "" || h.Token != token {
		return false
	}
	tab, err := g.Procs()
	if err != nil {
		return false
	}
	for _, a := range ancestors(tab, g.PID()) {
		if a == h.PID || (h.PGID > 1 && a == h.PGID) {
			return true
		}
	}
	return false
}

// releaseProfile is the profile of a release run: the command runs as it was typed, at normal priority, with go's test
// cache switched off if it says so (spec §5, cache module).
const releaseProfile = ReleaseProfile

// ReleaseProfile is the one profile the gate knows without a table in the config.
const ReleaseProfile = "release"

// foreground says the command is exempt from the priority and cache modules: --fg, or the release profile.
func (c Config) foreground(o Options) bool { return o.Foreground || c.Profile == releaseProfile }

// Run takes a slot for the command, runs it, and gives the slot back when its process tree is gone. It returns the
// command's exit code (128+n for a signal, ExitRefused for a command that was not run). An error is only for a command
// that could not be started.
func (g *Gate) Run(ctx context.Context, o Options) (int, error) {
	if len(o.Argv) == 0 {
		return 2, errors.New("no command")
	}
	started := g.Now()
	d := &Decision{Time: started, Argv: truncateArgv(o.Argv), Cwd: g.cwdOf(o), Session: g.Session, Outcome: "interrupted"}
	code, err := g.run(ctx, o, d)
	d.Exit, d.Duration = code, g.Now().Sub(started).Seconds()
	if o.WaitedOut != nil {
		*o.WaitedOut = d.Outcome == "wait-max" && code == ExitRefused
	}
	if d.Outcome != "not-gated" { // the commands the gate does not hold are many and cheap: they leave no line and touch no file
		g.recordRun(d.dir, d)
	}
	return code, err
}

func (g *Gate) run(ctx context.Context, o Options, d *Decision) (int, error) {
	if len(o.Argv) == 0 {
		return 2, errors.New("no command")
	}
	cfg := g.refresh()
	for _, w := range cfg.Warnings {
		g.say("config: %s", w)
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	class, source := o.Class, o.ClassSource
	if class == "" {
		class = cfg.Intercept.Classify(o.Argv, EnvOf(env, g.IsTTY != nil && g.IsTTY()))
		source = "classifier"
	}
	d.ClassSource = source
	d.Class, d.Profile, d.dir = class, cfg.Profile, cfg.Dir
	sigs, stop := g.signals()
	defer stop()

	// Nothing to hold: the gate is off, the command is not heavy, or it runs below a slot that really is held.
	if !cfg.Enabled || !class.Valid() || g.nested(cfg, env, class) || g.underLate(cfg) {
		switch {
		case !cfg.Enabled:
			d.Outcome = "off"
		case !class.Valid():
			d.Outcome = "not-gated"
			if kind := cfg.watchKind(o.Argv, EnvOf(env, g.IsTTY != nil && g.IsTTY())); kind != "" {
				return g.runWatched(ctx, cfg, o, env, sigs, d, kind)
			}
		default:
			d.Outcome, d.Nested = "nested", true
		}
		return g.start(ctx, cfg, o, o.Argv, env, nil, sigs, nil, d)
	}
	if class == ClassHeavy {
		if msg := g.diskRefusal(cfg, o.Dir); msg != "" {
			g.say("%s", msg)
			d.Outcome = "refused"
			return ExitRefused, nil
		}
	}

	slot, code, done := g.wait(ctx, cfg, class, o, sigs, d)
	if done {
		if code == ExitRefused && d.Outcome != "wait-max" && d.Outcome != "assist" {
			d.Outcome = "refused"
		}
		return code, nil
	}
	// The wait may have been long and the settings may have changed meanwhile (pdx gate set, a new profile): the caps, the
	// cache rule and the priority are those of the moment the command starts, and the record says the same.
	pool := cfg.Dir
	cfg = g.Config()
	cfg.Dir = pool
	d.Profile = cfg.Profile
	argv, note := cfg.Cache.Apply(o.Argv, cfg.foreground(o))
	if note != "" {
		g.say("%s", note)
	}
	limits := cfg.Parallelism.Resolve(class, g.Facts())
	argv, env, added := Capped(argv, env, limits.GoP, limits.VitestWorkers, limits.PnpmWorkspace)
	if slot == nil { // the directory is denied (a sandbox): run without a slot
		d.Outcome = "no-slot"
		return g.start(ctx, cfg, o, argv, env, nil, sigs, nil, d)
	}
	defer func() {
		if left := slot.Release(g.Holders); len(left) > 0 {
			g.say("these processes escaped the clean-up and are still running: %s; the %s slot is released", describeStragglers(left), slot.Class)
			for _, p := range left {
				d.Left = append(d.Left, strconv.Itoa(p.PID)+" "+p.Cmd)
			}
		}
	}()
	if class == ClassHeavy {
		// the wait may have taken minutes or hours: the floor is checked again, with today's settings, before the command starts
		if msg := g.diskRefusal(cfg, o.Dir); msg != "" {
			g.say("%s", msg)
			d.Outcome = "refused"
			return ExitRefused, nil
		}
	}
	d.Outcome, d.Slot, d.Added = "slot", slot.Label(), added
	d.appliedSettings(cfg, class, cfg.foreground(o), limits)
	if len(added) > 0 {
		g.say("%s", "limits added: "+strings.Join(added, ", "))
	}
	token := g.Token()
	if err := slot.SetHolder(g.holder(o, token)); err != nil {
		// the record is part of the nesting protocol: a command started without it could never prove its claim, and a
		// heavy command below it would wait for the very slot its parent holds
		g.say("holder record: %v: the command was not run", err)
		d.Outcome = "refused"
		return ExitRefused, nil
	}
	env = withEnv(env, HeldEnv, slot.Label()+":"+token)
	return g.start(ctx, cfg, o, argv, env, slot, sigs, nil, d)
}

// underLateRoot says whether a packaging gate above this process has the late slot for the tree this process is in: the mark in
// its environment names a gate pid that is an ancestor of this one. A mark left by another tree (its pid is not above this
// process) says nothing.
func (g *Gate) underLateRoot(env []string) bool {
	root, err := strconv.Atoi(envValue(env, LateRootEnv))
	if err != nil || root <= 1 {
		return false
	}
	tab, err := g.Procs()
	if err != nil {
		return false
	}
	for _, a := range ancestors(tab, g.PID()) {
		if a == root {
			return true
		}
	}
	return false
}

// runWatched runs a command that takes no slot as this process's child, so that it can be timed and, for a packaging script, put
// under a slot when it has run long (#2996). A script that already runs below a held slot (the claim in its environment, or a
// late holder above it) is only written down: its work is counted already.
func (g *Gate) runWatched(ctx context.Context, cfg Config, o Options, env []string, sigs <-chan os.Signal, d *Decision, kind string) (int, error) {
	started := g.Now()
	var lw *lateWatch
	d.Outcome = "none"
	if kind == watchPackaging {
		d.Outcome = "packaging"
		if cfg.Packaging.LateSlotAfter > 0 && !g.nested(cfg, env, ClassLight) && !g.underLate(cfg) && !g.underLateRoot(env) {
			lw = &lateWatch{after: cfg.Packaging.LateSlotAfter, class: cfg.Packaging.LateSlotClass}
		}
	}
	code, err := g.start(ctx, cfg, o, o.Argv, env, nil, sigs, lw, d)
	if kind == watchUnknown && g.Now().Sub(started) < cfg.Packaging.RecordUnknown {
		d.Outcome = "not-gated" // too short to be worth a line
	}
	return code, err
}

// diskRefusal is the message of a refusal when the disk of the command's directory or of $TMPDIR is below the floor
// (a build fills the caches and the temp files there), or "".
func (g *Gate) diskRefusal(cfg Config, dir string) string {
	if g.DiskFree == nil {
		return ""
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	for _, path := range []string{dir, os.TempDir()} {
		if path == "" {
			continue
		}
		free, err := g.DiskFree(path)
		if err != nil {
			continue // unreadable: not a reason to refuse
		}
		if msg := cfg.Admission.DiskRefusal(path, free); msg != "" {
			return msg
		}
	}
	return ""
}

func (g *Gate) signals() (<-chan os.Signal, func()) {
	if g.Signals != nil {
		return g.Signals, func() {}
	}
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return ch, func() { signal.Stop(ch) }
}

func (g *Gate) holder(o Options, token string) Holder {
	return Holder{PID: g.PID(), Argv: truncateArgv(o.Argv), Cwd: g.cwdOf(o), Started: g.Now(), Session: g.Session, Token: token, Tag: tagOf(o)}
}

// cwdOf is the directory the command runs in.
func (g *Gate) cwdOf(o Options) string {
	if o.Dir != "" {
		return o.Dir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// truncateArgv keeps the record short: at most 12 words of 120 bytes, the secrets in them replaced before the cut (redact.Argv).
func truncateArgv(argv []string) []string { return redact.Argv(argv, 120) }

// Status is what `pdx gate status` shows: the holders of both pools, the number of waiters, and the records whose
// gate is alive while their lock is free (something that should not happen: the lock is the truth, so it is flagged).
type Status struct {
	Heavy, Light []Holder
	WaitHeavy    int
	WaitLight    int
	// QueueHeavy and QueueLight list the commands that wait, first in line first (their number is WaitHeavy / WaitLight
	// when nothing changed between the two looks).
	QueueHeavy []Waiter
	QueueLight []Waiter
	Suspect    []Holder
	// Profile is the active profile; Settings is every setting in force, with where it came from.
	Profile  string
	Settings []Setting
	// Admission says whether a heavy start would be held back by the host right now (memory pressure, page rate, disk), by the
	// rule that decides it for a command.
	Admission AdmissionState
	// PausedHeavy and PausedLight say a class has a slot count of 0: its new commands wait until the count is raised.
	PausedHeavy bool `json:"PausedHeavy"`
	// Conflicts are the conflict rules of this gate directory (spec §5.3) and ConflictsError why the file cannot be used (then there
	// are none). Each waiter says in ConflictWith / ConflictRule when a rule holds it.
	Conflicts      []Conflict `json:"Conflicts"`
	ConflictsError string     `json:"ConflictsError,omitempty"`
	PausedLight    bool       `json:"PausedLight"`
	// SlotsHeavy and SlotsLight are the slot counts the gate really allows now: the settings, with a release hold of the local wwd
	// taking the light ones down to 1 (holdview.go). Holds is the name of every hold in force.
	SlotsHeavy int      `json:"SlotsHeavy"`
	SlotsLight int      `json:"SlotsLight"`
	Holds      []string `json:"Holds,omitempty"`
}

// AdmissionState is whether the gate is holding new heavy work because of the host, and why.
type AdmissionState struct {
	Held   bool   `json:"held"`
	Reason string `json:"reason,omitempty"`
}

// admission reads the host without sampling it (the level from the kernel, the rate from the cache other gates keep; the free
// space of the working directory and of $TMPDIR) and applies the rule that admits a command. A host that cannot be read is not
// held: status shows what is known. The disk it looks at is the status process's own: the person's directory for `pdx gate
// status`, the daemon's working directory and $TMPDIR when the daemon asks (the fleet's summary), which for the fleet is the
// main disk, the one that matters; a command's own directory is only known to the command (its start checks that one).
func (g *Gate) admission(cfg Config) AdmissionState {
	cwd, _ := os.Getwd()
	return g.admissionFor(cfg, cwd)
}

// admissionFor is admission with the directory whose free space counts (a command's own directory; $TMPDIR always counts too).
func (g *Gate) admissionFor(cfg Config, dir string) AdmissionState {
	if !cfg.Admission.Enabled {
		return AdmissionState{}
	}
	if pk, ok := g.Pressure.(PressurePeeker); ok {
		if p, have := pk.Peek(); have {
			if held, why := cfg.Admission.Hold(p); held {
				return AdmissionState{Held: true, Reason: why}
			}
		}
	}
	if g.DiskFree != nil {
		for _, path := range []string{dir, os.TempDir()} {
			if path == "" {
				continue
			}
			if free, err := g.DiskFree(path); err == nil {
				if short, why := cfg.Admission.DiskShort(free); short {
					return AdmissionState{Held: true, Reason: why}
				}
			}
		}
	}
	return AdmissionState{}
}

// Admission is whether a heavy start would be held back by the host right now (memory pressure, page rate, disk): the answer Status
// carries, without Status's look at every held slot and its lock holders.
func (g *Gate) Admission() AdmissionState { return g.admission(g.Config()) }

// AdmissionFor is Admission for a command that will work in dir: that directory's free space counts (and $TMPDIR's), not this process's
// working directory.
func (g *Gate) AdmissionFor(dir string) AdmissionState { return g.admissionFor(g.Config(), dir) }

// HoldersMode says for which held slots Status asks who has the lock file open (an lsof, which looks at every process of
// the host: about 0.3 s of CPU each time, #2933).
type HoldersMode int

const (
	HoldersAll      HoldersMode = iota // every held slot
	HoldersOrphaned                    // only the slots whose gate is gone (the only ones whose holders are not already known: the gate's own pid)
)

// Status reads the slot records straight from the directory (the daemon is not needed), with the lock holders of every held
// slot.
func (g *Gate) Status() Status { return g.StatusWith(HoldersAll) }

// StatusWith is Status with a choice of which held slots get their lock holders looked up.
func (g *Gate) StatusWith(mode HoldersMode) Status {
	cfg := g.effective(g.Config())
	h, l := g.pool(cfg, ClassHeavy), g.pool(cfg, ClassLight)
	heavy, light := h.Held(), l.Held()
	g.fillLocked(h, heavy, l, light, mode)
	rules, rulesErr := LoadConflicts(cfg.Dir)
	if rules == nil {
		rules = []Conflict{}
	}
	return Status{
		Conflicts: rules, ConflictsError: rulesErr,
		Heavy: heavy, Light: light, WaitHeavy: h.Waiting(), WaitLight: l.Waiting(),
		QueueHeavy: h.waitersWithConflicts(heavy, rules), QueueLight: l.waitersWithConflicts(light, rules),
		Suspect: append(h.Suspect(), l.Suspect()...), Profile: cfg.Profile, Settings: cfg.Effective(), Admission: g.admission(cfg),
		PausedHeavy: cfg.Enabled && cfg.Slots.Paused(ClassHeavy), PausedLight: cfg.Enabled && cfg.Slots.Paused(ClassLight),
		SlotsHeavy: cfg.Slots.Heavy, SlotsLight: cfg.Slots.Light, Holds: g.holdNames(cfg),
	}
}

// statusLookupBudget is how long `pdx gate status` spends in total on asking who holds the locks.
var statusLookupBudget = 3 * time.Second

// fillLocked adds, to each held slot of both pools, the processes that have its lock file open. The lookups run side
// by side and share one budget: whatever has not answered when it is spent is shown without its holders, so a slow lsof
// cannot hold `pdx gate status` for longer than that.
func (g *Gate) fillLocked(hp *Pool, heavy []Holder, lp *Pool, light []Holder, mode HoldersMode) {
	if g.Holders == nil {
		return
	}
	type answer struct {
		h *Holder
		s []Straggler
	}
	find := g.Holders
	n := 0
	for _, held := range [][]Holder{heavy, light} {
		for _, h := range held {
			if mode == HoldersAll || h.Orphaned {
				n++
			}
		}
	}
	if n == 0 {
		return
	}
	ch := make(chan answer, n) // buffered: a late answer is dropped without anyone waiting for it
	ask := func(p *Pool, held []Holder) {
		for i := range held {
			if mode == HoldersAll || held[i].Orphaned {
				go func(h *Holder) { ch <- answer{h, find(p.slotPath(h.Slot))} }(&held[i])
			}
		}
	}
	ask(hp, heavy)
	ask(lp, light)
	timeout := time.After(statusLookupBudget)
	for ; n > 0; n-- {
		select {
		case a := <-ch:
			a.h.Locked = a.s
		case <-timeout:
			return
		}
	}
}

// DirOf is the directory the gate keeps its state in.
func (g *Gate) DirOf() string { return filepath.Clean(g.Config().Dir) }

// tagOf is the run's tag: PDX_GATE_TAG of its environment (the process's own when the options carry none).
func tagOf(o Options) string {
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	return truncateTag(envValue(env, TagEnv))
}

// truncateTag keeps a tag to one short printable line.
func truncateTag(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}
