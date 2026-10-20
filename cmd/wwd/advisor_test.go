package main

import (
	"testing"

	"github.com/wake/purdex/internal/wwd/advisor"
	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/wire"
)

// The live evaluator and the replay run the same list: every rule once, each of a kind the wire knows, and the known residents reach the
// resident-growth rule.
func TestAdvisorRulesListsEachRuleOnceWithItsKnownResidents(t *testing.T) {
	seen := map[wire.AlertKind]bool{}
	var growth advisor.ResidentGrowth
	for _, r := range advisorRules([]string{"OrbStack Helper"}, daemon.AdvisorRule{}) {
		k := r.Kind()
		if !k.Valid() || seen[k] {
			t.Fatalf("kind %q is invalid or listed twice", k)
		}
		seen[k] = true
		if g, ok := r.(advisor.ResidentGrowth); ok {
			growth = g
		}
	}
	for _, want := range []wire.AlertKind{wire.AlertDiskLow, wire.AlertDiskFalling, wire.AlertWorkerCrash, wire.AlertPressureHeld, wire.AlertThrash, wire.AlertResidentGrowth, wire.AlertMservService, wire.AlertOrphans} {
		if !seen[want] {
			t.Errorf("rule %q is not in the list", want)
		}
	}
	if len(growth.Known) != 1 || growth.Known[0] != "OrbStack Helper" {
		t.Fatalf("known residents did not reach the rule: %+v", growth)
	}
}
