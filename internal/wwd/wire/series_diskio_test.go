package wire

import (
	"encoding/json"
	"testing"
	"time"
)

// The points carry the disks' throughput under the contract's names (host-metrics spec §2.3), a null rate stays null inside its entry,
// and the point shares nothing with the record.
func TestSeriesOfCarriesDiskIO(t *testing.T) {
	rb, wb := 1500.5, 0.0
	f := Facts{At: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), DiskIO: []DiskIO{
		{Device: "disk0", ReadBps: &rb, WriteBps: &wb},
		{Device: "disk4"},
	}}
	p := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{f}}, 5).Points[0]
	if len(p.DiskIO) != 2 || p.DiskIO[0].Device != "disk0" || p.DiskIO[0].ReadBps == nil || *p.DiskIO[0].ReadBps != 1500.5 ||
		p.DiskIO[0].WriteBps == nil || *p.DiskIO[0].WriteBps != 0 || p.DiskIO[1].Device != "disk4" || p.DiskIO[1].ReadBps != nil {
		t.Fatalf("%+v", p.DiskIO)
	}
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := `[{"device":"disk0","read_bps":1500.5,"write_bps":0,"read_iops":null,"write_iops":null},{"device":"disk4","read_bps":null,"write_bps":null,"read_iops":null,"write_iops":null}]`
	if string(m["disk_io"]) != want {
		t.Errorf("disk_io = %s", m["disk_io"])
	}
	*f.DiskIO[0].ReadBps = 1
	if *p.DiskIO[0].ReadBps != 1500.5 {
		t.Errorf("the point aliases the record")
	}
}

// A record that read no disk io gives null, written as null; a point from before the field decodes with it nil.
func TestSeriesOfDiskIOAbsentIsNull(t *testing.T) {
	p := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{{At: time.Unix(1, 0).UTC()}}}, 5).Points[0]
	if p.DiskIO != nil {
		t.Fatalf("%+v", p.DiskIO)
	}
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["disk_io"]) != "null" {
		t.Errorf("disk_io = %s want null", m["disk_io"])
	}
	old := `{"at":"2026-10-20T00:00:00Z","zone":"normal","cpu_idle_pct":90,"mem_available_mb":1000,"pressure":1,"page_out_rate":null,"disk_max_used_pct":50,"headroom":{"heavy":1,"light":2}}`
	q, err := DecodeLenient[SeriesPoint]([]byte(old))
	if err != nil || q.DiskIO != nil {
		t.Fatalf("%+v %v", q.DiskIO, err)
	}
}
