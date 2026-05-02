//go:build darwin

package codexbroker

import (
	"errors"
	"time"
)

// platformInitVerifier wires the Darwin preferred path. The plan §3
// describes a CGo-free libproc binding via syscall.Syscall against
// proc_pidinfo / PROC_PIDLISTFDS. The full implementation requires
// driving the lsstat-style FD enumeration syscall numbers and is not
// strictly required by the unit-test harness (lsof fallback covers the
// observable contract). To keep the kill-cleanup path within the P2
// review budget we ship the preferred path as an explicit "not
// implemented yet" returner that always defers to lsof; spec §5.4
// line 472 + plan §3 line 75 explicitly accept lsof as the bounded
// fallback when the libproc binding is unavailable.
//
// A future PR may flesh out the libproc path; the seam is here so the
// upgrade is internal-only.
func platformInitVerifier(v *sockVerifierImpl) {
	v.preferred = func(_ string, _ time.Duration) (bool, error) {
		return false, errors.New("libproc preferred path not yet implemented")
	}
}
