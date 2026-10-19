package reuse

import "testing"

func TestKeyForHostIsTheHostAsItIsNow(t *testing.T) {
	fp := map[string]string{"os_version": "6.8", "node": "24.14.0"}
	earned := HostNow{Earned: true, EarnedFingerprint: fp, CurrentFingerprint: fp}
	reference := HostNow{Reference: true, ReferenceFingerprint: fp, CurrentFingerprint: fp}
	a := KeyForHost("t", "tsc", nil, "d", "linux", "amd64", earned)
	if a != KeyForHost("t", "tsc", []string{}, "d", "linux", "amd64", earned) {
		t.Error("nil and empty args are one key")
	}
	if a == KeyForHost("t", "tsc", nil, "d", "linux", "amd64", reference) {
		t.Error("an earned host is keyed by the fingerprint it qualified with, the reference by none")
	}
	if a == KeyForHost("t", "tsc", nil, "d", "darwin", "amd64", earned) || a == KeyForHost("t", "tsc", nil, "d2", "linux", "amd64", earned) {
		t.Error("platform and definition are part of the key")
	}
	moved := HostNow{Earned: true, EarnedFingerprint: fp, CurrentFingerprint: map[string]string{"os_version": "6.9", "node": "24.14.0"}}
	if a == KeyForHost("t", "tsc", nil, "d", "linux", "amd64", moved) {
		t.Error("a host whose toolchain moved asks for another key")
	}
}
