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

// 9c-0 (#3892): the detector fields, patrol_idle_min and auto_wake may be left out of a PUT of the watch row, and then keep the values the row
// has. The Mac app stops writing the detector fields back when its screens for them go (MON-11); until the detectors are removed from the
// store, a PUT without them must not be refused and must not reset them.

// nonDefault is a row whose every optional-now field differs from its default, so a reset to the default shows.
const nonDefault = `{"enabled":true,"x_cores":3.5,"y_mb":4096,"t_s":90,"pressure_level":4,"pressure_t_s":120,"page_rate":250,"page_t_s":75,` +
	`"targets":["a26","mlab"],"mode":"resident","patrol_idle_min":45,"patrol_present_min":20,"patrol_busy_min":8,"patrol_calm_min":12,"patrol_rest_min":6,` +
	`"disk_low_gb":33,"auto_wake":false,"agent_command":"claude --model sonnet","workdir":"/srv/watch","start_prompt":"go","wake_prompt":"wake"}`

func putWatch(t *testing.T, m *Module, items string, rev int) int {
	t.Helper()
	return serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+items+`,"baseRevision":`+string(rune('0'+rev))+`}`).Code
}

func TestWatchPut_LeftOutDetectorFieldsKeepTheStoredValues(t *testing.T) {
	m := newTestModule(t)
	require.Equal(t, http.StatusOK, putWatch(t, m, nonDefault, 0))
	before, err := m.WatchSettings()
	require.NoError(t, err)
	require.NotEqual(t, watchsettings.Default().XCores, before.XCores, "the row is not the default")

	// what the app writes once its detector screens are gone: the switch and the delivery fields, nothing else of the detectors
	rest := `{"enabled":false,"targets":["a26","mlab"],"mode":"resident","patrol_present_min":20,"patrol_busy_min":8,"patrol_calm_min":12,"patrol_rest_min":6,` +
		`"disk_low_gb":33,"agent_command":"claude --model sonnet","workdir":"/srv/watch","start_prompt":"go","wake_prompt":"wake"}`
	require.Equal(t, http.StatusOK, putWatch(t, m, rest, 1))

	after, err := m.WatchSettings()
	require.NoError(t, err)
	want := before
	want.Enabled = false // the one thing the body changed
	assert.Equal(t, want, after, "the seven detector fields, patrol_idle_min and auto_wake are what they were")
	assert.Equal(t, 45, after.PatrolIdleMin, "patrol_idle_min left out is not reset to its default")
	assert.False(t, after.AutoWake, "auto_wake left out is not reset to its default (on)")
}

func TestWatchPut_OnlyEnabledOnANeverWrittenRowIsTheDefaults(t *testing.T) {
	m := newTestModule(t)
	require.Equal(t, http.StatusOK, putWatch(t, m, `{"enabled":false}`, 0))
	s, err := m.WatchSettings()
	require.NoError(t, err)
	want := watchsettings.Default()
	want.Enabled = false
	assert.Equal(t, want, s)
}

func TestWatchPut_SendingEverythingIsAsItWas(t *testing.T) {
	m := newTestModule(t)
	require.Equal(t, http.StatusOK, putWatch(t, m, nonDefault, 0))
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, 3.5, s.XCores)
	assert.Equal(t, 4096, s.YMB)
	assert.Equal(t, 90, s.TSeconds)
	assert.Equal(t, 4, s.PressureLevel)
	assert.Equal(t, 120, s.PressureTSeconds)
	assert.Equal(t, 250.0, s.PageRate)
	assert.Equal(t, 75, s.PageTSeconds)
	assert.Equal(t, 45, s.PatrolIdleMin)
	assert.False(t, s.AutoWake)
	// a second PUT that names every detector field replaces them
	changed := strings.Replace(nonDefault, `"x_cores":3.5`, `"x_cores":1.25`, 1)
	require.Equal(t, http.StatusOK, putWatch(t, m, changed, 1))
	s, err = m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, 1.25, s.XCores)
}

func TestWatchPut_WhatIsSentIsStillChecked(t *testing.T) {
	m := newTestModule(t)
	for name, body := range map[string]string{
		"enabled is still required":      `{"x_cores":2}`,
		"an empty object":                `{}`,
		"a detector out of range":        `{"enabled":true,"x_cores":0}`,
		"a detector the wrong type":      `{"enabled":true,"y_mb":2048.5}`,
		"a detector sent as null":        `{"enabled":true,"y_mb":null}`,
		"every detector null":            `{"enabled":true,"x_cores":null,"y_mb":null,"t_s":null,"pressure_level":null,"pressure_t_s":null,"page_rate":null,"page_t_s":null}`,
		"null in another case":           `{"enabled":true,"X_CORES":null}`,
		"null after a value, other case": `{"enabled":true,"x_cores":2,"X_Cores":null}`,
		"patrol idle out of range":       `{"enabled":true,"patrol_idle_min":0}`,
		"an unknown field":               `{"enabled":true,"bogus":1}`,
		"a duplicate key":                `{"enabled":true,"enabled":false}`,
		"an unknown field next to valid": `{"enabled":true,"x_cores":2,"nope":true}`,
	} {
		assert.Equal(t, http.StatusBadRequest, putWatch(t, m, body, 0), name)
	}
	e, err := m.store.Get(KeyWatch)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// The reader is not loosened (codex R2, ruled by lead): a stored row that lacks a detector field does not read whole, so the GET marks it
// invalid and shows the defaults, and WatchSettings is an error (the watcher stays off, watchsettings.Reader). Only a PUT may leave them out,
// and the row it stores is whole.
func TestWatchGet_AStoredRowWithoutTheDetectorFieldsIsInvalidAndTheReaderErrs(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyWatch, 0, func(Entry) (json.RawMessage, error) {
		return json.RawMessage(`{"enabled":true,"targets":["a26"],"mode":"resident"}`), nil
	})
	require.NoError(t, err)

	_, err = m.WatchSettings()
	assert.Error(t, err, "an enabled row that is not whole does not start the watcher")

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Contains(t, string(got["watch"]), `"invalid":true`)
}

// And a stored row that has every field but one out of its range is invalid as before (the reader checks the ranges of what a row has).
func TestWatchGet_AStoredRowWithAFieldOutOfRangeIsInvalidAndTheReaderErrs(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyWatch, 0, func(Entry) (json.RawMessage, error) {
		return json.RawMessage(strings.Replace(nonDefault, `"x_cores":3.5`, `"x_cores":0`, 1)), nil
	})
	require.NoError(t, err)
	_, err = m.WatchSettings()
	assert.Error(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	assert.Contains(t, rr.Body.String(), `"invalid":true`)
}

func TestWatchPut_ALeftOutBodyStoresAWholeRow(t *testing.T) {
	m := newTestModule(t)
	require.Equal(t, http.StatusOK, putWatch(t, m, `{"enabled":true}`, 0))
	e, err := m.store.Get(KeyWatch)
	require.NoError(t, err)
	var row map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(e.Value, &row))
	for _, k := range []string{"x_cores", "y_mb", "t_s", "pressure_level", "pressure_t_s", "page_rate", "page_t_s", "patrol_idle_min", "auto_wake"} {
		assert.Contains(t, row, k, "the stored row has "+k)
	}
	_, err = m.WatchSettings()
	assert.NoError(t, err, "and it reads")
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	assert.NotContains(t, rr.Body.String(), `"invalid"`)
}
