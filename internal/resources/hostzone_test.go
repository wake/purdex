package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func zoneUse(mem, load1 float64, pressure int) HostUse {
	return HostUse{Mem: mem, Load1: load1, NCPU: 10, Pressure: pressure}
}

func TestZone_String(t *testing.T) {
	assert.Equal(t, "unknown", ZoneUnknown.String())
	assert.Equal(t, "normal", ZoneNormal.String())
	assert.Equal(t, "warning", ZoneWarning.String())
	assert.Equal(t, "danger", ZoneDanger.String())
	assert.Equal(t, "unknown", Zone(99).String())
}

// Each enter condition alone, from a fresh latch. The boundaries are the
// thresholds themselves: mem exactly at 80 is warning, exactly at 90 danger.
func TestZoneLatch_EnterConditions(t *testing.T) {
	s := DefaultSettings()
	for _, c := range []struct {
		name string
		h    HostUse
		want Zone
	}{
		{"idle", zoneUse(40, 1, 1), ZoneNormal},
		{"mem 79.9", zoneUse(79.9, 1, 1), ZoneNormal},
		{"mem exactly 80", zoneUse(80, 1, 1), ZoneWarning},
		{"mem 89.9", zoneUse(89.9, 1, 1), ZoneWarning},
		{"mem exactly 90", zoneUse(90, 1, 1), ZoneDanger},
		{"pressure 2", zoneUse(40, 1, 2), ZoneWarning},
		{"pressure 4", zoneUse(40, 1, 4), ZoneDanger},
		{"load just under ncpu", zoneUse(40, 9.99, 1), ZoneNormal},
		{"load exactly ncpu", zoneUse(40, 10, 1), ZoneWarning},
		{"load far over ncpu never danger", zoneUse(40, 80, 1), ZoneWarning},
		{"pressure 0 (unknown reading) is not pressure", zoneUse(40, 1, 0), ZoneNormal},
	} {
		var l ZoneLatch
		assert.Equal(t, c.want, l.Update(c.h, true, s), c.name)
	}
}

// Thresholds follow the settings, not the defaults.
func TestZoneLatch_UsesSettingsThresholds(t *testing.T) {
	s := Settings{MemWarnPct: intp(60), MemDangerPct: intp(70)}.Effective()
	var l ZoneLatch
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(59.9, 1, 1), true, s))
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(60, 1, 1), true, s))
	assert.Equal(t, ZoneDanger, l.Update(zoneUse(70, 1, 1), true, s))
}

// Hysteresis: warning is left only when every condition clears with its
// margin (mem below warn-2, load below 0.9 ncpu, pressure below 2).
func TestZoneLatch_WarningHysteresis(t *testing.T) {
	s := DefaultSettings()
	var l ZoneLatch
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(81, 1, 1), true, s))
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(79, 1, 1), true, s), "79 is still inside the margin")
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(78, 1, 1), true, s), "78 is not below 78")
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(77, 1, 1), true, s))

	// Load margin: 0.9 x 10 = 9.
	l = ZoneLatch{}
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(40, 10, 1), true, s))
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(40, 9, 1), true, s), "9.0 is not below 9.0")
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(40, 8.9, 1), true, s))

	// Pressure margin: it must be back to 1.
	l = ZoneLatch{}
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(40, 1, 2), true, s))
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(40, 1, 2), true, s))
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(40, 1, 1), true, s))

	// One leave condition failing is enough to stay.
	l = ZoneLatch{}
	l.Update(zoneUse(81, 1, 1), true, s)
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(70, 9.5, 1), true, s), "load still holds it")
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(70, 1, 2), true, s), "pressure still holds it")
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(79, 1, 1), true, s), "mem still holds it")
}

// Danger steps down one level only when its leave conditions hold; it drops
// to warning, or straight to normal if warning's conditions are clear too.
func TestZoneLatch_DangerHysteresis(t *testing.T) {
	s := DefaultSettings()
	var l ZoneLatch
	assert.Equal(t, ZoneDanger, l.Update(zoneUse(91, 1, 1), true, s))
	assert.Equal(t, ZoneDanger, l.Update(zoneUse(88.5, 1, 1), true, s), "88.5 is inside the margin")
	assert.Equal(t, ZoneDanger, l.Update(zoneUse(88, 1, 1), true, s), "88 is not below 88")
	assert.Equal(t, ZoneDanger, l.Update(zoneUse(40, 1, 4), true, s), "critical pressure holds danger")
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(87, 1, 2), true, s), "pressure 2 keeps it at warning")

	l = ZoneLatch{}
	l.Update(zoneUse(91, 1, 1), true, s)
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(87, 1, 1), true, s), "mem 87 is still warning")
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(79, 1, 1), true, s))
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(77, 1, 1), true, s))

	// Straight to normal in one step when everything is clear.
	l = ZoneLatch{}
	l.Update(zoneUse(95, 1, 4), true, s)
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(30, 1, 1), true, s))

	// Load alone never lifts the latch to danger, even from warning.
	l = ZoneLatch{}
	l.Update(zoneUse(40, 12, 1), true, s)
	assert.Equal(t, ZoneWarning, l.Update(zoneUse(40, 50, 1), true, s))
}

// A reading that cannot be judged is unknown, and the latch restarts from
// normal afterwards (no stale hysteresis from before the gap).
func TestZoneLatch_Unknown(t *testing.T) {
	s := DefaultSettings()
	var l ZoneLatch
	assert.Equal(t, ZoneUnknown, l.Update(zoneUse(40, 1, 1), false, s), "unavailable")
	assert.Equal(t, ZoneUnknown, l.Update(HostUse{Mem: 40, NCPU: 0}, true, s), "no cpu count")
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(40, 1, 1), true, s), "and back")

	assert.Equal(t, ZoneWarning, l.Update(zoneUse(81, 1, 1), true, s))
	assert.Equal(t, ZoneUnknown, l.Update(zoneUse(81, 1, 1), false, s))
	assert.Equal(t, ZoneNormal, l.Update(zoneUse(79, 1, 1), true, s), "79 would have held warning, but the latch was reset")
}
