package hostconfig

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerGetEmpty(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{
		"projects":{"items":[],"revision":0},
		"commands":{"items":[],"revision":0},
		"resumeTemplates":{"items":{},"revision":0},
		"quickReplies":{"items":[],"revision":0},
		"relay":{"items":{"self_solo":true,"self_lead":true},"revision":0},
		"team":{"items":{"member_command":"claude --dangerously-skip-permissions"},"revision":0},
		"resources":{"items":{"mode":"lease","kinds":{"build":35,"lint-full":10,"test-full":35,"test-pkg":15},"deadline_s":540,"warmup_s":20,"floor_pct":50,"max_hold_s":3600,"ewma_half_life_s":15,"heavy_min_weight":30,"mem_warn_pct":80,"mem_danger_pct":90,"gap_s":30,"trend_window_s":120,"trend_mem_dppm":20,"trend_cpu_dppm":0,"park_s":900,"alert_danger_s":180,"alert_stuck_s":600,"alert_lease_mem_pct":20,"alert_push":0,"load_cap_pct":100},"revision":0},
		"relayQuota":{"items":{"rule":false},"revision":0},
		"probeHosts":{"items":[],"revision":0},
		"fleet":{"items":{"order":["air26","mlab"],"peer_ssh_alias":"","enabled":true},"revision":0},
		"workbook":{"items":{"push_wait_s":8},"revision":0},
		"wwdAlerts":{"items":{"push":false},"revision":0},
		"watch":{"items":{"enabled":true,"x_cores":1.5,"y_mb":1024,"t_s":60,"pressure_level":2,"pressure_t_s":120,"page_rate":50,"page_t_s":60,"targets":["air26","mlab"],"mode":"patrol","patrol_idle_min":120,"patrol_present_min":30,"patrol_busy_min":10,"patrol_calm_min":15,"patrol_rest_min":10,"disk_low_gb":14,"auto_wake":true,"agent_command":"claude --dangerously-skip-permissions --model opus","workdir":"~/Workspace/wake/mlab-purdex-watch","start_prompt":"","wake_prompt":""},"revision":0}
	}`, rr.Body.String())
}

func TestHandlerPutQuickRepliesRoundTrip(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/quick-replies",
		`{"items":[{"id":"continue","text":" continue "}],"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"continue","title":"continue","content":"continue","text":"continue"}],"revision":1}`, rr.Body.String())

	rr = serve(m, http.MethodPut, "/api/hostconfig/quick-replies", `{"items":[],"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[],"revision":2}`, rr.Body.String())

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":[],"revision":2}`, string(got["quickReplies"]))
}

func TestHandlerPutQuickRepliesConflict(t *testing.T) {
	m := newTestModule(t)
	body := `{"items":[{"id":"a","text":"hi"}],"baseRevision":0}`
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, "/api/hostconfig/quick-replies", body).Code)

	rr := serve(m, http.MethodPut, "/api/hostconfig/quick-replies", `{"items":[],"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"a","title":"hi","content":"hi","text":"hi"}],"revision":1}`, rr.Body.String())
}

func TestHandlerPutQuickRepliesRejectsInvalid(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/quick-replies", `{"items":[{"id":"a","text":""}],"baseRevision":0}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	e, err := m.store.Get(KeyQuickReplies)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

func TestHandlerPutProjectsRoundTrip(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/projects",
		`{"items":[{"id":"p1","name":" Purdex ","slug":"purdex","path":"~/w"}],"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"p1","name":"Purdex","slug":"purdex","path":"~/w"}],"revision":1}`, rr.Body.String())

	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"slug":"purdex"`)
	assert.Contains(t, rr.Body.String(), `"revision":1`)
}

func TestHandlerPutConflict(t *testing.T) {
	m := newTestModule(t)
	body := `{"items":[{"id":"c1","name":"x","command":"ls","icon":{"kind":"phosphor","value":"Terminal"}}],"baseRevision":0}`
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, "/api/hostconfig/commands", body).Code)

	rr := serve(m, http.MethodPut, "/api/hostconfig/commands", `{"items":[],"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"items":[{"id":"c1","name":"x","command":"ls","icon":{"kind":"phosphor","value":"Terminal"}}],"revision":1}`, rr.Body.String())
}

// A stale client must get the server copy (409) even when its payload is
// invalid — otherwise it can never recover the current state.
func TestHandlerPutStaleRevisionWinsOverValidation(t *testing.T) {
	m := newTestModule(t)
	stored := `{"items":[{"id":"p1","name":"Purdex","slug":"purdex","path":"~/w"}],"baseRevision":0}`
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, "/api/hostconfig/projects", stored).Code)

	rr := serve(m, http.MethodPut, "/api/hostconfig/projects",
		`{"items":[{"id":"p2","name":"n","slug":"BAD","path":"relative"}],"baseRevision":0}`)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":[{"id":"p1","name":"Purdex","slug":"purdex","path":"~/w"}],"revision":1}`, rr.Body.String())
}

