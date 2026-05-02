package codexbroker

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// TestSocketVerifier_OtherFallback_NoHolders — fake lsof exits non-zero with
// no matches → held=false. lsof exits with code 1 when nothing matches the
// query, so the verifier must treat exit-1 + empty stdout as "no holders"
// rather than a generic failure.
func TestSocketVerifier_OtherFallback_NoHolders(t *testing.T) {
	verifier := &sockVerifierImpl{
		runLsof: func(_ time.Duration, _ string) (string, error) {
			// lsof exit-1 with empty stdout: "no matches" outcome.
			return "", &exec.ExitError{}
		},
	}
	held, err := verifier.AnyPidHoldsSocket("/tmp/cxc-x/broker.sock", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if held {
		t.Errorf("expected held=false on empty lsof output")
	}
}

// TestSocketVerifier_OtherFallback_Held — fake lsof returns one COMMAND row
// → held=true.
func TestSocketVerifier_OtherFallback_Held(t *testing.T) {
	out := "COMMAND   PID USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME\nnode    12345 wake   17u  unix 0xfffff800 0t0 12345678 /tmp/cxc-x/broker.sock\n"
	verifier := &sockVerifierImpl{
		runLsof: func(_ time.Duration, _ string) (string, error) {
			return out, nil
		},
	}
	held, err := verifier.AnyPidHoldsSocket("/tmp/cxc-x/broker.sock", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !held {
		t.Errorf("expected held=true with at least one COMMAND row")
	}
}

// TestSocketVerifier_Timeout_ReturnsHeld — fake lsof returns DeadlineExceeded
// → held=true (conservative defer per plan §3 line 485).
func TestSocketVerifier_Timeout_ReturnsHeld(t *testing.T) {
	verifier := &sockVerifierImpl{
		runLsof: func(_ time.Duration, _ string) (string, error) {
			return "", context.DeadlineExceeded
		},
	}
	held, err := verifier.AnyPidHoldsSocket("/tmp/cxc-x/broker.sock", 50*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !held {
		t.Errorf("expected held=true on timeout (conservative defer)")
	}
}

// TestSocketVerifier_GenericError_ReturnsHeld — any non-exit-1 error from
// lsof (e.g. lsof not installed, EPERM) is treated conservatively as held.
func TestSocketVerifier_GenericError_ReturnsHeld(t *testing.T) {
	verifier := &sockVerifierImpl{
		runLsof: func(_ time.Duration, _ string) (string, error) {
			return "", errors.New("lsof: not found")
		},
	}
	held, err := verifier.AnyPidHoldsSocket("/tmp/cxc-x/broker.sock", 100*time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !held {
		t.Errorf("expected held=true on generic lsof error (conservative defer)")
	}
}
