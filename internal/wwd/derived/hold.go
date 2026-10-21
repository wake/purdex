package derived

import (
	"slices"
	"strings"

	"github.com/wake/purdex/internal/wwd/wire"
)

// Holds with `allow` (spec §13.4, WWD-8 8b-3). An exception that carries `allow` is selective: while it is on, the worker admits a job only
// if its class or its repo is listed. The facts keep saying what an outside reader needs — the headroom is 0 while any exception is on, and
// the reasons read exception:<name> — so a reader that knows nothing of `allow` still waits. The worker, which does know, sets the
// selective holds aside for the room it has and filters the jobs it takes by AdmitsJob. (So the work the worker can take may be more than
// the headroom says, and only because of a hold with `allow`; spec §3.2.)

// HeavyRoomForAllowed is the heavy room the worker has when the selective holds are set aside: the free heavy slots if every reason that
// holds heavy work is a selective hold (or the worker's own off switch, which the loop checks itself), else 0. With no selective hold there
// is nothing to set aside and it is 0: the headroom of the record is the answer.
func HeavyRoomForAllowed(f wire.Facts) int {
	selective := map[string]bool{}
	for _, e := range f.Exception {
		if e.Allow != nil {
			selective[e.Name] = true
		}
	}
	if len(selective) == 0 {
		return 0
	}
	for _, r := range Why(f).Heavy {
		if r == ReasonWorkerOff {
			continue
		}
		if name, ok := strings.CutPrefix(r, ReasonExceptionP); ok && selective[name] {
			continue
		}
		return 0
	}
	return free(f.Worker.Slots.Heavy, f.Worker.Running.Heavy)
}

// AdmitsJob says whether every selective hold lets a job of this class and repo through: its class is listed, or its repo is. Both lists
// of a hold empty (or left out) admit nothing. An exception without `allow` is not looked at here; it holds through the headroom.
func AdmitsJob(excs []wire.FactExc, class wire.JobClass, repo string) bool {
	for _, e := range excs {
		a := e.Allow
		if a == nil {
			continue
		}
		if !slices.Contains(a.Classes, class) && !slices.Contains(a.Repos, repo) {
			return false
		}
	}
	return true
}

// HasHeavyRoom says whether the facts leave room for a heavy job: the headroom, or (while only holds with `allow` are in the way) the room
// they leave, with the jobs they list the only ones to take. The worker's loop decides to claim on it, and the queue rules ask the same of a
// peer's facts, so the two cannot differ.
func HasHeavyRoom(f wire.Facts) bool {
	return f.Derived.Headroom.Heavy >= 1 || HeavyRoomForAllowed(f) >= 1
}
