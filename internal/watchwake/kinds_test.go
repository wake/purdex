package watchwake

import (
	"regexp"
	"slices"
	"testing"
)

// KnownKinds is what the App offers to tick in wwd_alert.kinds_off: every kind a wwd alert can have, with the levels it can come in.
func TestKnownKinds_AreWellFormedAndUnique(t *testing.T) {
	reKind := regexp.MustCompile(`^[a-z][a-z0-9_:-]{0,63}$`)
	seen := map[string]bool{}
	for _, k := range KnownKinds {
		if !reKind.MatchString(k.Kind) || k.Label == "" || len(k.Levels) == 0 || seen[k.Kind] {
			t.Errorf("%+v", k)
		}
		seen[k.Kind] = true
		// the order digest -> push -> watch, each once, only levels the document knows
		prev := -1
		for _, l := range k.Levels {
			i := slices.Index(levelOrder, l)
			if i < 0 || i <= prev {
				t.Errorf("%s: levels %v", k.Kind, k.Levels)
			}
			prev = i
		}
	}
	if len(KnownKinds) != 16 {
		t.Errorf("%d kinds", len(KnownKinds))
	}
}

// orphans climbs from the digest to a push (rules_orphans.go), so it can be filtered at either level; the queue and watcher rules are
// watch-level only; the disk and silence kinds push.
func TestKnownKinds_LevelsFollowTheAdvisorRules(t *testing.T) {
	want := map[string][]string{
		"orphans":           {"digest", "push"},
		"disk_low":          {"push"},
		"disk_falling":      {"digest"},
		"silence":           {"push"},
		"pressure_watch":    {"watch"},
		"pressure_danger":   {"push"},
		"swap_growth":       {"watch"},
		"queue_stalled":     {"watch"},
		"queue_unclaimable": {"watch"},
		"thrash":            {"digest"},
	}
	got := map[string][]string{}
	for _, k := range KnownKinds {
		got[k.Kind] = k.Levels
	}
	for kind, levels := range want {
		if !slices.Equal(got[kind], levels) {
			t.Errorf("%s: %v, want %v", kind, got[kind], levels)
		}
	}
}

func TestKindLabel(t *testing.T) {
	if l, ok := KindLabel("orphans"); !ok || l != "孤兒行程" {
		t.Fatalf("%q %v", l, ok)
	}
	if _, ok := KindLabel("nope"); ok {
		t.Fatal("an unknown kind has no label")
	}
}
