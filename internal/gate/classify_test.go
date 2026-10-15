package gate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func words(s string) []string { return strings.Fields(s) }

// Spec §3: heavy is a whole-project run, light a targeted one, none everything else.
func TestClassify_Table(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want Class
	}{
		// go
		{"go test ./...", ClassHeavy},
		{"go test -race ./...", ClassHeavy},
		{"go test -count=1 -timeout 30m ./internal/...", ClassHeavy},
		{"go test ./internal/store ./internal/team", ClassHeavy},
		{"go test -run TestX ./...", ClassHeavy},
		{"go test ./internal/store/", ClassLight},
		{"go test -race -count=1 ./internal/module/team/", ClassLight},
		{"go test -p 3 -run TestX ./internal/store", ClassLight},
		{"go test", ClassLight},
		{"go test ./internal/store -args one two", ClassLight}, // past -args the words are the test binary's
		{"go test -c -o x.test ./internal/store", ClassLight},
		{"go -C cmd test ./...", ClassHeavy},
		{"go -C=cmd test ./...", ClassHeavy},
		{"go -C=cmd version", ClassNone},
		{"go -C", ClassNone},
		{"go test -list TestName ./internal/store", ClassLight},
		{"go vet -vettool ./bin/analyzer ./internal/store", ClassLight},
		{"go build -toolexec ./wrap ./cmd/pdx", ClassLight},
		{"go build ./...", ClassHeavy},
		{"go build ./cmd/pdx", ClassLight},
		{"go build -buildvcs ./...", ClassHeavy}, // bare, it takes no value
		{"go build -buildvcs=false ./...", ClassHeavy},
		{"npx --package vitest -- vitest run", ClassHeavy},
		{"go build -o bin/pdx ./cmd/pdx", ClassLight},
		{"go vet ./...", ClassHeavy},
		{"go vet ./internal/store", ClassLight},
		{"go install ./cmd/pdx", ClassLight},
		{"/opt/homebrew/bin/go test ./...", ClassHeavy},
		{"go version", ClassNone},
		{"go env GOPATH", ClassNone},
		{"go mod tidy", ClassNone},
		{"go list ./...", ClassNone},
		{"go fmt ./...", ClassNone},
		{"go run ./cmd/x", ClassNone},
		// vitest
		{"vitest run", ClassHeavy},
		{"vitest run --reporter=dot", ClassHeavy},
		{"vitest run --maxWorkers=3", ClassHeavy},
		{"vitest run src/lib/foo.test.ts", ClassLight},
		{"vitest run -t foo", ClassLight},
		{"vitest related src/a.ts", ClassLight},
		{"vitest run --changed", ClassLight},
		{"vitest run --project spa", ClassLight},
		{"vitest", ClassHeavy}, // no terminal, no agent: it runs once
		{"vitest watch", ClassNone},
		{"vitest dev", ClassNone},
		{"vitest --version", ClassNone},
		{"npx vitest run", ClassHeavy},
		{"npx vitest run src/foo.test.ts", ClassLight},
		{"pnpm exec vitest run", ClassHeavy},
		{"pnpm vitest run src/foo.test.ts", ClassLight},
		{"pnpm -C spa exec vitest run", ClassHeavy},
		{"pnpm --prefix spa exec vitest run", ClassHeavy},
		{"pnpm --filter spa exec vitest run src/a.test.ts", ClassLight},
		// tsc
		{"tsc -b", ClassHeavy},
		{"tsc -b --force", ClassHeavy},
		{"tsc -p spa/tsconfig.json --noEmit", ClassHeavy},
		{"tsc --noEmit", ClassHeavy},
		{"tsc src/a.ts", ClassLight},
		{"tsc --version", ClassNone},
		{"npx tsc -b", ClassHeavy},
		{"pnpm exec tsc -b", ClassHeavy},
		// package scripts
		{"pnpm check", ClassHeavy},
		{"pnpm test", ClassHeavy},
		{"pnpm typecheck", ClassHeavy},
		{"pnpm lint", ClassHeavy},
		{"pnpm run build", ClassHeavy},
		{"pnpm run check", ClassHeavy},
		{"pnpm run typecheck", ClassHeavy},
		{"pnpm -r test", ClassHeavy},
		{"pnpm --recursive run lint", ClassHeavy},
		{"pnpm -r exec tsc -b", ClassHeavy},
		{"npm test", ClassHeavy},
		{"npm run lint", ClassHeavy},
		{"yarn test", ClassHeavy},
		{"yarn workspaces foreach -A run test", ClassHeavy},
		{"yarn workspaces foreach --all run build", ClassHeavy},
		{"yarn workspaces foreach -A run anything", ClassHeavy},
		{"yarn workspaces foreach run test", ClassHeavy},
		{"yarn workspaces foreach --include spa run dev", ClassNone},
		{"yarn workspaces list", ClassNone},
		{"pnpm exec ./node_modules/.bin/vitest run", ClassHeavy},
		{"npx --no-install ./node_modules/.bin/vitest run src/a.test.ts", ClassLight},
		{"pnpm run dev", ClassNone},
		{"pnpm install", ClassNone},
		{"pnpm add left-pad", ClassNone},
		{"pnpm dlx create-vite", ClassNone},
		{"pnpm --version", ClassNone},
		{"pnpm", ClassNone},
		{"npx", ClassNone},
		{"make test", ClassHeavy},
		{"make build", ClassNone},
		// everything else
		{"ls -la", ClassNone},
		{"node -e 1", ClassNone},
		{"git status", ClassNone},
		{"", ClassNone},
	} {
		assert.Equal(t, c.want, Classify(words(c.cmd), Env{}), c.cmd)
	}
}

