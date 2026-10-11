package hostconfig

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func putRaw(t *testing.T, m *Module, raw string) {
	t.Helper()
	_, stored, err := m.store.Put(KeyResumeTemplates, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(raw), nil })
	require.NoError(t, err)
	require.True(t, stored)
}

// A host that never wrote the key reads the defaults: the shapes that shipped hardcoded.
func TestResumeTemplates_DefaultsWhenNeverWritten(t *testing.T) {
	m := newTestModule(t)
	got, err := m.ResumeTemplates()
	require.NoError(t, err)
	assert.Equal(t, DefaultResumeTemplates, got)
}

// The reader hands out a copy: a caller editing the map must not change the defaults the next read sees.
func TestResumeTemplates_ReturnsACopy(t *testing.T) {
	m := newTestModule(t)
	got, _ := m.ResumeTemplates()
	got["cc"] = ResumeTemplatePair{Exact: "x"}
	delete(got, "codex")
	again, _ := m.ResumeTemplates()
	assert.Equal(t, DefaultResumeTemplates, again)
	assert.Equal(t, "claude --resume {id}", DefaultResumeTemplates["cc"].Exact)
}

// An override replaces that agent's pair whole (the SPA's lookup: own(overrides) ?? default); other agents keep theirs;
// an agent the defaults do not know is added.
func TestResumeTemplates_OverridesReplaceByAgent(t *testing.T) {
	m := newTestModule(t)
	putRaw(t, m, `{"cc":{"exact":"claude --resume {id} --verbose","fallback":""},"aider":{"exact":"aider --restore","fallback":"aider"}}`)
	got, err := m.ResumeTemplates()
	require.NoError(t, err)
	assert.Equal(t, ResumeTemplatePair{Exact: "claude --resume {id} --verbose", Fallback: ""}, got["cc"], "an empty fallback is the owner's: not backfilled from the default")
	assert.Equal(t, DefaultResumeTemplates["codex"], got["codex"])
	assert.Equal(t, ResumeTemplatePair{Exact: "aider --restore", Fallback: "aider"}, got["aider"])
}

// Lenient like the GET: a malformed row drops that agent (its default stays), a non-object value is no overrides at all —
// neither is an error, the launch panel must not fail because a host_config row is damaged.
func TestResumeTemplates_MalformedRowsAreReadLeniently(t *testing.T) {
	m := newTestModule(t)
	putRaw(t, m, `{"cc":"claude --resume {id}","codex":{"exact":7,"fallback":"x"},"opencode":{"exact":"opencode -s {id}\u0000","fallback":""},"BAD AGENT":{"exact":"a","fallback":"b"},"ok":{"exact":"ok {id}","fallback":"ok"}}`)
	got, err := m.ResumeTemplates()
	require.NoError(t, err)
	assert.Equal(t, DefaultResumeTemplates["cc"], got["cc"], "not an object: the default stays")
	assert.Equal(t, DefaultResumeTemplates["codex"], got["codex"], "wrong field type: the default stays")
	assert.Equal(t, DefaultResumeTemplates["opencode"], got["opencode"], "a NUL in a template: refused like the PUT does")
	assert.NotContains(t, got, "BAD AGENT")
	assert.Equal(t, ResumeTemplatePair{Exact: "ok {id}", Fallback: "ok"}, got["ok"])

	m2 := newTestModule(t)
	for _, raw := range []string{`[]`, `"x"`, `null`, `{`} {
		putRaw := func() {
			_, _, err := m2.store.Put(KeyResumeTemplates, currentRevision(t, m2), func(Entry) (json.RawMessage, error) { return json.RawMessage(raw), nil })
			require.NoError(t, err)
		}
		putRaw()
		got, err := m2.ResumeTemplates()
		require.NoError(t, err, raw)
		assert.Equal(t, DefaultResumeTemplates, got, raw)
	}
}

func currentRevision(t *testing.T, m *Module) int64 {
	t.Helper()
	e, err := m.store.Get(KeyResumeTemplates)
	require.NoError(t, err)
	return e.Revision
}

// Init publishes the module under ResumeTemplatesKey as the ResumeTemplateReader the session module asserts.
func TestInit_RegistersResumeTemplateReader(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(ResumeTemplatesKey)
	require.True(t, ok, "Init must register under ResumeTemplatesKey")
	reader, ok := svc.(ResumeTemplateReader)
	require.True(t, ok, "registry value must be a ResumeTemplateReader, got %T", svc)
	got, err := reader.ResumeTemplates()
	require.NoError(t, err)
	assert.Equal(t, DefaultResumeTemplates, got)
}

// The Go defaults are the SPA's DEFAULT_RESUME_TEMPLATES (spa/src/lib/resume-templates.ts), read from the source so
// editing either side alone goes red.
func TestDefaultResumeTemplates_MatchTheSPA(t *testing.T) {
	src, err := os.ReadFile("../../../spa/src/lib/resume-templates.ts")
	require.NoError(t, err, "the SPA source is part of this repo")
	line := regexp.MustCompile(`(?m)^\s*(\w+): Object\.freeze\(\{ exact: '([^']*)', fallback: '([^']*)' \}\),?$`)
	spa := map[string]ResumeTemplatePair{}
	for _, m := range line.FindAllStringSubmatch(string(src), -1) {
		spa[m[1]] = ResumeTemplatePair{Exact: m[2], Fallback: m[3]}
	}
	require.NotEmpty(t, spa, "the pattern found no template line: the SPA file's shape changed, update this test")
	assert.Equal(t, spa, DefaultResumeTemplates)
}
