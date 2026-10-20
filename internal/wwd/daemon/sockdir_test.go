package daemon

import (
	"os"
	"testing"
)

// shortSockDir is a 0700 directory with a short path for a unix socket. t.TempDir and os.TempDir() both follow TMPDIR, which inside a
// wwd job sits under the work root: the socket path then passes the sun_path limit (about 104 bytes on macOS).
func shortSockDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "wwd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
