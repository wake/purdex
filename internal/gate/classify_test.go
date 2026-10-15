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
		{"vitest", ClassNone},
		{"vitest watch", ClassNone},
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
		assert.Equal(t, c.want, Classify(words(c.cmd)), c.cmd)
	}
}

func TestClassify_ClassValid(t *testing.T) {
	assert.True(t, ClassHeavy.Valid())
	assert.True(t, ClassLight.Valid())
	assert.False(t, ClassNone.Valid())
	assert.False(t, Class("").Valid())
}
