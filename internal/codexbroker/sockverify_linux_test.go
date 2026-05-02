//go:build linux

package codexbroker

import (
	"testing"
	"time"
)

// TestSockverify_LinuxPreferred_ForcesLsofFallback — PR review finding E:
// the Linux preferred path used to compare /proc/<pid>/fd/<n> readlink
// targets against the supplied socket path. Linux stringifies socket fds
// as `socket:[<inode>]`, never as the path, so the comparison never
// matched and the verifier returned (false, nil) without consulting lsof.
// Step 6 then RemoveAll'd a still-held socket dir.
//
// Until proper inode-based verification (parse `socket:[inode]`, stat
// sockPath, cross-reference via /proc/net/unix or readlink) is built, the
// preferred path MUST return an error so the layered AnyPidHoldsSocket
// caller falls through to the conservative lsof fallback.
func TestSockverify_LinuxPreferred_ForcesLsofFallback(t *testing.T) {
	v := &sockVerifierImpl{}
	platformInitVerifier(v)
	if v.preferred == nil {
		t.Fatalf("expected platformInitVerifier to set preferred on linux")
	}
	_, err := v.preferred("/tmp/cxc-x/broker.sock", 200*time.Millisecond)
	if err == nil {
		t.Errorf("expected Linux preferred path to return non-nil err so caller falls through to lsof; got nil")
	}
}
