package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// (M: admit at level 2; M: ignore the page rate.)
func TestAdmission_LevelAndRate(t *testing.T) {
	a := Defaults().Admission // level 1, rate auto = 100
	assert.True(t, a.Admit(Pressure{Level: 1, PageRate: 0}))
	assert.True(t, a.Admit(Pressure{Level: 1, PageRate: 99.9}))
	assert.False(t, a.Admit(Pressure{Level: 1, PageRate: 100}), "at the rate")
	assert.False(t, a.Admit(Pressure{Level: 2}), "warn level")
	assert.False(t, a.Admit(Pressure{Level: 4}), "critical level")

	a.MaxPressureLevel = 2
	assert.True(t, a.Admit(Pressure{Level: 2}), "the level is a setting")
	a.PageRateAuto, a.MaxPageRate = false, 500
	assert.True(t, a.Admit(Pressure{Level: 1, PageRate: 400}))
	assert.False(t, a.Admit(Pressure{Level: 1, PageRate: 500}))
}

func TestAdmission_DisabledAdmitsEverythingAndChecksNoDisk(t *testing.T) {
	a := Defaults().Admission
	a.Enabled = false
	assert.True(t, a.Admit(Pressure{Level: 4, PageRate: 1e6}))
	assert.Empty(t, a.DiskRefusal("/x", 0))
}

func TestAdmission_DiskFloor(t *testing.T) {
	a := Defaults().Admission // 10 GB
	assert.Empty(t, a.DiskRefusal("/x", 10<<30), "exactly the floor is enough")
	msg := a.DiskRefusal("/work", 9<<30)
	assert.Contains(t, msg, "/work has 9.0 GB free")
	assert.Contains(t, msg, "disk_min_gb")
	a.DiskMinGB = 0
	assert.Empty(t, a.DiskRefusal("/x", 0), "0 turns it off")
}
