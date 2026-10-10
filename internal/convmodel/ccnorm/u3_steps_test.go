package ccnorm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// U3-0: the question, read range, search scope, created file and compaction summary (U1 spec §8.1, plan D12). Collie's
// parser (bridge/journal/claude.ts, tool-call.ts) is the reference; its cases are the cases here.

func askInput(qs ...obj) obj { return obj{"questions": qs} }

func question(text string, multi bool, labels ...string) obj {
	opts := make([]obj, 0, len(labels))
	for _, l := range labels {
		opts = append(opts, obj{"label": l, "description": "about " + l})
	}
	return obj{"question": text, "header": "H", "multiSelect": multi, "options": opts}
}

func askStep(t *testing.T, input obj, result []byte) *convmodel.Step {
	t.Helper()
	return oneStep(t, "AskUserQuestion", input, result)
}

// The question is recognised from the input; an open question (no result) has no answers.
func TestQuestion_FromInput(t *testing.T) {
	s := askStep(t, askInput(question("Which fruit?", false, "Apple", "Pear")), nil)
	q := s.Question
	if q == nil || len(q.Questions) != 1 || q.Answers != nil {
		t.Fatalf("question = %+v", q)
	}
	got := q.Questions[0]
	if got.Question != "Which fruit?" || got.Header != "H" || got.Multiple || len(got.Options) != 2 ||
		got.Options[0].Label != "Apple" || got.Options[0].Description != "about Apple" || got.Options[1].Label != "Pear" {
		t.Fatalf("item = %+v", got)
	}
	if s.Kind != convmodel.StepOther || s.Status != convmodel.StepRunning {
		t.Fatalf("kind %s status %s", s.Kind, s.Status)
	}
}

// The shape is the rule, not the name; a call without a usable question list has no question member.
func TestQuestion_ByShapeNotName(t *testing.T) {
	if s := oneStep(t, "question", askInput(question("Q?", false, "A")), nil); s.Question == nil {
		t.Fatal("a differently named tool with the same shape is not a question")
	}
	for name, in := range map[string]obj{
		"no questions":       obj{"x": "y"},
		"empty list":         obj{"questions": []obj{}},
		"no options":         obj{"questions": []obj{{"question": "Q?"}}},
		"blank question":     obj{"questions": []obj{{"question": "  ", "options": []obj{{"label": "A"}}}}},
		"not an object":      obj{"questions": []any{"Q?"}},
		"questions a string": obj{"questions": "Q?"},
	} {
		if s := askStep(t, in, nil); s.Question != nil {
			t.Errorf("%s: got a question %+v", name, s.Question)
		}
	}
}

// Options may be plain strings; a malformed question is dropped, the rest kept; a list with nothing valid is no question.
func TestQuestion_OptionsAndMalformedEntries(t *testing.T) {
	in := obj{"questions": []any{
		obj{"question": "Plain?", "options": []any{"yes", "no", obj{"label": ""}, 5}},
		"junk",
		obj{"question": "Second?", "multiple": true, "options": []obj{{"label": "x"}}},
	}}
	q := askStep(t, in, nil).Question
	if q == nil || len(q.Questions) != 2 {
		t.Fatalf("question = %+v", q)
	}
	if o := q.Questions[0].Options; len(o) != 2 || o[0].Label != "yes" || o[1].Label != "no" {
		t.Fatalf("plain string options = %+v", o)
	}
	if !q.Questions[1].Multiple {
		t.Fatal("`multiple` (opencode's spelling) must also mean multi-select")
	}
}

func TestQuestion_Caps(t *testing.T) {
	var qs []obj
	for i := 0; i < convmodel.MaxQuestions+3; i++ {
		var labels []string
		for j := 0; j < convmodel.MaxQuestionOptions+3; j++ {
			labels = append(labels, "o")
		}
		qs = append(qs, question(strings.Repeat("q", 5000), false, labels...))
	}
	q := askStep(t, askInput(qs...), nil).Question
	if len(q.Questions) != convmodel.MaxQuestions || len(q.Questions[0].Options) != convmodel.MaxQuestionOptions ||
		len(q.Questions[0].Question) != convmodel.MaxInputString {
		t.Fatalf("questions %d options %d text %d", len(q.Questions), len(q.Questions[0].Options), len(q.Questions[0].Question))
	}
}

