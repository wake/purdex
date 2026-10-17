package gate

import "time"

// WouldWait answers, for a hook that decides whether to move an agent's Bash call to the background: if a command of this
// class asked for a slot now, would it wait, and for what? It is cheap on purpose (a hook asks it for every heavy call): it
// reads the slot records and the queue directory, the memory-pressure level (one sysctl) and the rate from the samples
// other gates already cached. It starts no process, asks no daemon, and takes no slot.
//
// It is a hint, not a promise: it reads the slots, the queue and the pressure one after the other, so a gate that takes or
// releases a slot at that moment can make it wrong in either direction. The gate decides when the command really runs; a
// wrong answer costs a call that stays in the foreground (and is queued there) or one that goes to the background and
// starts at once. The probes try a lock without waiting and drop it at once, as `pdx gate status` does.

// WouldWait is the answer.
type WouldWait struct {
	Wait   bool   `json:"would_wait"`
	Reason string `json:"reason,omitempty"` // slots (all taken) | queue (a slot is free but others are in front) | pressure | paused (the class has no slot: slots.<class> = 0)
	Class  Class  `json:"class"`
	// Ahead counts the commands in front: the running ones and the queued ones.
	Ahead int `json:"ahead"`
	// HeldBy is the oldest holder, in words (command, project, age); empty when nothing holds the class.
	HeldBy string `json:"held_by,omitempty"`
	// Note is one line an agent can read: who has the slot, for how long, the place, and that the wait is normal.
	Note string `json:"note,omitempty"`
}

// PressurePeeker is a pressure reader that can answer without sampling (HostReader does).
type PressurePeeker interface {
	Peek() (Pressure, bool)
}

// Peek is the pressure from what is at hand. A cache that Read would reuse (younger than sampleReuse) gives exactly
// what Read gives. Otherwise: the level from the kernel now, and the rate from the cached samples when the newest is not
// older than staleLimit (without one the rate is unknown and counts as 0: the answer is then only a hint, and the gate
// itself still decides when the command runs). It never runs vm_stat and never writes.
func (r *HostReader) Peek() (Pressure, bool) {
	if p, ok := r.fresh(); ok {
		return p, true
	}
	level, lerr := r.Level()
	f := r.load()
	n := len(f.Samples)
	var p Pressure
	have := false
	if n > 0 {
		last := f.Samples[n-1]
		if age := r.Now().Sub(time.UnixMilli(last.At)); age >= 0 && age <= staleLimit {
			p, have = pressureOf(f.Samples, last), true
		}
	}
	if lerr == nil {
		p.Level, have = level, true
	}
	return p, have
}

// WouldWaitFor is WouldWait for a command whose words are known: besides the capacity and the pressure, it waits when a holder of the
// class, or a front or normal ticket in the queue, runs a command a conflict rule keeps apart from this one (spec §5.3). Back and held
// tickets never make it wait. The rules and the queue are read as the other readers read them, without the lock and without removing
// anything: a hint, as WouldWait is.
func (g *Gate) WouldWaitFor(class Class, argv []string) WouldWait {
	return g.ConflictView(class).Apply(g.WouldWait(class), argv)
}

// ConflictView is one reading of what the conflict rules of a class need: the rules, the queue and the holders. Several commands of
// one class are answered from one view, so they cannot disagree with each other about the state they were looked at in.
type ConflictView struct {
	class   Class
	enabled bool
	rules   []Conflict
	queue   []QueueEntry
	holders []Holder
}

// ConflictView reads the rules, the queue and the holders of the class once (read-only, no lock, nothing removed).
func (g *Gate) ConflictView(class Class) ConflictView {
	cfg := g.Config()
	v := ConflictView{class: class, enabled: cfg.Enabled && class.Valid()}
	if !v.enabled {
		return v
	}
	pool := g.pool(cfg, class)
	if v.rules, _ = LoadConflicts(pool.Dir); len(v.rules) == 0 {
		return v // no rules: nothing else is read
	}
	v.queue, v.holders = pool.peekSnapshot().Queue, pool.Held()
	return v
}

// Apply adds the rules to an answer of WouldWait for the view's class: a command that would not wait for capacity or pressure waits
// when a rule holds it back (reason "conflict"), as a new normal ticket at the end of the normal group.
func (v ConflictView) Apply(w WouldWait, argv []string) WouldWait {
	if w.Wait || len(argv) == 0 || !v.enabled || len(v.rules) == 0 {
		return w
	}
	res := walkQueue(v.class, v.queue, v.holders, v.rules, 1<<20, "", nil, argv)
	if b := res.Blocked[""]; b != nil {
		w.Wait, w.Reason = true, "conflict"
		w.Note = conflictLine(b)
	}
	return w
}

// WouldWait looks at the class now.
func (g *Gate) WouldWait(class Class) WouldWait {
	cfg := g.Config()
	w := WouldWait{Class: class}
	if !cfg.Enabled || !class.Valid() {
		return w
	}
	pool := g.pool(cfg, class)
	held := pool.Held()
	queued := pool.peekSnapshot().AheadOfNew() // the live front and normal tickets: back and held ones are passed by a new command (spec 5.2); removes nothing
	w.Ahead = len(held) + queued
	now := g.Now()
	if len(held) > 0 {
		oldest := held[0]
		for _, h := range held[1:] {
			if !h.Started.IsZero() && (oldest.Started.IsZero() || h.Started.Before(oldest.Started)) {
				oldest = h
			}
		}
		w.HeldBy = holderLine(oldest, now)
	}
	free := cfg.Slots.Size(class) - len(held)
	switch {
	case cfg.Slots.Paused(class):
		w.Wait, w.Reason = true, "paused"
		w.Note = pausedLine(class)
		return w
	case free <= 0:
		w.Wait, w.Reason = true, "slots"
	case queued >= free:
		w.Wait, w.Reason = true, "queue"
	}
	if w.Wait {
		w.Note = waitingLine(class, w.Ahead, held, now)
		return w
	}
	if pk, ok := g.Pressure.(PressurePeeker); ok && cfg.Admission.Enabled {
		if p, have := pk.Peek(); have && !cfg.Admission.Admit(p) {
			w.Wait, w.Reason = true, "pressure"
			w.Note = pressureLine(p, cfg.Admission, true)
		}
	}
	return w
}
