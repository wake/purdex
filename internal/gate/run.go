package gate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// pressureFailures is how many reads in a row may fail before the gate falls back to the level alone, and the next
// failure of the level itself before it admits.
const pressureFailures = 3

// wait queues for a slot and returns it. done is true when the run ends here (an interrupt while waiting, or a broken
// gate directory, with the exit code); a nil slot with done false means the directory is denied (a sandbox) and the
// command runs without a slot. The settings are read again at every look, so a waiting command sees a change.
func (g *Gate) wait(ctx context.Context, cfg Config, class Class, o Options, sigs <-chan os.Signal, d *Decision) (*Slot, int, bool) {
	broken := func(err error) (*Slot, int, bool) {
		if errors.Is(err, ErrUnavailable) {
			g.say("%v; running without a slot", err)
			return nil, 0, false
		}
		g.say("%v: the command was not run (fix %s)", err, cfg.Dir)
		return nil, ExitRefused, true
	}
	// The ticket lives in one directory; a setting that changes gate.dir while this command waits is for the next command.
	dir := cfg.Dir
	ticket, err := g.pool(cfg, class).EnqueueRanked(g.waiterInfo(o), g.bornRank(o))
	if err != nil {
		return broken(err)
	}
	defer ticket.Drop()
	// Whoever called this command is the one who waits for its output. When that process is gone (this one is handed to init
	// or to another reaper) nobody will read what it prints, and a slot taken then would run a whole suite for no one, so the
	// ticket is given up. A command that was started with no parent to lose (its parent was init already), or that was told
	// to outlive its caller, keeps waiting.
	parent := g.parentPID()
	keepOrphan := !orphanCheckOn(o.Env)
	var good *Pressure
	fails := 0
	warned := false
	lastPlace := time.Time{}
	began := g.Now()         // when the wait began: WaitMax counts from here, whatever the first look took
	waitStart := time.Time{} // set at the first look that did not get a slot
	var (
		pollAt   time.Time              // when the previous poll looked
		lastRank Rank      = RankNormal // the rank the last line was printed for
		prevHold bool                   // the previous poll found the ticket on hold: the time since then is held time
		heldFor  time.Duration
		// the same for a conflict rule that kept it waiting (spec §5.3)
		prevBlocked bool
		blockedFor  time.Duration
		lastBlock   string // the rule and holder the last line was printed for
	)
	for {
		if !keepOrphan && parent > 1 && g.parentPID() != parent {
			g.say("the process that ran this command is gone: it is not queued any more, and was not run")
			d.Outcome = "orphaned"
			return nil, ExitOrphaned, true
		}
		cfg = g.effective(g.refresh()) // the holds of the local wwd are looked at only here, where the command really asks for a slot
		cfg.Dir = dir
		ok := true
		paused := cfg.Slots.Paused(class) // a class with no slot: it waits for the count to be raised, whatever the host does
		if g.Pressure != nil && cfg.Admission.Enabled && !paused {
			p, err := g.Pressure.Read()
			switch {
			case err == nil:
				fails, good = 0, &p
				ok = cfg.Admission.Admit(p)
			default:
				fails++
				switch {
				case cfg.Admission.OnUnreadable == "admit" || fails >= pressureFailures:
					if !warned {
						g.say("memory pressure unreadable %d times (%v); admitting", fails, err)
						warned = true
					}
				case good != nil:
					ok = cfg.Admission.Admit(*good) // keep the last good reading until it has failed three times
				default:
					ok = false
				}
			}
		}
		holdName, holding := g.holdView(cfg.Dir).Waits(class) // a hold of the local wwd keeps a new command waiting (holdview.go)
		if holding && !paused {
			ok = false
		}
		ahead, aheadFront := 0, 0
		var rank RankInfo
		var blocked *ConflictBlock
		pool := g.pool(cfg, class)
		if ok {
			tk, err := pool.TryTakeRanked(ticket)
			if err != nil {
				return broken(err)
			}
			if tk.Slot != nil && o.WaitMax > 0 && g.Now().Sub(began) > o.WaitMax {
				tk.Slot.Release(nil) // got after the deadline (the look itself waited for the admit lock): it is not run
				ticket.Drop()
				g.say("not admitted within %s: the command was not run", o.WaitMax)
				d.Outcome = "wait-max"
				return nil, ExitRefused, true
			}
			if tk.Slot != nil {
				ticket.Drop()
				if prevHold && !pollAt.IsZero() {
					heldFor += g.Now().Sub(pollAt) // the last stretch: held at the previous poll, served now
				}
				if prevBlocked && !pollAt.IsZero() {
					blockedFor += g.Now().Sub(pollAt)
				}
				d.Rank, d.HeldS, d.ConflictS = rankName(tk.Rank), heldFor.Seconds(), blockedFor.Seconds()
				if !waitStart.IsZero() {
					d.WaitS = g.Now().Sub(waitStart).Seconds()
					g.say("%s", grantedLine(class, g.Now().Sub(waitStart)))
				}
				return tk.Slot, 0, false
			}
			ahead, aheadFront, rank, blocked = tk.Ahead, tk.AheadFront, tk.Rank, tk.Blocked
		} else {
			rank = rankOfTicket(pool.peekSnapshot(), ticket.Name) // a poll the pressure check keeps from asking still reads its rank
		}
		now := g.Now()
		// The time the ticket spent on hold: each interval between two polls counts when the earlier poll found it held (a hold
		// during a pressure wait, an expiry, a release and a new hold all add up the same way).
		if prevHold && !pollAt.IsZero() {
			heldFor += now.Sub(pollAt)
		}
		if prevBlocked && !pollAt.IsZero() {
			blockedFor += now.Sub(pollAt)
		}
		pollAt, prevHold, prevBlocked = now, rank.Rank == RankHold, blocked != nil
		d.Rank, d.HeldS, d.ConflictS = rankName(rank), heldFor.Seconds(), blockedFor.Seconds()
		blockKey := ""
		if blocked != nil {
			blockKey = strings.Join([]string{blocked.With, string(blocked.Rule.Class), blocked.Rule.A, blocked.Rule.B, blocked.Rule.Reason}, "|")
			d.Conflict = blocked.Rule.A + " × " + blocked.Rule.B
			d.why("conflict") // recorded beside whatever else keeps it waiting too
		}
		if rank.Rank == RankHold {
			d.why("held") // recorded whatever else keeps it waiting too: held_s is summed for it
		}
		switch {
		case paused:
			d.why("paused")
		case holding:
			d.why("hold:" + holdName)
		case rank.Rank == RankHold:
		case ok:
			d.why("queue")
		case good == nil:
			d.why("pressure-unreadable")
		default:
			d.why("memory-pressure")
		}
		if waitStart.IsZero() {
			waitStart = now
		}
		if rank.Rank != lastRank || blockKey != lastBlock { // moved in the queue, or a rule now holds it (or lets go): say so now
			lastRank, lastBlock, lastPlace = rank.Rank, blockKey, time.Time{}
		}
		if lastPlace.IsZero() || now.Sub(lastPlace) >= time.Minute {
			lastPlace = now
			switch {
			case paused:
				g.say("%s", pausedLine(class))
			case holding:
				g.say("%s", holdLine(class, holdName))
			case rank.Rank == RankHold:
				g.say("%s", heldLine(class, rank, now))
			case blocked != nil:
				g.say("%s", conflictLine(blocked))
			case ok:
				holders := pool.Held()
				g.say("%s", withFrontNote(waitingLine(class, ahead+len(holders), holders, now), aheadFront)) // everything in front of it: the running and the queued
			default:
				g.say("%s", pressureLine(lastPressure(good), cfg.Admission, good != nil))
			}
		}
		sleep := g.Poll()
		if o.WaitMax > 0 {
			left := o.WaitMax - now.Sub(began)
			if left <= 0 {
				g.say("not admitted within %s: the command was not run", o.WaitMax)
				d.Outcome = "wait-max"
				return nil, ExitRefused, true
			}
			sleep = min(sleep, left)
		}
		select {
		case sig := <-sigs:
			return nil, 128 + signalNumber(sig), true
		case <-ctx.Done():
			return nil, 130, true
		case <-time.After(sleep):
		}
	}
}

