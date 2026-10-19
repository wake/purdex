package fault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerBlock(t *testing.T) {
	f := filepath.Join(t.TempDir(), "owner-active")
	if (*Hooks)(nil).OwnerBlock() != nil || New(Config{Enabled: true}, nil).OwnerBlock() != nil {
		t.Fatal("a hook that is not named must not make up an owner")
	}
	g := &logs{}
	h := New(Config{Enabled: true, OwnerActiveFile: f}, g.printf)
	if b := h.OwnerBlock(); b == nil || b.Active || b.Reason != "fault: owner_active_file absent" {
		t.Fatalf("file absent: %+v", b)
	}
	before := g.n() // New itself may announce the hook
	if b := h.OwnerBlock(); b == nil || b.Active || g.n() != before {
		t.Fatal("a warning without a trigger")
	}
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if b := h.OwnerBlock(); b == nil || !b.Active || b.Reason != "fault: owner_active_file" {
		t.Fatalf("file present: %+v", b)
	}
	if g.n() <= before {
		t.Fatal("no warning on trigger (the fault package warns at every trigger)")
	}
}
