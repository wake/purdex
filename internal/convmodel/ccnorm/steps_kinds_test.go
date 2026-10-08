package ccnorm

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wake/purdex/internal/convmodel"
)

func TestKind_Table(t *testing.T) {
	// spec §8.1 kind table and lead ruling D6
	cases := map[string]convmodel.StepKind{
		"Edit": convmodel.StepEdit, "MultiEdit": convmodel.StepEdit, "Write": convmodel.StepEdit,
		"NotebookEdit": convmodel.StepEdit, "apply_patch": convmodel.StepEdit,
		"Bash": convmodel.StepExecute, "exec_command": convmodel.StepExecute, "Monitor": convmodel.StepExecute,
		"Read": convmodel.StepRead,
		"Grep": convmodel.StepSearch, "Glob": convmodel.StepSearch, "WebSearch": convmodel.StepSearch,
		"WebFetch": convmodel.StepFetch,
		"Agent":    convmodel.StepTask, "Task": convmodel.StepTask, "spawn_agent": convmodel.StepTask,
		"ToolSearch": convmodel.StepOther, "AskUserQuestion": convmodel.StepOther, "ExitPlanMode": convmodel.StepOther,
		"Skill": convmodel.StepOther, "mcp__x__y": convmodel.StepOther, "TodoWrite": convmodel.StepOther,
		"NoSuchTool": convmodel.StepOther, "bash": convmodel.StepOther, // names are case-sensitive
	}
	for name, want := range cases {
		s := oneStep(t, name, obj{"x": "y"}, nil)
		if s.Kind != want || s.Tool != name {
			t.Errorf("%s: kind %q tool %q, want %q", name, s.Kind, s.Tool, want)
		}
	}
}

func TestSummary_Table(t *testing.T) {
	cases := []struct {
		name  string
		input obj
		want  string
	}{
		{"Bash", obj{"command": "ls -la", "description": "list"}, "ls -la"},
		{"Bash", obj{"command": "echo a\necho b"}, "echo a"}, // first line only
		{"Bash", obj{"command": "  \n  echo a"}, "echo a"},
		{"Bash", obj{"command": "echo a\r\necho b"}, "echo a"},
		{"Read", obj{"file_path": "/work/x/src/main.go"}, "main.go"},
		{"Edit", obj{"file_path": "/work/x/a.txt", "old_string": "a", "new_string": "b"}, "a.txt"},
		{"Write", obj{"file_path": "/work/x/new.md", "content": "c"}, "new.md"},
		{"MultiEdit", obj{"file_path": "/work/x/pkg/m.go", "edits": []obj{{"old_string": "a", "new_string": "b"}}}, "m.go"},
		{"NotebookEdit", obj{"notebook_path": "/work/x/nb.ipynb", "new_source": "x"}, "nb.ipynb"},
		{"Grep", obj{"pattern": "foo.*bar", "path": "/work"}, "foo.*bar"},
		{"Glob", obj{"pattern": "**/*.go"}, "**/*.go"},
		{"WebFetch", obj{"url": "https://example.com/a", "prompt": "p"}, "https://example.com/a"},
		{"WebSearch", obj{"query": "go json"}, "go json"},
		{"Agent", obj{"description": "Explore the repo", "prompt": "long"}, "Explore the repo"},
		{"Task", obj{"description": "Old name", "prompt": "long"}, "Old name"},
		{"Monitor", obj{"description": "Watch the build", "command": "tail -f x"}, "Watch the build"},
		{"Skill", obj{"skill": "commit"}, "commit"},
		{"AskUserQuestion", obj{"questions": []obj{{"question": "貓還是狗？", "header": "h"}, {"question": "second"}}}, "貓還是狗？"},
		{"mcp__ploom__issue_get", obj{"id": "1"}, "ploom · issue_get"},
		{"mcp__a__b__c", obj{"id": "1"}, "a · b__c"}, // only the first separator splits
		// fallback: the first string input value in (sorted) key order
		{"ToolSearch", obj{"max_results": 5, "query": "select:X", "a_flag": true}, "select:X"},
		{"Whatever", obj{"b": "second", "a": "first", "n": 3}, "first"},
		{"Whatever", obj{"n": 3, "ok": true}, ""},
		{"Read", obj{"offset": 1}, ""},
		{"Read", obj{"pattern": "from-fallback"}, "from-fallback"}, // the named field is missing
		{"Bash", obj{"command": ""}, ""},
		{"Whatever", obj{}, ""},
	}
	for i, c := range cases {
		s := oneStep(t, c.name, c.input, nil)
		if s.Summary != c.want {
			t.Errorf("#%d %s %v: summary %q, want %q", i, c.name, c.input, s.Summary, c.want)
		}
	}
}