// treeBusy says whether anything below root holds a slot or waits for one: a late slot for the root would count the same work
// twice (and root would hold one while its own child waits for another, which is what #2985 was).
func (g *Gate) treeBusy(cfg Config, root int) bool {
	tab, err := g.Procs()
	if err != nil {
		return false
	}
	below := descendants(tab, root)
	if len(below) == 0 {
		return false
	}
	for _, c := range []Class{ClassHeavy, ClassLight} {
		p := g.pool(cfg, c)
		for _, h := range p.Held() {
			if _, ok := below[h.PID]; ok {
				return true
			}
			if _, ok := below[h.PGID]; ok && h.PGID > 1 {
				return true
			}
		}
		for _, w := range p.Waiters() {
			if _, ok := below[w.PID]; ok {
				return true
			}
		}
	}
	return false
}

// underLate says whether this process runs below a command that was put under a slot late: such a command has no environment
// claim to hand down (it started before it had a slot), so the record's pid is the proof. What runs below it is covered by its slot.
func (g *Gate) underLate(cfg Config) bool {
	tab, err := g.Procs()
	if err != nil {
		return false
	}
	up := ancestors(tab, g.PID())
	if len(up) == 0 {
		return false
	}
	for _, c := range []Class{ClassHeavy, ClassLight} {
		for _, h := range g.pool(cfg, c).Held() {
			if !h.Late || h.PGID <= 1 || h.Orphaned {
				continue
			}
			for _, a := range up {
				if a == h.PGID {
					return true
				}
			}
		}
	}
	return false
}