func askResult(answers obj) []byte {
	return resultRow("r1", 3, "toolu_1", "User has answered your questions.", false,
		toolUseResult(obj{"questions": []obj{}, "answers": answers, "annotations": obj{}}))
}

// answers come from toolUseResult.answers keyed by the question text.
func TestQuestion_Answers(t *testing.T) {
	in := askInput(question("Which fruit?", false, "Apple", "Pear"), question("Which colours?", true, "Red", "Green", "Blue, dark"))
	s := askStep(t, in, askResult(obj{"Which fruit?": "Pear", "Which colours?": `Red, "Blue, dark"`}))
	if s.Status != convmodel.StepDone {
		t.Fatalf("status %s", s.Status)
	}
	a := s.Question.Answers
	if len(a) != 2 || strings.Join(a[0], "|") != "Pear" || strings.Join(a[1], "|") != "Red|Blue, dark" {
		t.Fatalf("answers = %v", a)
	}
}

// Free text ("Other") is kept whole, also when it contains an option label as a part; a single-select answer is never split.
func TestQuestion_FreeTextKeptWhole(t *testing.T) {
	in := askInput(question("Which colours?", true, "Red", "Green"), question("One?", false, "Apple", "Pear"))
	s := askStep(t, in, askResult(obj{"Which colours?": "something else entirely", "One?": "Apple, Pear"}))
	a := s.Question.Answers
	if len(a) != 2 || strings.Join(a[0], "|") != "something else entirely" || strings.Join(a[1], "|") != "Apple, Pear" {
		t.Fatalf("answers = %v", a)
	}
}

// "Other" text equal to an option label reads as that option (indistinguishable in the transcript, as in Collie).
func TestQuestion_FreeTextEqualToALabel(t *testing.T) {
	s := askStep(t, askInput(question("Which?", false, "Apple")), askResult(obj{"Which?": "Apple"}))
	if a := s.Question.Answers; len(a) != 1 || a[0][0] != "Apple" {
		t.Fatalf("answers = %v", a)
	}
}

// A question without its answer string (or answers not an object) leaves the step with no answers at all.
func TestQuestion_IncompleteAnswersAreNone(t *testing.T) {
	in := askInput(question("One?", false, "A"), question("Two?", false, "B"))
	for name, r := range map[string][]byte{
		"one missing":    askResult(obj{"One?": "A"}),
		"not a string":   askResult(obj{"One?": "A", "Two?": 3}),
		"answers a list": resultRow("r1", 3, "toolu_1", "ok", false, toolUseResult(obj{"answers": []string{"A"}})),
		"no tur":         resultRow("r1", 3, "toolu_1", "ok", false),
	} {
		if s := askStep(t, in, r); s.Question == nil || s.Question.Answers != nil {
			t.Errorf("%s: answers = %v", name, s.Question)
		}
	}
}

// An answer is capped like user text (head), never longer than MaxText.
func TestQuestion_AnswerCap(t *testing.T) {
	s := askStep(t, askInput(question("Q?", false, "A")), askResult(obj{"Q?": strings.Repeat("x", convmodel.MaxText+50)}))
	if got := s.Question.Answers[0][0]; len(got) != convmodel.MaxText {
		t.Fatalf("answer is %d bytes", len(got))
	}
}

// The person dismissed the question: denied / user-rejected, no answers.
func TestQuestion_Dismissed(t *testing.T) {
	r := resultRow("r1", 3, "toolu_1", "The user dismissed the question without answering.", true)
	s := askStep(t, askInput(question("Q?", false, "A")), r)
	if s.Status != convmodel.StepDenied || s.Denial != "user-rejected" || s.Question == nil || s.Question.Answers != nil {
		t.Fatalf("status %s denial %q question %+v", s.Status, s.Denial, s.Question)
	}
}

// The step stays valid on the wire (round trip through the model's own Validate).
func TestQuestion_ValidatesAndSerializes(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"),
		toolCall("a1", 2, "toolu_1", "AskUserQuestion", askInput(question("Q?", false, "A", "B"))),
		askResult(obj{"Q?": "B"}))
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(stepNamed(t, c, "toolu_1"))
	if !strings.Contains(string(b), `"answers":[["B"]]`) {
		t.Fatalf("wire = %s", b)
	}
}

// ---- read range, search scope ----

