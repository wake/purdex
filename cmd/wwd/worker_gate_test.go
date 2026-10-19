package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/purdex/internal/wwd/gateheld"
)

// The adapter between the gate reader and the worker loop: a slot, "no gate here" (nil slot, ok) and "none to be had" (not ok) stay
// apart, and "no gate" is an untyped nil, not a nil *Held that would look like a slot to the loop.
func TestGateTakerAnswers(t *testing.T) {
	t.Setenv("PDX_GATE_DIR", "")
	root := t.TempDir()
	dir := filepath.Join(root, "gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfg, []byte("[gate]\ndir = \""+dir+"\"\n\n[gate.slots]\nheavy = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := gateTaker{gateheld.New(cfg, nil)}

	s, ok := g.TakeHeavy()
	if !ok || s == nil {
		t.Fatalf("a free slot: %v %v", s, ok)
	}
	if s2, ok := g.TakeHeavy(); ok || s2 != nil {
		t.Fatalf("the only slot is held: %v %v, want none to be had", s2, ok)
	}
	s.Release()
	s, ok = g.TakeHeavy()
	if !ok || s == nil {
		t.Fatalf("after the release: %v %v", s, ok)
	}
	s.Release()

	none := gateTaker{gateheld.New(filepath.Join(root, "no-such-config.toml"), nil)}
	t.Setenv("PDX_GATE_DIR", filepath.Join(root, "absent"))
	if s, ok := none.TakeHeavy(); !ok || s != nil {
		t.Fatalf("a host without a gate: %v %v, want a nil slot and ok", s, ok)
	}
}