// Watch kinds: what the gate does with a command that takes no slot but that it runs itself instead of handing over.
const (
	watchPackaging = "packaging" // a packaging script: written down, and put under a slot when it runs long
	watchUnknown   = "unknown"   // a script of the project's own that no rule knows: only timed
)

// watchKind says whether the gate runs this command itself, though it takes no slot. "" for all the rest, which the gate lets
// through (the shims exec them without making a gate at all).
func (c Config) watchKind(argv []string, e Env) string {
	if !c.Enabled {
		return ""
	}
	cls, why := c.Intercept.ClassifyReason(argv, e)
	if cls.Valid() {
		return ""
	}
	switch {
	case why == ReasonPackaging:
		return watchPackaging
	case why == ReasonUnknown && c.Packaging.RecordUnknown > 0 && len(argv) > 0:
		switch filepath.Base(argv[0]) {
		case "pnpm", "npm", "yarn", "bun": // a script of the project, not a tool run through the package manager
			if len(argv) > 1 {
				switch argv[1] {
				case "exec", "x", "dlx", "create", "init":
					return ""
				}
			}
			return watchUnknown
		}
	}
	return ""
}

// WatchKind is watchKind for a caller outside the package (the shim path, which has to decide before it makes a gate).
func (c Config) WatchKind(argv []string, e Env) string { return c.watchKind(argv, e) }

// parentPID is the parent of this process now; 0 when the gate has no way to ask.
func (g *Gate) parentPID() int {
	if g.Getppid == nil {
		return 0
	}
	return g.Getppid()
}

// orphanCheckOn says whether a waiting command is dropped when its caller is gone: yes unless KeepOrphanEnv says no.
func orphanCheckOn(env []string) bool {
	if env == nil {
		env = os.Environ()
	}
	v := envValue(env, KeepOrphanEnv)
	return v == "" || v == "0"
}

