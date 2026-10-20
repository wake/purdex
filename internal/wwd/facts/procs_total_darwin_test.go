//go:build darwin

package facts

import (
	"testing"
	"time"
)

// procs_total is the table's size from the one pass: a row that is then dropped (its pid changed hands before the top lists were
// checked) is still in it, and the number is not the length of the list.
func TestSnapshotTotalIsTheTableSizeNotTheListed(t *testing.T) {
	a, b := ident{sec: 100, usec: 1}, ident{sec: 200, usec: 5}
	rows := []tableRow{
		{ProcRec: ProcRec{PID: 10, PPID: 1, Name: "reused"}, id: a},
		{ProcRec: ProcRec{PID: 11, PPID: 1, Name: "ok"}, id: b},
		{ProcRec: ProcRec{PID: 12, PPID: 1, Name: "ok2"}, id: b},
	}
	r := fakeReader(rows, map[int]ident{10: {sec: 100, usec: 2}, 11: b, 12: b}, 5<<20)
	snap, _, err := r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Procs) != 2 || snap.Total != 3 {
		t.Fatalf("listed %d total %d want 2 and 3", len(snap.Procs), snap.Total)
	}
}
