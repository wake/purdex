package labels

import (
	"slices"
	"testing"
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
