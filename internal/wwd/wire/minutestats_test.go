package wire

import (
	"encoding/json"
	"strings"
	"testing"
)

// Only a rollup carries stats: a record without them has no "stats" key at all, and a stored minute from before
// decodes with Stats nil (readers then approximate).
func TestStatsAreAbsentUnlessARollupCarriesThem(t *testing.T) {
	f := roundTrip[Facts](t, "facts_linux.json")
	if f.Stats != nil {
		t.Fatalf("a 5 s record has no stats: %+v", f.Stats)
	}
	b, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"stats"`) {
		t.Fatalf("no stats key expected: %s", b)
	}
	m := roundTrip[Facts](t, "facts_minute_stats.json")
	if m.Stats == nil || m.Stats.SampledS != 60 || m.Stats.FullStarts != 2 || m.Stats.GateWaitingMax != nil {
		t.Fatalf("%+v", m.Stats)
	}
}

func TestMinuteStatsValidate(t *testing.T) {
	ok := MinuteStats{SampledS: 60, FullTicks: 12, FullStarts: 1, FullLongestS: 60, NormalS: 30, WarningS: 20, DangerS: 10}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]MinuteStats{
		"negative":           {SampledS: -1},
		"zones over sampled": {SampledS: 60, NormalS: 40, WarningS: 30},
		"ticks over sampled": {SampledS: 2, FullTicks: 3},
	} {
		if err := st.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
