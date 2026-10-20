package facts

import (
	"encoding/csv"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/wake/purdex/internal/wwd/wire"
)

func TestParsePSI(t *testing.T) {
	s10, s60, f10, err := ParsePSI("some avg10=1.50 avg60=2.25 avg300=0.00 total=123\nfull avg10=0.75 avg60=0.10 avg300=0.00 total=9\n")
	if err != nil || s10 != 1.5 || s60 != 2.25 || f10 != 0.75 {
		t.Errorf("got %v %v %v %v", s10, s60, f10, err)
	}
	if _, _, _, err := ParsePSI("garbage\n"); err == nil {
		t.Error("a file with no some/full line must fail")
	}
}

func TestParseMeminfo(t *testing.T) {
	m, err := ParseMeminfo("MemTotal:       16384000 kB\nMemFree:  100 kB\nMemAvailable:   12800000 kB\nSwapTotal: 2097152 kB\nSwapFree: 2000000 kB\n")
	if err != nil {
		t.Fatal(err)
	}
	if m.Total != 16384000<<10 || m.Available != 12800000<<10 || !m.HasAvailable || m.SwapTotal-m.SwapFree != 97152<<10 {
		t.Errorf("%+v", m)
	}
	if _, err := ParseMeminfo("MemFree: 1 kB\n"); err == nil {
		t.Error("no MemTotal must fail")
	}
	if m, _ := ParseMeminfo("MemTotal: 1 kB\n"); m.HasAvailable {
		t.Error("MemAvailable absent must be visible")
	}
}

func TestParseCPUStat(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	busy, total, ncpu, err := ParseCPUStat("cpu  100 10 50 800 40 0 0 0 5 0\ncpu0 50 5 25 400 20 0 0 0 0 0\ncpu1 50 5 25 400 20 0 0 0 0 0\nintr 1\n")
	if err != nil {
		t.Fatal(err)
	}
	// idle = idle + iowait = 840; guest is already inside user and is not added
	if total != 1000 || busy != 160 || ncpu != 2 {
		t.Errorf("busy %d total %d ncpu %d", busy, total, ncpu)
	}
	if _, _, _, err := ParseCPUStat("intr 1\n"); err == nil {
		t.Error("no cpu line must fail")
	}
}

func TestParseLoadavgAndVmstat(t *testing.T) {
	l1, l5, _, err := ParseLoadavg("0.07 0.10 0.05 1/200 1234\n")
	if err != nil || l1 != 0.07 || l5 != 0.10 {
		t.Errorf("%v %v %v", l1, l5, err)
	}
	out, swp, err := ParseVmstat("pgpgin 5\npgpgout 672\npswpin 0\npswpout 9\n")
	if err != nil || out != 672 || swp != 9 {
		t.Errorf("%v %v %v", out, swp, err)
	}
	if _, _, err := ParseVmstat("pgpgin 5\n"); err == nil {
		t.Error("missing counters must fail")
	}
}

var lt0 = time.Date(2026, 10, 18, 5, 0, 0, 0, time.UTC)

// feed runs readings every 5 s from lt0 and returns the level after each.
func feed(p *LinuxPressure, total, avail, swap int, psi *wire.PSI, n int, from time.Time) (levels []int, end time.Time) {
	at := from
	for i := 0; i < n; i++ {
		levels = append(levels, p.Update(at, total, avail, swap, psi))
		at = at.Add(5 * time.Second)
	}
	return levels, at
}

func TestLinuxPressureThresholds(t *testing.T) {
	const total = 16384
	cases := []struct {
		name        string
		avail, swap int
		psi         *wire.PSI
		want        int
	}{
		{"idle", 12800, 0, &wire.PSI{}, 1},
		{"no psi at all, plenty of memory: unknown, not calm", 12800, 0, nil, 0},
		{"no psi, but available is low: still a warning", 3200, 0, nil, 2},
		{"no psi, but swap is over: still a warning", 12800, 600, nil, 2},
		{"some10 at the line", 12800, 0, &wire.PSI{Some10: 10}, 2},
		{"some10 below it", 12800, 0, &wire.PSI{Some10: 9.9}, 1},
		{"full10 at 1", 12800, 0, &wire.PSI{Full10: 1}, 2},
		{"available under 20%", 3200, 0, &wire.PSI{}, 2},
		{"swap over 512", 12800, 513, &wire.PSI{}, 2},
		{"swap at 512", 12800, 512, &wire.PSI{}, 1},
		{"full10 at 10", 12800, 0, &wire.PSI{Full10: 10}, 4},
		{"some60 at 25", 12800, 0, &wire.PSI{Some60: 25}, 4},
		{"available under 1536", 1535, 0, &wire.PSI{}, 4},
	}
	for _, c := range cases {
		var p LinuxPressure
		// three readings 5 s apart: a worse level has held 10 s by the third
		lv, _ := feed(&p, total, c.avail, c.swap, c.psi, 3, lt0)
		if lv[2] != c.want {
			t.Errorf("%s: level %d, want %d (%v)", c.name, lv[2], c.want, lv)
		}
	}
}

