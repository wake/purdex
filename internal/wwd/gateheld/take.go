package gateheld

import (
	"os"
	"sync"
	"time"

	"github.com/wake/purdex/internal/gate"
)

// Until WWD-6 moves the entry point, a job the worker runs holds one of the gate's heavy slots for as long as it runs (#3502): the
// gate and the worker then keep one account, and a heavy command that arrives while a job runs queues behind it instead of making
// one more heavy thing on the host. It is the other direction of HeavyFull, which makes the worker not claim when the gate is full.
//
// The slot is the gate's own: a flock on its lock file, taken by this process, with a record naming it as wwd's job, so `pdx gate
// status` and the watcher see whose it is. It lives as long as this process: the kernel drops the lock when wwd dies, however it
// dies, and a record left beside a free lock is stale and ignored by every gate reader, so a killed wwd frees its slots without anyone
// cleaning up. The WWD-6 cut-over and the removal of the gate (WWD-9) take this out together with HeavyFull.

// TakeStatus says how a TakeHeavy came out.
type TakeStatus int

const (
	// NoGate: this host has no gate (or it is off). Nothing is held and the worker goes on without a slot.
	NoGate TakeStatus = iota
	// Taken: a slot is held; give it back with Release when the job is over.
	Taken
	// Full: the gate has no free heavy slot (all held, a command is waiting ahead, or the heavy class is paused). The worker claims nothing.
	Full
	// HeldByHost: the gate would hold a new heavy command now because of the host (memory pressure or page rate over its admission lines, or the
	// free disk under admission.disk_min_gb). The worker claims nothing.
	HeldByHost
	// Unreadable: there is a gate and it cannot be read or used. Counted as full, like HeavyFull does.
	Unreadable
)

// Held is a heavy slot of the gate held for a job.
type Held struct {
	mu   sync.Mutex
	slot *gate.Slot
}

// TakeHeavy takes a heavy slot without waiting. It queues like any command (a ticket behind the ones already waiting, dropped at
// once), so a command that has been waiting is served before it. nil with NoGate, Full or Unreadable.
func (r *Reader) TakeHeavy() (*Held, TakeStatus) {
	cfg, st := r.look()
	switch st {
	case gateAbsent:
		return nil, NoGate
	case gateUnreadable:
		return nil, Unreadable
	}
	p := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassHeavy, Size: cfg.Slots.Size(gate.ClassHeavy), Now: time.Now}
	if p.Size == 0 {
		r.clearLog()
		return nil, Full // paused
	}
	// The gate's own admission, the rule a heavy command meets before it gets a slot: the pressure and page-rate lines of
	// [gate.admission] as configured (they may be stricter than the zone the worker follows) and the free-disk floor. Running work is
	// never touched; this only keeps a new job from starting where the gate would hold a new command.
	if a := r.admission(cfg); a.Held {
		r.logHold(a.Reason)
		return nil, HeldByHost
	}
	r.clearHold()
	tk, err := p.EnqueueFor(gate.WaiterInfo{PID: os.Getpid(), Argv: []string{"wwd", "job"}})
	if err != nil {
		return nil, Unreadable
	}
	slot, _, err := p.TryTake(tk)
	tk.Drop()
	if err != nil {
		r.logf("wwd: the gate's heavy slot could not be taken, the job is not claimed: %v", err)
		return nil, Unreadable
	}
	r.clearLog()
	if slot == nil {
		return nil, Full
	}
	return &Held{slot: slot}, Taken
}

// logHold says once per change of reason that the gate's admission is holding the worker's new jobs.
func (r *Reader) logHold(why string) {
	r.mu.Lock()
	again := r.lastHold == why
	r.lastHold = why
	r.mu.Unlock()
	if !again {
		r.logf("wwd: the gate is holding new heavy work (%s), no job is claimed until it lets go", why)
	}
}

func (r *Reader) clearHold() {
	r.mu.Lock()
	r.lastHold = ""
	r.mu.Unlock()
}

// TakeHeavyLate takes a heavy slot for a command that is running already (a quarantined job's): the gate's own TakeLate, which does not
// ask whether the host admits more work and goes beyond the gate's size if it has to. NoGate: nothing to hold. Taken: the slot. Full:
// even the late slots are taken, or the heavy class is paused (a paused class takes no claim, late or over). Unreadable: the gate cannot
// be used (logged). The caller tries again for anything but Taken and NoGate.
func (r *Reader) TakeHeavyLate() (*Held, TakeStatus) {
	cfg, st := r.look()
	switch st {
	case gateAbsent:
		return nil, NoGate
	case gateUnreadable:
		return nil, Unreadable
	}
	p := &gate.Pool{Dir: cfg.Dir, Class: gate.ClassHeavy, Size: cfg.Slots.Size(gate.ClassHeavy), Now: time.Now}
	slot, err := p.TakeLate(gate.WaiterInfo{PID: os.Getpid(), Argv: []string{"wwd", "job"}})
	if err != nil {
		r.logf("wwd: a heavy slot for a quarantined job could not be taken: %v", err)
		return nil, Unreadable
	}
	if slot == nil {
		return nil, Full
	}
	return &Held{slot: slot}, Taken
}

// Describe writes whose the slot is: argv as `pdx gate status` shows it (wwd job <id> <task>), and the session if there is one. It only
// sets what the gate shows; a record that cannot be written leaves the placeholder the take wrote (this process and the moment).
func (h *Held) Describe(argv []string, session string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.slot == nil {
		return
	}
	cwd, _ := os.Getwd()
	_ = h.slot.SetHolder(gate.Holder{PID: os.Getpid(), Argv: argv, Cwd: cwd, Started: time.Now(), Session: session, Tag: "wwd"})
}

// Release gives the slot back (the record goes, then the lock). Calling it again, or on nil, does nothing.
func (h *Held) Release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	s := h.slot
	h.slot = nil
	h.mu.Unlock()
	s.Release(nil)
}
