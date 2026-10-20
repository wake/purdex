package wire

import (
	"encoding/json"
	"testing"
	"time"
)

func hostFacts(at time.Time) Facts {
	n, cpu, rss := 431, 12.5, 90
	mb := 4096
	wired, active, inactive, cache := 1000, 2000, 3000, 4000
	f := Facts{At: at, ProcsTotal: &n, Self: &Self{CPUPct: &cpu, RSSMB: &rss}}
	f.Mem = Mem{SwapTotalMB: &mb, WiredMB: &wired, ActiveMB: &active, InactiveMB: &inactive, FileCacheMB: &cache}
	return f
}

// The points carry the G-4 figures under the names the contract gives them (host-metrics spec §2.3); boot_at and rtt_ms do not go in.
func TestSeriesOfCarriesHostFields(t *testing.T) {
	f := hostFacts(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	p := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{f}}, 5).Points[0]
	if p.ProcsTotal == nil || *p.ProcsTotal != 431 || p.SelfCPUPct == nil || *p.SelfCPUPct != 12.5 || p.SelfRSSMB == nil || *p.SelfRSSMB != 90 {
		t.Fatalf("procs/self: %+v", p)
	}
	if p.SwapTotalMB == nil || *p.SwapTotalMB != 4096 || p.MemWiredMB == nil || *p.MemWiredMB != 1000 || p.MemActiveMB == nil || *p.MemActiveMB != 2000 ||
		p.MemInactiveMB == nil || *p.MemInactiveMB != 3000 || p.MemFileCacheMB == nil || *p.MemFileCacheMB != 4000 {
		t.Fatalf("mem: %+v", p)
	}
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"procs_total": "431", "self_cpu_pct": "12.5", "self_rss_mb": "90", "swap_total_mb": "4096", "mem_wired_mb": "1000",
		"mem_active_mb": "2000", "mem_inactive_mb": "3000", "mem_file_cache_mb": "4000"} {
		if string(m[k]) != want {
			t.Errorf("%s = %s, want %s", k, m[k], want)
		}
	}
	for _, k := range []string{"boot_at", "rtt_ms"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s is the current value only, not a series", k)
		}
	}
	// the point must not alias the record
	*f.ProcsTotal, *f.Self.CPUPct, *f.Mem.WiredMB = 1, 1, 1
	if *p.ProcsTotal != 431 || *p.SelfCPUPct != 12.5 || *p.MemWiredMB != 1000 {
		t.Fatalf("point aliases the record: %+v", p)
	}
}

// A record without the fields (an older wwd, a Linux host's wired) gives null, written as null; a self whose first sample has no cpu yet
// has a null cpu and still its memory.
func TestSeriesOfHostFieldsAreNullWhenAbsent(t *testing.T) {
	rss := 50
	recs := []Facts{{At: time.Unix(1, 0).UTC()}, {At: time.Unix(2, 0).UTC(), Self: &Self{RSSMB: &rss}}}
	pts := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: recs}, 5).Points
	p := pts[0]
	if p.ProcsTotal != nil || p.SelfCPUPct != nil || p.SelfRSSMB != nil || p.SwapTotalMB != nil || p.MemWiredMB != nil || p.MemActiveMB != nil ||
		p.MemInactiveMB != nil || p.MemFileCacheMB != nil {
		t.Fatalf("want nulls: %+v", p)
	}
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"procs_total", "self_cpu_pct", "self_rss_mb", "swap_total_mb", "mem_wired_mb", "mem_active_mb", "mem_inactive_mb", "mem_file_cache_mb"} {
		if string(m[k]) != "null" {
			t.Errorf("%s = %s, want null", k, m[k])
		}
	}
	if pts[1].SelfCPUPct != nil || pts[1].SelfRSSMB == nil || *pts[1].SelfRSSMB != 50 {
		t.Errorf("self without cpu: %+v", pts[1])
	}
}

// A point from before these fields decodes with them nil.
func TestSeriesPointHostFieldsOldFormatDecodes(t *testing.T) {
	old := `{"at":"2026-10-20T00:00:00Z","zone":"normal","cpu_idle_pct":90,"mem_available_mb":1000,"pressure":1,"page_out_rate":null,"disk_max_used_pct":50,"headroom":{"heavy":1,"light":2},"load1":1}`
	p, err := DecodeLenient[SeriesPoint]([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if p.ProcsTotal != nil || p.SelfCPUPct != nil || p.SwapTotalMB != nil || p.MemFileCacheMB != nil {
		t.Fatalf("%+v", p)
	}
}
