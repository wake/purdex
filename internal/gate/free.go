package gate

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"time"

	agentpkg "github.com/wake/purdex/internal/agent"
)

// Why lsof is enough: a flock belongs to the open file description, which a command's children share (fd 3), so a process that still has
// the lock file open and descends from the command is what holds the slot. A process that opens the file on its own is not told apart
// (the residual risk); the only ones that do are pdx's own short probes (gate status, the daemon's read), which are in protectedCmd, and
// the person's answer to the listing is the last guard.
//
// pdx gate free <class>-<k> (host-load-control spec §12.5, plan W4): end the processes that hold a slot's lock, for a slot that stays
// held after its gate is gone (or, with force, one whose gate is alive). The processes are those that have the slot's lock file open
// (lsof); each gets SIGTERM, and SIGKILL only if it is alive after the grace and still holds the lock; then the slot is looked at again.

// FreeGrace is how long a process has to end after SIGTERM before SIGKILL.
const FreeGrace = 5 * time.Second

// FreeEnv is what Free needs of the host; a test replaces it.
type FreeEnv struct {
	Holders func(path string) []Straggler // the processes that have the lock file open
	Signal  func(pid int, sig syscall.Signal) error
	Alive   func(pid int) bool
	Wait    func(d time.Duration)
	// StartTime is the text that tells a process from a later one with the same pid (nil: not checked).
	StartTime func(pid int) (string, error)
	Self      int // never signalled
}

// DefaultFreeEnv is the real host.
func DefaultFreeEnv() FreeEnv {
	return FreeEnv{Holders: lsofHolders, Signal: func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		Alive: pidAlive, Wait: time.Sleep, StartTime: agentpkg.ProcessStartText, Self: os.Getpid()}
}

var (
	// ErrSlotFree: nothing holds that slot.
	ErrSlotFree = errors.New("that slot is not held")
	// ErrSlotChanged: the slot is held by another command than the one that was shown.
	ErrSlotChanged = errors.New("the slot changed while it was being looked at")
	// ErrGateAlive: the gate that took the slot is alive, so the command is running as it should; interrupt it instead (or force).
	ErrGateAlive = errors.New("its gate is alive")
)

var slotNameRe = regexp.MustCompile(`^(heavy|light)-([0-9]{1,3})$`)

// ParseSlot reads "heavy-2" into the class and the slot number.
func ParseSlot(s string) (Class, int, error) {
	m := slotNameRe.FindStringSubmatch(s)
	if m == nil {
		return "", 0, fmt.Errorf("%q is not a slot: <heavy|light>-<number>, e.g. heavy-1", s)
	}
	k, _ := strconv.Atoi(m[2])
	if k < 1 || k > maxSlotNumber {
		return "", 0, fmt.Errorf("slot number %d is outside 1..%d", k, maxSlotNumber)
	}
	return Class(m[1]), k, nil
}

// FreeTarget is what freeing a slot would do: the record of the slot, whether its gate is alive, and the processes that hold the lock.
type FreeTarget struct {
	Holder    Holder
	GateAlive bool
	Locked    []Straggler
	// Skipped are the processes that only hold the lock because they inherited the file (a tmux server, a shell, the daemon): they are
	// shown and never ended, since ending one ends far more than the command.
	Skipped []Straggler
	// starts are the start times of the Locked processes when listed.
	starts map[int]string
}

// protectedCmd says a process name is one that serves more than the command that held the slot: ending it would end the person's
// terminals or sessions. Such a process can hold the lock only by inheriting the file.
func protectedCmd(cmd string) bool {
	switch cmd {
	case "tmux", "tmux: server", "tmux: client", "zsh", "bash", "sh", "fish", "dash", "ksh", "login", "sshd", "launchd", "init", "systemd", "pdx":
		return true
	}
	return false
}