func TestHandlerPutMatchingRevisionInvalidItemsLeavesRow(t *testing.T) {
	m := newTestModule(t)
	stored := `{"items":[{"id":"p1","name":"Purdex","slug":"purdex","path":"~/w"}],"baseRevision":0}`
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, "/api/hostconfig/projects", stored).Code)

	rr := serve(m, http.MethodPut, "/api/hostconfig/projects",
		`{"items":[{"id":"p2","name":"n","slug":"BAD","path":"/"}],"baseRevision":1}`)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())

	e, err := m.store.Get(KeyProjects)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.Revision)
	assert.JSONEq(t, `[{"id":"p1","name":"Purdex","slug":"purdex","path":"~/w"}]`, string(e.Value))
}

func TestHandlerPutResumeTemplates(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/resume-templates",
		`{"items":{"cc":{"exact":"cld --resume {id}","fallback":"cld -c"}},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"cc":{"exact":"cld --resume {id}","fallback":"cld -c"}},"revision":1}`, rr.Body.String())
}

func TestHandlerPutRejects(t *testing.T) {
	m := newTestModule(t)
	cases := map[string]string{
		"invalid json":       `{`,
		"missing items":      `{"baseRevision":0}`,
		"negative revision":  `{"items":[],"baseRevision":-1}`,
		"validation failure": `{"items":[{"id":"p1","name":"n","slug":"BAD","path":"/"}],"baseRevision":0}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rr := serve(m, http.MethodPut, "/api/hostconfig/projects", body)
			assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
		})
	}
}

func TestHandlerPutRejectsDuplicateKeys(t *testing.T) {
	m := newTestModule(t)
	cases := map[string]struct{ path, body string }{
		"top-level baseRevision": {"/api/hostconfig/projects", `{"items":[],"baseRevision":1,"baseRevision":0}`},
		"project path":           {"/api/hostconfig/projects", `{"items":[{"id":"p1","name":"n","slug":"s","path":"/a","path":"/b"}],"baseRevision":0}`},
		"command icon":           {"/api/hostconfig/commands", `{"items":[{"id":"c1","name":"x","command":"ls","icon":{"kind":"phosphor","value":"Terminal","value":"X"}}],"baseRevision":0}`},
		"resume agent key":       {"/api/hostconfig/resume-templates", `{"items":{"cc":{"exact":"a","fallback":"b"},"cc":{"exact":"c","fallback":"d"}},"baseRevision":0}`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rr := serve(m, http.MethodPut, c.path, c.body)
			assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
		})
	}
	e, err := m.store.Get(KeyProjects)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

func TestHandlerCheckPathRejectsDuplicateKeys(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPost, "/api/hostconfig/check-path", `{"path":"relative","path":"~"}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
}

func TestHandlerPutBodyTooLarge(t *testing.T) {
	m := newTestModule(t)
	body := `{"items":[],"baseRevision":0,"pad":"` + strings.Repeat("x", bodyCap) + `"}`
	rr := serve(m, http.MethodPut, "/api/hostconfig/projects", body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestHandlerPutAtCapNotTooLarge(t *testing.T) {
	m := newTestModule(t)
	prefix, suffix := `{"items":[],"baseRevision":0,"pad":"`, `"}`
	body := prefix + strings.Repeat("x", bodyCap-len(prefix)-len(suffix)) + suffix
	require.Len(t, body, bodyCap)
	rr := serve(m, http.MethodPut, "/api/hostconfig/projects", body)
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func TestHandlerCheckPath(t *testing.T) {
	m := newTestModule(t)
	home, err := m.home()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "file.txt"), []byte("x"), 0o600))

	cases := []struct {
		path, want string
	}{
		{"~", `{"status":"dir","resolved":"` + home + `"}`},
		{"~/file.txt", `{"status":"not_dir","resolved":"` + filepath.Join(home, "file.txt") + `"}`},
		{"~/missing", `{"status":"missing","resolved":"` + filepath.Join(home, "missing") + `"}`},
		{home, `{"status":"dir","resolved":"` + home + `"}`},
		{filepath.Join(home, "file.txt"), `{"status":"not_dir","resolved":"` + filepath.Join(home, "file.txt") + `"}`},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"path": c.path})
			rr := serve(m, http.MethodPost, "/api/hostconfig/check-path", string(body))
			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
			assert.JSONEq(t, c.want, rr.Body.String())
		})
	}

	rr := serve(m, http.MethodPost, "/api/hostconfig/check-path", `{"path":"relative"}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	rr = serve(m, http.MethodPost, "/api/hostconfig/check-path", `nope`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
