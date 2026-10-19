package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/alertsettings"
)

var _ alertsettings.Reader = (*Module)(nil)

// WWD-8a-2a-0: the one switch of the wwd alert delivery, push (default false: the alerts are written and gathered, the phone is not
// pushed until a person turns this on).
func TestWwdAlerts_DefaultsToOffAndRoundTrips(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"push":false},"revision":0}`, string(got["wwdAlerts"]))
	s, err := m.WwdAlertsSettings()
	require.NoError(t, err)
	assert.False(t, s.Push, "a never-written key answers off")

	rr = serve(m, http.MethodPut, "/api/hostconfig/wwd_alerts", `{"items":{"push":true},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"push":true},"revision":1}`, rr.Body.String())
	s, err = m.WwdAlertsSettings()
	require.NoError(t, err)
	assert.True(t, s.Push)

	rr = serve(m, http.MethodPut, "/api/hostconfig/wwd_alerts", `{"items":{"push":false},"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err = m.WwdAlertsSettings()
	require.NoError(t, err)
	assert.False(t, s.Push)
}

// Strict: exactly {"push": <bool>}; a save never means "left out = default" and a misspelt field is refused.
func TestWwdAlerts_IsStrict(t *testing.T) {
	m := newTestModule(t)
	for _, items := range []string{`{}`, `{"push":null}`, `{"push":"true"}`, `{"push":1}`, `{"push":true,"digest":true}`, `{"Push":true}`,
		`{"push":true,"push":false}`, `[]`, `null`, `true`} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/wwd_alerts", `{"items":`+items+`,"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, items)
	}
	e, err := m.store.Get(KeyWwdAlerts)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// A stored value that does not read is an error for the reader (the delivery treats it as off) and shows in the GET as invalid with
// the default items; a duplicate member in a stored value is invalid, not "the last one wins".
func TestWwdAlerts_AStoredGarbageValueIsAnError(t *testing.T) {
	for _, stored := range []string{`{"push":"yes"}`, `{"push":true,"push":false}`, `{"push":true,"x":1}`} {
		m := newTestModule(t)
		_, _, err := m.store.Put(KeyWwdAlerts, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(stored), nil })
		require.NoError(t, err)
		_, err = m.WwdAlertsSettings()
		assert.Error(t, err, stored)
		rr := serve(m, http.MethodGet, "/api/hostconfig", "")
		var got map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
		assert.JSONEq(t, `{"items":{"push":false},"revision":1,"invalid":true}`, string(got["wwdAlerts"]), stored)
	}
}
