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
	"syscall"
	"time"

	"golang.org/x/sys/unix"
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
	// Foreground keeps the command at normal priority (no background QoS), for a release gate on an idle host.
	Foreground bool
	Env        []string
	Dir        string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// Gate runs commands under slots.
type Gate struct {
	Cfg      Config
	Pressure PressureReader
	Now      func() time.Time
	// Signals delivers the interrupts to pass on to the command; nil listens for SIGINT, SIGTERM and SIGHUP.
	Signals <-chan os.Signal
	Stderr  io.Writer
	// Poll is how long a waiting gate sleeps between looks (1–3 s with jitter); KillGrace how long a group has
	// between SIGTERM and SIGKILL (5 s); TreeEvery how often the process tree of a terminal job is looked at.
	Poll      func() time.Duration
	KillGrace time.Duration
	TreeEvery time.Duration
	// Taskpolicy is the path of taskpolicy(8) ("" when the host has none).
	Taskpolicy string
	Session    string
	// Seams: the process table, whether stdin is a terminal, the free bytes on the disk the build caches live on, the
	// gate's own pid, and the token minted for a slot.
	Procs    Procs
	IsTTY    func() bool
	DiskFree func(path string) (int64, error)
	PID      func() int
	Token    func() string
}

// New is the gate of this host with the given config.
func New(cfg Config) *Gate {
	g := &Gate{
		Cfg: cfg, Now: time.Now, Stderr: os.Stderr, KillGrace: 5 * time.Second, TreeEvery: time.Second,
		Poll:    func() time.Duration { return time.Second + time.Duration(mrand.Int63n(int64(2*time.Second))) },
		Session: os.Getenv("CLAUDE_SESSION_ID"),
		Procs:   procTable, IsTTY: stdinIsTTY, DiskFree: freeBytes, PID: os.Getpid, Token: newToken,
	}
	g.Pressure = NewHostReader(cfg.Dir)
	if p, err := exec.LookPath("taskpolicy"); err == nil {
		g.Taskpolicy = p
	} else if _, err := os.Stat("/usr/sbin/taskpolicy"); err == nil {
		g.Taskpolicy = "/usr/sbin/taskpolicy"
	}
	return g
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

func (g *Gate) pool(c Class) *Pool {
	size := g.Cfg.LightSlots
	if c == ClassHeavy {
		size = g.Cfg.HeavySlots
	}
	return &Pool{Dir: g.Cfg.Dir, Class: c, Size: size, Now: g.Now}
}

func (g *Gate) background(c Class, fg bool) bool {
	if fg || g.Taskpolicy == "" {
		return false
	}
	if c == ClassHeavy {
		return g.Cfg.HeavyBackground
	}
	return g.Cfg.LightBackground
}

// nested says whether the claim in PDX_GATE_HELD is true for a command of the wanted class. All of it must hold: the
// token is the one in that slot's record; the slot's lock is held right now; and this process descends from the gate
// that took the slot (or from the leader of its command's group, which outlives the gate that was killed). A heavy slot
// covers a light command below it; a light slot never covers a heavy one, which takes a heavy slot of its own.
func (g *Gate) nested(env []string, want Class) bool {
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
	h, locked, ok := g.pool(held).Record(k)
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

// Run takes a slot for the command, runs it, and gives the slot back when its process tree is gone. It returns the
// command's exit code (128+n for a signal, ExitRefused for a command that was not run). An error is only for a command
// that could not be started.
func (g *Gate) Run(ctx context.Context, o Options) (int, error) {
	if len(o.Argv) == 0 {
		return 2, errors.New("no command")
	}
	env := o.Env
	if env == nil {
		env = os.Environ()
	}
	class := o.Class
	if class == "" {
		class = Classify(o.Argv)
	}
	sigs, stop := g.signals()
	defer stop()

	// Nothing to hold: the gate is off, the command is not heavy, or it runs below a slot that really is held.
	if !g.Cfg.Enabled || !class.Valid() || g.nested(env, class) {
		return g.start(ctx, o, o.Argv, env, nil, false, sigs)
	}
	if class == ClassHeavy {
		if msg := g.diskRefusal(o.Dir); msg != "" {
			g.say("%s", msg)
			return ExitRefused, nil
		}
	}

	argv, env, added := Capped(o.Argv, env, g.Cfg)
	slot, code, done := g.wait(ctx, class, o, sigs)
	if done {
		return code, nil
	}
	if slot == nil { // the directory is denied (a sandbox): run without a slot
		return g.start(ctx, o, argv, env, nil, false, sigs)
	}
	defer slot.Release()
	if len(added) > 0 {
		g.say("%s", "limits added: "+strings.Join(added, ", "))
	}
	token := g.Token()
	if err := slot.SetHolder(g.holder(o, token)); err != nil {
		g.say("holder record: %v", err)
	}
	env = withEnv(env, HeldEnv, slot.Label()+":"+token)
	return g.start(ctx, o, argv, env, slot, g.background(class, o.Foreground), sigs)
}

// diskRefusal is the message of a refusal when the disk of the command's directory or of $TMPDIR is below the floor
// (a build fills the caches and the temp files there), or "".
func (g *Gate) diskRefusal(dir string) string {
	if g.Cfg.DiskMinGB <= 0 || g.DiskFree == nil {
		return ""
	}
	if dir == "" {
		dir, _ = os.Getwd()
	}
	floor := int64(g.Cfg.DiskMinGB * (1 << 30))
	for _, path := range []string{dir, os.TempDir()} {
		if path == "" {
			continue
		}
		free, err := g.DiskFree(path)
		if err != nil {
			continue // unreadable: not a reason to refuse
		}
		if free < floor {
			return fmt.Sprintf("%s has %.1f GB free, below the %.1f GB of [gate] disk_min_gb: the command was not run (free some space, or lower the floor)",
				path, float64(free)/(1<<30), g.Cfg.DiskMinGB)
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

// pressureFailures is how many reads in a row may fail before the gate admits without a reading.
const pressureFailures = 3

// wait queues for a slot and returns it. done is true when the run ends here (an interrupt while waiting, or a broken
// gate directory, with the exit code); a nil slot with done false means the directory is denied (a sandbox) and the
// command runs without a slot.
func (g *Gate) wait(ctx context.Context, class Class, o Options, sigs <-chan os.Signal) (*Slot, int, bool) {
	pool := g.pool(class)
	broken := func(err error) (*Slot, int, bool) {
		if errors.Is(err, ErrUnavailable) {
			g.say("%v; running without a slot", err)
			return nil, 0, false
		}
		g.say("%v: the command was not run (fix %s)", err, g.Cfg.Dir)
		return nil, ExitRefused, true
	}
	ticket, err := pool.Enqueue()
	if err != nil {
		return broken(err)
	}
	defer ticket.Drop()
	var good *Pressure
	fails := 0
	warned := false
	lastPlace := time.Time{}
	for {
		ok := true
		if g.Pressure != nil {
			p, err := g.Pressure.Read()
			switch {
			case err == nil:
				fails, good = 0, &p
				ok = g.Cfg.Admit(p)
			default:
				fails++
				switch {
				case fails >= pressureFailures:
					if !warned {
						g.say("memory pressure unreadable %d times (%v); admitting", fails, err)
						warned = true
					}
				case good != nil:
					ok = g.Cfg.Admit(*good) // keep the last good reading until it has failed three times
				default:
					ok = false
				}
			}
		}
		ahead := 0
		if ok {
			slot, a, err := pool.TryTake(ticket)
			if err != nil {
				return broken(err)
			}
			if slot != nil {
				ticket.Drop()
				return slot, 0, false
			}
			ahead = a
		}
		now := g.Now()
		if lastPlace.IsZero() || now.Sub(lastPlace) >= time.Minute {
			lastPlace = now
			if ok {
				g.say("waiting for a %s slot (%d ahead)", class, ahead)
			} else {
				g.say("waiting: the host is under memory pressure")
			}
		}
		select {
		case sig := <-sigs:
			return nil, 128 + signalNumber(sig), true
		case <-ctx.Done():
			return nil, 130, true
		case <-time.After(g.Poll()):
		}
	}
}

func signalNumber(s os.Signal) int {
	if n, ok := s.(syscall.Signal); ok {
		return int(n)
	}
	return 0
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
	out := make([]string, 0, len(argv))
	for i, a := range argv {
		if i >= 12 {
			out = append(out, "…")
			break
		}
		if looksSecret(a) {
			a = "<redacted>"
		}
		if len(a) > 120 {
			a = a[:120] + "…"
		}
		out = append(out, a)
	}
	return out
}

func looksSecret(a string) bool {
	l := strings.ToLower(a)
	for _, k := range []string{"token=", "secret=", "password=", "passwd=", "apikey=", "api_key=", "authorization:", "bearer "} {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// start runs the command. Under a slot it inherits the slot's lock (fd 3), so the slot is held for as long as any
// process of its tree lives even if the gate is killed; it gets a process group of its own, cleared when the command
// exits — except at a terminal, where a group of its own would be stopped by SIGTTIN at the first read, so it stays in
// the gate's and the tree is followed by parentage instead. Without a slot (passed through, off, unslotted) it just
// runs; the slot above it, if any, clears it.
func (g *Gate) start(ctx context.Context, o Options, argv, env []string, slot *Slot, bg bool, sigs <-chan os.Signal) (int, error) {
	real := o.Real
	if real == "" {
		p, err := exec.LookPath(argv[0])
		if err != nil {
			g.say("%v", err)
			return 127, err
		}
		real = p
	}
	args := append([]string{real}, argv[1:]...)
	if bg && g.Taskpolicy != "" {
		args = append([]string{g.Taskpolicy, "-b"}, args...)
	}
	cmd := &exec.Cmd{Path: args[0], Args: args, Env: env, Dir: o.Dir, Stdin: stdinOr(o.Stdin), Stdout: outOr(o.Stdout, os.Stdout), Stderr: outOr(o.Stderr, os.Stderr)}
	// Output that is not a file goes through a pipe the command's leftovers keep open; Wait must not wait for them
	// (they are cleared right after, below).
	cmd.WaitDelay = 300 * time.Millisecond
	tty := slot != nil && g.IsTTY != nil && g.IsTTY()
	grouped := slot != nil && !tty
	if grouped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if slot != nil {
		cmd.ExtraFiles = []*os.File{slot.File()}
	}
	if err := cmd.Start(); err != nil {
		g.say("%v", err)
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return 127, err
		}
		return 126, err
	}
	pid := cmd.Process.Pid
	if slot != nil {
		if err := slot.SetCommand(pid); err != nil {
			g.say("holder record: %v", err)
		}
	}
	var followed *tree
	var tick <-chan time.Time
	if tty {
		followed = newTree(pid, g.Procs)
		t := time.NewTicker(g.TreeEvery)
		defer t.Stop()
		tick = t.C
	}
	target := pid
	if grouped {
		target = -pid
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case sig := <-sigs:
			if s, ok := sig.(syscall.Signal); ok {
				// At a terminal the child shares the foreground group, which the terminal already signals for Ctrl-C and a
				// hangup: passing those on again would run its handlers twice. SIGTERM, which no terminal sends, is passed.
				if tty && (s == syscall.SIGINT || s == syscall.SIGHUP) {
					continue
				}
				_ = syscall.Kill(target, s)
			}
		case <-tick:
			followed.scan()
		case err := <-done:
			code := exitCode(err, cmd)
			switch {
			case grouped:
				g.reap(pid)
			case followed != nil:
				followed.reap(g.KillGrace)
			}
			return code, nil
		case <-ctx.Done():
			ctx = context.Background() // once: pass the cancel on as SIGTERM, then keep waiting for the command
			_ = syscall.Kill(target, syscall.SIGTERM)
		}
	}
}

func stdinOr(r io.Reader) io.Reader {
	if r == nil {
		return os.Stdin
	}
	return r
}

func outOr(w io.Writer, def *os.File) io.Writer {
	if w == nil {
		return def
	}
	return w
}

func exitCode(err error, cmd *exec.Cmd) int {
	if cmd != nil && cmd.ProcessState != nil {
		if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return cmd.ProcessState.ExitCode() // also when Wait gave up on the output pipes (ErrWaitDelay)
	}
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// reap clears what is left in the command's process group: SIGTERM, KillGrace to go, SIGKILL for the rest. The slot
// is released only after this, so a job's leftovers (vitest workers, a dev server) never run outside a slot.
func (g *Gate) reap(pgid int) {
	if !groupAlive(pgid) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(g.KillGrace)
	for time.Now().Before(deadline) {
		if !groupAlive(pgid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	for i := 0; i < 100 && groupAlive(pgid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
}

func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func stdinIsTTY() bool {
	_, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	return err == nil
}

// freeBytes is the space available to this user on the volume of path.
func freeBytes(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
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
	h, l := g.pool(ClassHeavy), g.pool(ClassLight)
	return Status{Heavy: h.Held(), Light: l.Held(), WaitHeavy: h.Waiting(), WaitLight: l.Waiting(), Suspect: append(h.Suspect(), l.Suspect()...)}
}

// DirOf is the directory the gate keeps its state in.
func (g *Gate) DirOf() string { return filepath.Clean(g.Cfg.Dir) }
