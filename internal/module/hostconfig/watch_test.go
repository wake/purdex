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

func TestWatch_DefaultsToOnAndRoundTrips(t *testing.T) {
	m := newTestModule(t)
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, watchsettings.Default(), s)
	assert.True(t, s.Enabled, "on by default (spec §12.2)")

	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+watchOK+`,"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err = m.WatchSettings()
	require.NoError(t, err)
	want := watchsettings.Default()
	want.XCores, want.YMB, want.TSeconds, want.PressureLevel, want.PressureTSeconds, want.PageRate, want.PageTSeconds = 2, 2048, 30, 3, 60, 100, 45
	assert.Equal(t, want, s, "the delivery fields left out read as their defaults")
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
		strings.Replace(watchOK, `"enabled":true`, `"enabled":true,"bogus":1`, 1), // an unknown key
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
	assert.Contains(t, string(got["watch"]), `"enabled":true`)
}

const watchDelivery = `,"targets":["a26","mlab"],"mode":"resident","patrol_idle_min":30,"auto_wake":false,"model":"sonnet","workdir":"/srv/watch","start_prompt":"go\nnow","wake_prompt":"wake"`

func TestWatch_TheDeliveryFieldsRoundTrip(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+strings.TrimSuffix(watchOK, "}")+watchDelivery+`},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, []string{"a26", "mlab"}, s.Targets)
	assert.Equal(t, watchsettings.ModeResident, s.Mode)
	assert.Equal(t, 30, s.PatrolIdleMin)
	assert.False(t, s.AutoWake, "false is a value, not a left-out field")
	assert.Equal(t, "sonnet", s.Model)
	assert.Equal(t, "/srv/watch", s.Workdir)
	assert.Equal(t, "go\nnow", s.StartPrompt)
	assert.Equal(t, "wake", s.WakePrompt)
}

func TestWatch_TheDeliveryFieldsAreValidated(t *testing.T) {
	m := newTestModule(t)
	long := strings.Repeat("x", 9000)
	for name, extra := range map[string]string{
		"no targets":        `,"targets":[]`,
		"too many targets":  `,"targets":["a","b","c","d","e","f","g","h","i"]`,
		"a duplicate":       `,"targets":["mlab","MLAB"]`,
		"not an alias":      `,"targets":["a b"]`,
		"a path as alias":   `,"targets":["../x"]`,
		"null targets list": `,"targets":[null]`,
		"a bad mode":        `,"mode":"always"`,
		"patrol idle zero":  `,"patrol_idle_min":0`,
		"patrol idle huge":  `,"patrol_idle_min":1441`,
		"a bad model":       `,"model":"bad model"`,
		"an empty model":    `,"model":""`,
		"relative workdir":  `,"workdir":"work"`,
		"workdir with ctl":  `,"workdir":"/a\u0007b"`,
		"auto_wake string":  `,"auto_wake":"yes"`,
		"prompt too long":   `,"start_prompt":"` + long + `"`,
		"prompt with ctl":   `,"wake_prompt":"a\u001bb"`,
	} {
		body := `{"items":` + strings.TrimSuffix(watchOK, "}") + extra + `},"baseRevision":0}`
		rr := serve(m, http.MethodPut, "/api/hostconfig/watch", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, name+": "+rr.Body.String())
	}
	e, err := m.store.Get(KeyWatch)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// targets are held to the rule a paired host's alias is held to, whatever its length there allows.
func TestWatch_TargetsFollowTheConfigsAliasRule(t *testing.T) {
	m := newTestModule(t)
	long := strings.Repeat("a", 64) // config's aliasPattern allows 64
	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+strings.TrimSuffix(watchOK, "}")+`,"targets":["`+long+`"]},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+strings.TrimSuffix(watchOK, "}")+`,"targets":["`+long+`a"]},"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code, "65 is too long")
	rr = serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+strings.TrimSuffix(watchOK, "}")+`,"targets":[".."]},"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code, "a reserved alias")
}
