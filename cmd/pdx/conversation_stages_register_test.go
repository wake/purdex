package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// MS2-1: the real conversation module, registered into a real core, reports ready through the core, and /api/info announces
// conversations.stages.v1 only then (a fake core or a module without Status would never show it).
func TestRegisterServeModules_ConversationAnnouncesStages(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), Token: "t"}})
	require.NoError(t, registerServeModules(c, nil, nil))
	assert.True(t, c.Mounted("conversation"))

	caps := func() []string {
		info := callWith(t, c, http.MethodGet, "/api/info", "t", nil)
		require.Equal(t, http.StatusOK, info.Code)
		var body struct{ Capabilities []string }
		require.NoError(t, json.Unmarshal(info.Body.Bytes(), &body))
		return body.Capabilities
	}
	assert.NotContains(t, caps(), "conversations.stages.v1", "not announced before the module has initialised")
	assert.NotContains(t, caps(), "sessions.send_now.v1")

	require.NoError(t, c.InitModules())
	st, ok := c.ModuleStatus("conversation")
	require.True(t, ok, "the conversation module has no Status")
	assert.Equal(t, true, st["ready"])
	assert.Contains(t, caps(), "conversations.stages.v1")
	assert.Contains(t, caps(), "sessions.send_now.v1")
}
