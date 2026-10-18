package locks

import (
	"errors"
	"testing"
)

func TestRenameMovesTheKeysToTheNewOwnerAndKeepsTheHold(t *testing.T) {
	tb := New()
	h, err := tb.Acquire("local:1", []string{"tmux", "port:7860"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Rename("j_abc"); err != nil {
		t.Fatal(err)
	}
	if o, _ := tb.Holder("tmux"); o != "j_abc" {
		t.Fatalf("holder = %q", o)
	}
	if _, err := tb.Acquire("j_other", []string{"tmux"}, nil); err == nil {
		t.Fatal("the renamed hold stopped holding its keys")
	}
	tb.ReleaseOwner("j_abc") // a cancel that knows only the job id
	if _, ok := tb.Holder("tmux"); ok {
		t.Fatal("ReleaseOwner by the new name did not free the keys")
	}
	h.Release() // a late release of the old acquisition is harmless
}

func TestRenameThenReleaseByTheHoldFreesTheKeys(t *testing.T) {
	tb := New()
	h, _ := tb.Acquire("local:1", []string{"k"}, nil)
	if err := h.Rename("j_1"); err != nil {
		t.Fatal(err)
	}
	h.Release()
	if _, ok := tb.Holder("k"); ok || len(tb.Held()) != 0 {
		t.Fatalf("held after release: %v", tb.Held())
	}
}

func TestRenameRefusals(t *testing.T) {
	tb := New()
	a, _ := tb.Acquire("local:1", []string{"a"}, nil)
	b, _ := tb.Acquire("j_2", []string{"b"}, nil)
	if err := a.Rename("j_2"); !errors.Is(err, ErrAlreadyHeld) {
		t.Fatalf("a name that holds keys: %v", err)
	}
	if o, _ := tb.Holder("a"); o != "local:1" {
		t.Fatalf("a refused rename changed the hold: %q", o)
	}
	if err := a.Rename(""); err == nil {
		t.Fatal("an empty owner was accepted")
	}
	a.Release()
	if err := a.Rename("j_3"); !errors.Is(err, ErrReleased) {
		t.Fatalf("a released hold: %v", err)
	}
	if err := b.Rename("j_2"); err != nil {
		t.Fatalf("the same name: %v", err)
	}
}

func TestRenameOfAnAcquisitionWithNoKeys(t *testing.T) {
	tb := New()
	h, err := tb.Acquire("local:1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Rename("j_1"); err != nil {
		t.Fatal(err)
	}
	h.Release()
	if len(tb.Held()) != 0 {
		t.Fatalf("held: %v", tb.Held())
	}
}
