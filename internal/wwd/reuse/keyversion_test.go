package reuse

import "testing"

// The key carries a version, so a change in what a job runs with (the environment gained USER and LOGNAME, #3919) can retire the stored
// results without touching their fields: a result filed under the old version is never found under the new key. Both sides of a
// rolling deploy only miss, never hit wrongly.
func TestKeyVersionChangesTheKey(t *testing.T) {
	p := baseParts()
	if keyAt(keyVersion, p) != Key(p) {
		t.Fatalf("Key is not the key of the current version")
	}
	if keyAt(keyVersion+1, p) == Key(p) || keyAt(keyVersion-1, p) == Key(p) {
		t.Fatalf("another version gave the same key")
	}
}

// The version is 2 from the build that put USER and LOGNAME into a job's environment: a success of the older environment is not reused.
func TestKeyVersionIsTwoSinceUserAndLognameJoinedTheEnvironment(t *testing.T) {
	if keyVersion != 2 {
		t.Fatalf("keyVersion = %d, want 2", keyVersion)
	}
	if keyAt(1, baseParts()) == Key(baseParts()) {
		t.Fatalf("a result filed under version 1 would still be found")
	}
}