func TestReadRange(t *testing.T) {
	for name, c := range map[string]struct {
		in   obj
		want *convmodel.ReadRange
	}{
		"both":         {obj{"file_path": "/w/a.go", "offset": 10, "limit": 20}, &convmodel.ReadRange{Offset: 10, Limit: 20}},
		"offset only":  {obj{"file_path": "/w/a.go", "offset": 5}, &convmodel.ReadRange{Offset: 5}},
		"limit only":   {obj{"file_path": "/w/a.go", "limit": 7}, &convmodel.ReadRange{Limit: 7}},
		"whole file":   {obj{"file_path": "/w/a.go"}, nil},
		"zero values":  {obj{"file_path": "/w/a.go", "offset": 0, "limit": 0}, nil},
		"negative":     {obj{"file_path": "/w/a.go", "offset": -3, "limit": 5}, &convmodel.ReadRange{Limit: 5}},
		"not numbers":  {obj{"file_path": "/w/a.go", "offset": "ten", "limit": true}, nil},
		"numeric text": {obj{"file_path": "/w/a.go", "offset": "12", "limit": "3"}, &convmodel.ReadRange{Offset: 12, Limit: 3}},
		"fractions":    {obj{"file_path": "/w/a.go", "offset": 2.5, "limit": 3}, &convmodel.ReadRange{Limit: 3}},
	} {
		s := oneStep(t, "Read", c.in, nil)
		switch {
		case c.want == nil && s.Read != nil, c.want != nil && (s.Read == nil || *s.Read != *c.want):
			t.Errorf("%s: read = %+v, want %+v", name, s.Read, c.want)
		}
	}
	if s := oneStep(t, "Edit", obj{"file_path": "/w/a.go", "offset": 1, "limit": 2}, nil); s.Read != nil {
		t.Fatal("only Read has a read range")
	}
}

func TestSearchScope(t *testing.T) {
	for _, c := range []struct {
		tool string
		in   obj
		want string
	}{
		{"Grep", obj{"pattern": "foo", "path": "/w/src"}, "/w/src"},
		{"Grep", obj{"pattern": "foo", "glob": "*.go"}, "*.go"},
		{"Grep", obj{"pattern": "foo", "path": "/w/src", "glob": "*.go"}, "/w/src"}, // the path wins, as in Collie
		{"Glob", obj{"pattern": "**/*.go", "path": "/w"}, "/w"},
		{"WebSearch", obj{"query": "go generics"}, "web"},
		{"Grep", obj{"pattern": "foo"}, ""},
		{"Glob", obj{"pattern": "**/*.go"}, ""},
		{"Read", obj{"file_path": "/w/a.go", "path": "/p"}, ""},
	} {
		s := oneStep(t, c.tool, c.in, nil)
		got := ""
		if s.Search != nil {
			got = s.Search.Where
		}
		if got != c.want {
			t.Errorf("%s %v: where %q, want %q", c.tool, c.in, got, c.want)
		}
	}
}

// ---- a created file ----

