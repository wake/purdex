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
