package gate

import (
	"path/filepath"
	"strings"
)

// Module 1, intercept: what the gate takes in. The tools a shim stands in for, an extra classification rule or two
// (which come first), and whether the Claude Code mod prefixes direct `node_modules/.bin` calls (the mod reads that
// one; it is part of the schema here so one file configures the lot).
type InterceptConfig struct {
	Tools      []string
	ModRewrite bool
	Rules      []Rule
}

// Rule gives a command line that starts with Match the class Class (e.g. "pnpm run e2e" → heavy).
type Rule struct {
	Match string
	Class Class
}

// claude is here for `claude plugin test` only (the classifier answers none for every other claude command); no shim is made for it,
// the mod puts the gate in front of the call.
var defaultTools = []string{"go", "pnpm", "npx", "vitest", "tsc", "composer", "phpunit", "pest", "claude", "xcodebuild", "swift", "swift-build", "swift-test"}

func parseIntercept(r raw) InterceptConfig {
	c := InterceptConfig{
		Tools:      r.Strings("tools", defaultTools),
		ModRewrite: r.Bool("mod_rewrite", true),
	}
	var entries []map[string]any // [[gate.intercept.rule]] decodes as a list of tables
	switch v := r.m["rule"].(type) {
	case []map[string]any:
		entries = v
	case []any:
		for _, e := range v {
			if m, ok := e.(map[string]any); ok {
				entries = append(entries, m)
			} else {
				r.warn("rule", "want a table with match and class, got %v", e)
			}
		}
	case nil: // no rule
	default:
		r.warn("rule", "want a list of tables ([[gate.intercept.rule]]), got %v", v)
	}
	for _, m := range entries {
		match, cls := stringOf(m["match"]), Class(stringOf(m["class"]))
		if match == "" || (cls != ClassHeavy && cls != ClassLight && cls != ClassNone) {
			r.warn("rule", "want match and class (heavy, light or none), got %v", m)
			continue
		}
		c.Rules = append(c.Rules, Rule{Match: match, Class: cls})
	}
	return c
}

func stringOf(v any) string { s, _ := v.(string); return s }

// ClassifyReason is Classify with the reason the table gives for a command it lets through (a rule that matches gives none).
func (c InterceptConfig) ClassifyReason(argv []string, e Env) (Class, Reason) {
	if len(argv) == 0 {
		return ClassNone, ""
	}
	line := strings.Join(append([]string{filepath.Base(argv[0])}, argv[1:]...), " ")
	for _, r := range c.Rules {
		if line == r.Match || strings.HasPrefix(line, r.Match+" ") {
			return r.Class, ""
		}
	}
	name := filepath.Base(argv[0])
	for _, t := range c.Tools {
		if t == name {
			return ClassifyReason(argv, e)
		}
	}
	return ClassNone, ""
}

// Classify is the class of a command under this module: a rule that matches decides; else a tool that is not listed is
// none; else the built-in table (Classify).
func (c InterceptConfig) Classify(argv []string, e Env) Class {
	if len(argv) == 0 {
		return ClassNone
	}
	line := strings.Join(append([]string{filepath.Base(argv[0])}, argv[1:]...), " ")
	for _, r := range c.Rules {
		if line == r.Match || strings.HasPrefix(line, r.Match+" ") {
			return r.Class
		}
	}
	name := filepath.Base(argv[0])
	for _, t := range c.Tools {
		if t == name {
			return Classify(argv, e)
		}
	}
	return ClassNone
}
