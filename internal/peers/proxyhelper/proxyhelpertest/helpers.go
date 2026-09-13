package proxyhelpertest

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// TempDirs returns a socket dir and a created registry dir under a short
// /tmp/pdxp* root (Unix socket paths are length-limited), removed when
// the test ends. Nothing in a test should touch ~/.claude/sessions or
// /tmp/cc-socks.
func TempDirs(t testing.TB) (sockDir, registryDir string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pdxp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	registryDir = filepath.Join(root, "reg")
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "socks"), registryDir
}

// Exists reports whether path exists (without following a symlink).
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// WriteToSock dials the Unix socket and writes line plus a newline, the
// way a Claude Code peer delivers one frame.
func WriteToSock(t testing.TB, sock, line string) {
	t.Helper()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial %s: %v", sock, err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write to %s: %v", sock, err)
	}
}
