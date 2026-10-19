package peers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Picking rules (name pool v2 spec §6.2). The functions are pure: the free names, the history and the persona flags come in, a
// name or an ordered list comes out. pickLast stands for "any random pick": a test that must not depend on the pick uses
// pickLast and pickFirst and expects the same answer, or checks the candidate set.

func pickFirst(c []string) string { return c[0] }
func pickLast(c []string) string  { return c[len(c)-1] }

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestRuleMember_Order(t *testing.T) {
	free := []string{"adam", "bea", "cleo", "dan", "eli", "fay"}
	cases := []struct {
		name string
		in   nameRuleInput
		want string
	}{
		{"1 paired with the lead: most pairs first",
			nameRuleInput{Free: free, Pairs: map[string]nameStat{"cleo": {2, 10}, "dan": {5, 1}}, Pick: pickFirst}, "dan"},
		{"1 paired with the lead: equal pairs, most recent first",
			nameRuleInput{Free: free, Pairs: map[string]nameStat{"cleo": {2, 10}, "dan": {2, 30}, "eli": {2, 20}}, Pick: pickFirst}, "dan"},
		{"1 paired with the lead: equal in both, pool order",
			nameRuleInput{Free: free, Pairs: map[string]nameStat{"eli": {1, 5}, "bea": {1, 5}}, Pick: pickFirst}, "bea"},
		{"1 beats a newcomer; a paired name that is not free is never offered",
			nameRuleInput{Free: []string{"bea", "cleo"}, Pairs: map[string]nameStat{"adam": {9, 9}, "cleo": {1, 1}}, Familiar: set("cleo"), Pick: pickFirst}, "cleo"},
		{"1 may offer a lead:true name that is paired with the lead",
			nameRuleInput{Free: free, Lead: set("dan"), Pairs: map[string]nameStat{"dan": {1, 1}}, Pick: pickFirst}, "dan"},
		{"2 a newcomer: not familiar and not lead:true",
			nameRuleInput{Free: free, Familiar: set("adam", "bea", "cleo"), Lead: set("dan"), Pick: pickFirst}, "eli"},
		{"2 the newcomer is chosen by the picker among all newcomers",
			nameRuleInput{Free: free, Familiar: set("adam", "bea", "cleo"), Lead: set("dan"), Pick: pickLast}, "fay"},
		{"3 no newcomer: the name least paired with any lead",
			nameRuleInput{Free: []string{"adam", "bea", "cleo"}, Familiar: set("adam", "bea", "cleo"),
				AnyPairs: map[string]int{"adam": 3, "bea": 1, "cleo": 2}, Pick: pickFirst}, "bea"},
		{"3 a tie among the least paired goes to the picker",
			nameRuleInput{Free: []string{"adam", "bea", "cleo"}, Familiar: set("adam", "bea", "cleo"),
				AnyPairs: map[string]int{"adam": 1, "bea": 1, "cleo": 2}, Pick: pickLast}, "bea"},
		{"4 lead:true names come last",
			nameRuleInput{Free: []string{"adam", "bea"}, Familiar: set("adam", "bea"), Lead: set("adam", "bea"),
				AnyPairs: map[string]int{"adam": 2, "bea": 1}, Pick: pickFirst}, "bea"},
		{"4 a lead:true name is taken only when nothing else is free",
			nameRuleInput{Free: []string{"adam", "bea"}, Familiar: set("bea"), Lead: set("adam"), Pick: pickFirst}, "bea"},
	}
	for _, c := range cases {
		got, ok := pickForMember(c.in)
		assert.True(t, ok, c.name)
		assert.Equal(t, c.want, got, c.name)
	}
	_, ok := pickForMember(nameRuleInput{Pick: pickFirst})
	assert.False(t, ok, "nothing free")
}

