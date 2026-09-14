package ccuds

import (
	"syscall"
	"testing"
)

// ufAppend is UF_APPEND from <sys/stat.h>: a user-settable flag that makes
// a directory append-only — entries can be created but not unlinked.
const ufAppend = 0x4

// forbidUnlink makes dir append-only for the rest of the test so that
// creating files in it succeeds while unlinking them fails with EPERM.
func forbidUnlink(t *testing.T, dir string) {
	t.Helper()
	if err := syscall.Chflags(dir, ufAppend); err != nil {
		t.Fatalf("chflags uappnd %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := syscall.Chflags(dir, 0); err != nil {
			t.Errorf("clearing uappnd on %s: %v", dir, err)
		}
	})
}
