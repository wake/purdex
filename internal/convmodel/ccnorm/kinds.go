package ccnorm

import (
	"sort"
	"strings"

	"github.com/wake/purdex/internal/convmodel"
)

// kindOf groups a tool by what it does (spec §8.1 "Steps", lead ruling D6).
// The table follows the iOS parser (Transcript.swift Tool.kind) plus the
// Nexen / Codex names; anything else, MCP tools included, is "other".
func kindOf(tool string) convmodel.StepKind {
	switch tool {
	case "Edit", "MultiEdit", "Write", "NotebookEdit", "apply_patch":
		return convmodel.StepEdit
	case "Bash", "exec_command", "Monitor":
		return convmodel.StepExecute
	case "Read":
		return convmodel.StepRead
	case "Grep", "Glob", "WebSearch":
		return convmodel.StepSearch
	case "WebFetch":
		return convmodel.StepFetch
	case "Agent", "Task", "spawn_agent":
		return convmodel.StepTask
	}
	return convmodel.StepOther
}

// maxSummaryRunes is the longest one-line summary; past it the line stops being a summary and ends in an ellipsis (Collie's
// limit, U3-0).
const maxSummaryRunes = 200

// pickOrder is the named input fields tried, in this order, when a tool's own field is empty and for tools the table below
// does not know: the same list and order as Collie's `summarizeToolInput`, so `pattern` outranks the bare `path` of a Grep.
var pickOrder = [...]string{"file_path", "command", "pattern", "query", "url", "path", "description", "task", "prompt"}

// summaryOf is the one line a step shows (spec §8.1 "Steps", aligned with Collie's `summarizeToolInput` in U3-0; the table
// test summary_collie_test.go is the record of every difference kept). A question asked by the call comes first (by the
// shape of the input); then the tool's main argument; else the named fields of pickOrder; else the first string input value
// in sorted key order. Whitespace runs collapse to one space, and the line is cut at maxSummaryRunes.
func summaryOf(tool string, in object) string {
	if q := questionOf(in); q != nil {
		return oneLine(q.Questions[0].Question)
	}
	var s string
	switch tool {
	case "Bash":
		s = in.str("command")
	case "Read", "Edit", "MultiEdit", "Write":
		s = in.str("file_path")
	case "NotebookEdit":
		s = in.str("notebook_path")
		if s == "" {
			s = in.str("file_path")
		}
	case "Grep", "Glob":
		s = in.str("pattern")
	case "WebFetch":
		s = in.str("url")
	case "WebSearch":
		s = in.str("query")
	case "Agent", "Task", "Monitor":
		s = in.str("description")
	case "Skill":
		s = in.str("skill")
	default:
		// an MCP tool keeps its identity ("server · tool"); Collie shows the first argument instead
		if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
			if server, name, ok := strings.Cut(rest, "__"); ok {
				s = server + " · " + name
			}
		}
	}
	if strings.TrimSpace(s) == "" {
		for _, k := range pickOrder {
			if v := in.str(k); strings.TrimSpace(v) != "" {
				s = v
				break
			}
		}
	}
	if strings.TrimSpace(s) == "" {
		s = firstStringValue(in)
	}
	return oneLine(s)
}

// oneLine collapses every run of white space to one space, trims, and cuts the line at maxSummaryRunes runes with an
// ellipsis.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxSummaryRunes {
		return string(r[:maxSummaryRunes]) + "…"
	}
	return s
}

// firstStringValue is the first non-blank string input value in sorted key
// order, like iOS `input.keys.sorted()`.
func firstStringValue(in object) string {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := jsonString(in[k]); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
