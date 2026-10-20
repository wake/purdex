package derived

import "github.com/wake/purdex/internal/wwd/wire"

// AdmitLocal says why a fast-lane run (spec §5.2) may not start now: the reasons of the slot it takes, or none when there is room.
// heavy is an undeclared command (adhoc); a declared task takes a light slot. It is made from the latest record f and the runs that
// are going on now (running: queue jobs and fast-lane runs, counted live because the record is up to 5 s old), and does no I/O, so it
// can be asked while a lock is held.
//
// It differs from the headroom a queue job is held by in two things: the owner at the keyboard does not stop it (the owner is the
// one who asked for the run), and the worker being off does not either (that only stops the queue).
func AdmitLocal(f wire.Facts, heavy bool, running wire.Slots) []string {
	j := judgement{
		zone:    f.Derived.Zone,
		free:    wire.Slots{Heavy: free(f.Worker.Slots.Heavy, running.Heavy), Light: free(f.Worker.Slots.Light, running.Light)},
		diskLow: f.Derived.DiskLow,
	}
	for _, e := range f.Exception {
		j.holds = append(j.holds, Hold{Name: e.Name})
	}
	r := j.reasons()
	if heavy {
		if len(r.Heavy) == 0 {
			return nil
		}
		return r.Heavy
	}
	// a hold that stops light work too is not known from a record (Why guesses it); a light run is stopped by the zone and the slots only
	var out []string
	for _, s := range r.Light {
		if s == ReasonZoneDanger || s == ReasonSlotsFull {
			out = append(out, s)
		}
	}
	return out
}
