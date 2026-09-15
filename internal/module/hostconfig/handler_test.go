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
		"resumeTemplates":{"items":{},"revision":0}
	}`, rr.Body.String())
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
