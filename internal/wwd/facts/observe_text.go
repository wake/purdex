package facts

import (
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/redact"
	"github.com/wake/purdex/internal/wwd/wire"
)

// What an observation keeps of a command line, and the short form the undeclared list groups by. A command line can hold a secret
// (a token on the command line is common), and it is stored for 14 days and served to the pool, so it is scrubbed before it is kept.
// The scrubbing is internal/redact's (the gate's holder records and the load watcher use the same): the shape of a command line
// first (flags and variables that name a secret, credential headers, the password in a URL), then every word through the transcript
// scrubber (bearer tokens, sk-, ghp_, xox, AWS key ids, Purdex tokens, long mixed runs).

func dropControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// cleanArgv drops control characters and empty words.
func cleanArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		if a = dropControl(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

var shellCommands = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "fish": true, "ksh": true}

// shellScript finds the script of `sh -c <script>` (any of -c, -lc, -ic, ...): its index in argv, or -1.
func shellScript(argv []string) int {
	if len(argv) < 3 || !shellCommands[path.Base(argv[0])] {
		return -1
	}
	for i := 1; i+1 < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") && !strings.HasPrefix(argv[i], "--") && strings.Contains(argv[i], "c") {
			return i + 1
		}
	}
	return -1
}

// scrubbed is argv with the secrets in it replaced. The script of a `sh -c` is one word to the scrubber, which cannot see the flags
// inside it, so its words are scrubbed as a command line of their own.
func scrubbed(argv []string) []string {
	argv = cleanArgv(argv)
	out := redact.ScrubArgv(argv)
	if i := shellScript(argv); i >= 0 {
		if hardToSplit(argv[i]) {
			out[i] = redact.Mask // the words of the script cannot be told apart reliably: none of it is kept
		} else {
			out[i] = strings.Join(redact.ScrubArgv(splitShellWords(argv[i])), " ")
		}
	}
	return out
}

// hardToSplit says whether a script has syntax whose quoting a plain word split gets wrong: command substitution and parameter
// expansion and process substitution nest their own quotes, $'..' has its own escapes, here-documents carry free text. The whole script is then masked rather than half-parsed.
func hardToSplit(script string) bool {
	for _, p := range []string{"`", "$(", "${", "<<", "<(", ">(", "$'"} {
		if strings.Contains(script, p) {
			return true
		}
	}
	return false
}

// splitShellWords splits on white space outside quotes and keeps the quote characters in the words, so a quoted value with spaces
// ('secret value') stays one word for the scrubber instead of leaving its tail behind. A backslash outside single quotes keeps the
// next character in the word (first\ tail, \" inside a double quote). An unterminated quote runs to the end.
func splitShellWords(s string) []string {
	var words []string
	var cur strings.Builder
	var quote rune
	escaped := false // the rune before was a backslash (outside single quotes): this one is part of the word whatever it is
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			cur.WriteRune(r)
			escaped = true
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case unicode.IsSpace(r):
			if cur.Len() > 0 {
				words = append(words, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		words = append(words, cur.String())
	}
	return words
}

// RedactCommand is argv as one line with secrets replaced (internal/redact), control characters dropped and at most
// wire.MaxObservationCommand characters (a cut one ends in "…"). The scrubbing comes before the cut, so a secret far into a long
// word does not survive in its first characters. It is a best effort for the usual ways a secret reaches a command line, not a
// promise.
func RedactCommand(argv []string) string {
	return cut(strings.Join(scrubbed(argv), " "), wire.MaxObservationCommand)
}

func cut(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

var shellPrelude = map[string]bool{"cd": true, "export": true, "set": true, "source": true, ".": true, "unset": true, "ulimit": true, "umask": true, "true": true, "[": true}

const maxSignatureName = 32

// CommandSignature is the short form of a command the undeclared list groups by: the program and up to two words after it, the way
// a person names a command ("pnpm vitest run", "go test", "swift build"). Flags and what follows them, numbers, paths and
// KEY=value arguments are left out; env prefixes are looked through; for "bash -c <script>" it is the script's first real command.
// It is made from the scrubbed command line, so a secret given as a bare word is not in it. "unknown" when argv names nothing.
func CommandSignature(argv []string) string {
	sig := signature(scrubbed(argv), 0)
	if sig == "" {
		return "unknown"
	}
	return sig
}

// isScrubbed says whether a word is a replacement the scrubber made (or holds one).
func isScrubbed(a string) bool {
	return strings.Contains(a, "<redacted>") || strings.Contains(a, redact.Mask)
}

func signature(clean []string, depth int) string {
	if len(clean) == 0 || depth > 3 {
		return ""
	}
	if path.Base(clean[0]) == "env" {
		i := 1
		for i < len(clean) && (strings.Contains(clean[i], "=") || strings.HasPrefix(clean[i], "-")) {
			i++
		}
		return signature(clean[i:], depth+1)
	}
	name := path.Base(clean[0])
	if name == "." || name == "/" || isScrubbed(name) {
		return ""
	}
	if i := shellScript(clean); i >= 0 {
		return scriptSignature(clean[i], depth+1, name)
	}
	words := []string{trimName(name)}
	for _, a := range clean[1:] {
		if len(words) == 3 {
			break
		}
		if isScrubbed(a) {
			continue // a secret given as a bare word is not a word of the command
		}
		if strings.HasPrefix(a, "-") {
			if strings.Contains(a, "=") {
				continue // a self-contained option before the words (node --max-old-space-size=4096 ...)
			}
			break // a flag: what follows is its value or an argument, not a subcommand
		}
		if strings.Contains(a, "=") || isNumber(a) || strings.Contains(a, "://") {
			continue
		}
		if strings.Contains(a, "/") {
			if strings.HasSuffix(a, "/") || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") || strings.HasPrefix(a, "~") {
				continue // a path argument
			}
			a = strings.TrimSuffix(path.Base(a), path.Ext(a)) // a script or tool path: its name
		}
		if a = trimName(a); a != "" {
			words = append(words, a)
		}
	}
	return strings.Join(words, " ")
}

// trimName is s cut to at most maxSignatureName runes (no ellipsis: a signature is a key).
func trimName(s string) string {
	if utf8.RuneCountInString(s) <= maxSignatureName {
		return s
	}
	return string([]rune(s)[:maxSignatureName])
}

var reSegmentSplit = regexp.MustCompile(`&&|\|\||;|\||\n`)

// scriptSignature is the signature of the first real command of a shell script: the segments that only set the scene (cd, export,
// VAR=1 prefixes) are passed over.
func scriptSignature(script string, depth int, shell string) string {
	for _, seg := range reSegmentSplit.Split(script, -1) {
		f := strings.Fields(seg)
		for len(f) > 0 && strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "-") {
			f = f[1:] // VAR=value prefixes
		}
		if len(f) == 0 || shellPrelude[f[0]] {
			continue
		}
		if sig := signature(f, depth); sig != "" {
			return sig
		}
	}
	return trimName(shell)
}
