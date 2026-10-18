package facts

import (
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wake/purdex/internal/wwd/wire"
)

// What an observation keeps of a command line, and the short form the undeclared list groups by. A command line can hold a secret
// (a token on the command line is common), and it is stored and served to the pool, so it is redacted before it is kept.

var (
	// a flag whose next argument (or =value) is a secret
	reSecretFlag = regexp.MustCompile(`(?i)^-{1,2}(token|secret|password|passwd|pwd|pass|api[-_]?key|apikey|key|auth|authorization|bearer|cookie|credentials?|private[-_]?key|access[-_]?key)s?$`)
	// NAME=value with a secret-looking name
	reSecretName = regexp.MustCompile(`(?i)(token|secret|password|passwd|api_?key|auth|credential|bearer|cookie|private_?key|access_?key)`)
	reAuthScheme = regexp.MustCompile(`(?i)\b(bearer|basic)\s+\S+`)
	reURLUser    = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)
	reTokenShape = regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|xox[abprs]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}`)
)

func dropControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
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

// redactToken hides what looks like a secret inside one argument.
func redactToken(s string) string {
	s = reURLUser.ReplaceAllString(s, "${1}***@")
	s = reAuthScheme.ReplaceAllString(s, "$1 ***")
	return reTokenShape.ReplaceAllString(s, "***")
}

// RedactCommand is argv as one line with secrets hidden, control characters dropped and at most wire.MaxObservationCommand
// characters (a cut one ends in "…"). Hidden: the value of a flag like --token/--password/--api-key (as the next argument or after
// "="), NAME=value where NAME looks like a secret, "Bearer ..." and "Basic ..." credentials, the user:password of a URL, and a few
// well-known token shapes. It is a best effort for the usual ways a secret reaches a command line, not a promise.
func RedactCommand(argv []string) string {
	out := make([]string, 0, len(argv))
	hideNext, keepNumber := false, false
	for _, raw := range argv {
		a := dropControl(raw)
		if a == "" {
			continue
		}
		if hideNext {
			numeric := keepNumber && isNumber(a)
			hideNext, keepNumber = false, false
			if !strings.HasPrefix(a, "-") && !numeric {
				out = append(out, "***")
				continue
			}
		}
		switch {
		case reSecretFlag.MatchString(a):
			out = append(out, a)
			hideNext = true
		case a == "-p": // mysql -p <password>, but also ssh -p <port>: a number is no secret, so it is kept
			out = append(out, a)
			hideNext, keepNumber = true, true
		case strings.HasPrefix(a, "-") && strings.Contains(a, "="):
			name, _, _ := strings.Cut(a, "=")
			if reSecretFlag.MatchString(name) {
				out = append(out, name+"=***")
			} else {
				out = append(out, redactToken(a))
			}
		case !strings.HasPrefix(a, "-") && strings.Contains(a, "="):
			name, _, _ := strings.Cut(a, "=")
			if reSecretName.MatchString(name) && !strings.ContainsAny(name, "/ ") {
				out = append(out, name+"=***")
			} else {
				out = append(out, redactToken(a))
			}
		default:
			out = append(out, redactToken(a))
		}
	}
	return cut(strings.Join(out, " "), wire.MaxObservationCommand)
}

func cut(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

var shellCommands = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "fish": true, "ksh": true}
var shellPrelude = map[string]bool{"cd": true, "export": true, "set": true, "source": true, ".": true, "unset": true, "ulimit": true, "umask": true, "true": true, "[": true}

const maxSignatureName = 32

// CommandSignature is the short form of a command the undeclared list groups by: the program and up to two words after it, the way
// a person names a command ("pnpm vitest run", "go test", "swift build"). Flags and what follows them, numbers, paths and
// KEY=value arguments are left out; env prefixes are looked through; for "bash -c <script>" it is the script's first real command.
// "unknown" when argv names nothing.
func CommandSignature(argv []string) string {
	sig := signature(argv, 0)
	if sig == "" {
		return "unknown"
	}
	return sig
}

func signature(argv []string, depth int) string {
	clean := make([]string, 0, len(argv))
	for _, a := range argv {
		if a = dropControl(a); a != "" {
			clean = append(clean, a)
		}
	}
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
	if name == "." || name == "/" {
		return ""
	}
	if shellCommands[name] {
		for i := 1; i+1 < len(clean); i++ {
			if strings.HasPrefix(clean[i], "-") && strings.Contains(clean[i], "c") && !strings.HasPrefix(clean[i], "--") {
				return scriptSignature(clean[i+1], depth+1, name)
			}
		}
	}
	words := []string{trimName(name)}
	for _, a := range clean[1:] {
		if len(words) == 3 {
			break
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
		return signature(f, depth)
	}
	return trimName(shell)
}
