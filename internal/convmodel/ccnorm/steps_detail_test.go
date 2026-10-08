package ccnorm

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/wake/purdex/internal/convmodel"
)

// hunkObj is a structuredPatch hunk as Claude Code writes it.
func hunkObj(oldStart, oldLines, newStart, newLines int, lines ...string) obj {
	return obj{"oldStart": oldStart, "oldLines": oldLines, "newStart": newStart, "newLines": newLines, "lines": lines}
}

func editResult(path string, hunks ...obj) []byte {
	return resultRow("r1", 3, "toolu_1", "The file "+path+" has been updated successfully.", false,
		toolUseResult(obj{"filePath": path, "oldString": "a", "newString": "b", "structuredPatch": hunks}))
}

func TestDiff_FromStructuredPatchExact(t *testing.T) {
	// the input says 5 lines out and 7 in; the patch says 1 out and 2 in
	in := obj{"file_path": "/work/x/a.go", "old_string": "1\n2\n3\n4\n5", "new_string": "1\n2\n3\n4\n5\n6\n7"}
	s := oneStep(t, "Edit", in, editResult("/work/x/a.go",
		hunkObj(3, 4, 3, 5, " ctx1", "-old", "+new1", "+new2", " ctx2"),
		hunkObj(40, 1, 41, 1, "-z", "+y"),
	))
	d := s.Diff
	if d == nil {
		t.Fatalf("no diff: %+v", s)
	}
	if !d.Exact || d.Path != "/work/x/a.go" || d.Truncated {
		t.Errorf("diff = %+v", d)
	}
	if d.Added != 3 || d.Removed != 2 {
		t.Errorf("added %d removed %d, want 3 and 2 (counted from the patch, not the input's 7 and 5)", d.Added, d.Removed)
	}
	want := []convmodel.Hunk{
		{OldStart: 3, OldLines: 4, NewStart: 3, NewLines: 5, Lines: []string{" ctx1", "-old", "+new1", "+new2", " ctx2"}},
		{OldStart: 40, OldLines: 1, NewStart: 41, NewLines: 1, Lines: []string{"-z", "+y"}},
	}
	if !reflect.DeepEqual(d.Hunks, want) {
		t.Errorf("hunks = %+v", d.Hunks)
	}
	// the wire form is snake_case
	if got := jsonOf(t, d); !strings.Contains(got, `"old_start":3`) || !strings.Contains(got, `"exact":true`) {
		t.Errorf("json = %s", got)
	}
}

func TestDiff_FromInputWhenDenied(t *testing.T) {
	in := obj{"file_path": "/work/x/a.go", "old_string": "a\nb", "new_string": "a\nB\nc\n"}
	for name, row := range map[string][]byte{
		"denied by field": resultRow("r1", 3, "toolu_1", refusal, true, denialKind("user-rejected"), toolUseResult("User rejected tool use")),
		"denied by text":  resultRow("r1", 3, "toolu_1", refusal, true),
		"failed":          resultRow("r1", 3, "toolu_1", "<tool_use_error>String to replace not found</tool_use_error>", true),
		"no result":       nil,
		"empty patch":     editResult("/work/x/a.go"),
	} {
		d := oneStep(t, "Edit", in, row).Diff
		if d == nil {
			t.Errorf("%s: no diff", name)
			continue
		}
		if d.Exact || d.Path != "/work/x/a.go" || d.Added != 3 || d.Removed != 2 {
			t.Errorf("%s: diff = %+v", name, d)
		}
		if len(d.Hunks) != 1 || !reflect.DeepEqual(d.Hunks[0].Lines, []string{"-a", "-b", "+a", "+B", "+c"}) {
			t.Errorf("%s: hunks = %+v", name, d.Hunks)
		}
		if h := d.Hunks[0]; h.OldStart != 1 || h.OldLines != 2 || h.NewStart != 1 || h.NewLines != 3 {
			t.Errorf("%s: hunk header = %+v", name, h)
		}
	}
	// Write: everything is an addition
	d := oneStep(t, "Write", obj{"file_path": "/work/x/new.txt", "content": "l1\nl2\n"}, nil).Diff
	if d == nil || d.Exact || d.Added != 2 || d.Removed != 0 || d.Hunks[0].OldStart != 0 || d.Hunks[0].OldLines != 0 {
		t.Errorf("Write diff = %+v", d)
	}
	// the exact patch replaces the input-derived one when it arrives
	s := oneStep(t, "Edit", in, editResult("/work/x/a.go", hunkObj(1, 1, 1, 1, "-a", "+b")))
	if s.Diff == nil || !s.Diff.Exact || s.Diff.Added != 1 {
		t.Errorf("after the result: %+v", s.Diff)
	}
	// other kinds carry no diff; an Edit without strings carries none
	if s := oneStep(t, "Read", obj{"file_path": "/f", "old_string": "x"}, nil); s.Diff != nil {
		t.Errorf("Read has a diff: %+v", s.Diff)
	}
	if s := oneStep(t, "Edit", obj{"file_path": "/f"}, nil); s.Diff != nil {
		t.Errorf("Edit without strings has a diff: %+v", s.Diff)
	}
}