func TestClassify_ClassValid(t *testing.T) {
	assert.True(t, ClassHeavy.Valid())
	assert.True(t, ClassLight.Valid())
	assert.False(t, ClassNone.Valid())
	assert.False(t, Class("").Valid())
}

// Vitest (std-env 4, defaults.*.js): with no subcommand and no flag it watches when stdin is a terminal, outside CI, and
// not for an agent; otherwise it runs once. Only a sure watch is none. (M: --run unknown; M: --no-watch / --watch=false
// unknown; M: the terminal ignored; M: the agent ignored; M: CI ignored; M: --watch / -w ignored.)
func TestClassify_VitestWatchOrRunOnce(t *testing.T) {
	person := Env{StdinTTY: true}
	agent := Env{StdinTTY: false, Agent: true}
	agentTTY := Env{StdinTTY: true, Agent: true}
	ci := Env{StdinTTY: true, CI: true}
	script := Env{}
	for _, c := range []struct {
		name string
		cmd  string
		env  Env
		want Class
	}{
		{"person at a terminal, bare: watch", "vitest", person, ClassNone},
		{"person at a terminal, a file: watch", "vitest src/a.test.ts", person, ClassNone},
		{"person at a terminal, via npx: watch", "npx vitest", person, ClassNone},
		{"person at a terminal, --run: once, whole", "vitest --run", person, ClassHeavy},
		{"person at a terminal, --run with a file: once, one file", "vitest --run src/a.test.ts", person, ClassLight},
		{"person at a terminal, run: once", "vitest run", person, ClassHeavy},
		{"person at a terminal, --no-watch", "npx vitest --no-watch", person, ClassHeavy},
		{"person at a terminal, --watch=false", "vitest --watch=false", person, ClassHeavy},
		{"person at a terminal, --run=true", "vitest --run=true", person, ClassHeavy},
		{"person at a terminal, --run=true with a file", "npx vitest --run=true src/a.test.ts", person, ClassLight},
		{"person at a terminal, --run=false says nothing: watch", "vitest --run=false", person, ClassNone},
		{"--no-watch=true is an unknown option (cac): the default decides", "vitest --no-watch=true", person, ClassNone},
		{"--no-watch=false changes nothing for an agent: it runs once", "vitest --no-watch=false", agent, ClassHeavy},
		{"--no-watch=true changes nothing for an agent either", "vitest --no-watch=true", agent, ClassHeavy},
		{"an agent, --watch=false", "vitest --watch=false", agent, ClassHeavy},
		{"person at a terminal, related: watch", "vitest related src/a.ts", person, ClassNone},
		{"person at a terminal, related --run", "vitest related --run src/a.ts", person, ClassLight},
		{"an agent, bare: it runs once", "npx vitest", agent, ClassHeavy},
		{"an agent, pnpm vitest", "pnpm vitest", agent, ClassHeavy},
		{"an agent with a terminal: still once", "vitest", agentTTY, ClassHeavy},
		{"an agent, a file", "npx vitest src/a.test.ts", agent, ClassLight},
		{"an agent, --run", "npx vitest --run", agent, ClassHeavy},
		{"CI, bare: once", "vitest", ci, ClassHeavy},
		{"a script (no terminal), bare: once", "vitest", script, ClassHeavy},
		{"a script, a filter -t", "vitest -t foo", script, ClassLight},
		{"an agent, --watch: a real watch", "vitest --watch", agent, ClassNone},
		{"an agent, -w", "vitest -w", agent, ClassNone},
		{"an agent, --watch=true", "vitest --watch=true", agent, ClassNone},
		{"an agent, watch", "vitest watch", agent, ClassNone},
		{"an agent, dev", "vitest dev", agent, ClassNone},
		{"the last word wins: --run after --watch", "vitest --watch --run", agent, ClassHeavy},
		{"the last word wins: --watch after --run", "vitest --run --watch", agent, ClassNone},
		{"list runs nothing", "vitest list", agent, ClassNone},
		{"--version", "vitest --version", agent, ClassNone},
		{"through pnpm exec, with --prefix", "pnpm --prefix spa exec vitest", agent, ClassHeavy},
		{"a script name vitest-like is not vitest", "pnpm run vitest-ui", agent, ClassNone},
	} {
		assert.Equal(t, c.want, Classify(words(c.cmd), c.env), c.name+": "+c.cmd)
	}
}

