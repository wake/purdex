package main

import (
	"slices"
	"testing"

	"github.com/wake/purdex/internal/wwd/daemon"
	"github.com/wake/purdex/internal/wwd/wire"
)

// S3-3b-2: the four queue rules are in the list only where [advisor] queue_rules is on; the rest of the list is the same as before.
func TestAdvisorRulesHaveNoQueueRuleUnlessQueueRulesIsOn(t *testing.T) {
	got := kindsOf(advisorRules(nil, daemon.AdvisorRule{}))
	for _, k := range queueKinds {
		if slices.Contains(got, k) {
			t.Errorf("%s is in the list with queue_rules unset", k)
		}
	}
	on := kindsOf(advisorRules(nil, daemon.AdvisorRule{QueueRules: true}))
	for _, k := range queueKinds {
		if !slices.Contains(on, k) {
			t.Errorf("%s is not in the list with queue_rules on", k)
		}
	}
	if len(on) != len(got)+4 {
		t.Errorf("queue_rules adds the four and nothing else: %v -> %v", got, on)
	}
	// it is its own switch: watch_rules does not bring the queue rules, queue_rules does not bring the watcher rules
	if w := kindsOf(advisorRules(nil, daemon.AdvisorRule{WatchRules: true})); slices.Contains(w, wire.AlertQueueStalled) {
		t.Error("watch_rules turned on a queue rule")
	}
	if q := kindsOf(advisorRules(nil, daemon.AdvisorRule{QueueRules: true})); slices.Contains(q, wire.AlertPressureWatch) {
		t.Error("queue_rules turned on a watcher rule")
	}
}

// The alerts of the four are cleared by the evaluator when the rules are off, so that turning queue_rules off leaves none behind (the older
// build that would refuse the list is then safe to install).
func TestRetiredQueueKeys(t *testing.T) {
	want := []string{"queue_stalled", "job_requeued", "queue_backlog", "queue_unclaimable"}
	if got := retiredQueueKeys(daemon.AdvisorRule{}); !slices.Equal(got, want) {
		t.Errorf("queue_rules off: %v", got)
	}
	if got := retiredQueueKeys(daemon.AdvisorRule{QueueRules: true}); len(got) != 0 {
		t.Errorf("queue_rules on retires %v", got)
	}
	// the keys are the kinds' names, which is what the rules raise them under
	for _, r := range advisorRules(nil, daemon.AdvisorRule{QueueRules: true}) {
		if slices.Contains(queueKinds, r.Kind()) && !slices.Contains(want, string(r.Kind())) {
			t.Errorf("%s is not retired as its key", r.Kind())
		}
	}
}
