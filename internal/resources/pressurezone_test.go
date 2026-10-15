package resources

import "testing"

func TestPressureZoneLatch(t *testing.T) {
	const limit = 50.0
	type step struct {
		level       int
		rate        float64
		unavail     bool
		want        Zone
		noRateLimit bool
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"calm host stays normal", []step{{1, 0, false, ZoneNormal, false}, {1, 49.9, false, ZoneNormal, false}, {0, 0, false, ZoneNormal, false}}},
		{"warn level enters warning at once", []step{{1, 0, false, ZoneNormal, false}, {2, 0, false, ZoneWarning, false}}},
		{"page rate at the limit enters warning", []step{{1, 50, false, ZoneWarning, false}}},
		{"critical enters danger, even from normal", []step{{4, 0, false, ZoneDanger, false}}},
		{"danger leaves to warning while the warn level holds", []step{{4, 0, false, ZoneDanger, false}, {2, 0, false, ZoneWarning, false}}},
		{"danger leaves to normal directly when warning is clear too", []step{{4, 0, false, ZoneDanger, false}, {1, 0, false, ZoneNormal, false}}},
		{"warning is held until the rate is under 0.9 x the limit", []step{
			{1, 60, false, ZoneWarning, false}, {1, 49, false, ZoneWarning, false}, {1, 44, false, ZoneNormal, false}}},
		{"warning is held while the level is 2", []step{{2, 0, false, ZoneWarning, false}, {2, 10, false, ZoneWarning, false}, {1, 10, false, ZoneNormal, false}}},
		{"a rate alone never makes danger", []step{{1, 5000, false, ZoneWarning, false}}},
		{"an unavailable reading is unknown and starts again from normal", []step{
			{2, 0, false, ZoneWarning, false}, {0, 0, true, ZoneUnknown, false}, {1, 49, false, ZoneNormal, false}}},
		{"no rate limit: the level alone decides", []step{{1, 9999, false, ZoneNormal, true}, {2, 0, false, ZoneWarning, true}, {1, 9999, false, ZoneNormal, true}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var l PressureZoneLatch
			for i, s := range c.steps {
				lim := limit
				if s.noRateLimit {
					lim = 0
				}
				if got := l.Update(s.level, s.rate, lim, !s.unavail); got != s.want {
					t.Fatalf("step %d (level %d rate %v): zone = %v, want %v", i, s.level, s.rate, got, s.want)
				}
			}
		})
	}
}

// Memory in use and load do not enter this zone: that is what the older ZoneLatch judges, and what the fleet still uses.
func TestPressureZoneLatch_IgnoresMemoryPercentAndLoad(t *testing.T) {
	var l PressureZoneLatch
	var old ZoneLatch
	h := HostUse{NCPU: 10, Mem: 95, Load1: 20, Pressure: 1}
	if got := old.Update(h, true, DefaultSettings()); got == ZoneNormal {
		t.Fatalf("premise: the older latch should not read %+v as normal", h)
	}
	if got := l.Update(h.Pressure, 0, 50, true); got != ZoneNormal {
		t.Fatalf("pressure zone = %v, want normal at level 1 and no paging", got)
	}
}
