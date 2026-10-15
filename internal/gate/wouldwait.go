package gate

import "time"

// WouldWait answers, for a hook that decides whether to move an agent's Bash call to the background: if a command of this
// class asked for a slot now, would it wait, and for what? It is cheap on purpose (a hook asks it for every heavy call):
// it reads the slot records and the queue directory, the memory-pressure level (one sysctl) and the rate from the samples
// other gates already cached. It starts no process, asks no daemon, and takes nothing.

// WouldWait is the answer.
type WouldWait struct {
	Wait   bool   `json:"would_wait"`
	Reason string `json:"reason,omitempty"` // slots (all taken) | queue (a slot is free but others are in front) | pressure
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

// Peek is the pressure from what is at hand: the level from the kernel now, the rate from the cached samples when the
// newest is not older than staleLimit (otherwise the rate is taken as 0). It never runs vm_stat and never writes.
func (r *HostReader) Peek() (Pressure, bool) {
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

// WouldWait looks at the class now.
func (g *Gate) WouldWait(class Class) WouldWait {
	cfg := g.Config()
	w := WouldWait{Class: class}
	if !cfg.Enabled || !class.Valid() {
		return w
	}
	pool := g.pool(cfg, class)
	held := pool.Held()
	queued := pool.PeekWaiting() // counts the live tickets and removes nothing
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
