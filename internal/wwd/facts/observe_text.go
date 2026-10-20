package facts

import (
	"os"
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
	sig := signature(scrubbed(scriptNewlines(argv)), 0)
	if sig == "" {
		return "unknown"
	}
	return sig
}

// scriptNewlines is argv with the newlines of a `sh -c` script turned into ";": cleanArgv drops control characters, which would
// join the last word of a line to the first of the next ("export A=1\npnpm test" -> "export A=1pnpm test").
func scriptNewlines(argv []string) []string {
	i := shellScript(argv)
	if i < 0 || !strings.ContainsAny(argv[i], "\r\n") {
		return argv
	}
	out := append([]string(nil), argv...)
	out[i] = strings.NewReplacer("\r\n", ";", "\n", ";", "\r", ";").Replace(argv[i])
	return out
}

// isScrubbed says whether a word is a replacement the scrubber made (or holds one).
func isScrubbed(a string) bool {
	return strings.Contains(a, "<redacted>") || strings.Contains(a, redact.Mask)
}

// globalValueFlags are, per program, the flags that come before the subcommand and take a value in the next word ("docker --context
// prod build"): they are skipped with their value so the subcommand is still found. A flag not listed ends the scan, as before.
var globalValueFlags = map[string]map[string]bool{
	"docker":  {"--context": true, "-c": true, "--host": true, "-H": true, "--config": true, "--log-level": true, "-l": true},
	"kubectl": {"--context": true, "-n": true, "--namespace": true, "--kubeconfig": true, "--cluster": true, "--user": true, "-s": true, "--server": true},
	"helm":    {"--kube-context": true, "-n": true, "--namespace": true, "--kubeconfig": true},
	"git":     {"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true},
	"pnpm":    {"--filter": true, "-F": true, "-C": true, "--dir": true, "--prefix": true},
	"npm":     {"--prefix": true, "-C": true, "--workspace": true, "-w": true},
	"yarn":    {"--cwd": true, "--cache-folder": true},
}

var (
	reUUIDSuffix    = regexp.MustCompile(`[-_.][0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reDateSuffix    = regexp.MustCompile(`[-_.][0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	reCounterSuffix = regexp.MustCompile(`[-_.][0-9a-fA-F]+$`)
)

// tempDirs are where a file is throwaway by where it lives. Only a name under one of them is normalized (tempPath): anywhere else
// a number or a hash in a name is part of what the job is (a release, a version, a port).
var tempDirs = []string{"/tmp/", "/private/tmp/", "/var/tmp/", "/var/folders/", "/private/var/folders/"}

func tempPath(p string) bool {
	p = path.Clean(p) // where it really is: "/tmp/../opt/x" is in /opt, "/opt/../tmp/x" in /tmp
	for _, d := range tempDirs {
		if strings.HasPrefix(p, d) {
			return true
		}
	}
	if td := os.TempDir(); strings.HasPrefix(td, "/") && strings.HasPrefix(p, strings.TrimSuffix(path.Clean(td), "/")+"/") {
		return true // this process's own temp directory ($TMPDIR), cleaned the same way
	}
	return false
}

// stripCounter takes a whole uuid, an ISO date, a run counter or a hash off the end of a throwaway file name stem ("run-123" ->
// "run", "build-a1b2c3d4e5" -> "build", "backup-2026-10-18" -> "backup"), so the same job started from a temp script is one
// signature. A counter is a separator and three or more digits; a hash is a separator and eight or more hex digits with at least one
// digit: "es2020", "deadbeef", "build-prod" and "v8-12" are names. Call it for a name under a temp directory only.
func stripCounter(stem string) string {
	for _, re := range []*regexp.Regexp{reUUIDSuffix, reDateSuffix} {
		if loc := re.FindStringIndex(stem); loc != nil && loc[0] > 0 {
			return stem[:loc[0]]
		}
	}
	loc := reCounterSuffix.FindStringIndex(stem)
	if loc == nil || loc[0] == 0 {
		return stem
	}
	suffix := stem[loc[0]+1:]
	if isNumber(suffix) && len(suffix) >= 3 || len(suffix) >= 8 && strings.ContainsAny(suffix, "0123456789") {
		return stem[:loc[0]] // the regexp already holds the suffix to hex digits
	}
	return stem
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
	if name == "pdx" && len(clean) > 2 && clean[1] == "gate" {
		// the gate runs what follows "--" (the shim puts it in front of every heavy command): that is the command
		for i := 2; i < len(clean)-1; i++ {
			if clean[i] == "--" {
				return signature(clean[i+1:], depth+1)
			}
		}
	}
	if i := shellScript(clean); i >= 0 {
		return scriptSignature(clean[i], depth+1, name)
	}
	words := []string{trimName(name)}
	skip := globalValueFlags[name]
	for i := 1; i < len(clean); i++ {
		a := clean[i]
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
			if skip[a] && i+1 < len(clean) {
				i++ // a global flag of this tool and its value: the subcommand comes after
				continue
			}
			break // a flag: what follows is its value or an argument, not a subcommand
		}
		if strings.Contains(a, "=") || isNumber(a) || strings.Contains(a, "://") || a == "." || a == ".." {
			continue
		}
		if strings.Contains(a, "/") {
			if strings.HasSuffix(a, "/") || strings.HasPrefix(a, "./") || strings.HasPrefix(a, "../") || strings.HasPrefix(a, "~") {
				continue // a path argument
			}
			temp := tempPath(a)
			a = strings.TrimSuffix(path.Base(a), path.Ext(a)) // a script or tool path: its name
			if temp {
				a = stripCounter(a) // a throwaway script: without its counter or random suffix
			}
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

// splitSegments splits a script into its commands at &&, ||, ;, | and newlines outside quotes (a quoted "a && b" is data, which is
// what lets the argument of eval stay in one piece). A backslash outside single quotes keeps the next character; an unterminated
// quote runs to the end.
func splitSegments(s string) []string {
	var segs []string
	var cur strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		segs = append(segs, cur.String())
		cur.Reset()
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
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
		case r == ';' || r == '\n' || r == '|' || r == '&' && i+1 < len(rs) && rs[i+1] == '&':
			if (r == '|' || r == '&') && i+1 < len(rs) && rs[i+1] == r {
				i++
			}
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return segs
}

// unquoteWord is a shell word with its quoting taken off: 'single' is literal, "double" honours \" \\ \$ \` , a backslash outside
// quotes keeps the next character, and the pieces of "a"'b'c are one word ('\” and '"'"' are the usual ways to put a quote in one).
func unquoteWord(w string) string {
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range w {
		switch {
		case escaped:
			if quote == '"' && !strings.ContainsRune("\"\\$`", r) {
				b.WriteRune('\\')
			}
			b.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// scriptScene are the words that only set the scene for the command that follows (Claude Code's wrapper adds ":", setopt and a
// guard of unalias/unset in braces); "}" closes that guard.
var scriptScene = map[string]bool{":": true, "setopt": true, "unsetopt": true, "unalias": true, "}": true}

// redirectWord says whether a word starts a redirection ("< /dev/null", ">| f", "2>&1"): what follows it is not an argument.
func redirectWord(w string) bool {
	w = strings.TrimLeft(w, "0123456789")
	return strings.HasPrefix(w, "<") || strings.HasPrefix(w, ">")
}

// scriptSignature is the signature of the first real command of a shell script: the segments that only set the scene (cd, export,
// VAR=1 prefixes, setopt, a braced guard) are passed over, and `eval '<script>'` (what Claude Code wraps every command in) is read
// as the script it holds. The shell's own name when nothing real is found.
func scriptSignature(script string, depth int, shell string) string {
	if sig := scriptFirst(script, depth); sig != "" {
		return sig
	}
	return trimName(shell)
}

func scriptFirst(script string, depth int) string {
	segs := splitSegments(script)
	wrapped := false // only a script that ends in eval is Claude Code's wrapper: elsewhere setopt and unalias are the command
	for _, seg := range segs {
		if w := splitShellWords(seg); len(w) > 0 && unquoteWord(w[0]) == "eval" {
			wrapped = true
		}
	}
	for _, seg := range segs {
		f := splitShellWords(seg)
		for i := range f {
			f[i] = unquoteWord(f[i])
		}
		if len(f) == 0 || f[0] != "eval" { // the argument of eval is a script: its own words are checked when it is read
			for i := 1; i < len(f); i++ {
				if strings.ContainsAny(f[i], " \t") {
					f[i] = "-" // a quoted phrase is an argument, not a subcommand: it ends the words like a flag
				}
			}
			f = redact.ScrubArgv(f) // unquoting can join what the scrubber saw in pieces (sk-'abc...')
		}
		for len(f) > 0 && (f[0] == "{" || f[0] == "builtin" || f[0] == `\builtin` || strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "-")) {
			f = f[1:] // a brace group, \builtin, VAR=value prefixes
		}
		if len(f) == 0 || shellPrelude[f[0]] || wrapped && scriptScene[f[0]] {
			continue
		}
		if f[0] == "eval" {
			args := f[1:]
			for i, a := range args {
				if redirectWord(a) {
					args = args[:i]
					break
				}
			}
			if depth <= 3 {
				return scriptFirst(strings.Join(args, " "), depth+1)
			}
			return "" // eval is the command the wrapper exists for: what follows it (pwd -P) is not
		}
		if sig := signature(f, depth); sig != "" {
			return sig
		}
	}
	return ""
}
