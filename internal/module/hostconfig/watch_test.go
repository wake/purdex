package hostconfig

import (
	"encoding/json"
	"net/http"
	"strconv"
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

const watchDelivery = `,"targets":["a26","mlab"],"mode":"resident","patrol_idle_min":30,"auto_wake":false,"agent_command":"claude --model sonnet","workdir":"/srv/watch","start_prompt":"go now","wake_prompt":"wake"`

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
	assert.Equal(t, "claude --model sonnet", s.AgentCommand)
	assert.Equal(t, "/srv/watch", s.Workdir)
	assert.Equal(t, "go now", s.StartPrompt)
	assert.Equal(t, "wake", s.WakePrompt)
}

func TestWatch_TheDeliveryFieldsAreValidated(t *testing.T) {
	m := newTestModule(t)
	long := strings.Repeat("x", 4097)
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
		"patrol present 0":  `,"patrol_present_min":0`,
		"patrol busy huge":  `,"patrol_busy_min":1441`,
		"patrol calm 0":     `,"patrol_calm_min":0`,
		"patrol rest 0":     `,"patrol_rest_min":0`,
		"disk low 0":        `,"disk_low_gb":0`,
		"a bad model":       `,"model":"bad model"`,
		"an empty model":    `,"model":""`,
		"a multi-line cmd":  `,"agent_command":"claude\nrm -rf x"`,
		"a cmd with ESC":    `,"agent_command":"claude \u001b"`,
		"a cmd with NUL":    `,"agent_command":"claude \u0000"`,
		"an empty cmd":      `,"agent_command":""`,
		"a cmd too long":    `,"agent_command":"` + strings.Repeat("c", 513) + `"`,
		"a newline prompt":  `,"start_prompt":"a\nb"`,
		"a tab prompt":      `,"wake_prompt":"a\tb"`,
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

// The defaults of spec §12.2 (user decision 10-15): air26 first, then mlab; the mlab repo's checkout dedicated to the watch branch.
func TestWatch_TheDefaultsAreTheSpecs(t *testing.T) {
	d := watchsettings.Default()
	assert.Equal(t, []string{"air26", "mlab"}, d.Targets)
	assert.Equal(t, "~/Workspace/wake/mlab-purdex-watch", d.Workdir)
	assert.Equal(t, watchsettings.ModePatrol, d.Mode)
	assert.Equal(t, 120, d.PatrolIdleMin)
	assert.Equal(t, []int{30, 10, 15, 10, 14}, []int{d.PatrolPresentMin, d.PatrolBusyMin, d.PatrolCalmMin, d.PatrolRestMin, d.DiskLowGB}, "present / busy / calm / rest minutes, disk GB (plan W3b-5)")
	assert.True(t, d.AutoWake)
	assert.Equal(t, "claude --dangerously-skip-permissions --model opus", d.AgentCommand)
}

// Spec §12.2 `agent_command`: the old spelling `model` is read as the command it stood for; a write with both is refused; what
// is stored is always agent_command; the GET answers agent_command and no model.
func TestWatch_ModelIsTheOldSpellingOfAgentCommand(t *testing.T) {
	const legacy = "claude --dangerously-skip-permissions --model sonnet"
	m := newTestModule(t)
	body := func(extra string, rev int) string {
		return `{"items":` + strings.TrimSuffix(watchOK, "}") + extra + `},"baseRevision":` + strconv.Itoa(rev) + `}`
	}

	// a PUT with model only (an older client's read-modify-write) is stored as agent_command
	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", body(`,"model":"sonnet"`, 0))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	e, err := m.store.Get(KeyWatch)
	require.NoError(t, err)
	assert.Contains(t, string(e.Value), `"agent_command":"`+legacy+`"`)
	assert.NotContains(t, string(e.Value), `"model"`, "what is stored is always agent_command")
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, legacy, s.AgentCommand)

	// both → 400, nothing stored
	rr = serve(m, http.MethodPut, "/api/hostconfig/watch", body(`,"model":"opus","agent_command":"claude"`, 1))
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	e, err = m.store.Get(KeyWatch)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.Revision)

	// a row written by 741–744 (model, no agent_command) reads as the command, and a restart reads what is stored
	m2 := newTestModule(t)
	_, _, err = m2.store.Put(KeyWatch, 0, func(Entry) (json.RawMessage, error) {
		return json.RawMessage(strings.TrimSuffix(watchOK, "}") + `,"model":"haiku"}`), nil
	})
	require.NoError(t, err)
	s, err = m2.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, "claude --dangerously-skip-permissions --model haiku", s.AgentCommand)

	// the GET answers agent_command and no model; its items PUT back unchanged
	rr = serve(m2, http.MethodGet, "/api/hostconfig", "")
	var got struct {
		Watch struct {
			Items    map[string]any `json:"items"`
			Revision int            `json:"revision"`
		} `json:"watch"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	items := got.Watch.Items
	assert.Equal(t, "claude --dangerously-skip-permissions --model haiku", items["agent_command"])
	_, hasModel := items["model"]
	assert.False(t, hasModel)
	put, _ := json.Marshal(map[string]any{"items": items, "baseRevision": got.Watch.Revision})
	rr = serve(m2, http.MethodPut, "/api/hostconfig/watch", string(put))
	assert.Equal(t, http.StatusOK, rr.Code, "GET → PUT unchanged round trips: "+rr.Body.String())
}

// An invalid UTF-8 byte in the command or a prompt is refused, not replaced by U+FFFD and stored: the JSON decode alone would
// have changed it before any check on the string saw it.
func TestWatch_InvalidUTF8IsRefusedNotRewritten(t *testing.T) {
	m := newTestModule(t)
	// interpreted Go strings: \xff here is the byte 0xff itself, not a JSON escape
	for name, extra := range map[string]string{
		"agent_command": ",\"agent_command\":\"claude \xff\"",
		"start_prompt":  ",\"start_prompt\":\"a\xffb\"",
		"wake_prompt":   ",\"wake_prompt\":\"a\xc3(\"",
	} {
		body := `{"items":` + strings.TrimSuffix(watchOK, "}") + extra + `},"baseRevision":0}`
		rr := serve(m, http.MethodPut, "/api/hostconfig/watch", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, name+": "+rr.Body.String())
	}
	e, err := m.store.Get(KeyWatch)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// The patrol numbers round-trip, and a client that does not send them (an older App) keeps the defaults rather than failing.
func TestWatch_ThePatrolNumbersRoundTripAndDefaultWhenLeftOut(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+strings.TrimSuffix(watchOK, "}")+`,"patrol_present_min":45,"patrol_busy_min":5,"patrol_calm_min":20,"patrol_rest_min":30,"disk_low_gb":20},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, []int{45, 5, 20, 30, 20}, []int{s.PatrolPresentMin, s.PatrolBusyMin, s.PatrolCalmMin, s.PatrolRestMin, s.DiskLowGB})

	m2 := newTestModule(t)
	rr = serve(m2, http.MethodPut, "/api/hostconfig/watch", `{"items":`+watchOK+`,"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err = m2.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, []int{30, 10, 15, 10, 14}, []int{s.PatrolPresentMin, s.PatrolBusyMin, s.PatrolCalmMin, s.PatrolRestMin, s.DiskLowGB}, "never set and left out: the defaults")
}

// An older client's write (it does not send the patrol numbers) keeps the numbers a newer client set.
func TestWatch_AnOlderClientsWriteKeepsTheStoredPatrolNumbers(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+strings.TrimSuffix(watchOK, "}")+`,"patrol_present_min":45,"patrol_busy_min":5,"patrol_calm_min":20,"patrol_rest_min":30,"disk_low_gb":20},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = serve(m, http.MethodPut, "/api/hostconfig/watch", `{"items":`+watchOK+`,"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	s, err := m.WatchSettings()
	require.NoError(t, err)
	assert.Equal(t, []int{45, 5, 20, 30, 20}, []int{s.PatrolPresentMin, s.PatrolBusyMin, s.PatrolCalmMin, s.PatrolRestMin, s.DiskLowGB}, "left out: what is stored")
}
