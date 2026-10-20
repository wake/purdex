//go:build darwin

package facts

import "testing"

// A Mac whose owner frameworks load can read its owner, so it can have an owner rule: that is what the worker settings ask (OwnerRuleSupported).
func TestAMacCanReadItsOwner(t *testing.T) {
	if _, err := DarwinOwnerSources(); err != nil {
		t.Skipf("this machine cannot load the owner frameworks: %v", err)
	}
	if !OwnerReadable() {
		t.Fatal("the owner sources load, so the owner is readable")
	}
}
