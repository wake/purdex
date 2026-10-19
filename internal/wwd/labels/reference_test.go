package labels

import (
	"slices"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/wwd/wire"
)

var refTasks = []RefTask{
	{Repo: "purdex", Task: "spa-related", Reference: "air26", Needs: []string{"tool:node24", "tool:pnpm10"}},
	{Repo: "purdex", Task: "tsc", Reference: "air26", Needs: []string{"tool:node24"}},
	{Repo: "at-inwin", Task: "phpunit", Reference: "mlab", Needs: []string{"tool:php84"}},
}

// The reference host holds the label without a pair when it has the task's repo and every label the task needs.
func TestReferenceHoldsTaskLabelsItHasTheToolsFor(t *testing.T) {
	have := []string{"os:darwin", "repo:purdex", "tool:node24", "tool:pnpm10"}
	got := ReferenceLabels("air26", have, refTasks)
	want := []string{"task:purdex/spa-related", "task:purdex/tsc"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}

func TestReferenceNeedsEveryToolAndTheRepo(t *testing.T) {
	if got := ReferenceLabels("air26", []string{"repo:purdex", "tool:node24"}, refTasks); !slices.Equal(got, []string{"task:purdex/tsc"}) {
		t.Fatalf("one tool missing: %v", got)
	}
	if got := ReferenceLabels("air26", []string{"tool:node24", "tool:pnpm10"}, refTasks); len(got) != 0 {
		t.Fatalf("no repo: %v", got)
	}
}

// Only the reference holds it: another host gets nothing from the same facts, and neither does a host with no alias.
func TestOnlyTheReferenceHost(t *testing.T) {
	have := []string{"repo:purdex", "tool:node24", "tool:pnpm10"}
	if got := ReferenceLabels("mserv", have, refTasks); len(got) != 0 {
		t.Fatalf("not the reference: %v", got)
	}
	if got := ReferenceLabels("", have, []RefTask{{Repo: "purdex", Task: "x", Reference: ""}}); len(got) != 0 {
		t.Fatalf("empty alias: %v", got)
	}
}

func TestReferenceLabelsAreSortedAndNotNil(t *testing.T) {
	if got := ReferenceLabels("mlab", nil, refTasks); got == nil || len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	dup := append(slices.Clone(refTasks), refTasks[1])
	got := ReferenceLabels("air26", []string{"repo:purdex", "tool:node24", "tool:pnpm10"}, dup)
	if !slices.IsSorted(got) || len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

// The reference host advertises a long manifest task name (the store keeps it) and never a label the store would refuse.
func TestReferenceLabelsFollowTheStoreRule(t *testing.T) {
	have := []string{"repo:purdex"}
	long := strings.Repeat("a", 100)
	if got := ReferenceLabels("mlab", have, []RefTask{{Repo: "purdex", Task: long, Reference: "mlab"}}); len(got) != 1 {
		t.Fatalf("a long task name: %v", got)
	}
	tooLong := strings.Repeat("a", wire.MaxTaskLabelBytes)
	if got := ReferenceLabels("mlab", have, []RefTask{{Repo: "purdex", Task: tooLong, Reference: "mlab"}}); len(got) != 0 {
		t.Fatalf("a label the store refuses: %v", got)
	}
}

// A manifest cannot turn a requirement into a grant: a need that is not an environment label (task:, host:, repo:) keeps the task from
// being held, even when the host happens to have that label, and one task's label cannot satisfy another's need.
func TestOnlyEnvironmentLabelsCanBeNeeded(t *testing.T) {
	have := []string{"repo:purdex", "host:air26", "task:purdex/a", "tool:node24"}
	for _, need := range []string{"host:air26", "task:purdex/a", "repo:purdex", "custom:x"} {
		got := ReferenceLabels("air26", have, []RefTask{{Repo: "purdex", Task: "b", Reference: "air26", Needs: []string{"tool:node24", need}}})
		if len(got) != 0 {
			t.Errorf("need %q was satisfied: %v", need, got)
		}
	}
	chain := []RefTask{
		{Repo: "purdex", Task: "a", Reference: "air26", Needs: []string{"tool:node24"}},
		{Repo: "purdex", Task: "b", Reference: "air26", Needs: []string{"task:purdex/a"}},
	}
	if got := ReferenceLabels("air26", []string{"repo:purdex", "tool:node24"}, chain); !slices.Equal(got, []string{"task:purdex/a"}) {
		t.Fatalf("a chain through a task label: %v", got)
	}
}
