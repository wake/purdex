//go:build darwin

package facts

import (
	"encoding/binary"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// struct xsw_usage: total, avail, used (uint64 each), then the page size and the encrypted flag.
func TestParseSwapUsage(t *testing.T) {
	b := make([]byte, 32)
	binary.NativeEndian.PutUint64(b[0:], 4096<<20)
	binary.NativeEndian.PutUint64(b[8:], 3000<<20)
	binary.NativeEndian.PutUint64(b[16:], 1096<<20)
	total, used, err := parseSwapUsage(b)
	if err != nil || total != 4096<<20 || used != 1096<<20 {
		t.Fatalf("total %d used %d err %v", total, used, err)
	}
	if _, _, err := parseSwapUsage(b[:23]); err == nil {
		t.Error("a short reply must fail")
	}
}

func TestBootFromTimeval(t *testing.T) {
	got, ok := bootFromTimeval(&unix.Timeval{Sec: 1_760_000_000, Usec: 500_000})
	if !ok || !got.Equal(time.Unix(1_760_000_000, 500_000_000)) {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := bootFromTimeval(&unix.Timeval{}); ok {
		t.Error("a zero boot time is unknown, not the epoch")
	}
	if _, ok := bootFromTimeval(nil); ok {
		t.Error("no reading is unknown")
	}
}

// The real host gives every G-4 figure, and they are plausible (the fixtures stand in for this everywhere else).
func TestSystemSourcesHostFieldsSmoke(t *testing.T) {
	r, err := NewCollector(SystemSources()).Sample()
	if err != nil {
		t.Fatal(err)
	}
	if r.ProcsTotal == nil || *r.ProcsTotal < len(r.Procs) || *r.ProcsTotal < 50 {
		t.Errorf("procs_total %v with %d listed", r.ProcsTotal, len(r.Procs))
	}
	if r.BootAt == nil || !r.BootAt.Before(time.Now()) || r.BootAt.Year() < 2020 {
		t.Errorf("boot_at %v", r.BootAt)
	}
	if r.Self == nil || r.Self.RSSMB == nil || *r.Self.RSSMB <= 0 || *r.Self.RSSMB > 4096 || r.Self.CPUPct != nil {
		t.Errorf("self %+v: rss in MB, no cpu yet", r.Self)
	}
	m := r.Mem
	if m.SwapTotalMB == nil || m.SwapUsedMB == nil || *m.SwapTotalMB < *m.SwapUsedMB {
		t.Errorf("swap total %v used %v", m.SwapTotalMB, m.SwapUsedMB)
	}
	for name, v := range map[string]*int{"wired": m.WiredMB, "active": m.ActiveMB, "inactive": m.InactiveMB, "file cache": m.FileCacheMB} {
		if v == nil || *v <= 0 || *v > m.TotalMB {
			t.Errorf("%s %v of %d MB", name, v, m.TotalMB)
		}
	}
}
