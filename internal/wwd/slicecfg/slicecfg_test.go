package slicecfg

import (
	"strings"
	"testing"
)

func ip(n int) *int { return &n }

// With nothing set, the limits are the ones decided for the pool (#3549): jobs get half the weight of the production services, at most
// three of the four cores of mserv, 6G, no swap.
func TestDefaults(t *testing.T) {
	got := DropIn(Limits{})
	want := "[Slice]\nCPUWeight=50\nCPUQuota=300%\nMemoryMax=6G\nMemorySwapMax=0\nTasksMax=4096\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("drop-in:\n%s\nwant it to end with:\n%s", got, want)
	}
	if !strings.HasPrefix(got, "# ") {
		t.Errorf("a comment says where the file comes from: %q", got)
	}
}

// Every value can be overridden on its own; the others keep their default.
func TestOverridesOneAtATime(t *testing.T) {
	got := DropIn(Limits{CPUQuota: "200%", MemoryMax: "8G", CPUWeight: ip(100), TasksMax: ip(1000), MemorySwapMax: "1G"})
	for _, line := range []string{"CPUWeight=100\n", "CPUQuota=200%\n", "MemoryMax=8G\n", "MemorySwapMax=1G\n", "TasksMax=1000\n"} {
		if !strings.Contains(got, line) {
			t.Errorf("missing %q in\n%s", line, got)
		}
	}
	if got := DropIn(Limits{MemoryMax: "8G"}); !strings.Contains(got, "CPUQuota=300%\n") || !strings.Contains(got, "MemoryMax=8G\n") {
		t.Errorf("a partial override loses the other defaults:\n%s", got)
	}
}

// The file is read by systemd as root: nothing that is not a plain value may reach it (a newline would add a line of its own).
func TestValidateRefusesWhatIsNotAValue(t *testing.T) {
	for name, l := range map[string]Limits{
		"newline in quota":  {CPUQuota: "300%\nExecStart=/bin/sh"},
		"quota no percent":  {CPUQuota: "300"},
		"quota zero":        {CPUQuota: "0%"},
		"quota words":       {CPUQuota: "all"},
		"memory newline":    {MemoryMax: "6G\n[Service]"},
		"memory unit":       {MemoryMax: "6X"},
		"memory empty num":  {MemoryMax: "G"},
		"swap junk":         {MemorySwapMax: "a b"},
		"weight zero":       {CPUWeight: ip(0)},
		"weight too big":    {CPUWeight: ip(10001)},
		"tasks negative":    {TasksMax: ip(-1)},
		"tasks zero":        {TasksMax: ip(0)},
		"memory percent":    {MemoryMax: "50%"}, // a percent of what the host has is not what an operator reads in a config
		"quota over 100000": {CPUQuota: "100001%"},
	} {
		if err := l.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, l)
		}
	}
	for name, l := range map[string]Limits{
		"defaults":     {},
		"infinity":     {MemoryMax: "infinity"},
		"megabytes":    {MemoryMax: "512M"},
		"plain bytes":  {MemoryMax: "1048576"},
		"no swap":      {MemorySwapMax: "0"},
		"four cores":   {CPUQuota: "400%"},
		"weight range": {CPUWeight: ip(1)},
	} {
		if err := l.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
