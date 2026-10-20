package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

func loadAdvisor(t *testing.T, body string) (Config, error) {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, "c.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return LoadConfig(p, home)
}

// S3-2: the three watcher rules are off unless [advisor] watch_rules is true; `off` takes single kinds out where they mean nothing.
func TestAdvisorWatchRulesAreOffByDefault(t *testing.T) {
	c, err := loadAdvisor(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Advisor.WatchRules || len(c.Advisor.Off) != 0 || c.Advisor.SwapGrowthMB != 0 {
		t.Fatalf("defaults: %+v", c.Advisor)
	}
	for _, k := range []wire.AlertKind{wire.AlertPressureWatch, wire.AlertPressureDanger, wire.AlertSwapGrowth} {
		if c.Advisor.WatchRuleOn(k) {
			t.Errorf("%s is on with watch_rules unset", k)
		}
	}
}

func TestAdvisorWatchRulesOnAndOff(t *testing.T) {
	c, err := loadAdvisor(t, "[advisor]\nwatch_rules = true\noff = [\"pressure_watch\", \"swap_growth\"]\nswap_growth_mb = 900\n")
	if err != nil {
		t.Fatal(err)
	}
	a := c.Advisor
	if !a.WatchRules || a.SwapGrowthMB != 900 {
		t.Fatalf("%+v", a)
	}
	if a.WatchRuleOn(wire.AlertPressureWatch) || a.WatchRuleOn(wire.AlertSwapGrowth) || !a.WatchRuleOn(wire.AlertPressureDanger) {
		t.Errorf("off = %v: watch %v danger %v swap %v", a.Off, a.WatchRuleOn(wire.AlertPressureWatch), a.WatchRuleOn(wire.AlertPressureDanger), a.WatchRuleOn(wire.AlertSwapGrowth))
	}
	// a kind that is not one of the three is not something watch_rules decides
	if a.WatchRuleOn(wire.AlertDiskLow) {
		t.Error("WatchRuleOn answered yes for a kind that is not a watcher rule")
	}
	// off without watch_rules: still off, nothing turns on
	c, err = loadAdvisor(t, "[advisor]\noff = [\"pressure_danger\"]\n")
	if err != nil || c.Advisor.WatchRuleOn(wire.AlertPressureWatch) {
		t.Fatalf("watch_rules unset must keep every watcher rule off: %+v err %v", c, err)
	}
}

func TestAdvisorWatchConfigRefusals(t *testing.T) {
	for name, c := range map[string]struct{ body, key string }{
		"an old kind in off":  {"[advisor]\noff = [\"disk_low\"]\n", "advisor.off"},
		"a typo in off":       {"[advisor]\noff = [\"pressure_watching\"]\n", "advisor.off"},
		"an empty name":       {"[advisor]\noff = [\"\"]\n", "advisor.off"},
		"a negative growth":   {"[advisor]\nswap_growth_mb = -1\n", "advisor.swap_growth_mb"},
		"an absurd growth":    {"[advisor]\nswap_growth_mb = 99999999\n", "advisor.swap_growth_mb"},
		"watch_rules as text": {"[advisor]\nwatch_rules = \"yes\"\n", ""},
	} {
		_, err := loadAdvisor(t, c.body)
		if err == nil || (c.key != "" && !strings.Contains(err.Error(), c.key)) {
			t.Errorf("%s: want an error naming %q, got %v", name, c.key, err)
		}
	}
}
