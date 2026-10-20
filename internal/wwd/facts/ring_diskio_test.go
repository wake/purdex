package facts

import (
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func diskRec(i, v int, disks ...wire.DiskIO) wire.Facts {
	r := rec(t0.Add(time.Duration(i)*5*time.Second), 1, 1000, 1, wire.ZoneNormal)
	r.V = v
	r.DiskIO = disks
	return r
}

func dio(dev string, readBps, writeBps *float64) wire.DiskIO {
	return wire.DiskIO{Device: dev, ReadBps: readBps, WriteBps: writeBps}
}

// The minute lists the disks seen in it (a union), each averaged over the samples in which it had a rate; the last record is v1 and the
// field is not one the version gate clears.
func TestRollupDiskIOUnionAndMean(t *testing.T) {
	recs := []wire.Facts{
		diskRec(0, wire.FactsProto, dio("disk0", fp(100), nil)),
		diskRec(1, wire.FactsProto, dio("disk0", nil, fp(10)), dio("disk2", fp(7), fp(0))),
		diskRec(2, 1, dio("disk0", fp(300), fp(30))),
	}
	got := Rollup(recs)
	if got.V != 1 {
		t.Fatalf("test setup: the last record must be v1: %d", got.V)
	}
	if len(got.DiskIO) != 2 {
		t.Fatalf("%+v", got.DiskIO)
	}
	d0, d2 := got.DiskIO[0], got.DiskIO[1]
	if d0.Device != "disk0" || !nearTo(d0.ReadBps, 200) || !nearTo(d0.WriteBps, 20) { // (100+300)/2, (10+30)/2: a null sample is skipped
		t.Errorf("disk0 %+v", d0)
	}
	if d2.Device != "disk2" || !nearTo(d2.ReadBps, 7) || d2.WriteBps == nil || *d2.WriteBps != 0 || d2.ReadIOPS != nil {
		t.Errorf("disk2 %+v: only the samples it was in count, and a measured 0 stays 0", d2)
	}
}

// A disk whose every sample is null is listed with null rates; a minute with no disk_io is absent, and the rollup shares nothing with
// its inputs.
func TestRollupDiskIONullsAndAbsent(t *testing.T) {
	a := diskRec(0, wire.FactsProto, dio("disk0", nil, nil))
	b := diskRec(1, wire.FactsProto, dio("disk0", nil, nil))
	got := Rollup([]wire.Facts{a, b})
	if len(got.DiskIO) != 1 || got.DiskIO[0].ReadBps != nil || got.DiskIO[0].WriteBps != nil {
		t.Errorf("%+v", got.DiskIO)
	}
	if aliased(got, a) || aliased(got, b) {
		t.Error("rollup aliases an input")
	}
	if got := Rollup([]wire.Facts{diskRec(0, wire.FactsProto), diskRec(1, wire.FactsProto)}); got.DiskIO != nil {
		t.Errorf("no sample read disk io: absent, got %+v", got.DiskIO)
	}
}
