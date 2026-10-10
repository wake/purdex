package convmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

// U3-0: the additive step fields (question, read, search) and diff.created (U1 spec §8.1, evolution rule).

func stepItem(mut func(*Step)) Item {
	s := &Step{ID: "toolu_1", At: 1, Kind: StepOther, Tool: "AskUserQuestion", Status: StepDone, Summary: "s", StartedAt: 1, Input: json.RawMessage(`{}`)}
	mut(s)
	return Item{Type: ItemStep, Step: s}
}

// The new members are omitted when empty, so a step that has none is byte-identical to today's.
func TestStepNewFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(stepItem(func(*Step) {}))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"question"`, `"read"`, `"search"`, `"created"`} {
		if strings.Contains(string(b), k) {
			t.Fatalf("%s present in %s", k, b)
		}
	}
}

func TestStepNewFieldsRoundTrip(t *testing.T) {
	it := stepItem(func(s *Step) {
		s.Question = &StepQuestion{
			Questions: []QuestionItem{{Question: "Which?", Header: "Fruit", Multiple: true, Options: []QuestionOption{{Label: "Apple", Description: "red"}, {Label: "Pear"}}}},
			Answers:   [][]string{{"Apple", "Pear"}},
		}
		s.Read = &ReadRange{Offset: 10, Limit: 20}
		s.Search = &SearchScope{Where: "/work/src"}
		s.Diff = &Diff{Path: "/work/n.txt", Exact: true, Created: true}
	})
	b, err := json.Marshal(it)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"question":{"questions":[{"question":"Which?","header":"Fruit","multiple":true,"options":[{"label":"Apple","description":"red"},{"label":"Pear"}]}],"answers":[["Apple","Pear"]]}`,
		`"read":{"offset":10,"limit":20}`, `"search":{"where":"/work/src"}`, `"created":true`,
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
	var back Item
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Step.Question == nil || len(back.Step.Question.Questions) != 1 || back.Step.Question.Answers[0][1] != "Pear" ||
		back.Step.Read.Limit != 20 || back.Step.Search.Where != "/work/src" || !back.Step.Diff.Created {
		t.Fatalf("round trip lost a field: %+v", back.Step)
	}
}

// An open question has no answers member at all (not an empty list).
func TestStepQuestionWithoutAnswersHasNoAnswersMember(t *testing.T) {
	it := stepItem(func(s *Step) {
		s.Question = &StepQuestion{Questions: []QuestionItem{{Question: "Q?", Options: []QuestionOption{{Label: "A"}}}}}
	})
	b, _ := json.Marshal(it)
	if strings.Contains(string(b), `"answers"`) {
		t.Fatalf("answers present: %s", b)
	}
}

func TestValidateStepQuestion(t *testing.T) {
	q := func(mut func(*StepQuestion)) error {
		sq := &StepQuestion{Questions: []QuestionItem{{Question: "Q?", Options: []QuestionOption{{Label: "A"}}}}}
		mut(sq)
		return validateStep(stepItem(func(s *Step) { s.Question = sq }).Step)
	}
	if err := q(func(*StepQuestion) {}); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := q(func(s *StepQuestion) { s.Answers = [][]string{{"A"}} }); err != nil {
		t.Fatalf("valid with answers: %v", err)
	}
	for name, mut := range map[string]func(*StepQuestion){
		"no questions":          func(s *StepQuestion) { s.Questions = nil },
		"too many questions":    func(s *StepQuestion) { s.Questions = make([]QuestionItem, MaxQuestions+1) },
		"empty question":        func(s *StepQuestion) { s.Questions[0].Question = "" },
		"too many options":      func(s *StepQuestion) { s.Questions[0].Options = make([]QuestionOption, MaxQuestionOptions+1) },
		"answers of wrong size": func(s *StepQuestion) { s.Answers = [][]string{{"A"}, {"B"}} },
		"an empty answer":       func(s *StepQuestion) { s.Answers = [][]string{{}} },
	} {
		if err := q(mut); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestValidateReadAndSearch(t *testing.T) {
	bad := []func(*Step){
		func(s *Step) { s.Read = &ReadRange{} },                     // present but empty
		func(s *Step) { s.Read = &ReadRange{Offset: -1, Limit: 3} }, // negative
		func(s *Step) { s.Search = &SearchScope{} },                 // present but empty
	}
	for i, mut := range bad {
		if err := validateStep(stepItem(mut).Step); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	if err := validateStep(stepItem(func(s *Step) { s.Read = &ReadRange{Offset: 5}; s.Search = &SearchScope{Where: "web"} }).Step); err != nil {
		t.Fatalf("valid: %v", err)
	}
}
