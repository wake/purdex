package facts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

// The budget for G-2 (plan, common rules): a minute record grows by less than 2 KB for a 10-core host, with full-precision floats.
func TestMinuteRecordGrowthForTenCores(t *testing.T) {
	mk := func(withSplit bool) []byte {
		recs := make([]wire.Facts, 12)
		for i := range recs {
			f := rec(t0.Add(time.Duration(i)*5*time.Second), 1, 1000, 1, wire.ZoneNormal)
			if withSplit {
				l15, u, s := 1.0/3, 100.0/3, 100.0/7
				f.CPU.Load15, f.CPU.UserPct, f.CPU.SystemPct = &l15, &u, &s
				for c := 0; c < 10; c++ {
					cu, cs, ci := 100.0/3+float64(c), 100.0/7+float64(c), 100.0/9+float64(c)
					k := "p"
					f.CPU.Cores = append(f.CPU.Cores, wire.Core{UserPct: &cu, SystemPct: &cs, IdlePct: &ci, Kind: &k})
				}
			}
			recs[i] = f
		}
		b, err := json.Marshal(Rollup(recs))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	before, after := len(mk(false)), len(mk(true))
	t.Logf("minute record: %d bytes before, %d after (+%d)", before, after, after-before)
	if after-before >= 2048 {
		t.Errorf("a 10-core minute record grew by %d bytes, budget is under 2048", after-before)
	}
}
