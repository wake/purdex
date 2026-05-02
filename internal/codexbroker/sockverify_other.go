//go:build !darwin && !linux

package codexbroker

// platformInitVerifier on unsupported platforms leaves the preferred path
// nil so AnyPidHoldsSocket falls straight through to the lsof fallback.
// Spec §5.4 line 472 + plan §3 line 75 explicitly accept lsof as the
// universal fallback.
func platformInitVerifier(_ *sockVerifierImpl) {}
