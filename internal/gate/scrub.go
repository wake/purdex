package gate

import (
	"regexp"
	"strings"

	"github.com/wake/purdex/internal/redact"
)

// The holder record is readable by every user of the host (0644) and `pdx gate status` prints it, so the command line it
// keeps must carry no secret. Two layers, in this order:
//
//  1. The shape of a command line, which the transcript scrubber (internal/redact) cannot see because it reads text, not
//     words: a flag or variable that names a secret has its value replaced, whether the value is in the same word
//     (`--password=abc`, `TOKEN=abc`) or in the next one (`--token abc`, `-p abc` after a password-ish command), and a header
//     that carries a credential (`-H 'Cookie: …'`) and the password in a URL (`scheme://user:pass@host`).
//  2. Every word then goes through redact.String, the same scrubber the transcripts use: bearer tokens, `sk-…`, `ghp_…`,
//     `xox…`, AWS key ids, Purdex tokens, and long mixed runs of letters and digits.
//
// The cut to 120 bytes comes after both, so a secret far into a long word does not survive in its first bytes.

const redacted = "<redacted>"

// secretNames are the pieces of a flag or variable name that say its value is a secret.
var secretNames = []string{"token", "secret", "password", "passwd", "passphrase", "apikey", "api-key", "api_key", "auth", "bearer",
	"credential", "cookie", "private-key", "private_key", "access-key", "access_key", "session-key", "session_key"}

// shortSecretFlags are the one-letter flags that carry a secret in the tools this host runs: `-p` (mysql, psql, ssh-style
// password prompts given inline, sshpass), whose value is the next word. They apply when the command is one of passwordFlagTools.
var shortSecretFlags = map[string]bool{"-p": true}

// passwordFlagTools are the commands whose `-p` is a password (for others, `-p` is a package, a port or a path: go test -p 4,
// docker -p 80:80, tsc -p tsconfig.json, pnpm -p).
var passwordFlagTools = map[string]bool{"mysql": true, "mysqldump": true, "mysqladmin": true, "psql": true, "sshpass": true, "redis-cli": true}

// headerFlags take a header as the next word; a header that carries a credential is redacted whole.
var headerFlags = map[string]bool{"-H": true, "--header": true, "--headers": true}

var secretHeader = regexp.MustCompile(`(?i)^\s*(authorization|proxy-authorization|cookie|set-cookie|x-api-key|x-auth[a-z-]*|x-[a-z-]*token)\s*:`)

// userinfo in a URL: scheme://user:password@host
var urlPassword = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^/\s:@]*:)[^/\s@]+(@)`)

func namesSecret(name string) bool {
	l := strings.ToLower(strings.TrimLeft(name, "-"))
	for _, k := range secretNames {
		if strings.Contains(l, k) {
			return true
		}
	}
	return false
}

// scrubArgv returns the words with the secrets in them replaced; the input is not modified.
func scrubArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	tool := ""
	if len(argv) > 0 {
		tool = argv[0]
		if i := strings.LastIndexByte(tool, '/'); i >= 0 {
			tool = tool[i+1:]
		}
	}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		// glued forms: `-Hname: value`, `--header=name: value`, `-psecret` (for the tools whose -p is a password)
		if v, ok := strings.CutPrefix(a, "--header="); ok {
			out = append(out, "--header="+scrubHeader(v))
			continue
		}
		if v, ok := strings.CutPrefix(a, "-H"); ok && v != "" && !strings.HasPrefix(a, "--") {
			out = append(out, "-H"+scrubHeader(v))
			continue
		}
		if v, ok := strings.CutPrefix(a, "-p"); ok && v != "" && !strings.HasPrefix(a, "--") && passwordFlagTools[tool] {
			out = append(out, "-p"+redacted)
			continue
		}
		switch {
		case headerFlags[a] && i+1 < len(argv):
			out = append(out, a)
			i++
			out = append(out, scrubHeader(argv[i]))
			continue
		case strings.HasPrefix(a, "-") && !strings.Contains(a, "=") && i+1 < len(argv) &&
			(namesSecret(a) || (shortSecretFlags[a] && passwordFlagTools[tool])):
			out = append(out, a, redacted) // `--token abc`, `mysql -p abc`
			i++
			continue
		}
		out = append(out, scrubWord(a))
	}
	return out
}

// scrubHeader redacts a header that carries a credential whole (whatever its value looks like: a Basic credential is
// short base64 that no pattern would catch), and scrubs any other header like a word.
func scrubHeader(h string) string {
	if secretHeader.MatchString(h) {
		return redacted
	}
	return scrubWord(h)
}

// scrubWord redacts the value of `NAME=value` when NAME names a secret, an Authorization-like header and the password in a
// URL, then hands the word to the transcript scrubber.
func scrubWord(a string) string {
	if name, _, ok := strings.Cut(a, "="); ok && namesSecret(name) {
		return name + "=" + redacted
	}
	if secretHeader.MatchString(a) {
		return redacted
	}
	a = urlPassword.ReplaceAllString(a, "${1}"+redacted+"${2}")
	return redact.String(a)
}
