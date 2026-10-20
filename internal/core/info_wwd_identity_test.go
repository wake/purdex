package core

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
)

// poolStub is a module named "pool" whose status the test changes, like the real module's watch does.
type poolStub struct {
	stubModule
	mu     sync.Mutex
	status map[string]any
}

func (p *poolStub) Status() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *poolStub) set(st map[string]any) {
	p.mu.Lock()
	p.status = st
	p.mu.Unlock()
}

func capsOf(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, _ := body["capabilities"].([]any)
	var out []string
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// Once the pool module knows who the local wwd is, /api/info carries wwd {host_id, alias} and announces pool.identity.v1; the host_id is
// wwd's own, not the Purdex daemon's.
func TestHandleInfo_CarriesTheLocalWwdIdentity(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{HostID: "purdex-host"}})
	c.AddModule(&poolStub{stubModule: stubModule{name: "pool"}, status: map[string]any{
		"ready": true, "wwd": map[string]string{"host_id": "wwd-host", "alias": "mlab"},
	}})
	body := infoOf(t, c)
	assert.Equal(t, "purdex-host", body["host_id"], "the daemon's own id is unchanged")
	assert.Equal(t, map[string]any{"host_id": "wwd-host", "alias": "mlab"}, body["wwd"])
	assert.Contains(t, capsOf(t, body), "pool.identity.v1")
}

// Not known yet (no pool module, nothing read, wwd away): no key at all, and no capability.
func TestHandleInfo_NoWwdIdentityWhenNotKnown(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	body := infoOf(t, c)
	_, has := body["wwd"]
	assert.False(t, has, "no pool module")
	assert.False(t, slices.Contains(capsOf(t, body), "pool.identity.v1"))

	p := &poolStub{stubModule: stubModule{name: "pool"}, status: map[string]any{"ready": true}}
	c.AddModule(p)
	body = infoOf(t, c)
	_, has = body["wwd"]
	assert.False(t, has, "the pool module has not read wwd")
	assert.False(t, slices.Contains(capsOf(t, body), "pool.identity.v1"))

	p.set(map[string]any{"ready": true, "wwd": map[string]string{"host_id": "", "alias": ""}})
	_, has = infoOf(t, c)["wwd"]
	assert.False(t, has, "an empty id is never reported")
}

// /api/info answers from the module's own status and never waits on wwd: a status reporter that would block is not asked for anything
// beyond Status, and the answer comes at once.
func TestHandleInfo_WwdIdentityDoesNotWaitOnWwd(t *testing.T) {
	c := New(CoreDeps{Config: &config.Config{}})
	c.AddModule(&poolStub{stubModule: stubModule{name: "pool"}, status: map[string]any{
		"ready": true, "wwd": map[string]string{"host_id": "h", "alias": "a"},
	}})
	done := make(chan map[string]any, 1)
	go func() { done <- infoOf(t, c) }()
	select {
	case body := <-done:
		require.NotNil(t, body["wwd"])
	case <-time.After(2 * time.Second):
		t.Fatal("/api/info did not answer")
	}
}
