package core

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

func capabilitiesOf(t *testing.T, c *Core) []any {
	t.Helper()
	rec := httptest.NewRecorder()
	c.handleInfo(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body["capabilities"].([]any)
}

// push.v1 is the one conditional capability: announced only while the push module is mounted (push spec §3).
func TestHandleInfo_PushCapabilityFollowsTheModule(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	static := capabilitiesOf(t, c)
	assert.NotContains(t, static, "push.v1", "no push module, no capability")

	c.AddModule(&stubModule{name: "push"})
	with := capabilitiesOf(t, c)
	assert.Equal(t, append(append([]any{}, static...), "push.v1"), with, "appended after the static list, which is unchanged")

	// the capability list a later request sees is not polluted by the earlier one
	assert.Equal(t, with, capabilitiesOf(t, c))
}

func TestHandleInfo_AnotherModuleNamedLikePushDoesNotMatter(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.AddModule(&stubModule{name: "pushy"})
	assert.NotContains(t, capabilitiesOf(t, c), "push.v1")
}
