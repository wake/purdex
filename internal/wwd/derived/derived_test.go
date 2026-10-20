package derived

import (
	"math/rand"
	"testing"

	"github.com/wake/purdex/internal/resources"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The latch must give exactly the answers resources.PressureZoneLatch gives: it
// is the same rule (spec §3.2), copied because wwd cannot import internal/resources
// (it pulls in internal/agent). The sequence is random but seeded.
func TestZoneLatchMatchesPressureZoneLatch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var want resources.PressureZoneLatch
	var got ZoneLatch
	levels := []int{0, 1, 1, 1, 2, 4}
	for i := 0; i < 5000; i++ {
		level := levels[rng.Intn(len(levels))]
		rate := float64(rng.Intn(90))
		limit := []float64{0, PageRateLimit}[rng.Intn(2)]
		avail := rng.Intn(10) > 0
		w := want.Update(level, rate, limit, avail)
		g := got.Update(level, rate, limit, avail)
		if w.String() != string(g) {
			t.Fatalf("step %d level=%d rate=%v limit=%v avail=%v: resources %q, derived %q", i, level, rate, limit, avail, w, g)
		}
	}
}

func TestZoneTable(t *testing.T) {
	type step struct {
		level int
		rate  float64
		known bool
		want  wire.Zone
	}
	cases := []struct {
		name  string
		limit float64
		steps []step
	}{
		{"danger at pressure 4", PageRateLimit, []step{{4, 0, true, wire.ZoneDanger}}},
		{"warning at pressure 2", PageRateLimit, []step{{2, 0, true, wire.ZoneWarning}}},
		{"warning at the page rate limit", PageRateLimit, []step{{1, 50, true, wire.ZoneWarning}}},
		{"leaves warning only below 0.9 x limit", PageRateLimit, []step{{1, 50, true, wire.ZoneWarning}, {1, 46, true, wire.ZoneWarning}, {1, 44, true, wire.ZoneNormal}}},
		{"unknown resets the latch", PageRateLimit, []step{{2, 0, true, wire.ZoneWarning}, {0, 0, false, wire.ZoneUnknown}, {1, 0, true, wire.ZoneNormal}}},
		{"linux: the rate is not used", 0, []step{{1, 5000, true, wire.ZoneNormal}, {2, 0, true, wire.ZoneWarning}, {4, 0, true, wire.ZoneDanger}}},
	}
	for _, c := range cases {
		var l ZoneLatch
		for i, s := range c.steps {
			if got := l.Update(s.level, s.rate, c.limit, s.known); got != s.want {
				t.Errorf("%s step %d: got %q want %q", c.name, i, got, s.want)
			}
		}
	}
}

func in() Input {
	return Input{
		Pressure: 1, PageRate: 0, PressureKnown: true, RateLimit: PageRateLimit,
		Slots: wire.Slots{Heavy: 1, Light: 2}, Running: wire.Slots{},
		AvailableMB: 4096,
	}
}

func TestHeadroom(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*Input)
		heavy int
		light int
	}{
		{"idle host", func(i *Input) {}, 1, 2},
		{"slots in use are subtracted", func(i *Input) { i.Running = wire.Slots{Heavy: 1, Light: 1} }, 0, 1},
		{"running above the slots never goes negative", func(i *Input) { i.Running = wire.Slots{Heavy: 3, Light: 5} }, 0, 0},
		{"warning stops heavy, light continues", func(i *Input) { i.Pressure = 2 }, 0, 2},
		{"danger stops both", func(i *Input) { i.Pressure = 4 }, 0, 0},
		{"unknown zone stops heavy only", func(i *Input) { i.PressureKnown = false }, 0, 2},
		{"owner active stops both", func(i *Input) { i.OwnerActive = true }, 0, 0},
		{"an exception holds heavy", func(i *Input) { i.Exceptions = []Hold{{Name: "app-dev-package"}} }, 0, 2},
		{"an exception that also holds light", func(i *Input) { i.Exceptions = []Hold{{Name: "x", Light: true}} }, 0, 0},
		{"two exceptions, one holds light", func(i *Input) { i.Exceptions = []Hold{{Name: "a"}, {Name: "b", Light: true}} }, 0, 0},
		// 8b-3 PR-C: a release hold takes the light slots down to one while it lasts (spec §13.3); a plain hold does not
		{"a release hold caps the light slots at 1", func(i *Input) { i.Exceptions = []Hold{{Name: "r", Release: true}} }, 0, 1},
		{"a release hold counts what runs against its one slot", func(i *Input) {
			i.Exceptions = []Hold{{Name: "r", Release: true}}
			i.Running = wire.Slots{Light: 1}
		}, 0, 0},
		{"a release hold does not raise a smaller setting", func(i *Input) {
			i.Exceptions = []Hold{{Name: "r", Release: true}}
			i.Slots = wire.Slots{Light: 0}
		}, 0, 0},
		{"a release hold that also holds light still stops light", func(i *Input) { i.Exceptions = []Hold{{Name: "r", Light: true, Release: true}} }, 0, 0},
		{"a hold without release leaves the light slots", func(i *Input) { i.Exceptions = []Hold{{Name: "p"}} }, 0, 2},
		{"page rate at the limit is a warning", func(i *Input) { i.PageRate = 50 }, 0, 2},
		{"mserv sized slots", func(i *Input) { i.Slots = wire.Slots{Heavy: 0, Light: 1} }, 0, 1},
		// WWD-9 W3: the pdx gate is no longer read; a job running takes its own slot and nothing else
		{"a job running takes its slot and nothing else", func(i *Input) { i.Slots.Heavy = 2; i.Running = wire.Slots{Heavy: 1} }, 1, 2},
	}
	for _, c := range cases {
		inp := in()
		c.edit(&inp)
		var l ZoneLatch
		got := Compute(&l, inp)
		if got.Headroom.Heavy != c.heavy || got.Headroom.Light != c.light {
			t.Errorf("%s: headroom %+v want heavy=%d light=%d (zone %s)", c.name, got.Headroom, c.heavy, c.light, got.Zone)
		}
	}
}

func TestSessionsRoom(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Input)
		want int
	}{
		{"plenty of memory", func(i *Input) { i.AvailableMB = 4096 }, 6},
		{"capped", func(i *Input) { i.AvailableMB = 64000 }, 8},
		{"just the reserve", func(i *Input) { i.AvailableMB = 1024 }, 0},
		{"below the reserve", func(i *Input) { i.AvailableMB = 300 }, 0},
		{"warning means none", func(i *Input) { i.AvailableMB = 8000; i.Pressure = 2 }, 0},
		{"unknown pressure means none", func(i *Input) { i.AvailableMB = 8000; i.PressureKnown = false }, 0},
		{"compressed memory counts against the room", func(i *Input) { i.AvailableMB = 4096; i.CompressorMB = 4096 }, 4}, // (4096-1024-1024)/512
		{"a big compressor can empty the room", func(i *Input) { i.AvailableMB = 2000; i.CompressorMB = 8000 }, 0},
		{"each session is 512 MB", func(i *Input) { i.AvailableMB = 1024 + 512*3 + 100 }, 3},
	}
	for _, c := range cases {
		inp := in()
		c.edit(&inp)
		var l ZoneLatch
		if got := Compute(&l, inp).SessionsRoom; got != c.want {
			t.Errorf("%s: %d want %d", c.name, got, c.want)
		}
	}
}