// The agent and CI marks read from an environment are the ones std-env 4 reads.
func TestEnvOf_AgentAndCI(t *testing.T) {
	assert.Equal(t, Env{StdinTTY: true}, EnvOf([]string{"HOME=/h", "PATH=/usr/bin"}, true))
	for _, v := range []string{"AI_AGENT=x", "CLAUDECODE=1", "CLAUDE_CODE=1", "REPL_ID=abc", "GEMINI_CLI=1", "CODEX_SANDBOX=seatbelt",
		"CODEX_THREAD_ID=t", "OPENCODE=1", "AUGMENT_AGENT=1", "GOOSE_PROVIDER=p", "CURSOR_AGENT=1",
		"PATH=/Users/x/.pi/agent/bin:/usr/bin", "EDITOR=devin", "TERM_PROGRAM=kiro"} {
		assert.True(t, EnvOf([]string{v}, false).Agent, v)
	}
	for _, v := range []string{"AI_AGENT=", "CLAUDECODE=", "TERM_PROGRAM=iTerm.app", "EDITOR=vim", "PATH=/usr/bin"} {
		assert.False(t, EnvOf([]string{v}, false).Agent, v)
	}
	for _, v := range []string{"CI=true", "CI=1", "GITHUB_ACTIONS=true", "GITLAB_CI=true", "BUILDKITE=true", "JENKINS_URL=http://j"} {
		assert.True(t, EnvOf([]string{v}, true).CI, v)
	}
	assert.False(t, EnvOf([]string{"CI="}, true).CI)
	// every provider of std-env 4: AWS CodeBuild, Bitbucket, Codefresh and Netlify are CI; a local Netlify, Vercel and
	// CodeSandbox run is not (the provider says ci:false)
	for _, v := range []string{"CODEBUILD_BUILD_ARN=x", "BITBUCKET_COMMIT=x", "CF_BUILD_ID=x", "NETLIFY=true", "APPVEYOR=1", "bamboo_planKey=x", "SEMAPHORE=1", "RENDER=1"} {
		assert.True(t, EnvOf([]string{v}, true).CI, v)
	}
	for _, v := range []string{"NETLIFY_LOCAL=true", "VERCEL=1", "VERCEL_ENV=development", "CODESANDBOX_HOST=x"} {
		assert.False(t, EnvOf([]string{v}, true).CI, v)
	}
	assert.True(t, EnvOf([]string{"VERCEL=1", "CI=1"}, true).CI, "CI itself always counts")
	assert.Len(t, ciProviders, 57)
	assert.True(t, EnvOf([]string{"X=1"}, true).StdinTTY)
	assert.False(t, EnvOf([]string{"X=1"}, false).StdinTTY)
	assert.True(t, EnvOf([]string{"BROKEN"}, true).StdinTTY, "a word without = is skipped, not an error")
}
