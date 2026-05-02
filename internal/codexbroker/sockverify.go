package codexbroker

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// SocketVerifier checks whether any live pid currently holds an open file
// descriptor pointing at the supplied unix socket. Used by Step 6 to defer
// cleanup of the cxc-* dir if a stray child still has the socket open.
//
// The interface is platform-agnostic; production wiring uses
// NewSocketVerifier which returns the platform-best implementation.
type SocketVerifier interface {
	AnyPidHoldsSocket(sockPath string, timeout time.Duration) (held bool, err error)
}

// sockVerifierImpl implements SocketVerifier with a layered strategy:
//
//  1. Optional preferred path — populated by sockverify_darwin.go /
//     sockverify_linux.go to walk libproc / /proc directly without forking
//     a subprocess. Returning (held, nil) consumes the result directly;
//     returning a non-nil error falls through to the lsof fallback.
//  2. lsof fallback — `lsof -nP -- <sockPath>` with the timeout budget.
//     Per plan §3 line 485 / spec §5.4 line 472, lsof exit-1 with empty
//     stdout means "no holders" (not an error).
//
// runLsof is the test seam: production wiring leaves it nil and the
// receiver invokes execLsof directly.
type sockVerifierImpl struct {
	preferred func(sockPath string, timeout time.Duration) (held bool, err error)
	runLsof   func(timeout time.Duration, sockPath string) (string, error)
}

// NewSocketVerifier returns the production verifier. The preferred field is
// populated by the platform-specific init() in sockverify_darwin.go /
// sockverify_linux.go; on other platforms only the lsof fallback is used.
func NewSocketVerifier() SocketVerifier {
	v := &sockVerifierImpl{}
	platformInitVerifier(v)
	return v
}

// AnyPidHoldsSocket implements SocketVerifier.
func (v *sockVerifierImpl) AnyPidHoldsSocket(sockPath string, timeout time.Duration) (bool, error) {
	if sockPath == "" {
		return false, nil
	}
	if v.preferred != nil {
		held, err := v.preferred(sockPath, timeout)
		if err == nil {
			return held, nil
		}
		// Preferred path failed (e.g. EPERM); fall through to lsof.
	}
	runner := v.runLsof
	if runner == nil {
		runner = execLsof
	}
	out, err := runner(timeout, sockPath)
	if err != nil {
		// lsof exits with 1 when no matches found — out will also be empty
		// in that case. Treat as "no holders" rather than an error.
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.TrimSpace(out) == "" {
			return false, nil
		}
		// Anything else (timeout, lsof not found, EPERM) is conservative:
		// declare held so cleanup is deferred and the next sweep retries.
		return true, nil
	}
	return lsofOutputHasHolder(out), nil
}

// execLsof runs `lsof -nP -- <sockPath>` with a context-bounded budget.
// Returns stdout + (nil on success, *exec.ExitError on lsof exit-1, or
// context.DeadlineExceeded on timeout).
func execLsof(timeout time.Duration, sockPath string) (string, error) {
	if timeout <= 0 {
		timeout = 1 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "--", sockPath)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.String(), context.DeadlineExceeded
	}
	return stdout.String(), err
}

// lsofOutputHasHolder reports whether stdout contains at least one COMMAND
// row (i.e. a non-empty line that is not the header). lsof's header line
// starts with "COMMAND".
func lsofOutputHasHolder(out string) bool {
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "COMMAND") {
			continue
		}
		return true
	}
	return false
}
