package facts

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRedactCommand(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"plain":                                {[]string{"pnpm", "vitest", "run"}, "pnpm vitest run"},
		"a flag and its value":                 {[]string{"curl", "--token", "abc123", "https://x"}, "curl --token *** https://x"},
		"flag=value":                           {[]string{"tool", "--api-key=sk-live-1234567890abcdef", "go"}, "tool --api-key=*** go"},
		"password flag":                        {[]string{"mysql", "-p", "hunter2", "--password", "hunter2"}, "mysql -p *** --password ***"},
		"env assignment":                       {[]string{"env", "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789", "gh", "pr", "list"}, "env GITHUB_TOKEN=*** gh pr list"},
		"bearer header":                        {[]string{"curl", "-H", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdefghijk.lmnop", "https://x"}, "curl -H Authorization: Bearer *** https://x"},
		"basic auth":                           {[]string{"curl", "-H", "Authorization: Basic dXNlcjpwYXNz"}, "curl -H Authorization: Basic ***"},
		"url userinfo":                         {[]string{"git", "clone", "https://wake:s3cret@github.com/wake/purdex.git"}, "git clone https://***@github.com/wake/purdex.git"},
		"a known token shape":                  {[]string{"echo", "xoxb-1234567890-abcdefghij", "AKIAABCDEFGHIJKLMNOP"}, "echo *** ***"},
		"cookie":                               {[]string{"curl", "--cookie", "sid=abc", "https://x"}, "curl --cookie *** https://x"},
		"a benign key=value":                   {[]string{"make", "JOBS=4", "KEYBOARD=us"}, "make JOBS=4 KEYBOARD=us"},
		"a trailing secret flag with no value": {[]string{"tool", "--token"}, "tool --token"},
		"a port after -p is kept":              {[]string{"ssh", "-p", "22", "host"}, "ssh -p 22 host"},
		"a secret flag followed by a flag":     {[]string{"tool", "--token", "--verbose", "x"}, "tool --token --verbose x"},
		"the secret is not left anywhere":      {[]string{"deploy", "--password", "hunter2", "--token=abc"}, "deploy --password *** --token=***"},
	} {
		if got := RedactCommand(c.argv); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", name, got, c.want)
		}
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
	} {
		if got := CommandSignature(c.argv); got != c.want {
			t.Errorf("%s: CommandSignature(%q) = %q, want %q", name, c.argv, got, c.want)
		}
	}
}
