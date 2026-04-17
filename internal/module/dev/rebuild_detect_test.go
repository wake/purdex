package dev

import "testing"

func TestDetectRequiresFullRebuild(t *testing.T) {
	cases := []struct {
		name        string
		buildHash   string
		currentHash string
		wantFlag    bool
	}{
		{"empty build hash (legacy build info)", "", "abc123", false},
		{"unknown build hash", "unknown", "abc123", false},
		{"matching hashes", "abc123", "abc123", false},
		{"changed hashes", "abc123", "def456", true},
		{"current unknown (git failure)", "abc123", "unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &DevModule{
				hashFn: func(paths ...string) string {
					if len(paths) != len(rebuildTrackedPaths) {
						t.Fatalf("hashFn called with %d paths, want %d", len(paths), len(rebuildTrackedPaths))
					}
					return tc.currentHash
				},
			}
			got, reason := m.detectRequiresFullRebuild(tc.buildHash)
			if got != tc.wantFlag {
				t.Errorf("flag: want %v, got %v (reason=%q)", tc.wantFlag, got, reason)
			}
			if got && reason == "" {
				t.Error("reason: want non-empty when flag=true")
			}
		})
	}
}
