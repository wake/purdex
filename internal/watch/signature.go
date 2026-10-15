package watch

import (
	"path/filepath"
	"strings"

	"github.com/wake/purdex/internal/redact"
)

// runners are the package managers whose first word picks what runs (`pnpm exec vitest run`, `npx vitest`).
var runners = map[string]bool{"pnpm": true, "npm": true, "yarn": true, "npx": true, "pnpx": true, "bunx": true, "bun": true}

// runnerLaunch are the words after which a runner's next word is the tool or script it starts.
var runnerLaunch = map[string]bool{"exec": true, "x": true, "run": true, "run-script": true, "dlx": true}

// Signature is a command's kind with the paths and values taken out: the tool's name and the subcommand or script word
// (`go test`, `composer test:integration`, `pnpm exec vitest run`, `phpunit`), so a command seen again with other arguments is
// the same signature. Flags, values and path-like words are dropped; a runner's `exec`/`run` keeps the tool or script it starts and
// that tool's subcommand.
func Signature(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	if argv[0] == "" {
		return ""
	}
	tool := strings.TrimLeft(filepath.Base(argv[0]), "-")
	if tool == "" || tool == "." || tool == "/" || !plainWord(tool) {
		return ""
	}
	words := []string{tool}
	limit := 2 // the tool and its subcommand
	rest := wordsOf(argv[1:])
	if runners[tool] && len(rest) > 0 && runnerLaunch[rest[0]] {
		limit = 4 // pnpm exec vitest run
	}
	for _, w := range rest {
		if len(words) >= limit {
			break
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// wordsOf is the leading words of the arguments that name something: the scan stops at the first long flag (`--testsuite
// integration`: whether its next word is a value cannot be known, so nothing after a long flag is a word) and skips short
// flags (`-r`, `-race`). Paths, bare numbers and anything with a dot (a file: `phpunit.xml`) are values, not words.
func wordsOf(args []string) []string {
	var out []string
	for _, a := range args {
		switch {
		case a == "--" || strings.HasPrefix(a, "--"):
			return out
		case a == "" || strings.HasPrefix(a, "-"):
		case strings.ContainsAny(a, `/\.`) || strings.HasPrefix(a, "~"):
		case isNumber(a):
		case !plainWord(a):
		default:
			out = append(out, a)
		}
	}
	return out
}

// maxWordBytes is the longest word a signature keeps: a longer one is a value, not the name of a subcommand or a script.
const maxWordBytes = 40

// plainWord says whether a word may be part of a signature, which is kept (in memory, in state.json): no control characters
// (a newline), not long, and nothing the transcript scrubber would mask (a token that is not a flag's value is still a token).
func plainWord(w string) bool {
	if len(w) > maxWordBytes {
		return false
	}
	for _, r := range w {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return redact.String(w) == w
}

func isNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
