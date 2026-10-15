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
)

// HeldEnv names the slot a command runs under: <class>-<k>:<token>, e.g. heavy-1:9f3c…. A gate that finds it passes
// the command through only when the claim is true (see nested): the token is the one in the slot's record, the slot's
// lock is held, and the caller is a descendant of the holder. A variable someone set by hand, an old one from a job
// that is over, or a light slot claimed for a heavy command takes no one past the gate.
const HeldEnv = "PDX_GATE_HELD"

// ExitRefused is the exit code of a command the gate did not run: the disk is below the floor, or the gate directory is
// broken (sysexits EX_TEMPFAIL).
const ExitRefused = 75

// Options is one run.
type Options struct {
	Argv  []string // the command as the person typed it; argv[0] is its name
	Real  string   // the binary to run in place of argv[0] (a shim passes the real one: its own name finds the shim)
	Class Class    // "" classifies Argv
	// Foreground keeps the command at normal priority and untouched by the cache module, for a release gate on an idle host.
	Foreground bool
	Env        []string
	Dir        string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// state is what copies of a Gate share: the config as last read.
type state struct {
	mu  sync.RWMutex
	cfg Config
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
	Token   func() string
}

// New is the gate of this host with the given config.
func New(cfg Config) *Gate {
	g := &Gate{
		st: &state{cfg: cfg}, Now: time.Now, Stderr: os.Stderr, TreeEvery: 500 * time.Millisecond,
		Poll:    func() time.Duration { return time.Second + time.Duration(mrand.Int63n(int64(2*time.Second))) },
		Session: os.Getenv("CLAUDE_SESSION_ID"),
		Procs:   procTable, IsTTY: stdinIsTTY, DiskFree: freeBytes, Facts: ReadHostFacts, PID: os.Getpid, Token: newToken, Holders: lsofHolders,
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
const releaseProfile = "release"

// foreground says the command is exempt from the priority and cache modules: --fg, or the release profile.
func (c Config) foreground(o Options) bool { return o.Foreground || c.Profile == releaseProfile }

// Run takes a slot for the command, runs it, and gives the slot back when its process tree is gone. It returns the
// command's exit code (128+n for a signal, ExitRefused for a command that was not run). An error is only for a command
// that could not be started.
func (g *Gate) Run(ctx context.Context, o Options) (int, error) {
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
	class := o.Class
	if class == "" {
		class = cfg.Intercept.Classify(o.Argv, EnvOf(env, g.IsTTY != nil && g.IsTTY()))
	}
	sigs, stop := g.signals()
	defer stop()

	// Nothing to hold: the gate is off, the command is not heavy, or it runs below a slot that really is held.
	if !cfg.Enabled || !class.Valid() || g.nested(cfg, env, class) {
		return g.start(ctx, cfg, o, o.Argv, env, nil, sigs)
	}
	if class == ClassHeavy {
		if msg := g.diskRefusal(cfg, o.Dir); msg != "" {
			g.say("%s", msg)
			return ExitRefused, nil
		}
	}

	argv, note := cfg.Cache.Apply(o.Argv, cfg.foreground(o))
	if note != "" {
		g.say("%s", note)
	}
	limits := cfg.Parallelism.Resolve(class, g.Facts())
	argv, env, added := Capped(argv, env, limits.GoP, limits.VitestWorkers, limits.PnpmWorkspace)
	slot, code, done := g.wait(ctx, cfg, class, o, sigs)
	if done {
		return code, nil
	}
	if slot == nil { // the directory is denied (a sandbox): run without a slot
		return g.start(ctx, cfg, o, argv, env, nil, sigs)
	}
	defer func() {
		if left := slot.Release(g.Holders); len(left) > 0 {
			g.say("these processes escaped the clean-up and are still running: %s; the %s slot is released", describeStragglers(left), slot.Class)
		}
	}()
	if class == ClassHeavy {
		// the wait may have taken minutes or hours: the floor is checked again, with today's settings, before the command starts
		if msg := g.diskRefusal(g.Config(), o.Dir); msg != "" {
			g.say("%s", msg)
			return ExitRefused, nil
		}
	}
	if len(added) > 0 {
		g.say("%s", "limits added: "+strings.Join(added, ", "))
	}
	token := g.Token()
	if err := slot.SetHolder(g.holder(o, token)); err != nil {
		// the record is part of the nesting protocol: a command started without it could never prove its claim, and a
		// heavy command below it would wait for the very slot its parent holds
		g.say("holder record: %v: the command was not run", err)
		return ExitRefused, nil
	}
	env = withEnv(env, HeldEnv, slot.Label()+":"+token)
	return g.start(ctx, g.Config(), o, argv, env, slot, sigs)
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
	cwd := o.Dir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	return Holder{PID: g.PID(), Argv: truncateArgv(o.Argv), Cwd: cwd, Started: g.Now(), Session: g.Session, Token: token}
}

// truncateArgv keeps the record short: at most 12 words of 120 bytes. Words that look like a secret are not kept.
func truncateArgv(argv []string) []string {
	scrubbed := scrubArgv(argv) // before the cut: a secret in a long word must not survive in its first 120 bytes
	out := make([]string, 0, len(scrubbed))
	for i, a := range scrubbed {
		if i >= 12 {
			out = append(out, "…")
			break
		}
		if len(a) > 120 {
			a = a[:120] + "…"
		}
		out = append(out, a)
	}
	return out
}

// Status is what `pdx gate status` shows: the holders of both pools, the number of waiters, and the records whose
// gate is alive while their lock is free (something that should not happen: the lock is the truth, so it is flagged).
type Status struct {
	Heavy, Light []Holder
	WaitHeavy    int
	WaitLight    int
	Suspect      []Holder
}

// Status reads the slot records straight from the directory (the daemon is not needed).
func (g *Gate) Status() Status {
	cfg := g.Config()
	h, l := g.pool(cfg, ClassHeavy), g.pool(cfg, ClassLight)
	return Status{Heavy: h.Held(), Light: l.Held(), WaitHeavy: h.Waiting(), WaitLight: l.Waiting(), Suspect: append(h.Suspect(), l.Suspect()...)}
}

// DirOf is the directory the gate keeps its state in.
func (g *Gate) DirOf() string { return filepath.Clean(g.Config().Dir) }
