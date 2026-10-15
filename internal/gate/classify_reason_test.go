package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ClassifyReason: the class is Classify's, and the reason says why (spec §12: the load watcher marks a command that should
// have been gated apart from one that is allowed).
func TestClassifyReason_Table(t *testing.T) {
	tty := Env{StdinTTY: true}
	for _, c := range []struct {
		cmd    string
		env    Env
		class  Class
		reason Reason
	}{
		// heavy and light: the two pools
		{"go test ./...", Env{}, ClassHeavy, ReasonHeavy},
		{"go build ./...", Env{}, ClassHeavy, ReasonHeavy},
		{"composer test:integration", Env{}, ClassHeavy, ReasonHeavy},
		{"vendor/bin/phpunit --testsuite integration", Env{}, ClassHeavy, ReasonHeavy},
		{"pnpm test", Env{}, ClassHeavy, ReasonHeavy},
		{"pnpm -r lint", Env{}, ClassHeavy, ReasonHeavy},
		{"vitest run", Env{}, ClassHeavy, ReasonHeavy},
		{"tsc -b", Env{}, ClassHeavy, ReasonHeavy},
		{"make test", Env{}, ClassHeavy, ReasonHeavy},
		{"go test ./internal/store", Env{}, ClassLight, ReasonLight},
		{"vendor/bin/phpunit --filter FooTest", Env{}, ClassLight, ReasonLight},
		{"vitest run src/a.test.ts", Env{}, ClassLight, ReasonLight},
		{"composer test -- --filter Foo", Env{}, ClassLight, ReasonLight},
		// packaging: let through on purpose
		{"go build ./cmd/pdx", Env{}, ClassNone, ReasonPackaging},
		{"go build -o bin/pdx ./...", Env{}, ClassNone, ReasonPackaging},
		{"go -C cmd build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm run build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm run electron:build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm electron:build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm -r build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm --filter spa build", Env{}, ClassNone, ReasonPackaging},
		{"npm run build", Env{}, ClassNone, ReasonPackaging},
		{"yarn build", Env{}, ClassNone, ReasonPackaging},
		{"yarn workspaces foreach --all run build", Env{}, ClassNone, ReasonPackaging},
		{"vite build", Env{}, ClassNone, ReasonPackaging},
		{"npx vite build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm exec vite build", Env{}, ClassNone, ReasonPackaging},
		{"electron-vite build", Env{}, ClassNone, ReasonPackaging},
		{"pnpm exec electron-vite build", Env{}, ClassNone, ReasonPackaging},
		{"electron-builder --mac --arm64", Env{}, ClassNone, ReasonPackaging},
		{"npx electron-builder", Env{}, ClassNone, ReasonPackaging},
		{"composer build", Env{}, ClassNone, ReasonPackaging},
		{"composer run build", Env{}, ClassNone, ReasonPackaging},
		{"make build", Env{}, ClassNone, ReasonPackaging},
		// watch mode: never ends
		{"vitest", tty, ClassNone, ReasonWatchMode},
		{"vitest --watch", Env{}, ClassNone, ReasonWatchMode},
		{"vitest watch", Env{}, ClassNone, ReasonWatchMode},
		{"vitest dev", Env{}, ClassNone, ReasonWatchMode},
		{"pnpm vitest --watch", Env{}, ClassNone, ReasonWatchMode},
		{"npx vitest", tty, ClassNone, ReasonWatchMode},
		{"vite", Env{}, ClassNone, ReasonWatchMode},
		{"vite dev", Env{}, ClassNone, ReasonWatchMode},
		{"electron-vite dev", Env{}, ClassNone, ReasonWatchMode},
		// known tools, commands that cost little
		{"go version", Env{}, ClassNone, ReasonKnownPassthrough},
		{"go env GOPATH", Env{}, ClassNone, ReasonKnownPassthrough},
		{"go mod tidy", Env{}, ClassNone, ReasonKnownPassthrough},
		{"go", Env{}, ClassNone, ReasonKnownPassthrough},
		{"go -C cmd version", Env{}, ClassNone, ReasonKnownPassthrough},
		{"pnpm install", Env{}, ClassNone, ReasonKnownPassthrough},
		{"pnpm add left-pad", Env{}, ClassNone, ReasonKnownPassthrough},
		{"pnpm", Env{}, ClassNone, ReasonKnownPassthrough},
		{"npm i", Env{}, ClassNone, ReasonKnownPassthrough},
		{"yarn install", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer install", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer update", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer dump-autoload", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer run", Env{}, ClassNone, ReasonKnownPassthrough},
		{"vendor/bin/phpunit --version", Env{}, ClassNone, ReasonKnownPassthrough},
		{"vendor/bin/pest --list-tests", Env{}, ClassNone, ReasonKnownPassthrough},
		{"tsc --version", Env{}, ClassNone, ReasonKnownPassthrough},
		{"vitest --version", Env{}, ClassNone, ReasonKnownPassthrough},
		{"vitest list", Env{}, ClassNone, ReasonKnownPassthrough},
		{"vite --version", Env{}, ClassNone, ReasonKnownPassthrough},
		{"vite preview", Env{}, ClassNone, ReasonKnownPassthrough},
		{"make clean", Env{}, ClassNone, ReasonKnownPassthrough},
		{"make help", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer validate", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer show", Env{}, ClassNone, ReasonKnownPassthrough},
		{"composer require vendor/pkg", Env{}, ClassNone, ReasonKnownPassthrough},
		{"pnpm exec", Env{}, ClassNone, ReasonKnownPassthrough},
		// unknown: not in the table, or a script / program the table cannot judge, or a line it cannot read
		{"rsync -a src dst", Env{}, ClassNone, ReasonUnknown},
		{"ffmpeg -i a b", Env{}, ClassNone, ReasonUnknown},
		{"python3 train.py", Env{}, ClassNone, ReasonUnknown},
		{"go run ./cmd/x", Env{}, ClassNone, ReasonUnknown},
		{"go generate ./...", Env{}, ClassNone, ReasonUnknown},
		{"go tool pprof x", Env{}, ClassNone, ReasonUnknown},
		{"go -C", Env{}, ClassNone, ReasonUnknown},
		{"pnpm my-script", Env{}, ClassNone, ReasonUnknown},
		{"pnpm run my-script", Env{}, ClassNone, ReasonUnknown},
		{"npm run e2e", Env{}, ClassNone, ReasonUnknown},
		{"pnpm exec some-tool", Env{}, ClassNone, ReasonUnknown},
		{"npx some-tool", Env{}, ClassNone, ReasonUnknown},
		{"yarn workspaces foreach run my-script", Env{}, ClassNone, ReasonUnknown},
		{"composer run my-script", Env{}, ClassNone, ReasonUnknown},
		{"composer exec php-cs-fixer", Env{}, ClassNone, ReasonUnknown},
		{"make", Env{}, ClassNone, ReasonUnknown},
		{"make lint", Env{}, ClassNone, ReasonUnknown},
		{"make integration", Env{}, ClassNone, ReasonUnknown},
		{"composer deploy", Env{}, ClassNone, ReasonUnknown}, // a script of the project's, called by its bare name
		{"composer analyse", Env{}, ClassNone, ReasonUnknown},
		{"", Env{}, ClassNone, ReasonUnknown},
	} {
		class, reason := ClassifyReason(words(c.cmd), c.env)
		assert.Equal(t, c.class, class, c.cmd)
		assert.Equal(t, c.reason, reason, c.cmd)
		// the class is Classify's, always
		assert.Equal(t, Classify(words(c.cmd), c.env), class, "Classify disagrees: "+c.cmd)
	}
}

// Every reason is reached by the table above, the two pools go with their class, and the rest go with none.
func TestClassifyReason_ReasonsMatchClasses(t *testing.T) {
	all := []Reason{ReasonHeavy, ReasonLight, ReasonPackaging, ReasonWatchMode, ReasonKnownPassthrough, ReasonUnknown}
	assert.Len(t, all, 6)
	for _, r := range all {
		assert.NotEmpty(t, string(r))
	}
	assert.Equal(t, "known_passthrough", string(ReasonKnownPassthrough))
	assert.Equal(t, "watch_mode", string(ReasonWatchMode))
	// an argv with a path as its first word is classified by the tool's name
	c, r := ClassifyReason([]string{"/Users/wake/Library/Application Support/Herd/bin/composer", "install"}, Env{})
	assert.Equal(t, ClassNone, c)
	assert.Equal(t, ReasonKnownPassthrough, r)
	c, r = ClassifyReason([]string{"./node_modules/.bin/vitest", "run"}, Env{})
	assert.Equal(t, ClassHeavy, c)
	assert.Equal(t, ReasonHeavy, r)
}
