package agent

import "testing"

// parseProcStat is pure, so it is pinned on every platform, although only the
// Linux snapshot reads /proc.
func TestParseProcStat(t *testing.T) {
	// statLine lays out a /proc/<pid>/stat line: pid, (comm), state, then
	// fields 4 (ppid) to 22 (starttime) and two after it.
	statLine := func(comm, ppid, start string) string {
		return "4242 (" + comm + ") S " + ppid + " 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 " + start + " 1234567 100\n"
	}
	cases := []struct {
		name      string
		stat      string
		wantPPID  int
		wantTicks uint64
		wantErr   bool
	}{
		{"plain", statLine("bash", "1", "98765"), 1, 98765, false},
		{"no final newline", "7 (sh) R 3 7 7 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 55", 3, 55, false},
		// comm is whatever the process named itself, so it may hold blanks
		// and parentheses; only the last ')' ends it.
		{"comm with blanks and parens", statLine("a) b (c) S 9 9", "77", "123"), 77, 123, false},
		{"comm ending in a paren", statLine("x)", "5", "6"), 5, 6, false},
		{"empty comm", statLine("", "2", "3"), 2, 3, false},
		{"starttime past 2^32", statLine("sh", "1", "18446744073709551615"), 1, 1<<64 - 1, false},
		{"empty", "", 0, 0, true},
		{"no closing paren", "4242 (bash S 1 4242", 0, 0, true},
		{"stops before starttime", "4242 (bash) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0\n", 0, 0, true},
		{"only pid and comm", "4242 (bash)\n", 0, 0, true},
		{"non-numeric ppid", statLine("bash", "x", "98765"), 0, 0, true},
		{"non-numeric starttime", statLine("bash", "1", "soon"), 0, 0, true},
		{"negative starttime", statLine("bash", "1", "-1"), 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ppid, ticks, err := parseProcStat(tc.stat)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseProcStat = %d, %d, nil; want an error", ppid, ticks)
				}
				return
			}
			if err != nil || ppid != tc.wantPPID || ticks != tc.wantTicks {
				t.Fatalf("parseProcStat = %d, %d, %v; want %d, %d, nil", ppid, ticks, err, tc.wantPPID, tc.wantTicks)
			}
		})
	}
}
