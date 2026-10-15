package hostconfig

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watchsettings"
)

var _ watchsettings.Reader = (*Module)(nil)

const watchOK = `{"enabled":true,"x_cores":2,"y_mb":2048,"t_s":30,"pressure_level":3,"pressure_t_s":60,"page_rate":100,"page_t_s":45}`

func TestWatch_DefaultsToOffAndRoundTrips(t *testing.T) {
	m := newTestModule(t)
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, watchsettings.Default(), s)
	assert.False(t, s.Enabled, "off until a host turns it on")

	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+watchOK+`,"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err = m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, watchsettings.Settings{Enabled: true, XCores: 2, YMB: 2048, TSeconds: 30, PressureLevel: 3, PressureTSeconds: 60, PageRate: 100, PageTSeconds: 45}, s)
}

func TestWatch_IsStrictAndBounded(t *testing.T) {
	m := newTestModule(t)
	bad := []string{
		`{}`, `[]`, `null`, `true`,
		strings.Replace(watchOK, `"enabled":true,`, ``, 1),               // a missing field is not "default"
		strings.Replace(watchOK, `"enabled":true`, `"enabled":"yes"`, 1), // wrong type
		strings.Replace(watchOK, `"y_mb":2048`, `"y_mb":2048.5`, 1),      // a fraction for an integer
		strings.Replace(watchOK, `"y_mb":2048`, `"y_mb":null`, 1),        // null is not a value
		strings.Replace(watchOK, `"x_cores":2`, `"x_cores":0`, 1),        // below the range
		strings.Replace(watchOK, `"x_cores":2`, `"x_cores":257`, 1),      // above
		strings.Replace(watchOK, `"t_s":30`, `"t_s":9`, 1),
		strings.Replace(watchOK, `"t_s":30`, `"t_s":3601`, 1),
		strings.Replace(watchOK, `"pressure_level":3`, `"pressure_level":5`, 1),
		strings.Replace(watchOK, `"page_rate":100`, `"page_rate":0`, 1),
		strings.Replace(watchOK, `"enabled":true`, `"enabled":true,"targets":[]`, 1), // W2a's keys are not accepted yet
		strings.Replace(watchOK, `"enabled":true`, `"enabled":true,"enabled":false`, 1),
		watchOK + `{}`,
	}
	for _, items := range bad {
		rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+items+`,"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, items)
	}
	e, err := m.store.Get(KeyWatch)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// A stored value that does not read is an error for the reader (the watcher stays off), and the GET shows it as invalid with
// the default items.
func TestWatch_AStoredBadValueIsAnErrorAndInvalidInTheGet(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyWatch, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(`{"enabled":true}`), nil })
	require.NoError(t, err)
	_, err = m.WatchSettings()
	assert.Error(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Contains(t, string(got["watch"]), `"invalid":true`)
	assert.Contains(t, string(got["watch"]), `"enabled":false`)
}
