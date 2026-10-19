package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// [slice] is a known table (wwd install reads it; the daemon must still start with it), and a value that is not a plain value is refused
// at load, the same check the installer makes before it writes a file systemd reads as root (#3549).
func TestLoadConfigSliceTable(t *testing.T) {
	home := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(home, "c.toml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	c, err := LoadConfig(write("[slice]\ncpu_weight = 20\ncpu_quota = \"200%\"\nmemory_max = \"4G\"\nmemory_swap_max = \"0\"\ntasks_max = 1000\n"), home)
	if err != nil {
		t.Fatalf("a [slice] table must be accepted: %v", err)
	}
	if c.Slice.CPUWeight == nil || *c.Slice.CPUWeight != 20 || c.Slice.CPUQuota != "200%" || c.Slice.MemoryMax != "4G" || *c.Slice.TasksMax != 1000 {
		t.Fatalf("slice = %+v", c.Slice)
	}
	if _, err := LoadConfig(write("[slice]\nmemory_max = \"6G\\nExecStart=/bin/sh\"\n"), home); err == nil || !strings.Contains(err.Error(), "memory_max") {
		t.Fatalf("a value with a newline: err = %v", err)
	}
	if _, err := LoadConfig(write("[slice]\ncpu_quota = \"300\"\n"), home); err == nil {
		t.Fatal("a quota with no percent sign was accepted")
	}
	if _, err := LoadConfig(write("[slice]\nzzz = 1\n"), home); err == nil {
		t.Fatal("an unknown key in [slice] was accepted")
	}
}
