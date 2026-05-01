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
// See read_only_audit_test.go (Task K) for the AC7 verification.
type Signaller interface {
	Kill(pid int, sig syscall.Signal) error
}
