package hostconfig

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// putRow stores value under key the way a hand edit would: around the PUT's
// validation.
func putRow(t *testing.T, m *Module, key, value string) {
	t.Helper()
	_, stored, err := m.store.Put(key, 0, raw(value))
	require.NoError(t, err)
	require.True(t, stored)
}

// #1889: one stored value that is not valid JSON used to make the whole GET a
// 200 with an empty body. Now that collection alone answers its empty value
// marked invalid; relay fails closed (both off), team answers no command; the
// others are intact.
func TestHandlerGet_UnreadableValueIsInvalidOthersIntact(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyProjects, `[{"id":"p1"`)
	putRow(t, m, KeyRelay, `{"self_solo":true,"bogus":1}`)
	putRow(t, m, KeyTeam, `{"member_command":"claude; rm"}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, "/api/hostconfig/quick-replies",
		`{"items":[{"id":"q1","text":"go"}],"baseRevision":0}`).Code)

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	require.True(t, json.Valid(rr.Body.Bytes()), rr.Body.String())
	assert.JSONEq(t, `{
		"projects":{"items":[],"revision":1,"invalid":true},
		"commands":{"items":[],"revision":0},
		"resumeTemplates":{"items":{},"revision":0},
		"quickReplies":{"items":[{"id":"q1","text":"go"}],"revision":1},
		"relay":{"items":{"self_solo":false,"self_lead":false},"revision":1,"invalid":true},
		"team":{"items":{},"revision":1,"invalid":true},
		"resources":{"items":{"mode":"lease","kinds":{"build":35,"lint-full":10,"test-full":35,"test-pkg":15},"deadline_s":300,"warmup_s":20,"floor_pct":50,"max_hold_s":3600,"ewma_half_life_s":15,"heavy_min_weight":30},"revision":0},
		"relayQuota":{"items":{"rule":false},"revision":0},
		"workbook":{"items":{"push_wait_s":8},"revision":0}
	}`, rr.Body.String())
}

// Mixed rows: the good ones as a PUT would store them, plus the count and the
// reasons of the rest; and the kept rows always pass the PUT they go back in.
func TestHandlerGet_MixedRowsAnswerTheGoodOnesAndDropped(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyProjects, `[{"id":"p1","name":" A ","slug":"a","path":"/a"},{"id":"p2","name":"B","slug":"BAD","path":"/b"},`+
		`{"id":"p1","name":"C","slug":"c","path":"/c"},{"id":"p3","name":"D","slug":"d","path":" ~/d "}]`)
	putRow(t, m, KeyResumeTemplates, `{"cc":{"exact":"x {id}","fallback":"x"},"Codex":{"exact":"","fallback":""}}`)
	putRow(t, m, KeyRelay, `{"self_solo":false,"prompt_write":"W","prompt_fix":"[pdx-relay"}`)

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got), rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"p1","name":"A","slug":"a","path":"/a"},{"id":"p3","name":"D","slug":"d","path":"~/d"}],"revision":1,
		"dropped":{"count":2,"reasons":["item 1: invalid slug \"BAD\"","item 2: duplicate id \"p1\""]}}`, string(got["projects"]))
	assert.JSONEq(t, `{"items":{"cc":{"exact":"x {id}","fallback":"x"}},"revision":1,
		"dropped":{"count":1,"reasons":["invalid agent type \"Codex\""]}}`, string(got["resumeTemplates"]))
	assert.JSONEq(t, `{"items":{"self_solo":false,"self_lead":true,"prompt_write":"W"},"revision":1,
		"dropped":{"count":1,"reasons":["prompt_fix: a relay prompt may not contain [pdx-relay: the machine tag is the mod's"]}}`, string(got["relay"]))

	for field, path := range map[string]string{"projects": "projects", "resumeTemplates": "resume-templates", "relay": "relay"} {
		var c struct{ Items json.RawMessage }
		require.NoError(t, json.Unmarshal(got[field], &c))
		rr = serve(m, http.MethodPut, "/api/hostconfig/"+path, `{"items":`+string(c.Items)+`,"baseRevision":1}`)
		assert.Equal(t, http.StatusOK, rr.Code, "%s: %s", field, rr.Body.String())
	}
}

// A 409's current goes through the same view: over a broken row it is a 409
// with a JSON body and the markers, never an empty body.
func TestHandlerPut_ConflictOverABrokenRowCarriesTheMarkers(t *testing.T) {
	m := newTestModule(t)
	putRow(t, m, KeyProjects, `[{"id":"p1"`)
	rr := serve(m, http.MethodPut, "/api/hostconfig/projects", `{"items":[],"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	require.True(t, json.Valid(rr.Body.Bytes()), rr.Body.String())
	assert.JSONEq(t, `{"items":[],"revision":1,"invalid":true}`, rr.Body.String())

	putRow(t, m, KeyCommands, `[{"id":"c1","name":"n","command":"x","icon":{"kind":"emoji","value":"x"}},`+
		`{"id":"c2","name":" n ","command":"y","icon":{"kind":"agent","value":"codex"}}]`)
	rr = serve(m, http.MethodPut, "/api/hostconfig/commands", `{"items":[],"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.JSONEq(t, `{"items":[{"id":"c2","name":"n","command":"y","icon":{"kind":"agent","value":"codex"}}],"revision":1,
		"dropped":{"count":1,"reasons":["item 0: invalid icon kind \"emoji\""]}}`, rr.Body.String())
}

// One log line per collection per GET when something was left out, naming no
// more of a row than its reason does.
func TestHandlerGet_LogsOncePerCollectionWithoutTheRow(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	m := newTestModule(t)
	putRow(t, m, KeyProjects, `[{"id":"p1"`)
	putRow(t, m, KeyCommands, `[{"id":"c1","name":"n","command":"secret-cmd","icon":{"kind":"emoji","value":"x"}},`+
		`{"id":"c2","name":"n","command":"secret-cmd","icon":{"kind":"agent","value":"gemini"}}]`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodGet, "/api/hostconfig", "").Code)

	out := buf.String()
	assert.Len(t, strings.Split(strings.TrimSpace(out), "\n"), 2, out)
	assert.Contains(t, out, `[hostconfig] get projects: invalid: items must be a JSON array`)
	assert.Contains(t, out, `[hostconfig] get commands: dropped 2 (item 0: invalid icon kind "emoji")`)
	assert.NotContains(t, out, "secret-cmd")
}

// A response that does not marshal is a 500, not a 200 with an empty body.
// No handler reaches it any more (every answer is a normalised value), so it
// is tested directly.
func TestWriteJSON_MarshalFailureIs500(t *testing.T) {
	rr := httptest.NewRecorder()
	writeJSON(rr, http.StatusOK, map[string]any{"x": make(chan int)})
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "internal error\n", rr.Body.String())
}
