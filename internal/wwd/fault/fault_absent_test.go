package fault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerFileAbsent(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "owner-active")
	h := New(Config{Enabled: true, OwnerActiveFile: f}, nil)
	if !h.OwnerFileAbsent() {
		t.Fatal("no file: absent")
	}
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if h.OwnerFileAbsent() {
		t.Fatal("file there: not absent")
	}
	// a path that cannot be asked about (its parent is a file) is not "absent"
	if New(Config{Enabled: true, OwnerActiveFile: filepath.Join(f, "x")}, nil).OwnerFileAbsent() {
		t.Fatal("ENOTDIR is not absent")
	}
	// not named, or the hooks off: never
	var nilHooks *Hooks
	if nilHooks.OwnerFileAbsent() || New(Config{Enabled: true, Zone: "danger"}, nil).OwnerFileAbsent() || New(Config{OwnerActiveFile: f}, nil).OwnerFileAbsent() {
		t.Fatal("not named: never")
	}
}