func TestRuleOther_Order(t *testing.T) {
	free := []string{"adam", "bea", "cleo", "dan"}
	cases := []struct {
		name string
		in   nameRuleInput
		want string
	}{
		{"1 a newcomer, though a name is paired with a lead",
			nameRuleInput{Free: free, Familiar: set("adam", "bea"), Pairs: map[string]nameStat{"cleo": {9, 9}}, Pick: pickFirst}, "cleo"},
		{"1 a lead:true name is not a newcomer",
			nameRuleInput{Free: free, Familiar: set("adam"), Lead: set("bea"), Pick: pickFirst}, "cleo"},
		{"2 no newcomer: the least paired with any lead",
			nameRuleInput{Free: free, Familiar: set("adam", "bea", "cleo", "dan"),
				AnyPairs: map[string]int{"adam": 4, "bea": 2, "cleo": 1, "dan": 3}, Pick: pickFirst}, "cleo"},
		{"3 lead:true names last",
			nameRuleInput{Free: free, Familiar: set("adam", "bea", "cleo", "dan"), Lead: set("cleo"),
				AnyPairs: map[string]int{"cleo": 0, "adam": 7, "bea": 7, "dan": 7}, Pick: pickFirst}, "adam"},
		{"a name with no history counts as zero pairs",
			nameRuleInput{Free: []string{"adam", "bea"}, Familiar: set("adam", "bea"),
				AnyPairs: map[string]int{"adam": 1}, Pick: pickFirst}, "bea"},
	}
	for _, c := range cases {
		got, ok := pickForOther(c.in)
		assert.True(t, ok, c.name)
		assert.Equal(t, c.want, got, c.name)
	}
	_, ok := pickForOther(nameRuleInput{Pick: pickFirst})
	assert.False(t, ok, "nothing free")
}

func TestRuleOther_IgnoresWhoIsPairedWithTheLead(t *testing.T) {
	// the rule for any other session has no lead: Pairs (a lead's own pairs) must not steer it
	in := nameRuleInput{Free: []string{"adam", "bea", "cleo"}, Familiar: set("adam"), Pairs: map[string]nameStat{"cleo": {9, 9}}, Pick: pickFirst}
	got, _ := pickForOther(in)
	assert.Equal(t, "bea", got)
}

func TestRuleLead_Candidates(t *testing.T) {
	free := []string{"adam", "bea", "cleo", "dan", "eli"}
	in := nameRuleInput{
		Free:  free,
		Leads: map[string]nameStat{"dan": {1, 50}, "bea": {3, 90}, "gone": {9, 99}}, // gone is not free
		Lead:  set("cleo", "eli"),
	}
	// keep first (it is the current name, held by the session itself), then free former leads by recency, then free
	// lead:true personas in pool order, then the others
	assert.Equal(t, []string{"zed", "bea", "dan", "cleo", "eli", "adam"}, leadCandidates(in, "zed"))
	// nothing to keep (the session has no pool name): the list starts at the former leads
	assert.Equal(t, []string{"bea", "dan", "cleo", "eli", "adam"}, leadCandidates(in, ""))
	// the current name is also free or a former lead: it appears once, first
	assert.Equal(t, []string{"dan", "bea", "cleo", "eli", "adam"}, leadCandidates(in, "dan"))
	// a former lead that is also a lead:true persona is listed among the former leads, not twice
	in.Lead = set("bea", "cleo")
	assert.Equal(t, []string{"bea", "dan", "cleo", "adam", "eli"}, leadCandidates(in, ""))
	// nothing free and nothing to keep
	assert.Empty(t, leadCandidates(nameRuleInput{}, ""))
}

func TestRules_APickerThatAnswersSomethingElseFallsBackToTheFirstCandidate(t *testing.T) {
	in := nameRuleInput{Free: []string{"adam", "bea"}, Pick: func([]string) string { return "nope" }}
	got, ok := pickForOther(in)
	assert.True(t, ok)
	assert.Equal(t, "adam", got)
	in.Pick = nil // no picker: the first candidate
	got, _ = pickForOther(in)
	assert.Equal(t, "adam", got)
}
