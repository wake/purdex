//go:build linux

package codexbroker

import (
	"errors"
	"time"
)

// platformInitVerifier wires the Linux preferred path.
//
// PR review finding E: the previous implementation walked /proc/<pid>/fd
// symlinks looking for one whose readlink target equalled sockPath. Linux
// stringifies unix-socket fds as `socket:[<inode>]`, never as the bind
// path, so the comparison never matched and the verifier returned
// (false, nil). The caller (AnyPidHoldsSocket) saw a clean negative,
// SKIPPED the lsof fallback, and Step 6 RemoveAll'd a still-held socket
// directory.
//
// Until a proper inode-based verifier is built (parse `socket:[<inode>]`,
// stat sockPath for its inode, cross-reference via /proc/net/unix or by
// matching readlink against the inode set), the preferred path MUST
// return an error. The layered AnyPidHoldsSocket then falls through to
// `lsof -nP -- <sockPath>`, which IS the conservative-correct path on
// Linux today.
//
// Follow-up issue: implement real inode-based verification so the lsof
// fork/exec is no longer required on every Step 6 cleanup.
func platformInitVerifier(v *sockVerifierImpl) {
	v.preferred = func(_ string, _ time.Duration) (bool, error) {
		return false, errors.New("linux preferred path: inode-verify not yet implemented; falling through to lsof")
	}
}
