package peers

// ProcIdentity is the tri-state answer to "is pid still the process we
// started?", shared by the daemon's startup sweep (internal/module/peers)
// and `pdx msg selftest` (cmd/pdx). The states are never folded: unknown
// means the process must not be signalled and its files must not be
// touched. The zero value is unknown — the safe default.
type ProcIdentity int

const (
	// ProcUnknown: the pid is alive but its start time could not be read.
	ProcUnknown ProcIdentity = iota
	// ProcSame: the pid is alive and carries the recorded start time.
	ProcSame
	// ProcDifferent: the pid is dead, or held by another process.
	ProcDifferent
)

func (i ProcIdentity) String() string {
	switch i {
	case ProcSame:
		return "same"
	case ProcDifferent:
		return "different"
	}
	return "unknown"
}

// ClassifyProc classifies pid against wantProcStart through the two
// process seams: alive is the liveness answer, id the identity. A dead pid
// is ProcDifferent and its start time is never asked for; a live pid is
// ProcSame when procStart returns wantProcStart, ProcDifferent when it
// returns anything else, and ProcUnknown when it fails.
func ClassifyProc(pid int, wantProcStart string, alive func(int) bool, procStart func(int) (string, error)) (bool, ProcIdentity) {
	if !alive(pid) {
		return false, ProcDifferent
	}
	ps, err := procStart(pid)
	switch {
	case err != nil:
		return true, ProcUnknown
	case ps == wantProcStart:
		return true, ProcSame
	default:
		return true, ProcDifferent
	}
}
