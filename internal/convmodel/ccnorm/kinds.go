package ccnorm

import (
	"path"
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

// summaryOf is the one line a step shows: the tool's main argument, else the
// first string input value in sorted key order (the iOS fallback), first line
// only and at most one input string long. Rules: spec §8.1 "Steps".
func summaryOf(tool string, in object) string {
	var s string
	switch tool {
	case "Bash":
		s = in.str("command")
	case "Read", "Edit", "MultiEdit", "Write":
		s = baseName(in.str("file_path"))
	case "NotebookEdit":
		s = baseName(in.str("notebook_path"))
		if s == "" {
			s = baseName(in.str("file_path"))
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
	case "AskUserQuestion":
		s = firstQuestion(in.get("questions"))
	default:
		if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
			if server, name, ok := strings.Cut(rest, "__"); ok {
				s = server + " · " + name
			}
		}
	}
	if strings.TrimSpace(s) == "" {
		s = firstStringValue(in)
	}
	return firstLine(s)
}

func baseName(p string) string {
	if p == "" {
		return ""
	}
	return path.Base(p)
}

// firstQuestion is the first question of an AskUserQuestion input.
func firstQuestion(raw []byte) string {
	blocks, ok := contentBlocks(raw)
	if !ok || len(blocks) == 0 {
		return ""
	}
	return blocks[0].obj.str("question")
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

// firstLine is the first line of s after leading white space, capped.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimRight(s[:i], "\r")
	}
	s, _ = capText(s, convmodel.MaxInputString)
	return s
}