func TestDiff_MultiEditConcatenated(t *testing.T) {
	in := obj{"file_path": "/work/x/m.go", "edits": []obj{
		{"old_string": "a", "new_string": "b\nc"},
		{"old_string": "d\ne", "new_string": "f"},
	}}
	d := oneStep(t, "MultiEdit", in, nil).Diff
	if d == nil || d.Exact || d.Added != 3 || d.Removed != 3 || len(d.Hunks) != 2 {
		t.Fatalf("diff = %+v", d)
	}
	if !reflect.DeepEqual(d.Hunks[0].Lines, []string{"-a", "+b", "+c"}) || !reflect.DeepEqual(d.Hunks[1].Lines, []string{"-d", "-e", "+f"}) {
		t.Errorf("hunks = %+v", d.Hunks)
	}
}

func TestDiff_HunkLinesCapped(t *testing.T) {
	count := func(d *convmodel.Diff) int {
		n := 0
		for _, h := range d.Hunks {
			n += len(h.Lines)
		}
		return n
	}
	// a patch: 3 hunks, 1,000 lines in all
	var hunks []obj
	for h := range 3 {
		var lines []string
		for i := range 300 + h*50 {
			lines = append(lines, fmt.Sprintf("+h%d line %d", h, i))
		}
		hunks = append(hunks, hunkObj(1, 0, 1, len(lines), lines...))
	}
	d := oneStep(t, "Edit", obj{"file_path": "/f"}, editResult("/f", hunks...)).Diff
	if d == nil || !d.Truncated || count(d) != convmodel.MaxDiffLines {
		t.Fatalf("truncated=%v lines=%d", d != nil && d.Truncated, count(d))
	}
	if d.Added != 300+350+400-0 {
		t.Errorf("added = %d, want 1,050 (the whole patch)", d.Added)
	}
	if len(d.Hunks) != 2 || len(d.Hunks[1].Lines) != 100 {
		t.Errorf("the cut hunk should keep the first 100 of its lines, hunks: %d", len(d.Hunks))
	}
	// exactly at the cap is not truncated
	var lines []string
	for i := range convmodel.MaxDiffLines {
		lines = append(lines, fmt.Sprintf("+l%d", i))
	}
	d = oneStep(t, "Edit", obj{"file_path": "/f"}, editResult("/f", hunkObj(1, 0, 1, 400, lines...))).Diff
	if d.Truncated || count(d) != 400 {
		t.Errorf("exactly 400 lines: truncated=%v lines=%d", d.Truncated, count(d))
	}
	// built from input: a 1,000-line Write
	d = oneStep(t, "Write", obj{"file_path": "/f", "content": numbered(1000)}, nil).Diff
	if !d.Truncated || count(d) != convmodel.MaxDiffLines || d.Added != 1000 || d.Exact {
		t.Errorf("Write: truncated=%v lines=%d added=%d", d.Truncated, count(d), d.Added)
	}
	// MultiEdit: the cap is shared by all edits
	d = oneStep(t, "MultiEdit", obj{"file_path": "/f", "edits": []obj{
		{"old_string": numbered(300), "new_string": "x"}, {"old_string": numbered(300), "new_string": "y"},
	}}, nil).Diff
	if !d.Truncated || count(d) != convmodel.MaxDiffLines || d.Removed != 600 {
		t.Errorf("MultiEdit: truncated=%v lines=%d removed=%d", d.Truncated, count(d), d.Removed)
	}
}

