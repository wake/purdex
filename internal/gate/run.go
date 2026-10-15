package gate

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
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
	ticket, err := g.pool(cfg, class).EnqueueFor(WaiterInfo{PID: g.PID(), Argv: truncateArgv(o.Argv), Cwd: g.cwdOf(o), Session: g.Session})
	if err != nil {
		return broken(err)
	}
	defer ticket.Drop()
	var good *Pressure
	fails := 0
	warned := false
	lastPlace := time.Time{}
	waitStart := time.Time{} // set at the first look that did not get a slot
	for {
		cfg = g.refresh()
		cfg.Dir = dir
		ok := true
		if g.Pressure != nil && cfg.Admission.Enabled {
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
		ahead := 0
		if ok {
			slot, a, err := g.pool(cfg, class).TryTake(ticket)
			if err != nil {
				return broken(err)
			}
			if slot != nil {
				ticket.Drop()
				if !waitStart.IsZero() {
					d.WaitS = g.Now().Sub(waitStart).Seconds()
					g.say("%s", grantedLine(class, g.Now().Sub(waitStart)))
				}
				return slot, 0, false
			}
			ahead = a
		}
		now := g.Now()
		switch {
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
		if lastPlace.IsZero() || now.Sub(lastPlace) >= time.Minute {
			lastPlace = now
			if ok {
				holders := g.pool(cfg, class).Held()
				g.say("%s", waitingLine(class, ahead+len(holders), holders, now)) // everything in front of it: the running and the queued
			} else {
				g.say("%s", pressureLine(lastPressure(good), cfg.Admission, good != nil))
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
func (g *Gate) start(ctx context.Context, cfg Config, o Options, argv, env []string, slot *Slot, sigs <-chan os.Signal) (int, error) {
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
	// The command's tree is followed by parentage as well as (off a terminal) by its process group: a child that left the
	// group with setsid is still a child of the command while the command runs, and is remembered for the clean-up.
	// What cannot be caught: a child that is forked, leaves the group and is orphaned within one TreeEvery of the last look.
	var followed *tree
	var tick <-chan time.Time
	if slot != nil {
		followed = newTree(pid, g.Procs)
		if !followed.known() {
			g.say("the process table could not be read: what %s leaves running at the end will not be cleared", argv[0])
		}
		t := time.NewTicker(g.TreeEvery)
		defer t.Stop()
		tick = t.C
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
		case err := <-done:
			code := exitCode(err, cmd)
			if cfg.Reap.Enabled {
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
