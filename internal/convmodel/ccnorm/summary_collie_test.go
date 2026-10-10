package ccnorm

import (
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// U3-0 (plan D12): every kind's one-line summary, side by side with Collie's `summarizeToolInput` (bridge/journal/text.ts, 1.17.2).
// The `collie` column is what Collie's own function returned for the same input (run with node on its source);
// `ours` is what this normalizer returns. Differences are adopted (Collie's text becomes ours) unless they lose information the
// step does not carry elsewhere; a row where the two differ must say why in `why`, and that column is the record of what
// was kept. Regenerate the `collie` column with the script in the PR description if Collie changes.
var collieSummaryTable = []struct {
	name, tool, input, collie, ours, why string
}{
	{"bash one line", "Bash", `{"command": "go test ./...", "description": "run tests"}`, "go test ./...", "go test ./...", ""},
	{"bash multi line", "Bash", `{"command": "cat <<EOF\nhello\nworld\nEOF"}`, "cat <<EOF hello world EOF", "cat <<EOF hello world EOF", ""},
	{"bash long", "Bash", `{"command": "echo AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`, "echo AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA…", "echo AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA…", ""},
	{"bash blank", "Bash", `{"command": "   "}`, "", "", ""},
	{"read", "Read", `{"file_path": "/work/internal/a/b.go", "offset": 10, "limit": 20}`, "/work/internal/a/b.go", "/work/internal/a/b.go", ""},
	{"read whole", "Read", `{"file_path": "/work/x.go"}`, "/work/x.go", "/work/x.go", ""},
	{"edit", "Edit", `{"file_path": "/work/x.go", "old_string": "a", "new_string": "b"}`, "/work/x.go", "/work/x.go", ""},
	{"write", "Write", `{"file_path": "/work/new.txt", "content": "hi"}`, "/work/new.txt", "/work/new.txt", ""},
	{"multiedit", "MultiEdit", `{"file_path": "/work/m.go", "edits": [{"old_string": "a", "new_string": "b"}]}`, "/work/m.go", "/work/m.go", ""},
	{"notebook", "NotebookEdit", `{"notebook_path": "/work/n.ipynb", "new_source": "x"}`, "/work/n.ipynb", "/work/n.ipynb", ""},
	{"grep", "Grep", `{"pattern": "foo.*bar", "path": "/work/src"}`, "foo.*bar", "foo.*bar", ""},
	{"grep glob", "Grep", `{"pattern": "foo", "glob": "*.go"}`, "foo", "foo", ""},
	{"glob", "Glob", `{"pattern": "**/*.go", "path": "/work"}`, "**/*.go", "**/*.go", ""},
	{"glob no path", "Glob", `{"pattern": "**/*.ts"}`, "**/*.ts", "**/*.ts", ""},
	{"webfetch", "WebFetch", `{"url": "https://example.com/a", "prompt": "summarize"}`, "https://example.com/a", "https://example.com/a", ""},
	{"websearch", "WebSearch", `{"query": "golang generics"}`, "golang generics", "golang generics", ""},
	{"agent", "Agent", `{"description": "review the diff", "prompt": "long prompt", "subagent_type": "Explore"}`, "review the diff", "review the diff", ""},
	{"task", "Task", `{"description": "find usages", "prompt": "p"}`, "find usages", "find usages", ""},
	{"monitor", "Monitor", `{"description": "watch the build", "command": "tail -f log"}`, "tail -f log", "watch the build", "Collie has no per-tool rule and reads `command` before `description`; for Monitor the description is the readable line, and the command is in step.command.text"},
	{"skill", "Skill", `{"skill": "superpowers:brainstorming", "args": "x"}`, "superpowers:brainstorming", "superpowers:brainstorming", ""},
	{"ask", "AskUserQuestion", `{"questions": [{"question": "Which fruit?\nSecond line", "header": "Fruit", "multiSelect": false, "options": [{"label": "Apple", "description": "red"}, {"label": "Pear"}]}]}`, "Which fruit? Second line", "Which fruit? Second line", ""},
	{"mcp", "mcp__plooom__issue_get", `{"issue_id": "12", "project": "p"}`, "12", "plooom · issue_get", "Collie shows the first argument value; we keep the tool identity (server · tool), the arguments are in step.input"},
	{"mcp query", "mcp__x__search", `{"zeta": "z", "query": "find me"}`, "find me", "x · search", "same: MCP tools keep \"server · tool\""},
	{"unknown sorted", "Weird", `{"zeta": "z", "alpha": "a"}`, "z", "a", "Go decodes a JSON object into an unordered map, so \"the first string value\" is taken in sorted key order, not insertion order; deterministic, same kind of fallback"},
	{"unknown named", "Weird", `{"zeta": "z", "path": "/p", "alpha": "a"}`, "/p", "/p", ""},
	{"exit plan", "ExitPlanMode", `{"plan": "1. do\n2. more"}`, "1. do 2. more", "1. do 2. more", ""},
	{"toolsearch", "ToolSearch", `{"query": "select:Read", "max_results": 3}`, "select:Read", "select:Read", ""},
	{"no strings", "Weird", `{"n": 1, "b": true}`, "", "", ""},
	{"empty input", "Weird", `{}`, "", "", ""},
	{"description over path", "Weird", `{"description": "d", "path": "/p"}`, "/p", "/p", ""},
	{"prompt only", "Weird", `{"prompt": "just a prompt", "n": 1}`, "just a prompt", "just a prompt", ""},
	{"tabs and spaces", "Bash", `{"command": "a\t\tb   c\n\n d"}`, "a b c d", "a b c d", ""},
	{"multibyte long", "Bash", `{"command": "測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試"}`, "測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試", "測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試測試", ""},
}

func TestSummary_AgainstCollie(t *testing.T) {
	for _, c := range collieSummaryTable {
		var in object
		if err := json.Unmarshal([]byte(c.input), &in); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := summaryOf(c.tool, in)
		if got != c.ours {
			t.Errorf("%s: summary %q, want %q (Collie: %q)", c.name, got, c.ours, c.collie)
		}
		if (c.collie != c.ours) != (c.why != "") {
			t.Errorf("%s: collie %q ours %q why %q: a difference needs a reason, and a reason needs a difference", c.name, c.collie, c.ours, c.why)
		}
		if !utf8.ValidString(got) || len([]rune(got)) > maxSummaryRunes+1 {
			t.Errorf("%s: summary is not one short valid line: %q", c.name, got)
		}
	}
}
