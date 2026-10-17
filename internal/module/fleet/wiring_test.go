package fleetmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/fleet"
)

// The module mounts the measuring side's routes next to the peer routes, and feeds the engine the aggregator's newest age.
func TestModule_MountsTheMeasuringSide(t *testing.T) {
	r := newRig(t)
	require.NotNil(t, r.m.measure)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fleet", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Contains(t, []any{"primary", "standby", "off"}, got["role"])

	rec = httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/fleet/ssh-aliases", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// The switch: PDX_FLEET_SOURCE=pool gives the measuring side the pool as its source; anything else leaves it as it was.
func TestModule_PoolSourceOnlyWhenTheSwitchSaysSo(t *testing.T) {
	t.Setenv(SourceEnv, "ssh")
	assert.Nil(t, newRig(t).m.measure.d.Pool, "the default keeps the SSH and local sources")
	t.Setenv(SourceEnv, "pool")
	assert.NotNil(t, newRig(t).m.measure.d.Pool)
}

// The newest age the engine reports is the measuring side's: once the aggregator has read anything, the role answer carries it.
func TestModule_EngineReadsTheAggregatorsNewestAge(t *testing.T) {
	r := newRig(t)
	body := `{"to_host_id":"` + ourID + `","from_alias":"mlab","role":"standby","term":0}`
	require.Contains(t, r.post(pathRole, hostP, body).Body.String(), `"newest_age_ms":null`, "nothing read yet")

	r.m.measure.mu.Lock()
	r.m.measure.noteReadingLocked(time.Now())
	r.m.measure.mu.Unlock()
	assert.Regexp(t, `"newest_age_ms":\d+`, r.post(pathRole, hostP, body).Body.String())
}

// initWith is a Module initialised against a registry that holds local (nothing when nil).
func initWith(t *testing.T, local fleet.LocalSource) *Module {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), HostID: ourID}
	c := core.New(core.CoreDeps{Config: cfg, Registry: core.NewServiceRegistry()})
	if local != nil {
		c.Registry.Register(fleet.LocalSourceKey, local)
	}
	m := New()
	m.logf = func(string, ...any) {}
	m.settings = &fakeSettings{set: Settings{Order: []string{"air26", "mlab"}, Enabled: true, Self: "air26"}}
	m.statePath = t.TempDir() + "/state.json"
	m.caller = &fakeCaller{ids: map[string]string{}}
	require.NoError(t, m.Init(c))
	return m
}

// A registered LocalSource is the one both the module and its measuring side use; without one an empty FakeLocal stands in.
func TestModule_LocalSourceFromTheRegistryElseFakeLocal(t *testing.T) {
	mine := fleet.NewFakeLocal()
	m := initWith(t, mine)
	assert.Same(t, mine, m.local)
	assert.Same(t, mine, m.measure.d.Local)

	m = initWith(t, nil)
	assert.IsType(t, &fleet.FakeLocal{}, m.local)
	assert.Same(t, m.local, m.measure.d.Local)
}
