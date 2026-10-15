package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlots_Size(t *testing.T) {
	s := Defaults().Slots
	assert.Equal(t, 1, s.Size(ClassHeavy))
	assert.Equal(t, 3, s.Size(ClassLight))
}

// priority: nice or taskpolicy in front, nothing for none, for --fg, or on a host without the helper.
func TestPriority_Wrap(t *testing.T) {
	tools := Tools{Nice: "/usr/bin/nice", Taskpolicy: "/usr/sbin/taskpolicy"}
	cmd := []string{"/bin/go", "test"}
	assert.Equal(t, []string{"/usr/bin/nice", "-n", "10", "/bin/go", "test"}, PriorityConfig{Mode: "nice", Nice: 10}.Wrap(cmd, tools, false))
	assert.Equal(t, []string{"/usr/bin/nice", "-n", "5", "/bin/go", "test"}, PriorityConfig{Mode: "nice", Nice: 5}.Wrap(cmd, tools, false))
	assert.Equal(t, []string{"/usr/sbin/taskpolicy", "-b", "/bin/go", "test"}, PriorityConfig{Mode: "background"}.Wrap(cmd, tools, false))
	assert.Equal(t, cmd, PriorityConfig{Mode: "none"}.Wrap(cmd, tools, false))
	assert.Equal(t, cmd, PriorityConfig{Mode: "nice", Nice: 10}.Wrap(cmd, tools, true), "--fg")
	assert.Equal(t, cmd, PriorityConfig{Mode: "nice", Nice: 10}.Wrap(cmd, Tools{}, false), "no nice on this host")
	assert.Equal(t, cmd, PriorityConfig{Mode: "background"}.Wrap(cmd, Tools{Nice: "/usr/bin/nice"}, false), "no taskpolicy on this host")
}

// cache: -count=1 off the cache; warn, strip or leave.
func TestCache_Apply(t *testing.T) {
	warn, strip, off := CacheConfig{Count1: "warn"}, CacheConfig{Count1: "strip"}, CacheConfig{Count1: "off"}
	in := words("go test -count=1 -race ./...")
	out, note := warn.Apply(in, false)
	assert.Equal(t, in, out)
	assert.Contains(t, note, "-count=1")

	out, note = strip.Apply(in, false)
	assert.Equal(t, words("go test -race ./..."), out)
	assert.Contains(t, note, "taken out")

	out, note = strip.Apply(words("go test -count 1 ./..."), false)
	assert.Equal(t, words("go test ./..."), out, "the two-word form too")
	assert.NotEmpty(t, note)

	out, note = off.Apply(in, false)
	assert.Equal(t, in, out)
	assert.Empty(t, note)

	out, note = strip.Apply(in, true)
	assert.Equal(t, in, out, "--fg (the release gate) is never touched")
	assert.Empty(t, note)

	for _, other := range []string{"go test -count=2 ./...", "go test ./...", "go build ./...", "npx vitest run", "go vet ./..."} {
		out, note = strip.Apply(words(other), false)
		assert.Equal(t, words(other), out, other)
		assert.Empty(t, note, other)
	}
}

func TestIntercept_Classify(t *testing.T) {
	c := Defaults().Intercept
	assert.Equal(t, ClassHeavy, c.Classify(words("go test ./..."), Env{}))
	assert.Equal(t, ClassNone, c.Classify(words("make test"), Env{}), "make is not among the tools")
	c.Tools = []string{"go", "make"}
	assert.Equal(t, ClassHeavy, c.Classify(words("make test"), Env{}))
	assert.Equal(t, ClassNone, c.Classify(words("pnpm check"), Env{}), "a tool that is not listed passes through")
	c.Rules = []Rule{{Match: "pnpm run e2e", Class: ClassHeavy}, {Match: "go test ./...", Class: ClassNone}}
	assert.Equal(t, ClassHeavy, c.Classify(words("pnpm run e2e --headed"), Env{}), "a rule comes first, even for a tool not listed")
	assert.Equal(t, ClassNone, c.Classify(words("go test ./..."), Env{}), "and over the built-in table")
	assert.Equal(t, ClassHeavy, c.Classify(words("go test ./a ./b"), Env{}), "a rule matches whole words")
	assert.Equal(t, ClassNone, c.Classify(nil, Env{}))
	assert.Equal(t, ClassHeavy, c.Classify(words("/opt/homebrew/bin/go test ./a ./b"), Env{}), "by the name of the binary")
}