// PlanFree looks at the slot class-k.
func PlanFree(cfg Config, env FreeEnv, class Class, k int) (FreeTarget, error) {
	p := &Pool{Dir: cfg.Dir, Class: class, Size: cfg.Slots.Size(class)}
	for _, h := range p.Held() {
		if h.Slot != k {
			continue
		}
		t := FreeTarget{Holder: h, GateAlive: h.PID > 0 && env.Alive(h.PID)}
		t.starts = map[int]string{}
		for _, s := range env.Holders(p.slotPath(k)) {
			switch {
			case s.PID <= 1 || s.PID == env.Self:
			case protectedCmd(s.Cmd):
				t.Skipped = append(t.Skipped, s)
			default:
				t.Locked = append(t.Locked, s)
				if env.StartTime != nil {
					if st, err := env.StartTime(s.PID); err == nil {
						t.starts[s.PID] = st
					}
				}
			}
		}
		return t, nil
	}
	return FreeTarget{}, ErrSlotFree
}

// FreeResult is what Free did and what is left.
type FreeResult struct {
	Termed []int       // got SIGTERM
	Killed []int       // still alive after the grace and still holding the lock: got SIGKILL
	Free   bool        // the slot is free now
	Left   []Straggler // the processes that still hold the lock when it is not free
}

// Free ends the processes of t. Only the processes that were listed in t and still hold the lock are signalled; a pid that holds the
// lock now but was not listed is not touched.
func Free(cfg Config, env FreeEnv, class Class, k int, t FreeTarget) (FreeResult, error) {
	p := &Pool{Dir: cfg.Dir, Class: class, Size: cfg.Slots.Size(class)}
	path := p.slotPath(k)
	// the slot must still be the one that was shown: another command may have taken it while the question was asked
	if now, err := PlanFree(cfg, env, class, k); err != nil || now.Holder.PID != t.Holder.PID || !now.Holder.Started.Equal(t.Holder.Started) {
		return FreeResult{}, ErrSlotChanged
	}
	sameProcess := func(pid int) bool { // the pid is still the process that was listed (not a later one that got the number)
		if env.StartTime == nil {
			return true
		}
		want, ok := t.starts[pid]
		if !ok {
			return false // its start time was not known at the plan: not signalled
		}
		got, err := env.StartTime(pid)
		return err == nil && got == want
	}
	listed := map[int]string{} // pid → the command name it had at the plan
	for _, s := range t.Locked {
		listed[s.PID] = s.Cmd
	}
	holdingNow := func() map[int]bool {
		out := map[int]bool{}
		for _, s := range env.Holders(path) {
			// the same command as at the plan, and not one that serves more than the command (a process that exec'd into a shell or
			// tmux while the question was asked keeps its pid and start time, not its name)
			if cmd, ok := listed[s.PID]; ok && s.Cmd == cmd && !protectedCmd(s.Cmd) && s.PID > 1 && s.PID != env.Self {
				out[s.PID] = true
			}
		}
		return out
	}
	var res FreeResult
	for pid := range holdingNow() {
		if !sameProcess(pid) {
			continue
		}
		if env.Signal(pid, syscall.SIGTERM) == nil {
			res.Termed = append(res.Termed, pid)
		}
	}
	for waited := time.Duration(0); waited < FreeGrace; waited += 250 * time.Millisecond {
		alive := false
		for _, pid := range res.Termed {
			if env.Alive(pid) {
				alive = true
			}
		}
		if !alive {
			break
		}
		env.Wait(250 * time.Millisecond)
	}
	// SIGKILL only for those still alive that still hold the lock (a pid that ended, and was reused, is not on the list now)
	still := holdingNow()
	for _, pid := range res.Termed {
		if still[pid] && env.Alive(pid) && sameProcess(pid) {
			if env.Signal(pid, syscall.SIGKILL) == nil {
				res.Killed = append(res.Killed, pid)
			}
		}
	}
	if len(res.Killed) > 0 {
		env.Wait(250 * time.Millisecond)
	}
	if !slotLocked(p, k) {
		res.Free = true
		return res, nil
	}
	res.Left = env.Holders(path)
	return res, nil
}

// slotLocked says the slot's lock is held by somebody (probing it takes and drops it).
func slotLocked(p *Pool, k int) bool {
	f, err := os.OpenFile(p.slotPath(k), os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	st, _ := flockNB(f)
	return st != lockGot
}