func TestCommand_ExitCodeParsed(t *testing.T) {
	in := obj{"command": "false", "description": "Fail on purpose"}
	for text, want := range map[string]*int{
		"Exit code 1\nboom":       ptr(1),
		"Exit code 127":           ptr(127),
		"Exit code -1\nsignal":    ptr(-1),
		"fine":                    nil,
		"echo Exit code 2":        nil,
		"Exit code":               nil,
		"Exit codes were nice":    nil,
		"Exit code 99999999999 x": nil,
	} {
		s := oneStep(t, "Bash", in, resultRow("r1", 3, "toolu_1", text, true))
		cm := s.Command
		if cm == nil || cm.Text != "false" || cm.Description != "Fail on purpose" {
			t.Fatalf("%q: command = %+v", text, cm)
		}
		if !reflect.DeepEqual(cm.ExitCode, want) {
			t.Errorf("%q: exit_code = %v, want %v", text, deref(cm.ExitCode), deref(want))
		}
	}
	// before the result the command is there already
	if cm := oneStep(t, "Bash", in, nil).Command; cm == nil || cm.Text != "false" || cm.ExitCode != nil {
		t.Errorf("no result: %+v", cm)
	}
	// only execute steps have one, and only with a command
	if s := oneStep(t, "Read", obj{"command": "x", "file_path": "/f"}, resultRow("r1", 3, "toolu_1", "Exit code 1", true)); s.Command != nil {
		t.Errorf("Read has a command")
	}
	if s := oneStep(t, "Bash", obj{}, nil); s.Command != nil {
		t.Errorf("Bash without a command has one: %+v", s.Command)
	}
	if s := oneStep(t, "Monitor", obj{"command": "tail -f x", "description": "watch"}, nil); s.Command == nil || s.Command.Text != "tail -f x" {
		t.Errorf("Monitor: %+v", s.Command)
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestCommand_BackgroundTaskID(t *testing.T) {
	// c2 line 149: the result of a run_in_background Bash
	s := oneStep(t, "Bash", obj{"command": "sleep 20", "run_in_background": true},
		resultRow("r1", 3, "toolu_1", "Command running in background with ID: bnh3feck0.", false,
			toolUseResult(obj{"stdout": "", "stderr": "", "interrupted": false, "isImage": false, "noOutputExpected": false, "backgroundTaskId": "bnh3feck0"})))
	if s.Command == nil || s.Command.BackgroundTaskID != "bnh3feck0" || s.Status != convmodel.StepDone {
		t.Errorf("command = %+v status %q", s.Command, s.Status)
	}
	// a plain result has none, and a string toolUseResult is fine
	s = oneStep(t, "Bash", obj{"command": "ls"}, resultRow("r1", 3, "toolu_1", "x", false, toolUseResult("User rejected tool use")))
	if s.Command == nil || s.Command.BackgroundTaskID != "" {
		t.Errorf("command = %+v", s.Command)
	}
}

func TestSubagent_LinkFromToolUseResult(t *testing.T) {
	in := obj{"description": "Explore the repo", "subagent_type": "Explore", "prompt": "look around"}
	s := oneStep(t, "Agent", in, resultRow("r1", 3, "toolu_1", "The agent found things.", false,
		toolUseResult(obj{"status": "completed", "agentId": "a3f9c1d2e4b5", "isAsync": false, "totalToolUseCount": 4})))
	want := &convmodel.Subagent{AgentID: "a3f9c1d2e4b5", Description: "Explore the repo", Type: "Explore"}
	if !reflect.DeepEqual(s.Subagent, want) {
		t.Errorf("subagent = %+v, want %+v", s.Subagent, want)
	}
	s = oneStep(t, "Task", in, resultRow("r1", 3, "toolu_1", "Async agent launched.", false,
		toolUseResult(obj{"status": "async_launched", "isAsync": true, "agentId": "bb12"})))
	if s.Subagent == nil || !s.Subagent.Async || s.Subagent.AgentID != "bb12" {
		t.Errorf("async: %+v", s.Subagent)
	}
	// no link: no result, no agentId, a denied call, an id that is not a file name
	for name, row := range map[string][]byte{
		"no result": nil,
		"no id":     resultRow("r1", 3, "toolu_1", "x", false, toolUseResult(obj{"status": "completed"})),
		"denied":    resultRow("r1", 3, "toolu_1", refusal, true, denialKind("user-rejected"), toolUseResult("User rejected tool use")),
		"path id":   resultRow("r1", 3, "toolu_1", "x", false, toolUseResult(obj{"agentId": "../../etc/passwd"})),
		"long id":   resultRow("r1", 3, "toolu_1", "x", false, toolUseResult(obj{"agentId": strings.Repeat("a", 200)})),
	} {
		if s := oneStep(t, "Agent", in, row); s.Subagent != nil {
			t.Errorf("%s: subagent = %+v", name, s.Subagent)
		}
	}
	// other kinds never get one
	if s := oneStep(t, "Bash", obj{"command": "x"}, resultRow("r1", 3, "toolu_1", "x", false, toolUseResult(obj{"agentId": "a1"}))); s.Subagent != nil {
		t.Errorf("Bash got a subagent")
	}
}
