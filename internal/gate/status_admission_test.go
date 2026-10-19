package gate

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type peekOnly struct {
	p    Pressure
	have bool
}

func (peekOnly) Read() (Pressure, error)  { panic("status never samples") }
func (f peekOnly) Peek() (Pressure, bool) { return f.p, f.have }

// Hold is the rule, Admit its negation: for every pressure they agree, and the reason names what held it.
func TestAdmission_HoldIsTheRuleAdmitIsItsNegation(t *testing.T) {
	a := AdmissionConfig{Enabled: true, MaxPressureLevel: 1, PageRateAuto: false, MaxPageRate: 100}
	for _, c := range []struct {
		p      Pressure
		held   bool
		reason string
	}{
		{Pressure{Level: 1, PageRate: 0}, false, ""},
		{Pressure{Level: 1, PageRate: 99.9}, false, ""},
		{Pressure{Level: 1, PageRate: 100}, true, "page rate 100/s >= 100/s"}, // at the limit is over it
		{Pressure{Level: 2, PageRate: 0}, true, "pressure 2 > 1"},
		{Pressure{Level: 4, PageRate: 500}, true, "pressure 4 > 1"}, // the level is named first
		{Pressure{Level: 0, PageRate: 0}, false, ""},
	} {
		held, why := a.Hold(c.p)
		assert.Equal(t, c.held, held, "%+v", c.p)
		assert.Equal(t, c.reason, why, "%+v", c.p)
		assert.Equal(t, !c.held, a.Admit(c.p), "%+v", c.p)
	}
	off := a
	off.Enabled = false
	held, why := off.Hold(Pressure{Level: 4, PageRate: 1e6})
	assert.False(t, held)
	assert.Empty(t, why)
}

func TestAdmission_DiskShort(t *testing.T) {
	a := AdmissionConfig{Enabled: true, DiskMinGB: 10}
	short, why := a.DiskShort(8 << 30)
	assert.True(t, short)
	assert.Equal(t, "disk 8 GB < 10", why)
	short, _ = a.DiskShort(10 << 30)
	assert.False(t, short, "exactly the floor is enough")
	a.DiskMinGB = 0
	short, _ = a.DiskShort(0)
	assert.False(t, short, "no floor, no check")
	// the refusal message of a command is the same rule
	a.DiskMinGB = 10
	assert.Contains(t, a.DiskRefusal("/x", 8<<30), "8.0 GB free")
	assert.Empty(t, a.DiskRefusal("/x", 10<<30))
}

// Status carries the same answer a start would get, without sampling.
func TestStatus_AdmissionFollowsTheRule(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Admission.Enabled, c.Admission.MaxPressureLevel, c.Admission.PageRateAuto, c.Admission.MaxPageRate = true, 1, false, 100
	})
	g.DiskFree = func(string) (int64, error) { return 100 << 30, nil }

	g.Pressure = peekOnly{Pressure{Level: 1}, true}
	assert.Equal(t, AdmissionState{}, g.Status().Admission)

	g.Pressure = peekOnly{Pressure{Level: 2}, true}
	assert.Equal(t, AdmissionState{Held: true, Reason: "pressure 2 > 1"}, g.Status().Admission)

	g.Pressure = peekOnly{Pressure{Level: 1, PageRate: 250}, true}
	assert.Equal(t, AdmissionState{Held: true, Reason: "page rate 250/s >= 100/s"}, g.Status().Admission)

	g.Pressure = peekOnly{Pressure{}, false} // nothing readable: not held
	assert.False(t, g.Status().Admission.Held)

	g.Pressure = peekOnly{Pressure{Level: 1}, true}
	g.SetConfig(func(c *Config) { c.Admission.DiskMinGB = 10 })
	g.DiskFree = func(string) (int64, error) { return 8 << 30, nil }
	assert.Equal(t, AdmissionState{Held: true, Reason: "disk 8 GB < 10"}, g.Status().Admission)

	g.SetConfig(func(c *Config) { c.Admission.Enabled = false })
	assert.Equal(t, AdmissionState{}, g.Status().Admission, "a gate with no admission holds nothing")
}

// Admission is the same answer without Status's lsof pass over every held slot: for a reader that only asks whether the host would hold a
// new heavy start now (wwd's worker, before it takes a slot).
func TestAdmissionIsStatusAdmissionWithoutTheRest(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Admission.Enabled, c.Admission.MaxPressureLevel, c.Admission.PageRateAuto, c.Admission.MaxPageRate, c.Admission.DiskMinGB = true, 1, false, 100, 10
	})
	g.DiskFree = func(string) (int64, error) { return 100 << 30, nil }
	for _, p := range []Pressure{{Level: 1}, {Level: 2}, {Level: 1, PageRate: 250}, {Level: 4, PageRate: 9}} {
		g.Pressure = peekOnly{p, true}
		assert.Equal(t, g.Status().Admission, g.Admission(), "%+v", p)
	}
	g.Pressure = peekOnly{Pressure{Level: 1}, true}
	g.DiskFree = func(string) (int64, error) { return 8 << 30, nil }
	assert.Equal(t, AdmissionState{Held: true, Reason: "disk 8 GB < 10"}, g.Admission())
	g.SetConfig(func(c *Config) { c.Admission.Enabled = false })
	assert.Equal(t, AdmissionState{}, g.Admission())
}

// AdmissionFor looks at the directory it is given (a worker's work root on another volume), not at the working directory: the free space
// of that directory is what holds it.
func TestAdmissionForCountsTheGivenDirectory(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) {
		c.Admission.Enabled, c.Admission.MaxPressureLevel, c.Admission.PageRateAuto, c.Admission.MaxPageRate, c.Admission.DiskMinGB = true, 1, false, 100, 10
	})
	g.Pressure = peekOnly{Pressure{Level: 1}, true}
	g.DiskFree = func(p string) (int64, error) {
		if p == "/small-volume/work" {
			return 3 << 30, nil
		}
		return 100 << 30, nil
	}
	assert.Equal(t, AdmissionState{}, g.AdmissionFor("/big-volume/work"))
	assert.Equal(t, AdmissionState{Held: true, Reason: "disk 3 GB < 10"}, g.AdmissionFor("/small-volume/work"))
	assert.Equal(t, AdmissionState{}, g.Admission(), "the working directory is on the big volume")
	// $TMPDIR counts beside the directory
	g.DiskFree = func(p string) (int64, error) {
		if p == os.TempDir() {
			return 2 << 30, nil
		}
		return 100 << 30, nil
	}
	assert.Equal(t, AdmissionState{Held: true, Reason: "disk 2 GB < 10"}, g.AdmissionFor("/big-volume/work"))
}

// The state in Status and the decision a start takes come from one function: on the same reading they agree.
func TestStatus_AdmissionAgreesWithWouldWait(t *testing.T) {
	g, _ := testGate(t)
	g.SetConfig(func(c *Config) { c.Admission.Enabled, c.Admission.MaxPressureLevel = true, 1 })
	g.DiskFree = func(string) (int64, error) { return 100 << 30, nil }
	for _, level := range []int{1, 2, 4} {
		g.Pressure = peekOnly{Pressure{Level: level}, true}
		w := g.WouldWait(ClassHeavy)
		assert.Equal(t, w.Wait && w.Reason == "pressure", g.Status().Admission.Held, "level %d", level)
	}
	_ = time.Second
}
