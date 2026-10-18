package facts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The command line of an observation is stored for 14 days and served to the pool: no secret may reach the line or the signature.
// The scrubbing itself is internal/redact's (the gate's holder records and the load watcher use it); these are the forms a command
// line carries a secret in, with the secret each one holds.
func TestRedactCommandAndSignatureCarryNoSecret(t *testing.T) {
	for name, c := range map[string]struct {
		argv   []string
		secret string
	}{
		"a flag and its next word":    {[]string{"curl", "--token", "abc123secret", "https://x"}, "abc123secret"},
		"flag=value":                  {[]string{"tool", "--api-key=sk-live-1234567890abcdef", "go"}, "sk-live-1234567890abcdef"},
		"a hyphenated secret flag":    {[]string{"tool", "--access-token", "tok-value-9f8e7d", "run"}, "tok-value-9f8e7d"},
		"mysql -p with a glued value": {[]string{"mysql", "-phunter2", "db"}, "hunter2"},
		"mysql -p and the next word":  {[]string{"mysql", "-p", "hunter2", "db"}, "hunter2"},
		"a glued cookie header":       {[]string{"curl", "-HCookie:sid=cookievalue123", "https://x"}, "cookievalue123"},
		"an authorization header":     {[]string{"curl", "-H", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdefghijk.lmnop", "https://x"}, "abcdefghijk"},
		"a basic credential":          {[]string{"curl", "-H", "Authorization: Basic dXNlcjpwYXNz"}, "dXNlcjpwYXNz"},
		"a url password":              {[]string{"git", "clone", "https://wake:s3cretpw@github.com/wake/purdex.git"}, "s3cretpw"},
		"an env assignment":           {[]string{"env", "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789", "gh", "pr", "list"}, "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		"a positional token":          {[]string{"echo", "xoxb-1234567890-abcdefghij"}, "xoxb-1234567890-abcdefghij"},
		"a positional sk- key":        {[]string{"tool", "sk-live-abcdefghijklmnop", "go"}, "sk-live-abcdefghijklmnop"},
		"a purdex token":              {[]string{"pdx", "msg", "pdxp_AbCdEf0123456789"}, "pdxp_AbCdEf0123456789"},
		"a long mixed run":            {[]string{"deploy", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8"}, "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8"},
		"a shell script":              {[]string{"bash", "-c", "curl --token supersecretvalue1 https://x"}, "supersecretvalue1"},
	} {
		if got := RedactCommand(c.argv); strings.Contains(got, c.secret) {
			t.Errorf("%s: the command keeps the secret: %q", name, got)
		}
		if got := CommandSignature(c.argv); strings.Contains(got, c.secret) {
			t.Errorf("%s: the signature keeps the secret: %q", name, got)
		}
	}
}

// What is not a secret stays, and the line reads as the command did.
func TestRedactCommandKeepsWhatIsNotASecret(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"plain":                  {[]string{"pnpm", "vitest", "run", "--maxWorkers=3"}, "pnpm vitest run --maxWorkers=3"},
		"a benign key=value":     {[]string{"make", "JOBS=4", "KEYBOARD=us"}, "make JOBS=4 KEYBOARD=us"},
		"go test -p is a count":  {[]string{"go", "test", "-p", "4", "./..."}, "go test -p 4 ./..."},
		"a secret flag, no word": {[]string{"tool", "--token"}, "tool --token"},
	} {
		if got := RedactCommand(c.argv); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", name, got, c.want)
		}
	}
	if got := RedactCommand([]string{"curl", "--token", "abc123secret", "https://x"}); got != "curl --token <redacted> https://x" {
		t.Errorf("a flag's value is replaced where it stood: %q", got)
	}
}

func TestRedactCommandIsBoundedAndKeepsValidUTF8(t *testing.T) {
	long := RedactCommand([]string{"echo", strings.Repeat("é", 500)})
	if n := utf8.RuneCountInString(long); n > 200 {
		t.Fatalf("%d runes", n)
	}
	if !utf8.ValidString(long) || !strings.HasSuffix(long, "…") {
		t.Fatalf("cut mid-rune or not marked: %q", long)
	}
	if got := RedactCommand(nil); got != "" {
		t.Fatalf("no argv: %q", got)
	}
	// control characters never reach a record
	if got := RedactCommand([]string{"echo", "a\nb\x00c"}); strings.ContainsAny(got, "\n\x00") {
		t.Fatalf("%q", got)
	}
	// the secret far into a long word does not survive in its first characters
	line := RedactCommand([]string{"tool", "--password=" + strings.Repeat("p", 300) + "TAILSECRET9"})
	if strings.Contains(line, "TAILSECRET9") || strings.Contains(line, strings.Repeat("p", 20)) {
		t.Fatalf("a long secret leaked: %q", line)
	}
}

func TestCommandSignature(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"a runner and its subcommand":    {[]string{"/opt/homebrew/bin/pnpm", "vitest", "run", "--maxWorkers=3"}, "pnpm vitest run"},
		"go test with a package":         {[]string{"/usr/local/go/bin/go", "test", "-race", "./internal/wwd/facts/"}, "go test"},
		"node running a script":          {[]string{"node", "/x/node_modules/.bin/vitest", "run"}, "node vitest run"},
		"a numeric argument is dropped":  {[]string{"sleep", "600"}, "sleep"},
		"env prefix and wrapper":         {[]string{"env", "FOO=1", "BAR=2", "swift", "build", "-c", "release"}, "swift build"},
		"a shell -c takes the script":    {[]string{"bash", "-lc", "cd /x && pnpm test --run"}, "pnpm test"},
		"zsh -c with exports first":      {[]string{"zsh", "-c", "export A=1; xcodebuild test -scheme X"}, "xcodebuild test"},
		"a pipeline keeps its first":     {[]string{"sh", "-c", "go vet ./... | tee out.txt"}, "go vet"},
		"nothing":                        {nil, "unknown"},
		"an empty argv0":                 {[]string{""}, "unknown"},
		"a long name is cut":             {[]string{strings.Repeat("x", 80), "build"}, strings.Repeat("x", 32) + " build"},
		"a control character is dropped": {[]string{"to\nol", "go"}, "tool go"},
		"a secret word is not a word":    {[]string{"tool", "sk-live-abcdefghijklmnop", "go"}, "tool go"},
	} {
		if got := CommandSignature(c.argv); got != c.want {
			t.Errorf("%s: CommandSignature(%q) = %q, want %q", name, c.argv, got, c.want)
		}
	}
}
