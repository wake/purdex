package devicestate

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testClientID = "c_0123456789ab"
	testPayload  = `{"version":1,"workspaces":[{"id":"w1"},{"id":"w2"}],"tabs":{"t1":{"id":"t1"}},"tabOrder":["t1"]}`
)

func putBody(t *testing.T, name string, capturedAt int64, payload string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"deviceName": name,
		"appVersion": "1.0.0-alpha.1",
		"capturedAt": capturedAt,
		"payload":    json.RawMessage(payload),
	})
	require.NoError(t, err)
	return b
}

func decodeStored(t *testing.T, body []byte) bool {
	t.Helper()
	var resp struct {
		Stored *bool `json:"stored"`
	}
	require.NoError(t, json.Unmarshal(body, &resp))
	require.NotNil(t, resp.Stored)
	return *resp.Stored
}

func TestHandlerPutListGet(t *testing.T) {
	m := newTestModule(t)

	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, putBody(t, "  Air  ", 1000, testPayload))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.True(t, decodeStored(t, rr.Body.Bytes()))

	// List: summary only, no payload key.
	rr = serveBytes(m, http.MethodGet, "/api/device-state", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var list []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &list))
	require.Len(t, list, 1)
	_, hasPayload := list[0]["payload"]
	assert.False(t, hasPayload)
	assert.JSONEq(t, `"`+testClientID+`"`, string(list[0]["clientId"]))
	assert.JSONEq(t, `"Air"`, string(list[0]["deviceName"]))
	assert.JSONEq(t, `2`, string(list[0]["workspaceCount"]))
	assert.JSONEq(t, `1`, string(list[0]["tabCount"]))
	assert.JSONEq(t, `1000`, string(list[0]["capturedAt"]))

	// Get one: payload round-trips.
	rr = serveBytes(m, http.MethodGet, "/api/device-state/"+testClientID, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var one map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &one))
	assert.JSONEq(t, testPayload, string(one["payload"]))
	assert.JSONEq(t, `"1.0.0-alpha.1"`, string(one["appVersion"]))
}

func TestHandlerPutStoresRawPayloadBytes(t *testing.T) {
	m := newTestModule(t)
	raw := `{ "tabOrder": [], "version": 1, "tabs": {}, "workspaces": [] }`
	body := `{"deviceName":"Air","capturedAt":5,"payload":` + raw + `}`
	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, []byte(body))
	require.Equal(t, http.StatusOK, rr.Code)

	got, found, err := m.store.Get(testClientID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, raw, string(got.Payload), "payload stored byte-for-byte as received")
}

func TestHandlerPutStale(t *testing.T) {
	m := newTestModule(t)
	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, putBody(t, "New", 2000, testPayload))
	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, decodeStored(t, rr.Body.Bytes()))

	rr = serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, putBody(t, "Old", 1000, testPayload))
	require.Equal(t, http.StatusOK, rr.Code)
	assert.False(t, decodeStored(t, rr.Body.Bytes()))

	got, _, err := m.store.Get(testClientID)
	require.NoError(t, err)
	assert.Equal(t, "New", got.DeviceName)
}

func TestHandlerPutTooLarge(t *testing.T) {
	m := newTestModule(t)
	body := bytes.Repeat([]byte(" "), putBodyCap+1)
	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestHandlerPutAtCapNotTooLarge(t *testing.T) {
	m := newTestModule(t)
	valid := putBody(t, "Air", 1, testPayload)
	body := append(valid, bytes.Repeat([]byte(" "), putBodyCap-len(valid))...)
	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, body)
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func TestHandlerPutBadRequests(t *testing.T) {
	m := newTestModule(t)
	cases := map[string]struct {
		path string
		body string
	}{
		"malformed json":   {"/api/device-state/" + testClientID, `{"deviceName":`},
		"wrong type":       {"/api/device-state/" + testClientID, `{"deviceName":1,"capturedAt":1,"payload":` + testPayload + `}`},
		"bad clientId":     {"/api/device-state/c_0123456789AB", string(putBody(t, "Air", 1, testPayload))},
		"empty name":       {"/api/device-state/" + testClientID, string(putBody(t, "   ", 1, testPayload))},
		"long name":        {"/api/device-state/" + testClientID, string(putBody(t, strings.Repeat("機", 65), 1, testPayload))},
		"long appVersion":  {"/api/device-state/" + testClientID, `{"deviceName":"Air","appVersion":"` + strings.Repeat("a", 65) + `","capturedAt":1,"payload":` + testPayload + `}`},
		"capturedAt zero":  {"/api/device-state/" + testClientID, string(putBody(t, "Air", 0, testPayload))},
		"missing payload":  {"/api/device-state/" + testClientID, `{"deviceName":"Air","capturedAt":1}`},
		"null payload":     {"/api/device-state/" + testClientID, `{"deviceName":"Air","capturedAt":1,"payload":null}`},
		"array payload":    {"/api/device-state/" + testClientID, string(putBody(t, "Air", 1, `[]`))},
		"version 2":        {"/api/device-state/" + testClientID, string(putBody(t, "Air", 1, `{"version":2,"workspaces":[],"tabs":{},"tabOrder":[]}`))},
		"missing tabOrder": {"/api/device-state/" + testClientID, string(putBody(t, "Air", 1, `{"version":1,"workspaces":[],"tabs":{}}`))},
	}
	for name, tc := range cases {
		rr := serveBytes(m, http.MethodPut, tc.path, []byte(tc.body))
		assert.Equal(t, http.StatusBadRequest, rr.Code, name)
	}

	rr := serveBytes(m, http.MethodGet, "/api/device-state", nil)
	assert.JSONEq(t, `[]`, rr.Body.String(), "no partial writes")
}

func TestHandlerListEmpty(t *testing.T) {
	m := newTestModule(t)
	rr := serveBytes(m, http.MethodGet, "/api/device-state", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "[]", strings.TrimSpace(rr.Body.String()))
}

func TestHandlerGetNotFound(t *testing.T) {
	m := newTestModule(t)
	rr := serveBytes(m, http.MethodGet, "/api/device-state/c_ffffffffffff", nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandlerGetBadClientID(t *testing.T) {
	m := newTestModule(t)
	rr := serveBytes(m, http.MethodGet, "/api/device-state/nope", nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	rr = serveBytes(m, http.MethodDelete, "/api/device-state/nope", nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlerDelete(t *testing.T) {
	m := newTestModule(t)
	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, putBody(t, "Air", 1, testPayload))
	require.Equal(t, http.StatusOK, rr.Code)

	rr = serveBytes(m, http.MethodDelete, "/api/device-state/"+testClientID, nil)
	assert.Equal(t, http.StatusNoContent, rr.Code)
	rr = serveBytes(m, http.MethodGet, "/api/device-state/"+testClientID, nil)
	assert.Equal(t, http.StatusNotFound, rr.Code)

	// Missing row is also 204.
	rr = serveBytes(m, http.MethodDelete, "/api/device-state/"+testClientID, nil)
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandlerStoreErrors500(t *testing.T) {
	m := newTestModule(t)
	require.NoError(t, m.store.Close())

	rr := serveBytes(m, http.MethodPut, "/api/device-state/"+testClientID, putBody(t, "Air", 1, testPayload))
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	rr = serveBytes(m, http.MethodGet, "/api/device-state", nil)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	rr = serveBytes(m, http.MethodGet, "/api/device-state/"+testClientID, nil)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	rr = serveBytes(m, http.MethodDelete, "/api/device-state/"+testClientID, nil)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}
