package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/resources"
)

// A write is strict; a read of the stored document ignores the keys that were RETIRED, and only those (L2): when a setting is
// retired, the documents already stored with it stay readable and the next write drops it, while a typo or a key a newer binary
// wrote is still refused (reading it as "left out" would turn `{"mdoe":"off"}` into the default mode, and a GET→PUT would delete
// the newer setting).

// retire makes key a retired one for the test, as L3 will for the lease's keys.
func retire(t *testing.T, key string) {
	t.Helper()
	old := retiredResourcesKeys
	retiredResourcesKeys = append(append([]string{}, old...), key)
	t.Cleanup(func() { retiredResourcesKeys = old })
}

// A stored document with a retired key reads fine: the known keys keep their values, the retired one is ignored and not served
// back, on the reader and on the GET.
func TestResourcesSettings_StoredDocumentWithARetiredKeyIsRead(t *testing.T) {
	retire(t, "retired_a")
	retire(t, "retired_b")
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"off","gap_s":45,"retired_a":1,"retired_b":{"x":[1,2]}}`)

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
	assert.False(t, row.Invalid, "a retired key does not make the row invalid")
	assert.JSONEq(t, `"off"`, string(row.Items["mode"]))
	assert.JSONEq(t, `45`, string(row.Items["gap_s"]))
	assert.NotContains(t, row.Items, "retired_a")
}

// A key that is not retired and not known is refused on a read too: a typo is not "left out = default", and a setting of a newer
// binary is not silently dropped by the next GET→PUT.
func TestResourcesSettings_StoredTypoOrFutureKeyIsInvalid(t *testing.T) {
	m := newTestModule(t)
	for _, doc := range []string{`{"mdoe":"off"}`, `{"mode":"off","future_key":9}`, `{"deadline":60}`} {
		putRow2(t, m, doc)
		_, err := m.ResourcesSettings()
		assert.Error(t, err, doc)
		rr := serve(m, http.MethodGet, "/api/hostconfig", "")
		require.Equal(t, http.StatusOK, rr.Code)
		var got map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
		var row struct {
			Items   map[string]json.RawMessage `json:"items"`
			Invalid bool                       `json:"invalid"`
		}
		require.NoError(t, json.Unmarshal(got["resources"], &row))
		assert.True(t, row.Invalid, doc)
		assert.Empty(t, row.Items, doc)
	}
}

// putRow2 replaces the stored row whatever its revision is.
func putRow2(t *testing.T, m *Module, value string) {
	t.Helper()
	e, err := m.store.Get(KeyResources)
	require.NoError(t, err)
	_, stored, err := m.store.Put(KeyResources, e.Revision, raw(value))
	require.NoError(t, err)
	require.True(t, stored)
}

// The write stays strict even for a retired key (it is gone: the document is stored without it), and the next write of a
// document read leniently stores it without the retired key.
func TestResourcesSettings_PutStaysStrictAndDropsTheRetiredKey(t *testing.T) {
	retire(t, "retired_a")
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"off","retired_a":1}`)

	rr := serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"mode":"off","retired_a":1},"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code, "a PUT with a retired key is refused")

	rr = serve(m, http.MethodPut, "/api/hostconfig/resources", `{"items":{"mode":"measure"},"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	e, err := m.store.Get(KeyResources)
	require.NoError(t, err)
	var stored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(e.Value, &stored))
	assert.NotContains(t, stored, "retired_a", "the write stored the document without the retired key")
	assert.JSONEq(t, `"measure"`, string(stored["mode"]))
}

// Lenient is about retired keys, not values: a known key with a bad value is still an invalid row, and so is a document that is
// not an object.
func TestResourcesSettings_LenientReadStillRefusesABadKnownValue(t *testing.T) {
	retire(t, "retired_a")
	m := newTestModule(t)
	putRow(t, m, KeyResources, `{"mode":"off","retired_a":1,"deadline_s":5}`)
	_, err := m.ResourcesSettings()
	assert.Error(t, err)
	_, err = normalizeStoredResources(json.RawMessage(`[]`))
	assert.Error(t, err)
}

// A document with every key the settings have today plus a retired one round-trips: read, written back by the PUT without
// the retired one, and read again as the same settings.
func TestResourcesSettings_RoundTripWithoutTheRetiredKey(t *testing.T) {
	retire(t, "future_key")
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

// The list names the lease's keys that L3 retires: each is either known now (parsed) or ignored (retired) and never refused.
func TestResourcesSettings_EveryRetiredKeyIsReadWhateverItsStatus(t *testing.T) {
	for _, k := range retiredResourcesKeys {
		_, err := normalizeStoredResources(json.RawMessage(`{"` + k + `":null}`))
		if err != nil {
			assert.Contains(t, err.Error(), k, "if it is refused it is for its value, not as an unknown key")
			assert.NotContains(t, err.Error(), "unknown resources field", k)
		}
	}
}
