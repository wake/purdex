package daemon

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/facts"
	"github.com/wake/purdex/internal/wwd/wire"
)

// sigClock is the time the sources read; tickAt moves it with the sampler, since the rate is measured between two readings.
var sigClock atomic.Int64

func tickAt(s *Sampler, at time.Time) {
	sigClock.Store(at.UnixNano())
	s.Tick(at)
}

// withDecompress is fakeSources whose memory reading carries the compressor counter (macOS), growing by step each read.
func withDecompress(level *int, step uint64) facts.Sources {
	src := fakeSources(level, nil)
	src.Now = func() time.Time { return time.Unix(0, sigClock.Load()) }
	var n atomic.Uint64
	src.Mem = func() (facts.RawMem, error) {
		return facts.RawMem{TotalBytes: 16 << 30, PageSize: 16384, Free: 100000, Inactive: 100000, Level: *level,
			Decompressions: n.Add(step), HaveDecompressions: true}, nil
	}
	return src
}

func signalsSampler(level *int, open *atomic.Bool, queued *atomic.Int32) *Sampler {
	return NewSampler(SamplerOptions{
		HostID: "h1", Alias: "mlab", OSVersion: "26.6.2", Sources: withDecompress(level, 1000),
		Slots: wire.Slots{Heavy: 1, Light: 2}, WorkerEnabled: true,
		SignalsOpen: open.Load, Queued: func() (int, error) { return int(queued.Load()), nil },
	})
}

// Spec §13.1 phase 1: while the gate is shut the record is the v1 shape, whatever the readings are.
func TestTheNewSignalsStayOutWhileTheGateIsShut(t *testing.T) {
	level := 1
	var open atomic.Bool
	var queued atomic.Int32
	queued.Store(4)
	s := signalsSampler(&level, &open, &queued)
	tickAt(s, s0)
	tickAt(s, s0.Add(5*time.Second))
	f, _ := s.Latest()
	b, _ := json.Marshal(f)
	for _, k := range []string{"decompress_rate", `"queue"`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("the shut gate let %s out: %s", k, b)
		}
	}
	if f.V != wire.WriteProto {
		t.Fatalf("v %d", f.V)
	}
}

// Gate open: v 2, the decompress rate from the counter, this host's queued jobs.
func TestTheNewSignalsAreWrittenWhenTheGateIsOpen(t *testing.T) {
	level := 1
	var open atomic.Bool
	var queued atomic.Int32
	open.Store(true)
	queued.Store(4)
	s := signalsSampler(&level, &open, &queued)
	tickAt(s, s0)
	tickAt(s, s0.Add(5*time.Second))
	f, _ := s.Latest()
	if f.V != 2 {
		t.Fatalf("v %d: a record with the new fields is v 2", f.V)
	}
	if f.Mem.DecompressRate == nil || *f.Mem.DecompressRate != 200 { // 1000 pages / 5 s
		t.Fatalf("decompress_rate %+v want 200", f.Mem)
	}
	if f.Queue == nil || f.Queue.Queued != 4 {
		t.Fatalf("queue %+v", f.Queue)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	// it round-trips through the contract decoder
	b, _ := json.Marshal(f)
	if _, err := wire.Decode[wire.Facts](b); err != nil {
		t.Fatal(err)
	}
	// and closes again: the next record is v 1 again (a host rolled back, or one gone from the pool)
	open.Store(false)
	tickAt(s, s0.Add(10*time.Second))
	f, _ = s.Latest()
	if f.V != wire.WriteProto || f.Mem.DecompressRate != nil || f.Queue != nil {
		t.Fatalf("the gate closed but the record still carries the new fields: %+v", f)
	}
}

// The one-minute rollups that go to wwd.db are built from the records, so they carry nothing while the gate is shut either.
func TestRollupsFollowTheGate(t *testing.T) {
	level := 1
	var open atomic.Bool
	var queued atomic.Int32
	queued.Store(2)
	s := signalsSampler(&level, &open, &queued)
	for i := 0; i < 14; i++ {
		tickAt(s, s0.Add(time.Duration(i)*5*time.Second))
	}
	for _, m := range s.History(s0.Add(-time.Hour), wire.Step1m) {
		if m.Mem.DecompressRate != nil || m.Queue != nil {
			t.Fatalf("a rollup made while the gate was shut has the new fields: %+v", m)
		}
	}
}

// No gate function at all (a test, or a build before the wiring) is a shut gate.
func TestNoGateFunctionIsAShutGate(t *testing.T) {
	level := 1
	s := NewSampler(SamplerOptions{HostID: "h1", Alias: "mlab", Sources: withDecompress(&level, 10), Queued: func() (int, error) { return 9, nil }})
	tickAt(s, s0)
	f, _ := s.Latest()
	if f.V != wire.WriteProto || f.Queue != nil || f.Mem.DecompressRate != nil {
		t.Fatalf("%+v", f)
	}
}
