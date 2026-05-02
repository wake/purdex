package codexbroker

import (
	"net"
	"syscall"
)

// Dialer is the read-only audit seam for network connections. P1 NEVER calls
// Dialer.Dial — the interface exists solely so a recordingDialer can detect
// any future regression that adds a broker RPC. Production scanners take a
// nil Dialer; tests inject a recordingDialer.
//
// See read_only_audit_test.go (Task K) for the AC7 verification.
type Dialer interface {
	Dial(network, address string) (net.Conn, error)
}

// Signaller is the read-only audit seam for sending process signals. P1
// NEVER calls Signaller.Kill — the interface exists solely so a
// recordingSignaller can detect any future regression that adds a kill /
// graceful-shutdown path. Production scanners take a nil Signaller; tests
// inject a recordingSignaller.
//
// P2 wires Signaller.Kill in `KillSequence` (kill steps 3 + 4); the P1
// scanner still never invokes it.
//
// See read_only_audit_test.go (Task K) for the AC7 verification.
type Signaller interface {
	Kill(pid int, sig syscall.Signal) error
}

// realSignaller delegates to syscall.Kill. Production wiring (task R) uses
// this in KillSequence.Signaller.
type realSignaller struct{}

// Kill implements Signaller.
func (realSignaller) Kill(pid int, sig syscall.Signal) error {
	return syscall.Kill(pid, sig)
}
