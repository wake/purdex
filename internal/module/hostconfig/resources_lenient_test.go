package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/resources"
)

// A write is strict, a read of the stored document is lenient (L2): when a setting is retired, the documents already stored
// with it must stay readable, and the next write drops it.

// A stored document with keys this binary does not know (as the retired lease keys will be) reads fine: the known keys keep
// their values, the others are ignored, on the reader and on the GET.
func TestResourcesSettings_StoredDocumentWithUnknownKeysIsRead(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"off","gap_s":45,"retired_a":1,"retired_b":{"x":[1,2]},"retired_c":"text"}`)

	s, err := m.ResourcesSettings()
	require.NoError(t, err)
	assert.Equal(t, "off", s.Mode)
	require.NotNil(t, s.GapS)
	assert.Equal(t, 45, *s.GapS)

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	var row struct {
		Items   map[string]json.RawMessage `json:"items"`
		Invalid bool                       `json:"invalid"`
	}
	require.NoError(t, json.Unmarshal(got["resources"], &row))
	assert.False(t, row.Invalid, "an unknown key does not make the row invalid")
	assert.JSONEq(t, `"off"`, string(row.Items["mode"]))
	assert.JSONEq(t, `45`, string(row.Items["gap_s"]))
	assert.NotContains(t, row.Items, "retired_a", "and it is not served back")
}

// The write stays strict, and the next write of a document that was read leniently stores it without the unknown keys.
func TestResourcesSettings_PutStaysStrictAndDropsWhatWasIgnored(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"off","retired_a":1}`)

	rr := serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"mode":"off","retired_a":1},"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code, "a PUT with an unknown key is refused")

	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"mode":"measure"},"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	e, err := m.store.Get(KeyResources)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(e.Value, &stored))
	assert.NotContains(t, stored, "retired_a", "the write stored the document without the key it did not know")
	assert.JSONEq(t, `"measure"`, string(stored["mode"]))
}

// Lenient is about keys, not values: a known key with a bad value is still an invalid row.
func TestResourcesSettings_LenientReadStillRefusesABadKnownValue(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"off","retired_a":1,"deadline_s":5}`)
	_, err := m.ResourcesSettings()
	assert.Error(t, err)
	_, err = normalizeStoredResources(json.RawMessage(`[]`))
	assert.Error(t, err, "and the document must still be an object")
}

// A document with every key the settings have today plus a stranger round-trips: read, written back by the PUT without the
// stranger, and read again as the same settings.
func TestResourcesSettings_RoundTripWithoutTheUnknownKeys(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyResources, resourcesDefaultItems[:len(resourcesDefaultItems)-1]+`,"future_key":9}`)
	s, err := m.ResourcesSettings()
	require.NoError(t, err)
	assert.Equal(t, resources.DefaultSettings(), s)

	b, err := json.Marshal(s)
	require.NoError(t, err)
	rr := serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":`+string(b)+`,"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	again, err := m.ResourcesSettings()
	require.NoError(t, err)
	assert.Equal(t, s, again)
	e, _ := m.store.Get(KeyResources)
	assert.NotContains(t, string(e.Value), "future_key")
}
