package gate

import (
	"path/filepath"
	"strings"
	"time"
)

// Conflicts: two kinds of command that do not run together (#3058, spec §5.3). This file is the vocabulary: what a rule is and how a
// command line is read for it. Where a rule is stored, how the queue walk applies it and the CLI are built on this.

// Conflict is a rule of one class: a command that starts with pattern A does not run beside one that starts with pattern B (A and B
// may be the same pattern). Patterns are the start of the command's words (commandWords).
type Conflict struct {
	Class  Class     `json:"class"`
	A      string    `json:"a"`
	B      string    `json:"b"`
	Reason string    `json:"reason"`
	By     string    `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// matchesPrefix says whether a command's words begin with the pattern's words: `tsc -b` matches `tsc -b --force`, not `tsc --force
// -b`, and `vitest related` matches `vitest related --run a.ts`. A pattern longer than the command, or empty, matches nothing. The
// value of an option is never taken for a word of the pattern because only a leading run of words is compared.
func matchesPrefix(pattern, words []string) bool {
	if len(pattern) == 0 || len(words) < len(pattern) {
		return false
	}
	for i, w := range pattern {
		if words[i] != w {
			return false
		}
	}
	return true
}

// patternWords reads a pattern as typed: its words, split on blanks.
func patternWords(p string) []string { return strings.Fields(p) }

// collides says whether the rule keeps x and y apart: one of them starts with A and the other with B, in either order, and the rule
// belongs to class. (The commands themselves are of the class the walk is made for; a rule of the other class does nothing.)
func (c Conflict) collides(class Class, x, y []string) bool {
	if c.Class != class {
		return false
	}
	a, b := patternWords(c.A), patternWords(c.B)
	return (matchesPrefix(a, x) && matchesPrefix(b, y)) || (matchesPrefix(b, x) && matchesPrefix(a, y))
}

// commandWords reads a command line as a rule sees it: the tool and what follows, without the runner that started it. The words the
// classifier reads are skipped the way it skips them:
//
//	npx|pnpx|bunx [options] <tool …>             bun x <tool …>
//	pnpm|npm|yarn|bun [options] exec|x [--] <tool …>
//	pnpm [options] <tool …>                      when the word is a tool Classify knows
//
// An option before the subcommand is skipped with its value when it takes one (runnerValueOpts, `--opt value` and `--opt=value`).
// Every tool word (argv[0], and the one after a runner) is reduced to its base name, so /repo/node_modules/.bin/tsc is tsc. A package
// script is not opened: `pnpm run typecheck` stays `pnpm run typecheck` (the options before `run` are dropped). `yarn dlx` is not exec.
func commandWords(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	runner := filepath.Base(argv[0])
	args := argv[1:]
	switch runner {
	case "npx", "pnpx", "bunx":
		i := skipRunnerOptions(args)
		return withBaseTool(args[i:])
	case "bun":
		if len(args) > 0 && args[0] == "x" {
			return withBaseTool(args[1:][skipRunnerOptions(args[1:]):])
		}
		fallthrough
	case "pnpm", "npm", "yarn":
		i := skipRunnerOptions(args)
		if i >= len(args) {
			return []string{runner}
		}
		switch args[i] {
		case "exec", "x":
			rest := args[i+1:] // exec takes options of its own (`npm exec --package typescript -- tsc -b`), and a `--` before the tool
			return withBaseTool(rest[skipRunnerOptions(rest):])
		}
		if Classify(args[i:], Env{}) != ClassNone {
			return withBaseTool(args[i:]) // `pnpm tsc -b`: the word is a tool in node_modules/.bin
		}
		return append([]string{runner}, args[i:]...)
	}
	return withBaseTool(argv)
}

// skipRunnerOptions is the index of the first word after the options a package runner takes before its subcommand; an option
// that takes a value (runnerValueOpts, in the `--opt value` form) takes the next word with it. A bare `--` ends the options.
func skipRunnerOptions(args []string) int {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			return i + 1
		case !strings.HasPrefix(a, "-"):
			return i
		case runnerValueOpts[a]:
			i += 2
		default:
			i++ // a switch, or `--opt=value`
		}
	}
	return len(args)
}

// withBaseTool is words with the first one (the tool) reduced to its base name.
func withBaseTool(words []string) []string {
	if len(words) == 0 {
		return nil
	}
	if first := strings.Fields(words[0]); len(first) > 1 {
		// one word holding a whole command line (`npm exec --call "tsc -b"`): it is read as the words it holds
		words = append(first, words[1:]...)
	}
	return append([]string{filepath.Base(words[0])}, words[1:]...)
}
