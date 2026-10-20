package main

import (
	"slices"
	"testing"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/wire"
)

func kindsOf(rs []advisor.Rule) []wire.AlertKind {
	var out []wire.AlertKind
	for _, r := range rs {
		out = append(out, r.Kind())
	}
	return out
}

var watchKinds = []wire.AlertKind{wire.AlertPressureWatch, wire.AlertPressureDanger, wire.AlertSwapGrowth}

// S3-2b: with watch_rules unset none of the three is in the list; the rest of the list is the same as before.
func TestAdvisorRulesHaveNoWatcherRuleUnlessWatchRulesIsOn(t *testing.T) {
	got := kindsOf(advisorRules(nil, daemon.AdvisorRule{}))
	for _, k := range watchKinds {
		if slices.Contains(got, k) {
			t.Errorf("%s is in the list with watch_rules unset", k)
		}
	}
	if len(got) != 8 {
		t.Errorf("the other rules changed: %v", got)
	}
	// off without watch_rules turns nothing on
	if got := kindsOf(advisorRules(nil, daemon.AdvisorRule{Off: []string{"pressure_watch"}})); slices.Contains(got, wire.AlertPressureDanger) {
		t.Error("off turned a rule on")
	}
}

func TestAdvisorRulesWithWatchRulesOnAndOff(t *testing.T) {
	all := kindsOf(advisorRules(nil, daemon.AdvisorRule{WatchRules: true}))
	for _, k := range watchKinds {
		if !slices.Contains(all, k) {
			t.Errorf("%s is not in the list with watch_rules on", k)
		}
	}
	air19 := kindsOf(advisorRules(nil, daemon.AdvisorRule{WatchRules: true, Off: []string{"pressure_watch", "swap_growth"}}))
	if slices.Contains(air19, wire.AlertPressureWatch) || slices.Contains(air19, wire.AlertSwapGrowth) || !slices.Contains(air19, wire.AlertPressureDanger) {
		t.Errorf("off = pressure_watch, swap_growth: %v", air19)
	}
	// the override reaches the swap rule
	var swap advisor.SwapGrowth
	for _, r := range advisorRules(nil, daemon.AdvisorRule{WatchRules: true, SwapGrowthMB: 900}) {
		if s, ok := r.(advisor.SwapGrowth); ok {
			swap = s
		}
	}
	if swap.ThresholdMB != 900 {
		t.Errorf("swap_growth_mb did not reach the rule: %+v", swap)
	}
}

// Every rule that is not judged on this host has its key retired: its standing alert is cleared at the next pass.
func TestRetiredWatchKeys(t *testing.T) {
	if got := retiredWatchKeys(daemon.AdvisorRule{}); !slices.Equal(got, []string{"pressure_watch", "pressure_danger", "swap_growth"}) {
		t.Errorf("watch_rules unset retires %v", got)
	}
	if got := retiredWatchKeys(daemon.AdvisorRule{WatchRules: true}); len(got) != 0 {
		t.Errorf("all on retires %v", got)
	}
	if got := retiredWatchKeys(daemon.AdvisorRule{WatchRules: true, Off: []string{"swap_growth"}}); !slices.Equal(got, []string{"swap_growth"}) {
		t.Errorf("off swap_growth retires %v", got)
	}
}
