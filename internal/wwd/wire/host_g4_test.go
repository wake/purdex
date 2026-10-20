package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The G-4 fields have the names the contract gives them (host-metrics spec §2.1, §2.2), and are not written when they were not read.
func TestHostMetricFieldNames(t *testing.T) {
	n, f, rss := 431, 12.5, 90
	boot := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	mb := 4096
	b, err := json.Marshal(Facts{
		ProcsTotal: &n, BootAt: &boot, Self: &Self{CPUPct: &f, RSSMB: &rss},
		Mem: Mem{SwapTotalMB: &mb, WiredMB: &mb, ActiveMB: &mb, InactiveMB: &mb, FileCacheMB: &mb},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"procs_total":431`, `"boot_at":"2026-10-01T08:00:00Z"`, `"self":{"cpu_pct":12.5,"rss_mb":90}`,
		`"swap_total_mb":4096`, `"wired_mb":4096`, `"active_mb":4096`, `"inactive_mb":4096`, `"file_cache_mb":4096`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %s in %s", want, b)
		}
	}
	// a first sample has a self with no cpu yet: the key stays, as null, inside the object
	b, _ = json.Marshal(Facts{Self: &Self{RSSMB: &rss}})
	if !strings.Contains(string(b), `"self":{"cpu_pct":null,"rss_mb":90}`) {
		t.Errorf("self without cpu: %s", b)
	}
	for _, absent := range []string{"procs_total", "boot_at", "self", "swap_total_mb", "wired_mb", "file_cache_mb"} {
		b, _ = json.Marshal(Facts{})
		if strings.Contains(string(b), absent) {
			t.Errorf("%s written for a record that has none: %s", absent, b)
		}
	}
	rtt := 3.25
	b, _ = json.Marshal(Peer{RttMS: &rtt})
	if !strings.Contains(string(b), `"rtt_ms":3.25`) {
		t.Errorf("peer: %s", b)
	}
	b, _ = json.Marshal(PoolHost{RttMS: &rtt})
	if !strings.Contains(string(b), `"rtt_ms":3.25`) {
		t.Errorf("pool host: %s", b)
	}
}

// A record written before the fields (or by an older wwd) reads with them nil, and null is the same as absent. (Plain json: the
// lenient decode also validates the zone, which has nothing to do with this.)
func TestHostMetricFieldsAbsentOrNullDecodeNil(t *testing.T) {
	for _, body := range []string{`{"v":1,"mem":{}}`, `{"v":1,"procs_total":null,"boot_at":null,"self":null,"mem":{"swap_total_mb":null,"wired_mb":null}}`} {
		var f Facts
		err := json.Unmarshal([]byte(body), &f)
		if err != nil {
			t.Fatal(err)
		}
		if f.ProcsTotal != nil || f.BootAt != nil || f.Self != nil || f.Mem.SwapTotalMB != nil || f.Mem.WiredMB != nil {
			t.Errorf("%s: %+v", body, f)
		}
	}
}
