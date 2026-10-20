package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func cpuRec(at time.Time, v int, l15, user, sys *float64, cores ...wire.Core) wire.Facts {
	f := rec(at, 1, 1000, 1, wire.ZoneNormal)
	f.V = v
	f.CPU.Load15, f.CPU.UserPct, f.CPU.SystemPct, f.CPU.Cores = l15, user, sys, cores
	return f
}

func str(s string) *string { return &s }

// The new CPU fields are not proto 2 fields: a minute whose last record is v 1 still averages them (spec §2.4), and the mean is
// over the records that have a value.
func TestRollupAveragesCPUSplitEvenWhenTheLastRecordIsV1(t *testing.T) {
	recs := []wire.Facts{
		cpuRec(t0, wire.FactsProto, nil, nil, nil, wire.Core{}, wire.Core{}), // the first sample after a restart: no figures
		cpuRec(t0.Add(5*time.Second), wire.FactsProto, fp(1), fp(10), fp(4), wire.Core{UserPct: fp(20), SystemPct: fp(10), IdlePct: fp(70), Kind: str("e")}, wire.Core{UserPct: fp(0), SystemPct: fp(0), IdlePct: fp(100), Kind: str("p")}),
		cpuRec(t0.Add(10*time.Second), 1, fp(3), fp(30), fp(8), wire.Core{UserPct: fp(40), SystemPct: fp(20), IdlePct: fp(40), Kind: str("e")}, wire.Core{UserPct: fp(10), SystemPct: fp(0), IdlePct: fp(90), Kind: str("p")}),
	}
	got := Rollup(recs)
	if got.V != 1 {
		t.Fatalf("the test needs a v1 last record, got %d", got.V)
	}
	c := got.CPU
	if !near(c.Load15, 2) || !near(c.UserPct, 20) || !near(c.SystemPct, 6) {
		t.Errorf("load15 %v user %v system %v", c.Load15, c.UserPct, c.SystemPct)
	}
	if len(c.Cores) != 2 || !near(c.Cores[0].UserPct, 30) || !near(c.Cores[0].SystemPct, 15) || !near(c.Cores[0].IdlePct, 55) {
		t.Fatalf("core 0: %+v", c.Cores)
	}
	if !near(c.Cores[1].UserPct, 5) || !near(c.Cores[1].SystemPct, 0) || !near(c.Cores[1].IdlePct, 95) { // system: one value, 0
		t.Errorf("core 1: %+v", c.Cores[1])
	}
	if *c.Cores[0].Kind != "e" || *c.Cores[1].Kind != "p" {
		t.Errorf("kinds %+v", c.Cores)
	}
}

func TestRollupCPUSplitIsNullWhenNoRecordHasIt(t *testing.T) {
	got := Rollup([]wire.Facts{cpuRec(t0, 1, nil, nil, nil), cpuRec(t0.Add(5*time.Second), 1, nil, nil, nil)})
	if got.CPU.Load15 != nil || got.CPU.UserPct != nil || got.CPU.SystemPct != nil || len(got.CPU.Cores) != 0 {
		t.Errorf("%+v", got.CPU)
	}
}

// The core count of the last record decides the list; only records that have the same core index are averaged into it.
func TestRollupCoresWhenTheCoreCountChanged(t *testing.T) {
	recs := []wire.Facts{
		cpuRec(t0, 1, nil, nil, nil, wire.Core{UserPct: fp(10)}, wire.Core{UserPct: fp(50)}, wire.Core{UserPct: fp(90)}),
		cpuRec(t0.Add(5*time.Second), 1, nil, nil, nil, wire.Core{UserPct: fp(30)}),
	}
	got := Rollup(recs).CPU.Cores
	if len(got) != 1 || !near(got[0].UserPct, 20) {
		t.Errorf("%+v", got)
	}
}

// cpu1 goes offline and cpu2 takes index 1: the sampler leaves index 1 null at that sample, and the minute must not average the
// cpu1 values before it with the cpu2 values after it.
func TestRollupDropsWhatCameBeforeACoreWasReplaced(t *testing.T) {
	recs := []wire.Facts{
		cpuRec(t0, 1, nil, nil, nil, wire.Core{UserPct: fp(10)}, wire.Core{UserPct: fp(90)}),
		cpuRec(t0.Add(5*time.Second), 1, nil, nil, nil, wire.Core{UserPct: fp(20)}, wire.Core{}), // index 1 is cpu2 now: no baseline
		cpuRec(t0.Add(10*time.Second), 1, nil, nil, nil, wire.Core{UserPct: fp(30)}, wire.Core{UserPct: fp(4)}),
		cpuRec(t0.Add(15*time.Second), 1, nil, nil, nil, wire.Core{UserPct: fp(40)}, wire.Core{UserPct: fp(6)}),
	}
	got := Rollup(recs).CPU.Cores
	if !near(got[1].UserPct, 5) { // cpu2 only: (4+6)/2, not the 90 of cpu1
		t.Errorf("index 1: %v", got[1].UserPct)
	}
	if !near(got[0].UserPct, 25) { // cpu0 was never replaced
		t.Errorf("index 0: %v", got[0].UserPct)
	}
}

func TestRollupDoesNotAliasTheNewCPUFields(t *testing.T) {
	recs := []wire.Facts{cpuRec(t0, 1, fp(1), fp(2), fp(3), wire.Core{UserPct: fp(4)})}
	got := Rollup(recs)
	*got.CPU.Load15, *got.CPU.Cores[0].UserPct = 99, 99
	if *recs[0].CPU.Load15 != 1 || *recs[0].CPU.Cores[0].UserPct != 4 {
		t.Error("the rollup shares memory with its input")
	}
}
