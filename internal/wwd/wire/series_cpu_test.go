package wire

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSeriesOfCarriesCPUSplit(t *testing.T) {
	f := fullFacts(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	l15, u, s := 0.75, 31.5, 8.25
	f.CPU.Load15, f.CPU.UserPct, f.CPU.SystemPct = &l15, &u, &s
	k := "p"
	f.CPU.Cores = []Core{{Kind: &k}}
	p := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{f}}, 5).Points[0]
	if p.Load15 == nil || *p.Load15 != 0.75 || p.CPUUserPct == nil || *p.CPUUserPct != 31.5 || p.CPUSystemPct == nil || *p.CPUSystemPct != 8.25 {
		t.Fatalf("%+v", p)
	}
	l15 = 99 // the point is its own copy
	if *p.Load15 != 0.75 {
		t.Error("point aliases the record")
	}
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	if _, ok := m["cores"]; ok {
		t.Error("cores are not in a series (spec §2.3)")
	}
	for _, key := range []string{"load15", "cpu_user_pct", "cpu_system_pct"} {
		if _, ok := m[key]; !ok {
			t.Errorf("%s missing", key)
		}
	}
	bare := SeriesOf(FactsHistoryResponse{Step: Step5s, Records: []Facts{{At: time.Unix(1, 0).UTC()}}}, 5).Points[0]
	if bare.Load15 != nil || bare.CPUUserPct != nil || bare.CPUSystemPct != nil {
		t.Errorf("want nulls: %+v", bare)
	}
}
