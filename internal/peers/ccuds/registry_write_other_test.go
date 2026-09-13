//go:build !darwin

package ccuds

import "testing"

// forbidUnlink: on Linux an append-only directory needs chattr +a, which
// requires CAP_LINUX_IMMUTABLE, so the rollback-failure test cannot run
// unprivileged there.
func forbidUnlink(t *testing.T, dir string) {
	t.Helper()
	t.Skip("append-only directories need root on this platform; rollback failure not reproducible")
}
