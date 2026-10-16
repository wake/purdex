package gate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// `claude plugin test` starts an engine and a test runner per file: a whole run is heavy, a run narrowed with --file light; no
// other claude command is held (the agent itself must never wait for a slot).
func TestClassify_ClaudePluginTest(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want Class
	}{
		{"claude plugin test", ClassHeavy},
		{"claude plugin test /tmp/purdex-gate-test", ClassHeavy},
		{"claude plugin test /tmp/x --reporter dots", ClassHeavy},
		{"/usr/local/bin/claude plugin test dir", ClassHeavy}, // argv[0] may be a path
		{"claude plugin test dir --file hooks/gate.test.ts", ClassLight},
		{"claude plugin test dir --file=hooks/gate.test.ts", ClassLight},
		{"claude plugin test --file a.test.ts --file b.test.ts dir", ClassLight},
		{"claude plugin test dir -- --file", ClassHeavy}, // past -- the words are not claude's
		{"claude plugin test --filename x dir", ClassHeavy},
		{"claude", ClassNone},
		{"claude plugin", ClassNone},
		{"claude plugin list", ClassNone},
		{"claude plugin tests", ClassNone},
		{"claude -p hello", ClassNone},
		{"claude mcp list", ClassNone},
	} {
		assert.Equal(t, c.want, Classify(words(c.cmd), Env{}), c.cmd)
	}
}

// The path the gate really takes is InterceptConfig.Classify with the default tools: claude is listed there, or the classifier above is
// never reached (R1 of #2970).
func TestIntercept_DefaultToolsReachTheClaudeClassifier(t *testing.T) {
	cfg, err := Load(t.TempDir() + "/none.toml") // no file: the defaults
	assert.NoError(t, err)
	for _, c := range []struct {
		cmd  string
		want Class
	}{
		{"claude plugin test /tmp/x", ClassHeavy},
		{"claude plugin test /tmp/x --file hooks/a.test.ts", ClassLight},
		{"claude -p hi", ClassNone},
		{"claude plugin list", ClassNone},
	} {
		assert.Equal(t, c.want, cfg.Intercept.Classify(words(c.cmd), Env{}), c.cmd)
	}
	// a config that names its own tools without claude does not hold it
	own := InterceptConfig{Tools: []string{"go"}}
	assert.Equal(t, ClassNone, own.Classify(words("claude plugin test x"), Env{}))
}
