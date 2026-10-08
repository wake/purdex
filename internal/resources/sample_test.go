package resources

import (
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"
)

// loadavgBuf lays out struct loadavg as M-R1 measured it: three uint32
// fixed-point values, 4 bytes of padding, then fscale as uint64 (24 bytes).
func loadavgBuf(l0, l1, l2 uint32, fscale uint64) []byte {
	b := make([]byte, 24)
	binary.NativeEndian.PutUint32(b[0:], l0)
	binary.NativeEndian.PutUint32(b[4:], l1)
	binary.NativeEndian.PutUint32(b[8:], l2)
	binary.NativeEndian.PutUint64(b[16:], fscale)
	return b
}

func TestParseLoadavg(t *testing.T) {
	// load1 17.59, load5 12.25, load15 3.5 at fscale 2048. Distinct values
	// per slot, so reading the wrong one shows.
	buf := loadavgBuf(36024, 25088, 7168, 2048)
	got, err := parseLoadavg(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got < 17.58 || got > 17.60 {
		t.Errorf("load1 = %v, want about 17.59", got)
	}

	for _, n := range []int{0, 12, 23, 25} {
		if _, err := parseLoadavg(make([]byte, n)); err == nil {
			t.Errorf("length %d must be an error", n)
		}
	}
	if _, err := parseLoadavg(loadavgBuf(1, 1, 1, 0)); err == nil {
		t.Error("fscale 0 must be an error")
	}
}

const vmStatSample = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                    68039.
Pages active:                                 332323.
Pages inactive:                               318361.
Pages speculative:                             13661.
Pages throttled:                                   0.
Pages wired down:                             137688.
Pages purgeable:                                4350.
"Translation faults":                    61269535748.
Pages copy-on-write:                     14260042953.
`

func TestParseVMStat_Speculative(t *testing.T) {
	got, err := parseVMStat(vmStatSample)
	if err != nil {
		t.Fatal(err)
	}
	want := vmStat{PageSize: 16384, Free: 68039, Inactive: 318361, Speculative: 13661}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	for name, raw := range map[string]string{
		"no page size":    strings.Replace(vmStatSample, "page size of 16384", "page size", 1),
		"no free":         strings.Replace(vmStatSample, "Pages free:", "Pages gone:", 1),
		"no inactive":     strings.Replace(vmStatSample, "Pages inactive:", "Pages gone:", 1),
		"no speculative":  strings.Replace(vmStatSample, "Pages speculative:", "Pages gone:", 1),
		"empty":           "",
		"not vm_stat out": "hello\nworld\n",
	} {
		if _, err := parseVMStat(raw); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestParsePS(t *testing.T) {
	out := "    1     0   4.5  15200\n" +
		"  558     1   0.2  13600\n" +
		"  600   558 100.0      4\n" +
		"  601   558   2,5   2048\n" + // a comma locale: skipped
		"  this is not a process\n" + // skipped
		"  602   558   1.0\n" + // too few fields: skipped
		"   -5   558   1.0   10\n" + // bad pid: skipped
		"\n"
	procs, skipped, err := parsePS(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []Proc{
		{PID: 1, PPID: 0, Pcpu: 4.5, RSSBytes: 15200 * 1024},
		{PID: 558, PPID: 1, Pcpu: 0.2, RSSBytes: 13600 * 1024},
		{PID: 600, PPID: 558, Pcpu: 100, RSSBytes: 4 * 1024},
	}
	if !slices.Equal(procs, want) {
		t.Errorf("procs = %+v, want %+v", procs, want)
	}
	if skipped != 4 {
		t.Errorf("skipped = %d, want 4 (blank lines are not counted)", skipped)
	}

	if _, _, err := parsePS("garbage\nmore garbage\n"); err == nil {
		t.Error("zero valid lines must be an error")
	}
	if _, _, err := parsePS(""); err == nil {
		t.Error("empty output must be an error")
	}
}

// fakeHost stages the four seams for one test. Anything not set in the
// struct behaves like a healthy darwin host.
type fakeHost struct {
	failSysctl map[string]bool // names whose sysctl fails
	vmErr      error
	psErr      error
	psOut      string
	cmds       []fakeCmd
}

type fakeCmd struct {
	name string
	env  []string
	args []string
}

func (f *fakeHost) install(t *testing.T) {
	t.Helper()
	oldRaw, oldU32, oldU64, oldRun := sysctlRaw, sysctlU32, sysctlU64, runCmd
	t.Cleanup(func() { sysctlRaw, sysctlU32, sysctlU64, runCmd = oldRaw, oldU32, oldU64, oldRun })

	fail := func(name string) error {
		if f.failSysctl[name] {
			return errors.New("sysctl " + name + " failed")
		}
		return nil
	}
	sysctlRaw = func(name string) ([]byte, error) {
		if err := fail(name); err != nil {
			return nil, err
		}
		return loadavgBuf(20480, 0, 0, 2048), nil // load1 = 10
	}
	sysctlU32 = func(name string) (uint32, error) {
		if err := fail(name); err != nil {
			return 0, err
		}
		switch name {
		case "hw.ncpu":
			return 10, nil
		case "kern.memorystatus_vm_pressure_level":
			return 2, nil
		case "kern.memorystatus_level":
			return 48, nil
		}
		return 0, errors.New("unexpected sysctl " + name)
	}
	sysctlU64 = func(name string) (uint64, error) {
		if err := fail(name); err != nil {
			return 0, err
		}
		if name == "hw.memsize" {
			return 17179869184, nil
		}
		return 0, errors.New("unexpected sysctl " + name)
	}
	runCmd = func(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
		f.cmds = append(f.cmds, fakeCmd{name: name, env: env, args: args})
		switch name {
		case "vm_stat":
			if f.vmErr != nil {
				return nil, f.vmErr
			}
			return []byte(vmStatSample), nil
		case "ps":
			if f.psErr != nil {
				return nil, f.psErr
			}
			if f.psOut != "" {
				return []byte(f.psOut), nil
			}
			return []byte("  100     1  50.0  1000\n  101   100  25.5  2000\n"), nil
		}
		return nil, errors.New("unexpected command " + name)
	}
}

func TestSampler_Healthy(t *testing.T) {
	f := &fakeHost{psOut: "  100     1  50.0  1000\n  101   100  25.5  2000\n  oops\n"}
	f.install(t)
	s := &sysSampler{}
	raw, procs, err := s.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if raw.Load1 != 10 || raw.NCPU != 10 || raw.MemBytes != 17179869184 ||
		raw.Pressure != 2 || raw.MemorystatusLevel != 48 {
		t.Errorf("raw = %+v", raw)
	}
	if raw.PageSize != 16384 || raw.Free != 68039 || raw.Inactive != 318361 || raw.Speculative != 13661 {
		t.Errorf("vm_stat figures = %+v", raw)
	}
	if raw.PcpuSum != 75.5 {
		t.Errorf("PcpuSum = %v, want 75.5", raw.PcpuSum)
	}
	if len(procs) != 2 || procs[1].RSSBytes != 2000*1024 {
		t.Errorf("procs = %+v", procs)
	}
	if got := s.PSLinesSkipped(); got != 1 {
		t.Errorf("PSLinesSkipped = %d, want 1", got)
	}

	// ps is forked once with a C locale (decimal point) and vm_stat once.
	var ps *fakeCmd
	counts := map[string]int{}
	for i := range f.cmds {
		counts[f.cmds[i].name]++
		if f.cmds[i].name == "ps" {
			ps = &f.cmds[i]
		}
	}
	if counts["ps"] != 1 || counts["vm_stat"] != 1 || len(f.cmds) != 2 {
		t.Errorf("forks = %v", counts)
	}
	if ps == nil || !slices.Contains(ps.env, "LC_ALL=C") {
		t.Errorf("ps must run with LC_ALL=C, env = %v", ps)
	}
	if !slices.Equal(ps.args, []string{"-axo", "pid=,ppid=,pcpu=,rss="}) {
		t.Errorf("ps args = %v", ps.args)
	}
}

func TestSampler_PressureAndLevelErrorsAreSoft(t *testing.T) {
	f := &fakeHost{failSysctl: map[string]bool{
		"kern.memorystatus_vm_pressure_level": true,
		"kern.memorystatus_level":             true,
	}}
	f.install(t)
	raw, procs, err := (&sysSampler{}).Sample(context.Background())
	if err != nil {
		t.Fatalf("soft sysctls must not fail the sample: %v", err)
	}
	if raw.Pressure != 0 || raw.MemorystatusLevel != -1 {
		t.Errorf("Pressure = %d, MemorystatusLevel = %d; want 0 and -1", raw.Pressure, raw.MemorystatusLevel)
	}
	if raw.NCPU != 10 || len(procs) == 0 {
		t.Errorf("the rest of the sample must survive: %+v", raw)
	}
}

func TestSampler_VMStatFailureFails(t *testing.T) {
	f := &fakeHost{vmErr: errors.New("vm_stat: boom")}
	f.install(t)
	if _, _, err := (&sysSampler{}).Sample(context.Background()); err == nil {
		t.Fatal("a vm_stat failure must fail the sample")
	}
}

func TestSampler_HardFailures(t *testing.T) {
	for _, name := range []string{"vm.loadavg", "hw.ncpu", "hw.memsize"} {
		t.Run(name, func(t *testing.T) {
			f := &fakeHost{failSysctl: map[string]bool{name: true}}
			f.install(t)
			if _, _, err := (&sysSampler{}).Sample(context.Background()); err == nil {
				t.Fatalf("%s failing must fail the sample", name)
			}
		})
	}
	t.Run("ps", func(t *testing.T) {
		f := &fakeHost{psErr: errors.New("ps: boom")}
		f.install(t)
		if _, _, err := (&sysSampler{}).Sample(context.Background()); err == nil {
			t.Fatal("a ps failure must fail the sample")
		}
	})
	t.Run("ps with no valid line", func(t *testing.T) {
		f := &fakeHost{psOut: "nothing here\n"}
		f.install(t)
		if _, _, err := (&sysSampler{}).Sample(context.Background()); err == nil {
			t.Fatal("zero valid ps lines must fail the sample")
		}
	})
}
