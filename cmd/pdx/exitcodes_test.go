package main

import "testing"

// TestExitCodes_PinnedToSpec14 pins the process exit codes to spec §14. A
// skill and a mod read these numbers; renumbering them is a wire change.
func TestExitCodes_PinnedToSpec14(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"ExitOK", ExitOK, 0},
		{"ExitError", ExitError, 1},
		{"ExitUsage", ExitUsage, 2},
		{"ExitDenied", ExitDenied, 10},
		{"ExitTimeout", ExitTimeout, 11},
		{"ExitCancelled", ExitCancelled, 12},
		{"ExitRefused", ExitRefused, 13},
		{"ExitMemberFailed", ExitMemberFailed, 14},
		{"ExitUnavailable", ExitUnavailable, 20},
		{"ExitUnsupported", ExitUnsupported, 21},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d (spec §14)", tc.name, tc.got, tc.want)
		}
	}
}