func TestSummary_CappedAtOneLine(t *testing.T) {
	s := oneStep(t, "Bash", obj{"command": repeat("x", 10000)}, nil)
	if len(s.Summary) > convmodel.MaxInputString || !utf8.ValidString(s.Summary) {
		t.Errorf("summary is %d bytes, valid=%v", len(s.Summary), utf8.ValidString(s.Summary))
	}
}

func TestStep_BasicFields(t *testing.T) {
	c := conv(t, userRow("u1", 1, "go"), toolCall("a1", 2.5, "toolu_1", "Bash", obj{"command": "ls"}))
	s := stepsIn(t, c, 0)
	if len(s) != 1 {
		t.Fatalf("steps = %d\n%s", len(s), dump(c))
	}
	if s[0].ID != "toolu_1" || s[0].At != ms(2.5) || s[0].StartedAt != ms(2.5) || s[0].Tool != "Bash" {
		t.Errorf("step = %+v", s[0])
	}
	if s[0].Status != convmodel.StepRunning || s[0].Denial != "" || s[0].Output != nil {
		t.Errorf("a step with no result in a running turn: %+v", s[0])
	}
	// the row's uuid is not the step id, and the row opens no second item
	if got := itemsOf(t, c, 0); len(got) != 2 {
		t.Errorf("items = %v", sigs(got))
	}
}

