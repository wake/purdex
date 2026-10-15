// Package gatetest keeps the tests of the gate away from the host's real gate directory: a test that takes a slot in
// ~/.config/pdx/gate competes with the real commands of the whole host, and one that leaks its process tree holds that slot
// for hours (#2932). A package's TestMain calls Main; AssertIsolated is the guard test.
package gatetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var realHome string

// Main runs the package's tests with HOME and PDX_GATE_DIR pointing into a temporary directory (removed afterwards) and exits
// with their result. Call it as the body of TestMain (it does not return).
func Main(m *testing.M) {
	realHome, _ = os.UserHomeDir()
	tmp, err := os.MkdirTemp("", "pdx-gatetest-")
	if err != nil {
		panic(err)
	}
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Setenv("PDX_GATE_DIR", filepath.Join(tmp, "gate"))
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// AssertIsolated fails the test when the gate directory or the home directory the process sees is under the real home the
// tests started with, or when Main was not used at all (the guard test of each package calls it with gate.DefaultDir()).
func AssertIsolated(t *testing.T, gateDir string) {
	t.Helper()
	if realHome == "" {
		t.Fatal("gatetest.Main was not called from this package's TestMain: its tests may touch the real ~/.config/pdx/gate")
	}
	under := func(p, root string) bool {
		p, root = filepath.Clean(p), filepath.Clean(root)
		return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
	}
	if under(gateDir, realHome) {
		t.Fatalf("the gate directory %s is under the real home %s", gateDir, realHome)
	}
	if h, _ := os.UserHomeDir(); under(h, realHome) {
		t.Fatalf("HOME %s is the real home", h)
	}
}
