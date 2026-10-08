package hostconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/resources"
)

const resourcesDefaultItems = `{"mode":"lease","kinds":{"build":35,"lint-full":10,"test-full":35,"test-pkg":15},` +
	`"deadline_s":300,"warmup_s":20,"floor_pct":50,"max_hold_s":3600,"ewma_half_life_s":15}`

// A host that never wrote the key reads the defaults, on the GET and through
// the reader; a body that leaves everything out stores them filled in.
func TestResourcesSettings_Defaults(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":`+resourcesDefaultItems+`,"revision":0}`, string(got["resources"]))

	s, err := m.ResourcesSettings()
	require.NoError(t, err)
	assert.Equal(t, resources.DefaultSettings(), s)

	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":`+resourcesDefaultItems+`,"revision":1}`, rr.Body.String())
}

// Strict, like relay: a misspelt field must not save as "left out = default".
func TestResourcesSettings_RejectsUnknownField(t *testing.T) {
	m := newTestModule(t)
	for _, items := range []string{
		`{"mdoe":"off"}`, `{"mode":"off","extra":1}`, `{"deadline":60}`, `{"kinds":{"build":35},"Kinds":{}}`,
		`[]`, `"lease"`, `null`,
	} {
		_, err := normalizeResources(json.RawMessage(items))
		assert.Error(t, err, items)
		rr := serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":`+items+`,"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, items)
	}
	e, err := m.store.Get(KeyResources)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// Every field at, just inside and just outside its range; the error names the
// field. null, strings and fractions are refused, never read as "unset".
func TestResourcesSettings_Ranges(t *testing.T) {
	ints := []struct {
		field    string
		lo, hi   int
		defValue int
	}{
		{"deadline_s", 10, 590, 300},
		{"warmup_s", 0, 300, 20},
		{"floor_pct", 0, 100, 50},
		{"max_hold_s", 60, 86400, 3600},
		{"ewma_half_life_s", 5, 300, 15},
	}
	for _, f := range ints {
		for _, ok := range []int{f.lo, f.hi, f.defValue} {
			raw, _ := json.Marshal(map[string]int{f.field: ok})
			s, err := normalizeResources(raw)
			require.NoError(t, err, string(raw))
			b, _ := json.Marshal(s)
			var back map[string]any
			require.NoError(t, json.Unmarshal(b, &back))
			assert.EqualValues(t, ok, back[f.field], string(raw))
		}
		for _, bad := range []int{f.lo - 1, f.hi + 1} {
			raw, _ := json.Marshal(map[string]int{f.field: bad})
			_, err := normalizeResources(raw)
			require.Error(t, err, string(raw))
			assert.Contains(t, err.Error(), f.field)
		}
		for _, bad := range []string{`null`, `"60"`, `60.5`, `true`, `[60]`} {
			_, err := normalizeResources(json.RawMessage(`{"` + f.field + `":` + bad + `}`))
			require.Error(t, err, f.field+bad)
			assert.Contains(t, err.Error(), f.field)
		}
	}

	for _, mode := range []string{"off", "measure", "advise", "lease"} {
		s, err := normalizeResources(json.RawMessage(`{"mode":"` + mode + `"}`))
		require.NoError(t, err, mode)
		assert.Equal(t, mode, s.Mode)
	}
	for _, bad := range []string{`"on"`, `""`, `"Lease"`, `null`, `1`} {
		_, err := normalizeResources(json.RawMessage(`{"mode":` + bad + `}`))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "mode")
	}

	// kinds: weights 1..100, merged over the built-ins; custom names allowed.
	s, err := normalizeResources(json.RawMessage(`{"kinds":{"build":1,"test-pkg":100,"my-kind":7}}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"test-full": 35, "build": 1, "test-pkg": 100, "lint-full": 10, "my-kind": 7}, s.Kinds)
	for _, bad := range []string{
		`{"build":0}`, `{"build":101}`, `{"build":-1}`, `{"build":null}`, `{"build":"35"}`, `{"build":35.5}`,
		`{"Bad Name":5}`, `{"":5}`, `null`, `[]`, `"x"`,
	} {
		_, err := normalizeResources(json.RawMessage(`{"kinds":` + bad + `}`))
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "kinds", bad)
	}
}

// PUT then GET: the revision counts up, a stale base is a 409 with the server
// copy, a duplicate JSON key is a 400, and the reader answers what was stored.
func TestResourcesSettings_PutGetRoundTrip(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/resources",
		`{"items":{"mode":"advise","warmup_s":0,"kinds":{"build":20,"mine":5}},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	const stored = `{"mode":"advise","kinds":{"build":20,"lint-full":10,"mine":5,"test-full":35,"test-pkg":15},` +
		`"deadline_s":300,"warmup_s":0,"floor_pct":50,"max_hold_s":3600,"ewma_half_life_s":15}`
	assert.JSONEq(t, `{"items":`+stored+`,"revision":1}`, rr.Body.String())

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":`+stored+`,"revision":1}`, string(got["resources"]))

	s, err := m.ResourcesSettings()
	require.NoError(t, err)
	assert.Equal(t, "advise", s.Mode)
	assert.Equal(t, 0, *s.WarmupS, "an explicit 0 survives")
	assert.Equal(t, 20, s.Kinds["build"])

	// What the GET answers goes back in unchanged.
	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":`+stored+`,"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":`+stored+`,"revision":2}`, rr.Body.String())

	// Stale: 409 with the server copy, even for a payload that is invalid.
	for _, items := range []string{`{"mode":"off"}`, `{"nope":1}`} {
		rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":`+items+`,"baseRevision":1}`)
		require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
		assert.JSONEq(t, `{"items":`+stored+`,"revision":2}`, rr.Body.String())
	}

	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"mode":"off","mode":"lease"},"baseRevision":2}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"kinds":{"build":1,"build":2}},"baseRevision":2}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"deadline_s":5},"baseRevision":2}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "deadline_s")
}

// A stored value that no longer validates (written around the PUT) is an
// error from the reader and invalid, answering {}, on the GET.
func TestResourcesSettings_StoredGarbageIsAnError(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"lease","deadline_s":5}`)
	_, err := m.ResourcesSettings()
	assert.Error(t, err)

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{},"revision":1,"invalid":true}`, string(got["resources"]))
}

// Init publishes the module under resources.SettingsKey as the reader the
// resources module type-asserts; the handler tests build their Module without
// Init, so dropping the registration goes red here.
func TestInit_RegistersResourcesSettingsReader(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(resources.SettingsKey)
	require.True(t, ok, "Init must register under resources.SettingsKey")
	reader, ok := svc.(resources.SettingsReader)
	require.True(t, ok, "registry value must be a resources.SettingsReader, got %T", svc)
	s, err := reader.ResourcesSettings()
	require.NoError(t, err)
	assert.Equal(t, resources.DefaultSettings(), s)
}
