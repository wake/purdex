package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/gate/shim"
)

func TestSetupGateShims_InstallsAndUninstallsInATempHome(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte("export KEEP=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	setupGateShims(home, "/p/pdx", &out)
	if !strings.Contains(out.String(), "pdx gate shims installed in "+shim.DirIn(home)) {
		t.Fatalf("output: %q", out.String())
	}
	for _, tool := range shim.Tools {
		if _, err := os.Stat(filepath.Join(shim.DirIn(home), tool)); err != nil {
			t.Fatalf("shim %s: %v", tool, err)
		}
	}
	// a second run says the same and changes nothing
	before, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	setupGateShims(home, "/p/pdx", &bytes.Buffer{})
	after, _ := os.ReadFile(filepath.Join(home, ".zshenv"))
	if !bytes.Equal(before, after) {
		t.Fatalf(".zshenv changed on the second setup:\n%s", after)
	}
	out.Reset()
	if err := uninstallGateShims(home, &out); err != nil || !strings.Contains(out.String(), "removed") {
		t.Fatalf("uninstall: %v %q", err, out.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, ".zshenv")); string(b) != "export KEEP=1\n" {
		t.Fatalf(".zshenv after uninstall: %q", b)
	}
}

// A home that cannot take the shims is a line on the writer, not a failed setup (the agent hooks are installed already).
func TestSetupGateShims_AnUnwritableHomeOnlyWarns(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".config"), []byte("a file where the directory should be"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	setupGateShims(home, "/p/pdx", &out)
	if !strings.Contains(out.String(), "gate shims not installed") {
		t.Fatalf("output: %q", out.String())
	}
}
