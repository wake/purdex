package gate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---- GC (#3058): the vocabulary of a conflict rule ----

// What a rule sees of a command line: the tool and its arguments, without the runner. Mutation gates: a runner option's value taken
// for the tool → the --filter / --package cases red; `--` not skipped → the `exec --` cases red; the base name not taken → the path
// cases red; a script opened → the `pnpm run` case red.
func TestCommandWords(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want string
	}{
		{"tsc -b --force", "tsc -b --force"},
		{"/repo/node_modules/.bin/tsc -b", "tsc -b"},
		{"./node_modules/.bin/tsc -b", "tsc -b"},
		{"vitest related --run a.ts", "vitest related --run a.ts"},
		{"npx tsc -b", "tsc -b"},
		{"npx -y tsc -b", "tsc -b"},
		{"npx --package typescript -- tsc -b", "tsc -b"},
		{"npx --package=typescript tsc -b", "tsc -b"},
		{"pnpx vitest related", "vitest related"},
		{"bunx tsc -b", "tsc -b"},
		{"bun x tsc -b", "tsc -b"},
		{"bun x -- tsc -b", "tsc -b"},
		{"pnpm exec tsc -b", "tsc -b"},
		{"pnpm exec -- tsc -b", "tsc -b"},
		{"pnpm --filter spa exec tsc -b", "tsc -b"},
		{"pnpm --filter=spa exec tsc -b", "tsc -b"},
		{"pnpm -C spa exec tsc -b", "tsc -b"},
		{"pnpm exec ./node_modules/.bin/tsc -b", "tsc -b"},
		{"/opt/homebrew/bin/pnpm tsc -b", "tsc -b"},
		{"pnpm tsc -b", "tsc -b"},
		{"pnpm vitest related a.ts", "vitest related a.ts"},
		{"npm exec -- tsc -b", "tsc -b"},
		{"npm exec tsc -b", "tsc -b"},
		{"npm exec --package typescript -- tsc -b", "tsc -b"},
		{"npm exec --package=typescript -- tsc -b", "tsc -b"},
		{"pnpm exec --filter spa tsc -b", "tsc -b"},
		{"bun x --bun tsc -b", "tsc -b"},
		{"yarn exec tsc -b", "tsc -b"},
		{"yarn x tsc", "tsc"},
		{"yarn dlx tsc", "yarn dlx tsc"}, // dlx is not exec
		{"pnpm run build", "pnpm run build"},
		{"pnpm --filter spa run typecheck", "pnpm run typecheck"},
		{"npm run typecheck", "npm run typecheck"},
		{"pnpm", "pnpm"},
		{"pnpm exec", ""},
		{"go test ./...", "go test ./..."},
		{"/usr/local/go/bin/go vet ./...", "go vet ./..."},
	} {
		got := strings.Join(commandWords(strings.Fields(c.cmd)), " ")
		assert.Equal(t, c.want, got, c.cmd)
	}
	assert.Nil(t, commandWords(nil))
}

// A prefix of the command's words. Mutation gates: a contains match instead of a prefix → `tsc --force -b` red; a pattern longer
// than the command accepted → red.
func TestMatchesPrefix(t *testing.T) {
	for _, c := range []struct {
		pattern, cmd string
		want         bool
	}{
		{"tsc -b", "tsc -b --force", true},
		{"tsc -b", "tsc -b", true},
		{"tsc -b", "tsc --force -b", false},
		{"vitest related", "vitest related --run a.ts", true},
		{"vitest related", "vitest --project related run", false},
		{"vitest related", "vitest", false},
		{"tsc -b --force", "tsc -b", false},
		{"tsc", "tsc", true},
		{"tsc", "tsc-watch", false},
		{"", "tsc", false},
	} {
		assert.Equal(t, c.want, matchesPrefix(patternWords(c.pattern), strings.Fields(c.cmd)), c.pattern+" | "+c.cmd)
	}
}

// A rule keeps two commands apart in either order, only in its class, and may pair a pattern with itself.
func TestConflictCollides(t *testing.T) {
	tsc, rel := strings.Fields("tsc -b --force"), strings.Fields("vitest related --run a.ts")
	r := Conflict{Class: ClassHeavy, A: "tsc -b", B: "vitest related", Reason: "thrash"}
	assert.True(t, r.collides(ClassHeavy, tsc, rel))
	assert.True(t, r.collides(ClassHeavy, rel, tsc), "either order")
	assert.False(t, r.collides(ClassLight, tsc, rel), "a rule of the other class does nothing")
	assert.False(t, r.collides(ClassHeavy, tsc, strings.Fields("go vet ./...")))
	assert.False(t, r.collides(ClassHeavy, tsc, tsc), "tsc beside tsc is not this rule")
	same := Conflict{Class: ClassHeavy, A: "tsc -b", B: "tsc -b"}
	assert.True(t, same.collides(ClassHeavy, tsc, strings.Fields("tsc -b")), "a pattern may be paired with itself")
	assert.False(t, same.collides(ClassHeavy, tsc, rel))
}