func TestStep_RowWithTextAndToolUseKeepsUUIDForText(t *testing.T) {
	// old multi-block rows: the step is named by its tool_use id and takes
	// no uuid slot, so the text keeps the row uuid
	c := conv(t, userRow("u1", 1, "go"),
		multiBlockAssistant("m1", 2, toolUseBlock("toolu_1", "Bash", obj{"command": "ls"}), textBlock("hello"), textBlock("more")))
	var ids []string
	for _, it := range itemsOf(t, c, 0) {
		ids = append(ids, itemID(it))
	}
	if want := []string{"u1", "toolu_1", "m1", "m1#1"}; !equalStrings(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func TestStep_NoIDOrDuplicateIDSkipped(t *testing.T) {
	n := norm(t, userRow("u1", 1, "go"),
		assistantRow("a1", 2, "claude-opus-5-5", obj{"type": "tool_use", "name": "Bash", "input": obj{}}),
		toolCall("a2", 3, "toolu_1", "Bash", obj{"command": "first"}),
		toolCall("a3", 4, "toolu_1", "Bash", obj{"command": "second"}),
	)
	c := validated(t, n)
	s := stepsIn(t, c, 0)
	if len(s) != 1 || s[0].Summary != "first" {
		t.Fatalf("steps = %+v", s)
	}
	if n.Stats().Skipped["step:no_id"] != 1 || n.Stats().Skipped["step:duplicate"] != 1 {
		t.Errorf("Skipped = %v", n.Stats().Skipped)
	}
}

func decodeInput(t testing.TB, s *convmodel.Step) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(s.Input, &m); err != nil {
		t.Fatalf("input is not a JSON object: %v: %s", err, s.Input)
	}
	return m
}

func TestInput_StringValuesCapped(t *testing.T) {
	s := oneStep(t, "Write", obj{"file_path": "/work/x/a.txt", "content": repeat("x", 10000)}, nil)
	m := decodeInput(t, s)
	if got := m["content"].(string); len(got) != convmodel.MaxInputString {
		t.Errorf("content = %d bytes, want %d", len(got), convmodel.MaxInputString)
	}
	if m["file_path"] != "/work/x/a.txt" {
		t.Errorf("file_path = %v", m["file_path"])
	}
	if !s.InputTruncated {
		t.Error("input_truncated not set after a string was cut")
	}

	// nested strings are capped too, and a cut never splits a rune
	s = oneStep(t, "MultiEdit", obj{"file_path": "/f", "edits": []obj{{"old_string": repeat("世", 3000), "new_string": "n"}}}, nil)
	m = decodeInput(t, s)
	old := m["edits"].([]any)[0].(map[string]any)["old_string"].(string)
	if !utf8.ValidString(old) || len(old) > convmodel.MaxInputString || len(old) < convmodel.MaxInputString-3 {
		t.Errorf("nested old_string: %d bytes, valid=%v", len(old), utf8.ValidString(old))
	}
	if !s.InputTruncated {
		t.Error("input_truncated not set for a nested cut")
	}

	// exactly at the cap is not cut
	s = oneStep(t, "Write", obj{"file_path": "/f", "content": repeat("y", convmodel.MaxInputString)}, nil)
	if s.InputTruncated || len(decodeInput(t, s)["content"].(string)) != convmodel.MaxInputString {
		t.Errorf("a string of exactly 4 KiB was cut (truncated=%v)", s.InputTruncated)
	}
}

func TestInput_WholeCapped(t *testing.T) {
	in := obj{}
	for i := range 40 {
		in[fmt.Sprintf("k%02d", i)] = repeat("v", 1000) // each under the string cap, 40 KB together
	}
	s := oneStep(t, "Whatever", in, nil)
	if !s.InputTruncated {
		t.Error("input_truncated not set after the whole input was cut")
	}
	if n := len(s.Input); n > convmodel.MaxInput || n < convmodel.MaxInput-3 {
		t.Errorf("input is %d bytes, want %d (a cut on the boundary)", n, convmodel.MaxInput)
	}
	m := decodeInput(t, s) // still a JSON object
	if m["k00"] != repeat("v", 1000) {
		t.Error("the first keys are kept whole")
	}
	if len(m) >= 40 {
		t.Errorf("%d keys survive a 40 KB input", len(m))
	}
}

func TestInput_SmallInputUntouched(t *testing.T) {
	s := oneStep(t, "Read", obj{"file_path": "/work/x/a.go", "limit": 12345678901234567, "ok": true, "none": nil, "f": 1.5}, nil)
	if s.InputTruncated {
		t.Error("a small input was flagged truncated")
	}
	got := string(s.Input)
	for _, want := range []string{`"limit":12345678901234567`, `"ok":true`, `"none":null`, `"f":1.5`, `"file_path":"/work/x/a.go"`} {
		if !strings.Contains(got, want) {
			t.Errorf("input %s lacks %s", got, want)
		}
	}
}

func TestInput_MissingOrOddInputBecomesEmptyObject(t *testing.T) {
	// the spec says a step input is an object; anything else is stored as {}
	// and is not "truncated" (nothing of an object was cut)
	cases := map[string]any{
		"missing": nil, "string": "not an object", "array": []int{1},
		"number": 5, "bool": true, "null": json.RawMessage("null"),
	}
	summaries := map[string]string{}
	for name, in := range cases {
		b := obj{"type": "tool_use", "id": "toolu_1", "name": "Bash"}
		if name != "missing" {
			b["input"] = in
		}
		c := conv(t, userRow("u1", 1, "go"), assistantRow("a1", 2, "claude-opus-5-5", b))
		s := stepNamed(t, c, "toolu_1")
		if m := decodeInput(t, s); m == nil || len(m) != 0 || string(s.Input) != "{}" {
			t.Errorf("%s: input %q, want {}", name, s.Input)
		}
		if s.InputTruncated {
			t.Errorf("%s: a non-object input must not set input_truncated", name)
		}
		summaries[name] = s.Summary
	}
	for name, got := range summaries {
		if got != summaries["missing"] {
			t.Errorf("%s: summary %q, want the missing-input fallback %q", name, got, summaries["missing"])
		}
	}
}
