package hostconfig

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// Lead-team-relay spec §8.7 (a): the relay switches are a host config
// section; both default to true, a PUT may set either, the reader the team
// module uses answers the stored value or the defaults.
func TestRelaySwitches_DefaultsPutAndReader(t *testing.T) {
	m := newTestModule(t)
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, DefaultRelaySwitches, sw)

	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true},"revision":1}`, rr.Body.String())
	sw, err = m.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, RelaySwitches{SelfSolo: false, SelfLead: true}, sw)

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"relay":{"items":{"self_solo":false,"self_lead":true},"revision":1}`)

	// Stale revision: 409 with the server copy.
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_lead":false},"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true},"revision":1}`, rr.Body.String())

	// Not an object, or not booleans: 400, nothing stored.
	for _, body := range []string{`{"items":[true],"baseRevision":1}`, `{"items":{"self_solo":"yes"},"baseRevision":1}`,
		`{"items":{"self_solo":null},"baseRevision":1}`, `{"items":{"self_lead":null},"baseRevision":1}`, `{"items":{"self_lead":1},"baseRevision":1}`,
		`{"items":{"self_leaad":false},"baseRevision":1}`, `{"items":{"self_lead":false,"extra":1},"baseRevision":1}`} {
		rr = serve(m, http.MethodPut, "/api/hostconfig/relay", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, body)
	}
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.Revision)
}

// Init publishes the module under RelaySwitchesKey as the RelaySwitchReader
// the team module type-asserts (spec §8.7 (a)); the plan's mutation gate
// "drop RelaySwitchesKey from Init → red" lands here, since the handler
// tests build their Module without Init.
func TestInit_RegistersRelaySwitchReader(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(RelaySwitchesKey)
	require.True(t, ok, "Init must register under RelaySwitchesKey")
	reader, ok := svc.(RelaySwitchReader)
	require.True(t, ok, "registry value must be a RelaySwitchReader, got %T", svc)
	sw, err := reader.RelaySwitches()
	require.NoError(t, err)
	assert.Equal(t, DefaultRelaySwitches, sw)
}
