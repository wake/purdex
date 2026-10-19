package wire

import "testing"

// GET /api/pool/hosts/{alias}/exceptions (#3479 G4): the App copies these two files as its fixtures, so the shape they hold is the contract.
// One exception is bounded by a time, the other by a process (exactly one of the two, Exception.Validate), and it carries allow and flags.
func TestPoolHostExceptionsFixtures(t *testing.T) {
	known := roundTrip[PoolHostExceptions](t, "pool_host_exceptions.json")
	if !known.Known || known.Host != "air26" || len(known.Exceptions) != 2 {
		t.Fatalf("known: %+v", known)
	}
	for _, e := range known.Exceptions {
		if err := e.Validate(); err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
	}
	if e := known.Exceptions[0]; e.Until == nil || e.PID != nil || len(e.Holds) != 2 {
		t.Fatalf("the time-bound exception: %+v", e)
	}
	if e := known.Exceptions[1]; e.Until != nil || e.PID == nil || *e.PID != 4242 || e.Allow == nil || len(e.Flags) != 2 {
		t.Fatalf("the process-bound exception: %+v", e)
	}

	unknown := roundTrip[PoolHostExceptions](t, "pool_host_exceptions_unknown.json")
	if unknown.Known || unknown.Host != "mserv" || unknown.Exceptions == nil || len(unknown.Exceptions) != 0 {
		t.Fatalf("unknown: %+v", unknown)
	}
}
