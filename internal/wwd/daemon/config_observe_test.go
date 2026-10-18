package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigObserveDefaultsAndValues(t *testing.T) {
	home := t.TempDir()
	c, err := LoadConfig(filepath.Join(home, "none.toml"), home)
	if err != nil {
		t.Fatal(err)
	}
	if c.Observe.MinCPUSeconds != 0 || c.Observe.MinMemMB != 0 {
		t.Fatalf("unset stays 0, the tracker fills its defaults: %+v", c.Observe)
	}
	p := filepath.Join(home, "c.toml")
	os.WriteFile(p, []byte("[observe]\nmin_cpu_seconds = 30.5\nmin_mem_mb = 2048\n"), 0o600)
	c, err = LoadConfig(p, home)
	if err != nil || c.Observe.MinCPUSeconds != 30.5 || c.Observe.MinMemMB != 2048 {
		t.Fatalf("%+v err %v", c.Observe, err)
	}
}

func TestLoadConfigObserveRefusesNegativeThresholds(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	for name, body := range map[string]string{
		"cpu":     "[observe]\nmin_cpu_seconds = -1\n",
		"mem":     "[observe]\nmin_mem_mb = -5\n",
		"nan":     "[observe]\nmin_cpu_seconds = nan\n", // TOML has nan and inf: either would switch the threshold off without a word
		"inf":     "[observe]\nmin_cpu_seconds = inf\n",
		"plusinf": "[observe]\nmin_cpu_seconds = +inf\n",
		"neginf":  "[observe]\nmin_cpu_seconds = -inf\n",
	} {
		os.WriteFile(p, []byte(body), 0o600)
		_, err := LoadConfig(p, home)
		if err == nil || !strings.Contains(err.Error(), "observe.") {
			t.Fatalf("%s: a negative threshold must fail naming the key: %v", name, err)
		}
	}
	os.WriteFile(p, []byte("[observe]\nmin_cpu_second = 1\n"), 0o600) // a typo
	if _, err := LoadConfig(p, home); err == nil {
		t.Fatal("unknown key must fail")
	}
}
