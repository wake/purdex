package core

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

func infoOf(t *testing.T, c *Core) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	c.handleInfo(rec, httptest.NewRequest("GET", "/api/info", nil))
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return body
}

// pushStub is a module named "push" that reports a status, like the real one.
type pushStub struct {
	stubModule
	status map[string]any
}

func (p *pushStub) Status() map[string]any { return p.status }

func pushCore(cfgDir string, stub *pushStub) *Core {
	cfg := &config.Config{}
	if cfgDir != "" {
		cfg.Push = &config.PushConfig{APNsDir: cfgDir}
	}
	c := New(CoreDeps{Config: cfg})
	if stub != nil {
		c.AddModule(stub)
	}
	return c
}

// push.v1 is the one conditional capability: announced only while the push module is READY (it has its key), not merely
// mounted (push spec §3; a broken key must not take the daemon down, it just leaves push off).
func TestHandleInfo_PushCapabilityFollowsReadiness(t *testing.T) {
	static := infoOf(t, pushCore("", nil))["capabilities"].([]any)
	assert.NotContains(t, static, "push.v1", "no push module, no capability")

	ready := &pushStub{stubModule{name: "push"}, map[string]any{"ready": true, "init_error": ""}}
	with := infoOf(t, pushCore("/keys", ready))["capabilities"].([]any)
	assert.Equal(t, append(append([]any{}, static...), "push.v1"), with, "appended after the static list, which is unchanged")

	broken := &pushStub{stubModule{name: "push"}, map[string]any{"ready": false, "init_error": "push: apns key file cannot be read"}}
	assert.NotContains(t, infoOf(t, pushCore("/keys", broken))["capabilities"], "push.v1", "mounted but not ready")
}

func TestHandleInfo_PushBlock(t *testing.T) {
	off := infoOf(t, pushCore("", nil))["push"].(map[string]any)
	assert.Equal(t, map[string]any{"configured": false, "ready": false, "init_error": ""}, off)

	ready := &pushStub{stubModule{name: "push"}, map[string]any{"ready": true, "init_error": ""}}
	assert.Equal(t, map[string]any{"configured": true, "ready": true, "init_error": ""},
		infoOf(t, pushCore("/keys", ready))["push"])

	broken := &pushStub{stubModule{name: "push"}, map[string]any{"ready": false, "init_error": "push: apns key file cannot be read"}}
	assert.Equal(t, map[string]any{"configured": true, "ready": false, "init_error": "push: apns key file cannot be read"},
		infoOf(t, pushCore("/keys", broken))["push"])
}

// The module cannot overrule what the core knows: "configured" comes from the config.
func TestHandleInfo_PushConfiguredIsNotTheModulesToSet(t *testing.T) {
	liar := &pushStub{stubModule{name: "push"}, map[string]any{"ready": true, "configured": false}}
	assert.Equal(t, true, infoOf(t, pushCore("/keys", liar))["push"].(map[string]any)["configured"])
}

func TestHandleInfo_AnotherModuleNamedLikePushDoesNotMatter(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.AddModule(&stubModule{name: "pushy"})
	assert.NotContains(t, infoOf(t, c)["capabilities"], "push.v1")
}