func TestLinuxPressureSwapGrowth(t *testing.T) {
	var p LinuxPressure
	if got := p.Update(lt0, 16384, 12800, 100, &wire.PSI{}); got != 1 {
		t.Fatalf("start: %d", got)
	}
	// +1 GB in a minute is the line (> 1 GB is danger); the debounce then holds it for 10 s
	var last int
	for i := 1; i <= 14; i++ {
		last = p.Update(lt0.Add(time.Duration(i)*5*time.Second), 16384, 12800, 100+1025, &wire.PSI{})
	}
	if last != 4 {
		t.Errorf("a swap that grew more than 1 GB within a minute: level %d, want 4", last)
	}
}

func TestLinuxPressureDebounce(t *testing.T) {
	var p LinuxPressure
	hot := &wire.PSI{Some10: 50}
	// a single spike moves nothing
	lv, at := feed(&p, 16384, 12800, 0, &wire.PSI{}, 1, lt0)
	l2, at := feed(&p, 16384, 12800, 0, hot, 1, at)
	l3, at := feed(&p, 16384, 12800, 0, &wire.PSI{}, 3, at)
	for _, l := range append(append(lv, l2...), l3...) {
		if l != 1 {
			t.Fatalf("a one-sample spike moved the level: %v %v %v", lv, l2, l3)
		}
	}
	// held for 10 s it rises (samples 5 s apart: at the third, 10 s after the first)
	up, at := feed(&p, 16384, 12800, 0, hot, 3, at)
	if up[0] != 1 || up[1] != 1 || up[2] != 2 {
		t.Errorf("rise after 10 s: %v", up)
	}
	// and falls only after 30 s of calm
	down, _ := feed(&p, 16384, 12800, 0, &wire.PSI{}, 8, at)
	for i, l := range down {
		want := 2
		if i >= 6 { // the 7th calm reading is 30 s after the first
			want = 1
		}
		if l != want {
			t.Errorf("fall: reading %d level %d, want %d (%v)", i, l, want, down)
		}
	}
}

func TestMemBuilderLinux(t *testing.T) {
	swap := uint64(97152) << 10
	raw := RawMem{
		Linux: true, TotalBytes: 16384 << 20, PageSize: 4096, AvailableBytes: 12800 << 20,
		SwapUsedBytes: &swap, PSI: &wire.PSI{Some10: 0.5}, Pageouts: 100, Swapouts: 7,
	}
	m := NewMemBuilder().Build(lt0, raw)
	if m.TotalMB != 16384 || m.AvailableMB != 12800 || m.Pressure != 1 {
		t.Errorf("%+v", m)
	}
	if m.PSI == nil || m.PSI.Some10 != 0.5 {
		t.Errorf("psi: %+v", m.PSI)
	}
	if m.SwapUsedMB == nil || *m.SwapUsedMB != 94 {
		t.Errorf("swap used: %v", m.SwapUsedMB)
	}
	if m.PageOutRate == nil || m.SwapOutRate == nil {
		t.Error("page and swap out rates are reported on Linux")
	}
	if m.CompressorMB != nil {
		t.Error("no compressor on Linux")
	}
}

// M-L c-squeeze: a job throttled inside its own memory limit drove the host-wide
// PSI to some10 92 / full10 90 while system.slice and user.slice stayed at 0 and
// MemAvailable barely moved. The zone must read the slices, so the level stays 1
// through the whole recording. (The CSV's mem_* columns are the host-wide ones.)
func TestLinuxPressureIgnoresHostWidePSIOfTheSqueezeRecording(t *testing.T) {
	f, err := os.Open("testdata/ml-squeeze.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range rows[0] {
		col[h] = i
	}
	num := func(r []string, name string) float64 {
		v, err := strconv.ParseFloat(r[col[name]], 64)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v
	}
	var p LinuxPressure
	hostWideHigh := 0
	for i, r := range rows[1:] {
		at, err := time.Parse("15:04:05", r[col["ts"]])
		if err != nil {
			t.Fatal(err)
		}
		if num(r, "mem_some10") > 80 {
			hostWideHigh++
		}
		// the production slices read 0.00 in this recording
		lvl := p.Update(at, 16384, int(num(r, "memavail_mb")), int(num(r, "swapused_mb")), &wire.PSI{})
		if lvl != 1 {
			t.Fatalf("row %d (%s): level %d although the production slices were at 0", i, r[col["ts"]], lvl)
		}
	}
	if hostWideHigh == 0 {
		t.Fatal("the fixture no longer shows the host-wide pressure it is meant to ignore")
	}
}