// lastPressure is the last good reading, or the zero Pressure when there has not been one.
func lastPressure(p *Pressure) Pressure {
	if p == nil {
		return Pressure{}
	}
	return *p
}

func signalNumber(s os.Signal) int {
	if n, ok := s.(syscall.Signal); ok {
		return int(n)
	}
	return 0
}

// start runs the command. Under a slot it inherits the slot's lock (fd 3), so the slot is held for as long as any
// process of its tree lives even if the gate is killed; it gets a process group of its own, cleared when the command
// exits — except at a terminal, where a group of its own would be stopped by SIGTTIN at the first read, so it stays in
// the gate's and the tree is followed by parentage instead. Without a slot (passed through, off, unslotted) it just
// runs; the slot above it, if any, clears it.
// lateWatch is what start is told about a command that takes no slot at its start but may be put under one later (packaging):
// after is how long it must have run, class the pool the late slot comes from. A nil lateWatch is a command that is only run.
type lateWatch struct {
	after time.Duration
	class Class
}

// lateRecheck is how long start waits to look again when a late slot was due and the command's tree held or waited for a slot.
var lateRecheck = 5 * time.Second

func (g *Gate) start(ctx context.Context, cfg Config, o Options, argv, env []string, slot *Slot, sigs <-chan os.Signal, lw *lateWatch, d *Decision) (int, error) {
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
	if slot != nil {
		args = cfg.Priority.Wrap(args, g.Tools, cfg.foreground(o))
	}
	cmd := &exec.Cmd{Path: args[0], Args: args, Env: env, Dir: o.Dir, Stdin: stdinOr(o.Stdin), Stdout: outOr(o.Stdout, os.Stdout), Stderr: outOr(o.Stderr, os.Stderr)}
	// Output that is not a file goes through a pipe the command's leftovers keep open; Wait must not wait for them
	// (they are cleared right after, below).
	cmd.WaitDelay = 300 * time.Millisecond
	// Whether the terminal already signals the command (Ctrl-C reaches its group) has nothing to do with the command having a slot.
	// A command that may be put under a slot later is made ready at its start: it gets its own process group (off a terminal) and
	// its tree is followed from the first moment, so the clean-up at its end covers what it started before the slot came.
	tty := g.IsTTY != nil && g.IsTTY()
	watching := lw != nil && lw.after > 0
	tracked := slot != nil || watching
	grouped := tracked && !tty
	if watching {
		// A gate below this command must not take a late slot of its own for the same tree (#3031): this one will, for all of it.
		// The mark is given now, not when the slot is taken, because what the command starts before that must already see it.
		cmd.Env = withEnv(env, LateRootEnv, strconv.Itoa(g.PID()))
	}
	if grouped {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if slot != nil {
		cmd.ExtraFiles = []*os.File{slot.File()}
	}
	nonce := envValue(env, AdmittedMarkEnv)
	if nonce != "" {
		cmd.Env = withoutEnv(cmd.Env, AdmittedMarkEnv)         // the command must not learn the nonce
		_, _ = io.WriteString(cmd.Stderr, AdmittedMark(nonce)) // before the start: nothing the command prints can come first
	}
	if err := cmd.Start(); err != nil {
		if nonce != "" {
			_, _ = io.WriteString(cmd.Stderr, NotStartedMark(nonce))
		}
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
	// The command's tree is followed by parentage as well as (off a terminal) by its process group: a child that left the
	// group with setsid is still a child of the command while the command runs, and is remembered for the clean-up.
	// What cannot be caught: a child that is forked, leaves the group and is orphaned within one TreeEvery of the last look.
	var followed *tree
	var tick <-chan time.Time
	if tracked {
		followed = newTree(pid, g.Procs)
		if !followed.known() {
			g.say("the process table could not be read: what %s leaves running at the end will not be cleared", argv[0])
		}
		t := time.NewTicker(g.TreeEvery)
		defer t.Stop()
		tick = t.C
	}
	var lateC <-chan time.Time
	var lateSlot *Slot
	began := g.Now()
	if watching {
		t := time.NewTimer(lw.after)
		defer t.Stop()
		lateC = t.C
		defer func() {
			if lateSlot != nil {
				if left := lateSlot.Release(g.Holders); len(left) > 0 {
					g.say("these processes escaped the clean-up and are still running: %s; the %s slot is released", describeStragglers(left), lateSlot.Class)
				}
			}
		}()
	}
	var longRun <-chan time.Time
	if slot != nil && cfg.Lifetime.AlertAfter > 0 {
		t := time.NewTimer(cfg.Lifetime.AlertAfter)
		defer t.Stop()
		longRun = t.C
	}
	target := pid
	if grouped {
		target = -pid
	}
	grace := cfg.Reap.Grace
	if g.KillGrace > 0 {
		grace = g.KillGrace
	}
	var killAt <-chan time.Time // set when the context is cancelled: SIGTERM now, SIGKILL when the grace is over
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
		case <-longRun:
			g.say("%s has held its %s slot for %s (it is not stopped; see `pdx gate status`)", argv[0], slot.Class, cfg.Lifetime.AlertAfter)
		case <-lateC:
			lateC = nil
			if g.treeBusy(cfg, pid) {
				lateC = time.After(lateRecheck) // it holds or waits for a slot below: not a second one for the same work
				continue
			}
			// the settings as they are now: a class set to 0 (or raised from it) since the command started decides this claim, not the
			// ones the command was started under (W3c)
			s, err := g.pool(g.effective(g.refresh()), lw.class).TakeLate(g.waiterInfo(o))
			switch {
			case err != nil:
				g.say("late slot: %v (the command goes on without one)", err)
			case s == nil:
				g.say("late slot: no room beyond the %s pool, or the class is paused (the command goes on without one)", lw.class)
			default:
				h := g.holder(o, g.Token())
				h.Late, h.PGID = true, pid
				if err := s.SetHolder(h); err != nil {
					g.say("late slot record: %v (the command goes on without one)", err)
					s.Release(nil)
					break
				}
				lateSlot = s
				if d != nil {
					d.LateSlot, d.LateAfterS = true, g.Now().Sub(began).Seconds()
				}
				g.say("%s has run for %s: it now holds a %s slot until it ends (it is not stopped; people behind it queue)", argv[0], lw.after, lw.class)
			}
		case err := <-done:
			code := exitCode(err, cmd)
			d.setUsage(cmd)
			if cfg.Reap.Enabled && (slot != nil || lateSlot != nil) {
				if grouped {
					g.reap(pid, grace)
				}
				if followed != nil {
					followed.reap(grace) // what left the group, or all of it at a terminal
				}
			}
			return code, nil
		case <-ctx.Done():
			ctx = context.Background()                // once
			_ = syscall.Kill(target, syscall.SIGTERM) // the cancel is passed on as SIGTERM…
			killAt = time.After(grace)                // …and a command that ignores it does not keep the run (and its slot) for ever
		case <-killAt:
			killAt = nil
			_ = syscall.Kill(target, syscall.SIGKILL)
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
	return 1
}

// reap clears what is left in the command's process group: SIGTERM, a grace period, SIGKILL for the rest. The slot
// is released only after this, so a job's leftovers (vitest workers, a dev server) never run outside a slot.
func (g *Gate) reap(pgid int, grace time.Duration) {
	if !groupAlive(pgid) {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	deadline := time.Now().Add(grace)
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

// waiterInfo is what the gate says about the command it runs, for its ticket and for the record of the slot it takes (a late claim
// too): the same argv in both, so whoever takes the admit lock next sees what holds the slot.
func (g *Gate) waiterInfo(o Options) WaiterInfo {
	return WaiterInfo{PID: g.PID(), Argv: truncateArgv(o.Argv), Cwd: g.cwdOf(o), Session: g.Session, Tag: tagOf(o)}
}
