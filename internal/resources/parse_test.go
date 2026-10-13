package resources

import "testing"

func TestParseZoneAndTrend_RoundTrip(t *testing.T) {
	for _, z := range []Zone{ZoneUnknown, ZoneNormal, ZoneWarning, ZoneDanger} {
		if got := ParseZone(z.String()); got != z {
			t.Errorf("zone %v -> %v", z, got)
		}
	}
	for _, tr := range []Trend{TrendUnknown, TrendFalling, TrendFlat, TrendRising} {
		if got := ParseTrend(tr.String()); got != tr {
			t.Errorf("trend %v -> %v", tr, got)
		}
	}
	if ParseZone("") != ZoneUnknown || ParseZone("bogus") != ZoneUnknown || ParseTrend("") != TrendUnknown || ParseTrend("x") != TrendUnknown {
		t.Error("an unrecognised text must read as unknown")
	}
}
