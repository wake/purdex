package wire

import (
	"encoding/json"
	"testing"
	"time"
)

func fullFacts(at time.Time) Facts {
	dec, so := 12.5, 3.0
	swap, comp := 2048, 512
	return Facts{
		At:     at,
		CPU:    CPU{Load1: 1.5, Load5: 2.5},
		Mem:    Mem{SwapUsedMB: &swap, CompressorMB: &comp, SwapOutRate: &so, DecompressRate: &dec},
		Disk:   []Disk{{Mount: "/", UsedPct: 60, FreeGB: 90}, {Mount: "/Volumes/x", UsedPct: 20, FreeGB: 800}},
		Worker: FactsWorker{Running: Slots{Heavy: 2, Light: 1}},
		Queue:  &Queue{Queued: 4},
	}
}

func TestSeriesOfCarriesFactsFields(t *testing.T) {
	f := fullFacts(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	p := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{f}}, 5).Points[0]
	if p.Load1 == nil || *p.Load1 != 1.5 || p.Load5 == nil || *p.Load5 != 2.5 {
		t.Fatalf("load: %+v", p)
	}
	if p.SwapUsedMB == nil || *p.SwapUsedMB != 2048 || p.CompressorMB == nil || *p.CompressorMB != 512 ||
		p.SwapOutRate == nil || *p.SwapOutRate != 3 || p.DecompressRate == nil || *p.DecompressRate != 12.5 {
		t.Fatalf("mem: %+v", p)
	}
	if p.WorkerRunning == nil || *p.WorkerRunning != (Slots{Heavy: 2, Light: 1}) || p.QueueQueued == nil || *p.QueueQueued != 4 {
		t.Fatalf("worker/queue: %+v", p)
	}
	if len(p.Disks) != 2 || p.Disks[1] != (SeriesDisk{Mount: "/Volumes/x", UsedPct: 20, FreeGB: 800}) {
		t.Fatalf("disks: %+v", p.Disks)
	}
	// the point must not alias the record
	*f.Mem.SwapUsedMB = 1
	f.Queue.Queued = 99
	if *p.SwapUsedMB != 2048 || *p.QueueQueued != 4 {
		t.Fatalf("point aliases the record: %+v", p)
	}
}

func TestSeriesOfAbsentFieldsAreNull(t *testing.T) {
	p := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{{At: time.Unix(1, 0).UTC()}}}, 5).Points[0]
	if p.SwapUsedMB != nil || p.CompressorMB != nil || p.SwapOutRate != nil || p.DecompressRate != nil || p.QueueQueued != nil || p.Disks != nil {
		t.Fatalf("want nulls: %+v", p)
	}
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"swap_used_mb", "compressor_mb", "swap_out_rate", "decompress_rate", "queue_queued", "disks"} {
		if string(m[k]) != "null" {
			t.Errorf("%s = %s, want null", k, m[k])
		}
	}
}

// A point written before these fields existed (the 8 old keys) still decodes, with the new ones nil.
func TestSeriesPointOldFormatDecodes(t *testing.T) {
	old := `{"at":"2026-10-20T00:00:00Z","zone":"normal","cpu_idle_pct":90,"mem_available_mb":1000,"pressure":1,"page_out_rate":null,"disk_max_used_pct":50,"headroom":{"heavy":1,"light":2}}`
	p, err := DecodeLenient[SeriesPoint]([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if p.CPUIdlePct != 90 || p.Load1 != nil || p.WorkerRunning != nil || p.Disks != nil || p.QueueQueued != nil {
		t.Fatalf("%+v", p)
	}
	full := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{fullFacts(time.Unix(5, 0).UTC())}}, 1).Points[0]
	b, _ := json.Marshal(full)
	back, err := DecodeLenient[SeriesPoint](b)
	if err != nil || back.Load5 == nil || *back.Load5 != 2.5 || len(back.Disks) != 2 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}
