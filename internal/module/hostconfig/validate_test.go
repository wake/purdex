package hostconfig

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeProjectsOK(t *testing.T) {
	got, err := normalizeProjects(json.RawMessage(`[
		{"id":"p1","name":"  Purdex ","slug":"purdex","path":" ~/Workspace/wake/purdex "},
		{"id":"p2","name":"Root","slug":"r-2","path":"/"},
		{"id":"p3","name":"Home","slug":"home","path":"~"}
	]`))
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "Purdex", got[0].Name)
	assert.Equal(t, "~/Workspace/wake/purdex", got[0].Path)
}

func TestNormalizeProjectsEmptyIsNonNil(t *testing.T) {
	got, err := normalizeProjects(json.RawMessage(`[]`))
	require.NoError(t, err)
	assert.NotNil(t, got)
}

func TestNormalizeProjectsRejects(t *testing.T) {
	long := strings.Repeat("a", 65)
	cases := map[string]string{
		"not array":      `{}`,
		"null":           `null`,
		"bad id":         `[{"id":"a b","name":"n","slug":"s1","path":"/"}]`,
		"dup id":         `[{"id":"a","name":"n","slug":"s1","path":"/"},{"id":"a","name":"n","slug":"s2","path":"/"}]`,
		"empty name":     `[{"id":"a","name":"  ","slug":"s1","path":"/"}]`,
		"long name":      `[{"id":"a","name":"` + long + `","slug":"s1","path":"/"}]`,
		"bad slug":       `[{"id":"a","name":"n","slug":"Bad","path":"/"}]`,
		"slug dash lead": `[{"id":"a","name":"n","slug":"-x","path":"/"}]`,
		"dup slug":       `[{"id":"a","name":"n","slug":"s","path":"/"},{"id":"b","name":"n","slug":"s","path":"/"}]`,
		"relative path":  `[{"id":"a","name":"n","slug":"s1","path":"foo/bar"}]`,
		"tilde user":     `[{"id":"a","name":"n","slug":"s1","path":"~bob/x"}]`,
		"empty path":     `[{"id":"a","name":"n","slug":"s1","path":" "}]`,
		"nul path":       `[{"id":"a","name":"n","slug":"s1","path":"/a\u0000b"}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeProjects(json.RawMessage(raw))
			assert.Error(t, err)
		})
	}
}

func TestNormalizeProjectsMax200(t *testing.T) {
	items := make([]string, 201)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id":"p%d","name":"n","slug":"s%d","path":"/"}`, i, i)
	}
	_, err := normalizeProjects(json.RawMessage("[" + strings.Join(items, ",") + "]"))
	assert.Error(t, err)
}

func TestNormalizeCommandsOK(t *testing.T) {
	got, err := normalizeCommands(json.RawMessage(`[
		{"id":"c1","name":" Claude ","command":"cld-yolo","icon":{"kind":"agent","value":"cc-bot"}},
		{"id":"c2","name":"Shell","command":"echo hi && ls","icon":{"kind":"phosphor","value":"Terminal"}}
	]`))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Claude", got[0].Name)
	assert.Equal(t, "cld-yolo", got[0].Command)
}

func TestNormalizeCommandsRejects(t *testing.T) {
	cases := map[string]string{
		"empty command": `[{"id":"a","name":"n","command":"","icon":{"kind":"agent","value":"codex"}}]`,
		"long command":  `[{"id":"a","name":"n","command":"` + strings.Repeat("x", 4097) + `","icon":{"kind":"agent","value":"codex"}}]`,
		"nul command":   `[{"id":"a","name":"n","command":"a\u0000","icon":{"kind":"agent","value":"codex"}}]`,
		"bad kind":      `[{"id":"a","name":"n","command":"x","icon":{"kind":"emoji","value":"x"}}]`,
		"bad agent":     `[{"id":"a","name":"n","command":"x","icon":{"kind":"agent","value":"gemini"}}]`,
		"bad phosphor":  `[{"id":"a","name":"n","command":"x","icon":{"kind":"phosphor","value":"terminal"}}]`,
		"dup id":        `[{"id":"a","name":"n","command":"x","icon":{"kind":"agent","value":"codex"}},{"id":"a","name":"n","command":"y","icon":{"kind":"agent","value":"codex"}}]`,
		"empty name":    `[{"id":"a","name":"","command":"x","icon":{"kind":"agent","value":"codex"}}]`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeCommands(json.RawMessage(raw))
			assert.Error(t, err)
		})
	}
}

func TestNormalizeResumeTemplates(t *testing.T) {
	got, err := normalizeResumeTemplates(json.RawMessage(`{"cc":{"exact":"cld --resume {id}","fallback":""}}`))
	require.NoError(t, err)
	assert.Equal(t, "cld --resume {id}", got["cc"].Exact)

	empty, err := normalizeResumeTemplates(json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.NotNil(t, empty)

	for name, raw := range map[string]string{
		"array":     `[]`,
		"null":      `null`,
		"bad agent": `{"CC":{"exact":"","fallback":""}}`,
		"too long":  `{"cc":{"exact":"` + strings.Repeat("x", 4097) + `","fallback":""}}`,
		"nul":       `{"cc":{"exact":"a\u0000","fallback":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeResumeTemplates(json.RawMessage(raw))
			assert.Error(t, err)
		})
	}

	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf(`"a%d":{"exact":"","fallback":""}`, i)
	}
	_, err = normalizeResumeTemplates(json.RawMessage("{" + strings.Join(many, ",") + "}"))
	assert.Error(t, err)
}
