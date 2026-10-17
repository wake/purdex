//go:build darwin

package facts

import (
	"encoding/binary"
	"testing"
	"time"
	"unsafe"
)

// fakeReader is a ProcReader over staged reads: pid 10 and 11 are in the table;
// startNow says what start time each pid has when the per-pid reads come back.
func fakeReader(rows []tableRow, startNow map[int]ident, footprint uint64) *ProcReader {
	return &ProcReader{
		numer: 1, denom: 1,
		prev: map[int]cpuPrev{}, args: map[argKey]procArgs{}, kinds: map[argKey]string{}, idents: map[int]ident{},
		table: func() ([]tableRow, error) { return rows, nil },
		startOf: func(pid int) (ident, bool) {
			id, ok := startNow[pid]
			return id, ok
		},
		procArgs: func(pid int) ([]byte, error) {
			return procargsBuf("/x/node", []string{"node", "vite"}, []string{"WWD_JOB=j_old"}), nil
		},
		rusage: func(pid int32, flavor int32, b unsafe.Pointer) int32 {
			buf := unsafe.Slice((*byte)(b), 512)
			binary.LittleEndian.PutUint64(buf[rusageOffFoot:], footprint)
			binary.LittleEndian.PutUint64(buf[rusageOffUser:], 1_000_000_000)
			return 0
		},
	}
}

func TestSnapshotDropsReadsOfAReusedPid(t *testing.T) {
	a := ident{sec: 100, usec: 1}
	b := ident{sec: 200, usec: 5}
	rows := []tableRow{
		{ProcRec: ProcRec{PID: 10, PPID: 1, Name: "old"}, id: a},
		{ProcRec: ProcRec{PID: 11, PPID: 1, Name: "ok"}, id: b},
	}
	// by the time rusage and procargs2 are read, pid 10 belongs to a process
	// that started in the same second but a microsecond later
	r := fakeReader(rows, map[int]ident{10: {sec: 100, usec: 2}, 11: b}, 5<<20)
	snap, ctx, err := r.Snapshot(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	byPID := map[int]ProcRec{}
	for _, p := range snap.Procs {
		byPID[p.PID] = p
	}
	if byPID[10].FootprintBytes != 0 {
		t.Errorf("pid 10 took the footprint of the process that reused it: %d", byPID[10].FootprintBytes)
	}
	if byPID[11].FootprintBytes != 5<<20 {
		t.Errorf("pid 11 footprint %d", byPID[11].FootprintBytes)
	}
	if id, ok := ctx.JobOf(10); ok {
		t.Errorf("pid 10 took the job marker %q of another process", id)
	}
	if args := ctx.ArgsOf(10); args != nil {
		t.Errorf("pid 10 args %v", args)
	}
	if len(r.args) != 0 && func() bool { _, ok := r.args[argKey{10, a}]; return ok }() {
		t.Error("a mismatched read must not be cached")
	}
	if id, ok := ctx.JobOf(11); !ok || id != "j_old" {
		t.Errorf("pid 11 job %q %v", id, ok)
	}
}

func TestCPUDeltaNeedsTheSameFullStartTime(t *testing.T) {
	a := ident{sec: 100, usec: 1}
	rows := []tableRow{{ProcRec: ProcRec{PID: 10, PPID: 1, Name: "p"}, id: a}}
	r := fakeReader(rows, map[int]ident{10: a}, 1<<20)
	t0 := time.Now()
	if _, _, err := r.Snapshot(t0); err != nil {
		t.Fatal(err)
	}
	// same pid and second, different microsecond: another process, no delta
	r.table = func() ([]tableRow, error) {
		return []tableRow{{ProcRec: ProcRec{PID: 10, PPID: 1, Name: "p"}, id: ident{sec: 100, usec: 9}}}, nil
	}
	r.startOf = func(int) (ident, bool) { return ident{sec: 100, usec: 9}, true }
	r.rusage = func(pid int32, flavor int32, b unsafe.Pointer) int32 {
		binary.LittleEndian.PutUint64(unsafe.Slice((*byte)(b), 512)[rusageOffUser:], 9_000_000_000)
		return 0
	}
	snap, _, err := r.Snapshot(t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.Procs[0].CPUPct != 0 {
		t.Errorf("cpu share %v across two processes", snap.Procs[0].CPUPct)
	}
}
