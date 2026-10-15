package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGoFlagsEnv(t *testing.T) {
	assert.Equal(t, []string{"A=1", "GOFLAGS=-p=3"}, GoFlagsEnv([]string{"A=1"}, 3), "no GOFLAGS: added")
	assert.Equal(t, []string{"GOFLAGS=-p=3"}, GoFlagsEnv([]string{"GOFLAGS="}, 3), "empty GOFLAGS: filled")
	assert.Equal(t, []string{"GOFLAGS=-mod=vendor -p=3"}, GoFlagsEnv([]string{"GOFLAGS=-mod=vendor"}, 3), "others kept")
	for _, own := range []string{"GOFLAGS=-p=8", "GOFLAGS=-mod=vendor -p=2", "GOFLAGS=-p 8", "GOFLAGS=--p=1"} {
		assert.Equal(t, []string{own}, GoFlagsEnv([]string{own}, 3), own+": the person's own -p is left")
	}
	assert.Equal(t, []string{"A=1"}, GoFlagsEnv([]string{"A=1"}, 0), "0 turns it off")
	in := []string{"A=1"}
	_ = GoFlagsEnv(in, 3)
	assert.Equal(t, []string{"A=1"}, in, "the input is not modified")
}

func TestVitestArgs(t *testing.T) {
	for _, c := range []struct {
		in, want string
		changed  bool
	}{
		{"vitest run", "vitest run --maxWorkers=3", true},
		{"npx vitest run", "npx vitest run --maxWorkers=3", true},
		{"pnpm exec vitest run --reporter=dot", "pnpm exec vitest run --reporter=dot --maxWorkers=3", true},
		{"pnpm vitest run src/a.test.ts", "pnpm vitest run src/a.test.ts --maxWorkers=3", true},
		{"vitest run -- --foo", "vitest run --maxWorkers=3 -- --foo", true},
		{"vitest run --maxWorkers=2", "vitest run --maxWorkers=2", false},
		{"vitest run --max-workers 2", "vitest run --max-workers 2", false},
		{"vitest run --poolOptions.threads.maxThreads=2", "vitest run --poolOptions.threads.maxThreads=2", false},
		{"pnpm run test", "pnpm run test", false},       // a script: its own flags are not ours to add
		{"pnpm exec tsc -b", "pnpm exec tsc -b", false}, // not vitest
		{"go test ./...", "go test ./...", false},
	} {
		got, ch := VitestArgs(words(c.in), 3)
		assert.Equal(t, words(c.want), got, c.in)
		assert.Equal(t, c.changed, ch, c.in)
	}
	got, ch := VitestArgs(words("vitest run"), 0)
	assert.False(t, ch)
	assert.Equal(t, words("vitest run"), got)
}

func TestPnpmArgs(t *testing.T) {
	got, ch := PnpmArgs(words("pnpm -r test"), 2)
	assert.True(t, ch)
	assert.Equal(t, words("pnpm --workspace-concurrency=2 -r test"), got)
	got, ch = PnpmArgs(words("pnpm --recursive run lint"), 2)
	assert.True(t, ch)
	assert.Equal(t, words("pnpm --workspace-concurrency=2 --recursive run lint"), got)
	_, ch = PnpmArgs(words("pnpm -r --workspace-concurrency=8 test"), 2)
	assert.False(t, ch, "the person's own value is left")
	_, ch = PnpmArgs(words("pnpm check"), 2)
	assert.False(t, ch, "not recursive")
	_, ch = PnpmArgs(words("npm -r test"), 2)
	assert.False(t, ch)
}

func TestCapped_NamesWhatItAdded(t *testing.T) {
	cfg := Defaults()
	argv, env, added := Capped(words("go test ./..."), []string{"A=1"}, cfg)
	assert.Equal(t, words("go test ./..."), argv)
	assert.Equal(t, []string{"A=1", "GOFLAGS=-p=3"}, env)
	assert.Equal(t, []string{"GOFLAGS -p=3"}, added)

	argv, env, added = Capped(words("npx vitest run"), []string{"GOFLAGS=-p=1"}, cfg)
	assert.Equal(t, words("npx vitest run --maxWorkers=3"), argv)
	assert.Equal(t, []string{"GOFLAGS=-p=1"}, env)
	assert.Equal(t, []string{"--maxWorkers=3"}, added)

	_, env, added = Capped(words("pnpm check"), nil, cfg)
	assert.Equal(t, []string{"GOFLAGS=-p=3"}, env, "a script that runs go gets the cap in its environment")
	assert.Empty(t, added, "but the line names only what the command line got")
}
