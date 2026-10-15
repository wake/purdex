package gate

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A decision says what ran (cut and scrubbed like a holder's record), where, and in which session.
func TestDecision_NamesTheCommandItsDirectoryAndItsSession(t *testing.T) {
	g, _ := testGate(t)
	g.Session = "sess-1"
	dir := t.TempDir()
	runSync(t, g, Options{Argv: []string{"sh", "-c", "true", "--token=hunter2", strings.Repeat("a", 300)}, Class: ClassHeavy, Dir: dir})

	recs := readDecisions(t, g)
	require.Len(t, recs, 1)
	assert.Equal(t, dir, recs[0]["cwd"])
	assert.Equal(t, "sess-1", recs[0]["session"])
	argv := recs[0]["argv"].([]any)
	require.Len(t, argv, 5)
	assert.Equal(t, "sh", argv[0])
	assert.Equal(t, "--token=<redacted>", argv[3], "the secret is replaced before the line is written")
	assert.Less(t, len(argv[4].(string)), 130, "a long word is cut")
	assert.True(t, strings.HasSuffix(argv[4].(string), "…"))
	assert.NotContains(t, recs[0], "nested")
}

// No session in the environment: the field is left out, not empty.
func TestDecision_NoSessionIsNoField(t *testing.T) {
	g, _ := testGate(t)
	g.Session = ""
	runSync(t, g, Options{Argv: sh("true"), Class: ClassHeavy})
	assert.NotContains(t, readDecisions(t, g)[0], "session")
}

// A command that runs below a held slot leaves a line too, marked nested, with the same fields.
func TestDecision_ANestedCommandIsRecordedAndMarked(t *testing.T) {
	g, _ := testGate(t)
	g.Session = "sess-2"
	g.SetConfig(func(c *Config) { c.Slots.Heavy = 1 })
	o := startOuter(t, g, ClassHeavy, 1001)
	defer func() { o.release(); <-o.done }()
	code, ran := o.inner(t, ClassHeavy, 2002, descendantProcs(2002, 1001), o.env)
	require.True(t, ran)
	require.Equal(t, 0, code)

	var nested []map[string]any
	for _, r := range readDecisions(t, g) {
		if r["nested"] == true {
			nested = append(nested, r)
		}
	}
	require.Len(t, nested, 1)
	assert.Equal(t, "nested", nested[0]["outcome"])
	assert.Equal(t, "sess-2", nested[0]["session"])
	assert.NotEmpty(t, nested[0]["cwd"])
	assert.NotEmpty(t, nested[0]["argv"])
}
