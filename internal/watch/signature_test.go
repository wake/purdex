package watch

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSignature(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"go", "test", "./..."}, "go test"},
		{[]string{"/opt/homebrew/bin/go", "test", "-race", "-count=1", "./internal/store"}, "go test"},
		{[]string{"go", "-C", "cmd", "build", "./..."}, "go cmd"}, // a short flag's value looks like a word: all the signature can do
		{[]string{"composer", "test:integration"}, "composer test:integration"},
		{[]string{"composer", "test:integration", "--no-ansi"}, "composer test:integration"},
		{[]string{"/Users/wake/Library/Application Support/Herd/bin/composer", "test"}, "composer test"},
		{[]string{"vendor/bin/phpunit", "--testsuite", "integration"}, "phpunit"},
		{[]string{"./vendor/bin/phpunit", "-c", "phpunit.xml", "--filter", "Foo"}, "phpunit"},
		{[]string{"vendor/bin/pest", "tests/Feature/FooTest.php"}, "pest"},
		{[]string{"pnpm", "exec", "vitest", "run"}, "pnpm exec vitest run"},
		{[]string{"pnpm", "exec", "vitest", "run", "src/a.test.ts", "--coverage"}, "pnpm exec vitest run"},
		{[]string{"pnpm", "run", "electron:build"}, "pnpm run electron:build"},
		{[]string{"pnpm", "run", "electron:build", "--mac"}, "pnpm run electron:build"},
		{[]string{"pnpm", "install"}, "pnpm install"},
		{[]string{"pnpm", "test"}, "pnpm test"},
		{[]string{"npx", "vitest", "run"}, "npx vitest"},
		{[]string{"npx", "--package", "vitest", "--", "vitest", "run"}, "npx"},
		{[]string{"-zsh"}, "zsh"},
		{[]string{"node", "/Users/wake/script.js", "arg"}, "node arg"},
		{[]string{"node", "./script.js"}, "node"},
		{[]string{"sleep", "30"}, "sleep"},
		{[]string{"rsync", "-a", "src/", "dst/"}, "rsync"},
		{[]string{"tool", "", "--", "x"}, "tool"},
		// secrets and oddities never become part of a signature
		{[]string{"deploy", "sk-ant-api03-AbCdEf1234567890"}, "deploy"},
		{[]string{"tool", "ghp_AbCdEf1234567890xyz"}, "tool"},
		{[]string{"tool", "aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3aB3"}, "tool"},
		{[]string{"tool", "line\nbreak", "ok"}, "tool ok"},
		{[]string{"tool", "has\x00nul", "ok"}, "tool ok"},
		{[]string{"tool", string(make([]byte, 0)) + "this-is-a-very-long-word-that-is-more-than-forty-bytes-long", "ok"}, "tool ok"},
		{[]string{"sk-ant-api03-AbCdEf1234567890"}, ""},
		{nil, ""},
		{[]string{""}, ""},
	} {
		assert.Equal(t, c.want, Signature(c.argv), "%v", c.argv)
	}
}
