package fleetmod

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
