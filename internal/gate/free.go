package gate

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

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
	Self    int // never signalled
}

// DefaultFreeEnv is the real host.
func DefaultFreeEnv() FreeEnv {
	return FreeEnv{Holders: lsofHolders, Signal: func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) },
		Alive: pidAlive, Wait: time.Sleep, Self: os.Getpid()}
}

var (
	// ErrSlotFree: nothing holds that slot.
	ErrSlotFree = errors.New("that slot is not held")
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
}

// PlanFree looks at the slot class-k.
func PlanFree(cfg Config, env FreeEnv, class Class, k int) (FreeTarget, error) {
	p := &Pool{Dir: cfg.Dir, Class: class, Size: cfg.Slots.Size(class)}
	for _, h := range p.Held() {
		if h.Slot != k {
			continue
		}
		t := FreeTarget{Holder: h, GateAlive: h.PID > 0 && !h.Orphaned && env.Alive(h.PID)}
		for _, s := range env.Holders(p.slotPath(k)) {
			if s.PID > 1 && s.PID != env.Self {
				t.Locked = append(t.Locked, s)
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
func Free(cfg Config, env FreeEnv, class Class, k int, t FreeTarget) FreeResult {
	p := &Pool{Dir: cfg.Dir, Class: class, Size: cfg.Slots.Size(class)}
	path := p.slotPath(k)
	listed := map[int]bool{}
	for _, s := range t.Locked {
		listed[s.PID] = true
	}
	holdingNow := func() map[int]bool {
		out := map[int]bool{}
		for _, s := range env.Holders(path) {
			if listed[s.PID] && s.PID > 1 && s.PID != env.Self {
				out[s.PID] = true
			}
		}
		return out
	}
	var res FreeResult
	for pid := range holdingNow() {
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
		if still[pid] && env.Alive(pid) {
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
		return res
	}
	res.Left = env.Holders(path)
	return res
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
