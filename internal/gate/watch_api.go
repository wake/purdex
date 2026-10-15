package gate

// What the daemon's load watcher reads of the gate (host-load-control spec §12). Neither forks nor waits: the slot records and
// the pid checks are file and signal reads; the lock lookup is an lsof the watcher asks for only about a slot that stays orphaned.

// HeldSlots are the held slots of both pools of the gate in cfg (Orphaned set where the gate that took the slot is gone), read
// straight from the directory.
func HeldSlots(cfg Config) (heavy, light []Holder) {
	p := func(c Class) *Pool { return &Pool{Dir: cfg.Dir, Class: c, Size: cfg.Slots.Size(c)} }
	return p(ClassHeavy).Held(), p(ClassLight).Held()
}

// LockPIDs are the processes that have the lock file of h's slot open (an lsof over the whole host: about 0.3 s of CPU).
func LockPIDs(cfg Config, h Holder) []int {
	p := &Pool{Dir: cfg.Dir, Class: h.Class, Size: cfg.Slots.Size(h.Class)}
	var pids []int
	for _, s := range lsofHolders(p.slotPath(h.Slot)) {
		pids = append(pids, s.PID)
	}
	return pids
}