func TestDiffCreated(t *testing.T) {
	create := toolUseResult(obj{"type": "create", "filePath": "/w/new.txt", "content": "hi\n", "structuredPatch": []any{}, "originalFile": nil})
	update := toolUseResult(obj{"type": "update", "filePath": "/w/old.txt", "structuredPatch": []obj{{"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1, "lines": []string{"-a", "+b"}}}})
	s := oneStep(t, "Write", obj{"file_path": "/w/new.txt", "content": "hi\n"}, resultRow("r1", 3, "toolu_1", "created", false, create))
	if s.Diff == nil || !s.Diff.Created || s.Diff.Path != "/w/new.txt" || s.Diff.Added != 1 {
		t.Fatalf("created write: %+v", s.Diff)
	}
	s = oneStep(t, "Write", obj{"file_path": "/w/old.txt", "content": "b\n"}, resultRow("r1", 3, "toolu_1", "updated", false, update))
	if s.Diff == nil || s.Diff.Created || !s.Diff.Exact {
		t.Fatalf("updated write: %+v", s.Diff)
	}
	// still running, or denied: nothing says the file was created
	if s = oneStep(t, "Write", obj{"file_path": "/w/new.txt", "content": "hi\n"}, nil); s.Diff == nil || s.Diff.Created {
		t.Fatalf("running write: %+v", s.Diff)
	}
	s = oneStep(t, "Write", obj{"file_path": "/w/new.txt", "content": "hi\n"}, resultRow("r1", 3, "toolu_1", refusal, true, denialKind("user-rejected"), create))
	if s.Diff != nil && s.Diff.Created {
		t.Fatalf("denied write claims created: %+v", s.Diff)
	}
}

// A created empty file has no input diff to carry the flag: a diff of its own is made.
func TestDiffCreated_EmptyFile(t *testing.T) {
	create := toolUseResult(obj{"type": "create", "filePath": "/w/empty.txt", "structuredPatch": []any{}, "originalFile": nil})
	s := oneStep(t, "Write", obj{"file_path": "/w/empty.txt", "content": ""}, resultRow("r1", 3, "toolu_1", "created", false, create))
	if s.Diff == nil || !s.Diff.Created || s.Diff.Path != "/w/empty.txt" || s.Diff.Added != 0 {
		t.Fatalf("empty create: %+v", s.Diff)
	}
}

// Only a Write (an edit step) can create: the same result type on another tool is ignored.
func TestDiffCreated_OnlyEditSteps(t *testing.T) {
	create := toolUseResult(obj{"type": "create"})
	if s := oneStep(t, "Read", obj{"file_path": "/w/a.go"}, resultRow("r1", 3, "toolu_1", "x", false, create)); s.Diff != nil {
		t.Fatalf("a Read got a diff: %+v", s.Diff)
	}
}

// ---- the compaction summary ----

func compactSummary(uuid string, sec float64, text string) []byte {
	return userRow(uuid, sec, text, isMeta(), with("isCompactSummary", true), without("turnPosition"))
}

func compactedDetail(t *testing.T, c convmodel.Conversation) map[string]any {
	t.Helper()
	for _, tr := range c.Turns {
		for _, it := range tr.Items {
			if it.System != nil && it.System.Kind == convmodel.SystemCompacted {
				var d map[string]any
				if len(it.System.Detail) > 0 {
					if err := json.Unmarshal(it.System.Detail, &d); err != nil {
						t.Fatal(err)
					}
				}
				return d
			}
		}
	}
	t.Fatalf("no compacted item\n%s", dump(c))
	return nil
}

func TestCompactedSummary(t *testing.T) {
	c := conv(t, userRow("u1", 1, "/compact"), compactBoundary("cb1", 2, "manual"), compactSummary("cs1", 2.1, "The session so far: we did X."))
	d := compactedDetail(t, c)
	if d["trigger"] != "manual" || d["summary"] != "The session so far: we did X." || d["truncated"] != nil {
		t.Fatalf("detail = %v", d)
	}
}

// The summary is capped like user text, with truncated set.
func TestCompactedSummary_Cap(t *testing.T) {
	c := conv(t, userRow("u1", 1, "/compact"), compactBoundary("cb1", 2, "auto"), compactSummary("cs1", 2.1, strings.Repeat("s", convmodel.MaxText+10)))
	d := compactedDetail(t, c)
	if got := d["summary"].(string); len(got) != convmodel.MaxText || d["truncated"] != true {
		t.Fatalf("summary %d bytes, truncated %v", len(got), d["truncated"])
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A summary row with no boundary before it stays skipped (and counted); a boundary with no summary has no summary member.
func TestCompactedSummary_Absent(t *testing.T) {
	n := norm(t, userRow("u1", 1, "hi"), compactSummary("cs1", 2, "orphan"))
	if n.Stats().Skipped["compact_summary"] != 1 {
		t.Fatalf("skipped = %v", n.Stats().Skipped)
	}
	d := compactedDetail(t, conv(t, userRow("u1", 1, "/compact"), compactBoundary("cb1", 2, "manual")))
	if _, has := d["summary"]; has {
		t.Fatalf("detail = %v", d)
	}
}

// Feeding the rows one by one gives the same model as feeding them together (the incremental rule).
func TestCompactedSummary_Incremental(t *testing.T) {
	rows := [][]byte{userRow("u1", 1, "/compact"), compactBoundary("cb1", 2, "manual"), compactSummary("cs1", 2.1, "S")}
	whole := compactedDetail(t, conv(t, rows...))
	n := New(Options{SessionID: sidA})
	for _, r := range rows {
		feed(t, n, r)
	}
	if got := compactedDetail(t, validated(t, n)); got["summary"] != whole["summary"] || got["summary"] != "S" {
		t.Fatalf("incremental detail = %v", got)
	}
}
