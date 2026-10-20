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
		"a flag and its next word":            {[]string{"curl", "--token", "abc123secret", "https://x"}, "abc123secret"},
		"flag=value":                          {[]string{"tool", "--api-key=sk-live-1234567890abcdef", "go"}, "sk-live-1234567890abcdef"},
		"a hyphenated secret flag":            {[]string{"tool", "--access-token", "tok-value-9f8e7d", "run"}, "tok-value-9f8e7d"},
		"mysql -p with a glued value":         {[]string{"mysql", "-phunter2", "db"}, "hunter2"},
		"mysql -p and the next word":          {[]string{"mysql", "-p", "hunter2", "db"}, "hunter2"},
		"a glued cookie header":               {[]string{"curl", "-HCookie:sid=cookievalue123", "https://x"}, "cookievalue123"},
		"an authorization header":             {[]string{"curl", "-H", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdefghijk.lmnop", "https://x"}, "abcdefghijk"},
		"a basic credential":                  {[]string{"curl", "-H", "Authorization: Basic dXNlcjpwYXNz"}, "dXNlcjpwYXNz"},
		"a url password":                      {[]string{"git", "clone", "https://wake:s3cretpw@github.com/wake/purdex.git"}, "s3cretpw"},
		"an env assignment":                   {[]string{"env", "GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789", "gh", "pr", "list"}, "ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
		"a positional token":                  {[]string{"echo", "xoxb-1234567890-abcdefghij"}, "xoxb-1234567890-abcdefghij"},
		"a positional sk- key":                {[]string{"tool", "sk-live-abcdefghijklmnop", "go"}, "sk-live-abcdefghijklmnop"},
		"a purdex token":                      {[]string{"pdx", "msg", "pdxp_AbCdEf0123456789"}, "pdxp_AbCdEf0123456789"},
		"a long mixed run":                    {[]string{"deploy", "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8"}, "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8"},
		"a shell script":                      {[]string{"bash", "-c", "curl --token supersecretvalue1 https://x"}, "supersecretvalue1"},
		"a quoted script value with a space":  {[]string{"bash", "-c", "curl --token 'first tailfragment' https://x"}, "tailfragment"},
		"a double-quoted flag=value, spaced":  {[]string{"sh", "-lc", `tool --password="one tailfragment" go`}, "tailfragment"},
		"nested quotes in a substitution":     {[]string{"sh", "-c", `tool --password="$(printf "first tailfragment")" go`}, "tailfragment"},
		"a backtick substitution":             {[]string{"sh", "-c", "tool --password=\"`printf 'first tailfragment'`\" go"}, "tailfragment"},
		"a process substitution":              {[]string{"sh", "-c", "tool --token <(printf 'first tailfragment') go"}, "tailfragment"},
		"an output process substitution":      {[]string{"sh", "-c", "tool --token >(printf 'first tailfragment') go"}, "tailfragment"},
		"an ANSI-C quoted value":              {[]string{"sh", "-c", `tool --token $'first\' tailfragment' go`}, "tailfragment"},
		"a here-document":                     {[]string{"bash", "-c", "tool --token <<EOF\nfirst tailfragment\nEOF"}, "tailfragment"},
		"a backslash-escaped space":           {[]string{"sh", "-c", `tool --password=first\ tailfragment go`}, "tailfragment"},
		"an escaped quote inside quotes":      {[]string{"sh", "-c", `tool --token "a\" tailfragment" go`}, "tailfragment"},
		"an unterminated quote in the script": {[]string{"bash", "-c", "curl --token 'first tailfragment"}, "tailfragment"},
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

// $TMPDIR is a temp directory too, even when it is written with dot segments.
func TestCommandSignatureTreatsTMPDIRAsATempDirectory(t *testing.T) {
	t.Setenv("TMPDIR", "/home/u/work/../scratch")
	for _, p := range []string{"/home/u/scratch/run-123.js", "/home/u/scratch/run-456.js"} {
		if got := CommandSignature([]string{"node", p}); got != "node run" {
			t.Errorf("CommandSignature(%q) = %q, want \"node run\"", p, got)
		}
	}
	if got := CommandSignature([]string{"node", "/home/u/work/run-123.js"}); got != "node run-123" {
		t.Errorf("outside it: %q", got)
	}
}

// Global flags with a value, before the subcommand, do not hide it: different jobs must not fall into one signature.
func TestCommandSignatureSkipsKnownGlobalFlagsOfATool(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"docker context build":                       {[]string{"docker", "--context", "prod", "build", "."}, "docker build"},
		"docker context run":                         {[]string{"docker", "--context", "prod", "run", "--rm", "alpine"}, "docker run"},
		"docker -H and a log level":                  {[]string{"docker", "-H", "tcp://10.0.0.5:2375", "-l", "debug", "compose", "up"}, "docker compose up"},
		"kubectl namespace":                          {[]string{"kubectl", "-n", "kube-system", "get", "pods"}, "kubectl get pods"},
		"kubectl context and config":                 {[]string{"kubectl", "--context", "c1", "--kubeconfig", "/x/cfg", "rollout", "status"}, "kubectl rollout status"},
		"git -C and -c":                              {[]string{"git", "-C", "/x/repo", "-c", "core.x=1", "fetch", "origin"}, "git fetch origin"},
		"pnpm filter":                                {[]string{"pnpm", "--filter", "web", "test", "--run"}, "pnpm test"},
		"pnpm -C":                                    {[]string{"pnpm", "-C", "spa", "run", "build"}, "pnpm run build"},
		"a flag with = needs no skip":                {[]string{"docker", "--context=prod", "build", "."}, "docker build"},
		"an unknown flag still stops":                {[]string{"docker", "--debug", "build"}, "docker"},
		"another tool's flag is not known":           {[]string{"swift", "-n", "x", "build"}, "swift"},
		"the value of a known flag is the last word": {[]string{"docker", "--context"}, "docker"},
	} {
		if got := CommandSignature(c.argv); got != c.want {
			t.Errorf("%s: CommandSignature(%q) = %q, want %q", name, c.argv, got, c.want)
		}
	}
}

// The same job run from a temp script with a counter or a random suffix is one signature.
func TestCommandSignatureNormalizesTempNames(t *testing.T) {
	same := func(t *testing.T, want string, argvs ...[]string) {
		t.Helper()
		for _, a := range argvs {
			if got := CommandSignature(a); got != want {
				t.Errorf("CommandSignature(%q) = %q, want %q", a, got, want)
			}
		}
	}
	same(t, "node run",
		[]string{"node", "/tmp/run-123.js"}, []string{"node", "/tmp/run-456.js"}, []string{"node", "/private/tmp/run_98765.js"})
	same(t, "bash build",
		[]string{"bash", "/var/folders/ab/cd/T/build-a1b2c3d4e5.sh"}, []string{"bash", "/var/folders/ab/cd/T/build.9f8e7d6c5b.sh"})
	same(t, "python3 train",
		[]string{"python3", "/tmp/train-2026.py"}, []string{"python3", "/tmp/train-2027.py"})
	// the other shapes a throwaway name has: a whole uuid, an ISO date, a mktemp suffix of mixed letters and digits
	same(t, "node job",
		[]string{"node", "/tmp/job-3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b.js"}, []string{"node", "/tmp/job-0a1b2c3d-4e5f-6a7b-8c9d-0e1f2a3b4c5d.js"})
	same(t, "bash backup",
		[]string{"bash", "/tmp/backup-2026-10-18.sh"}, []string{"bash", "/tmp/backup-2026-10-19.sh"})
	same(t, "bash tmp",
		[]string{"bash", "/var/folders/ab/T/tmp.aB3dE9"}, []string{"bash", "/var/folders/ab/T/tmp.Qz7Lm2x"})
	// outside a temp directory a number is part of the name: different jobs stay different
	same(t, "pnpm release-2026", []string{"pnpm", "release-2026"})
	same(t, "pnpm release-2027", []string{"pnpm", "release-2027"})
	same(t, "tool node-180", []string{"tool", "node-180"})
	same(t, "tool node-181", []string{"tool", "node-181"})
	same(t, "tool build-a1b2c3d4e5", []string{"tool", "build-a1b2c3d4e5"})
	same(t, "tool build-a1b2c3d4e6", []string{"tool", "build-a1b2c3d4e6"})
	same(t, "node run-123", []string{"node", "scripts/run-123.js"}) // a relative path: not known to be throwaway
	same(t, "node run-124", []string{"node", "scripts/run-124.js"})
	same(t, "node run-123.js", []string{"node", "run-123.js"})
	same(t, "python3 run-123", []string{"python3", "run-123"})
	// a path that only starts with a temp directory but leads out of it is not in it
	same(t, "node release-2026", []string{"node", "/tmp/../opt/jobs/release-2026.js"})
	same(t, "node release-2027", []string{"node", "/tmp/../opt/jobs/release-2027.js"})
	same(t, "node run-123", []string{"node", "/tmp2/run-123.js"})         // a different directory with the same start
	same(t, "node run", []string{"node", "/opt/../tmp/./sub/run-123.js"}) // and one that leads in
	// real names stay, in a temp directory too
	same(t, "node vitest", []string{"node", "/x/node_modules/.bin/vitest"})
	same(t, "node es2020", []string{"node", "/tmp/es2020"})
	same(t, "node deadbeef", []string{"node", "/tmp/deadbeef"})     // letters only: a word, not a random suffix
	same(t, "node build-prod", []string{"node", "/tmp/build-prod"}) // not a counter
	same(t, "node v8-12", []string{"node", "/tmp/v8-12"})           // two digits are a version, not a counter
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

// ccWrap is the zsh -c script Claude Code builds around every Bash command (a real sample from ps; the newlines are real ones): the
// shell snapshot, two exports, setopt, an unalias guard, then the command as the single-quoted argument of eval, with stdin and the
// cwd file around it.
func ccWrap(evalArg string) []string {
	return []string{"/bin/zsh", "-c", "source /Users/wake/.claude/shell-snapshots/snapshot-zsh-1791868536359-gzelyx.sh 2>/dev/null || true && " +
		"export CODEX_COMPANION_SESSION_ID='8c088675-7d6b-42da-8b1d-0f9b5ed031cc'\nexport CLAUDE_PLUGIN_DATA='/Users/wake/.claude/plugins/data/codex-openai-codex'\n" +
		": && setopt NO_EXTENDED_GLOB NO_BARE_GLOB_QUAL 2>/dev/null || true && " +
		"{ \\builtin unalias -- 'unsetenv'; \\builtin unset -f -- 'unsetenv'; } >/dev/null 2>&1 || true && " +
		"eval " + evalArg + " < /dev/null && pwd -P >| /tmp/claude-19cb-cwd"}
}

func TestCommandSignatureSeesThroughTheClaudeCodeWrapper(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"a bare command":             {ccWrap(`'go test ./...'`), "go test"},
		"a quoted program path":      {ccWrap(`''"'"'/Users/wake/Workspace/wake/purdex/bin/pdx'"'"' test go-all'`), "pdx test go-all"},
		"cd then the command":        {ccWrap(`'cd /x && git status --short'`), "git status"},
		"a quote escaped as '\\''":   {ccWrap(`'cd /x && grep -rn '\''a && b'\'' src | wc -l'`), "grep"},
		"a newline between commands": {ccWrap("'cd /x\npnpm vitest run a.test.ts'"), "pnpm vitest run"},
		"a && inside quotes is data": {ccWrap(`'echo "a && go test" && true'`), "echo"},
		"the wrapper only":           {ccWrap(`''`), "zsh"},
		"no eval: as before":         {[]string{"/bin/zsh", "-c", ": && setopt NO_EXTENDED_GLOB 2>/dev/null || true"}, ":"},
		"plain zsh -c still works":   {[]string{"zsh", "-c", "cd /x && swift build"}, "swift build"},
	} {
		if got := CommandSignature(c.argv); got != c.want {
			t.Errorf("%s: CommandSignature = %q, want %q", name, got, c.want)
		}
	}
}

func TestCommandSignatureWrapperReadingKeepsTheOtherRules(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"a secret joined by unquoting is not a word": {[]string{"zsh", "-c", "tool sk-'abcdefgh12345' go"}, "tool go"},
		"a secret inside the eval is not a word":     {ccWrap(`'tool sk-'\''abcdefgh12345'\'' go'`), "tool go"},
		"unalias outside the wrapper is the command": {[]string{"zsh", "-c", "unalias dangerous; go test ./..."}, "unalias dangerous"},
		"setopt outside the wrapper is the command":  {[]string{"zsh", "-c", "setopt xtrace; deploy production"}, "setopt xtrace"},
		"a phrase word in a plain argv stays":        {[]string{"tool", "release candidate", "deploy"}, "tool release candidate deploy"},
		"a quoted phrase in a script ends the words": {[]string{"zsh", "-c", `echo "a b" c`}, "echo"},
	} {
		if got := CommandSignature(c.argv); got != c.want {
			t.Errorf("%s: CommandSignature = %q, want %q", name, got, c.want)
		}
	}
}

func TestCommandSignatureSeesThroughPdxGate(t *testing.T) {
	for name, c := range map[string]struct {
		argv []string
		want string
	}{
		"the command after --":                {[]string{"pdx", "gate", "--class", "heavy", "--", "go", "test", "./x/"}, "go test"},
		"a full path to pdx":                  {[]string{"/Users/wake/Workspace/wake/purdex/bin/pdx", "gate", "--", "pnpm", "vitest", "run", "a.test.ts"}, "pnpm vitest run"},
		"no flags":                            {[]string{"pdx", "gate", "--", "swift", "build"}, "swift build"},
		"gate flags with values only":         {[]string{"pdx", "gate", "--class", "heavy"}, "pdx gate"},
		"nothing after --":                    {[]string{"pdx", "gate", "--"}, "pdx gate"},
		"other pdx commands stay":             {[]string{"pdx", "test", "go-changed", "./x/"}, "pdx test go-changed"},
		"a script that runs the gate":         {[]string{"zsh", "-c", "cd /x && pdx gate --class heavy -- go vet ./..."}, "go vet"},
		"gate inside the CC wrapper":          {ccWrap(`'pdx gate --class heavy -- go test ./x/'`), "go test"},
		"a gated shell inside the CC wrapper": {ccWrap(`'pdx gate -- sh -c "go test ./..."'`), "go test"},
		"a shell -c inside the CC wrapper":    {ccWrap(`'bash -c "go test ./..."'`), "go test"},
		"a gate query is not the command":     {[]string{"pdx", "gate", "would-wait", "--auto", "--", "go", "test", "./..."}, "pdx gate would-wait"},
		"a gate query about a script":         {[]string{"pdx", "gate", "would-wait", "--auto", "--", "sh", "-c", "go test ./..."}, "pdx gate would-wait"},
		"a gate in a gate":                    {[]string{"pdx", "gate", "--", "pdx", "gate", "--", "go", "build"}, "go build"},
		"a secret after -- is kept out":       {[]string{"pdx", "gate", "--", "tool", "sk-live-abcdefghijklmnop", "go"}, "tool go"},
	} {
		if got := CommandSignature(c.argv); got != c.want {
			t.Errorf("%s: CommandSignature(%q) = %q, want %q", name, c.argv, got, c.want)
		}
	}
}
