package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// parsePSTable is pure, so it is pinned on every platform, although only the
// Linux snapshot reads its table through ps.
func TestParsePSTable(t *testing.T) {
	type want struct {
		ppid   int
		lstart string
		// start is zero where the lstart text must not parse: the entry then
		// carries the per-PID reader's parse error instead.
		start time.Time
	}
	at := func(day, hour, min, sec int) time.Time {
		return time.Date(2026, time.October, day, hour, min, sec, 0, time.Local)
	}
	cases := []struct {
		name string
		out  string
		want map[int]want
	}{
		{"empty output", "", map[int]want{}},
		{
			"procps right-aligns the numbers",
			"      1       0 Mon Oct  5 21:00:00 2026\n  12345       1 Tue Oct 13 05:11:40 2026\n",
			map[int]want{
				1:     {0, "Mon Oct  5 21:00:00 2026", at(5, 21, 0, 0)},
				12345: {1, "Tue Oct 13 05:11:40 2026", at(13, 5, 11, 40)},
			},
		},
		{
			// The pad before a one-digit day is part of what ps prints and what
			// frames store, so lstart is the rest of the line, not a field.
			"one-digit day keeps its double space",
			"42 1 Tue Oct  6 05:11:40 2026\n",
			map[int]want{42: {1, "Tue Oct  6 05:11:40 2026", at(6, 5, 11, 40)}},
		},
		{
			"trailing blanks and no final newline",
			"42 1 Tue Oct  6 05:11:40 2026   ",
			map[int]want{42: {1, "Tue Oct  6 05:11:40 2026", at(6, 5, 11, 40)}},
		},
		{
			// A ps under a non-C locale; the text is still what ps -p prints.
			"unparseable lstart keeps its text",
			"77 1 Di  6 Okt 05:11:40 2026\n",
			map[int]want{77: {1, "Di  6 Okt 05:11:40 2026", time.Time{}}},
		},
		{
			"pid and ppid with no lstart",
			"78 1\n",
			map[int]want{78: {1, "", time.Time{}}},
		},
		{
			"short line",
			"79\n80 1 Tue Oct  6 05:11:40 2026\n",
			map[int]want{80: {1, "Tue Oct  6 05:11:40 2026", at(6, 5, 11, 40)}},
		},
		{
			"non-numeric pid or ppid",
			"  PID  PPID STARTED\nabc 1 Tue Oct  6 05:11:40 2026\n81 x Tue Oct  6 05:11:40 2026\n82 1 Tue Oct  6 05:11:40 2026\n",
			map[int]want{82: {1, "Tue Oct  6 05:11:40 2026", at(6, 5, 11, 40)}},
		},
		{
			"blank lines",
			"\n   \n83 1 Tue Oct  6 05:11:40 2026\n\n",
			map[int]want{83: {1, "Tue Oct  6 05:11:40 2026", at(6, 5, 11, 40)}},
		},
		{
			"pid zero or negative",
			"0 0 Tue Oct  6 05:11:40 2026\n-5 1 Tue Oct  6 05:11:40 2026\n84 0 Tue Oct  6 05:11:40 2026\n",
			map[int]want{84: {0, "Tue Oct  6 05:11:40 2026", at(6, 5, 11, 40)}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parsePSTable([]byte(tc.out))
			if len(got) != len(tc.want) {
				t.Errorf("parsed %d entries, want %d", len(got), len(tc.want))
			}
			for pid, w := range tc.want {
				e, ok := got[pid]
				if !ok {
					t.Errorf("pid %d missing", pid)
					continue
				}
				if e.ppid != w.ppid {
					t.Errorf("pid %d: ppid = %d, want %d", pid, e.ppid, w.ppid)
				}
				if e.lstart != w.lstart {
					t.Errorf("pid %d: lstart = %q, want %q", pid, e.lstart, w.lstart)
				}
				if w.start.IsZero() {
					prefix := fmt.Sprintf("parse start time for pid %d: ", pid)
					if e.startErr == nil || !strings.HasPrefix(e.startErr.Error(), prefix) {
						t.Errorf("pid %d: startErr = %v, want the per-PID reader's %q...", pid, e.startErr, prefix)
					}
					continue
				}
				if e.startErr != nil {
					t.Errorf("pid %d: startErr = %v", pid, e.startErr)
				}
				if !e.start.Equal(w.start) || e.start.Location() != time.Local {
					t.Errorf("pid %d: start = %v (%v), want %v in time.Local", pid, e.start, e.start.Location(), w.start)
				}
			}
		})
	}
}
