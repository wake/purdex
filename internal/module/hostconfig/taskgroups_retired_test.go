package hostconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

// The per-host task groups are retired (task groups spec §8, TG-6): the groups are the profile's `purdex-task-groups`. This pins that
// the daemon neither serves them nor announces them, and that a row an older daemon left in host_config does no harm.

func retiredModule(t *testing.T, dir string) (*Module, *http.ServeMux) {
	t.Helper()
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: dir}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	m.RegisterRoutes(mux)
	return m, mux
}

func TestTaskGroupsRetired_TheRouteIsGone(t *testing.T) {
	m, mux := retiredModule(t, t.TempDir())
	t.Cleanup(func() { _ = m.Close() })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/hostconfig/task-groups", nil))
	assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code, "PUT /api/hostconfig/task-groups must not be served")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hostconfig/task-groups", nil))
	assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code)
}

func TestTaskGroupsRetired_GetHostconfigHasNoTaskGroups(t *testing.T) {
	m, mux := retiredModule(t, t.TempDir())
	t.Cleanup(func() { _ = m.Close() })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hostconfig", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &all))
	assert.NotContains(t, all, "taskGroups")
	assert.Contains(t, all, "fleet", "the other collections are still there")
}

// The real module registered in a real core, asked through GET /api/info (the lesson of fleet.v1).
func TestTaskGroupsRetired_InfoDoesNotAnnounceTheCapability(t *testing.T) {
	m, mux := retiredModule(t, t.TempDir())
	t.Cleanup(func() { _ = m.Close() })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	caps, _ := body["capabilities"].([]any)
	assert.NotContains(t, caps, "task_groups.v1")
	assert.Contains(t, caps, "personas.v1", "the module is up, and its other capability is announced")
}

// A host_config row that an older daemon wrote stays in the database and is never read: the daemon starts again over it and serves
// every collection.
func TestTaskGroupsRetired_AnOldRowDoesNotAffectLoading(t *testing.T) {
	dir := t.TempDir()
	m, _ := retiredModule(t, dir)
	row := `[{"id":"aaaa1111","name":"A","roots":["r1"],"created_at":1,"updated_at":1}]`
	_, ok, err := m.store.Put("task_groups", 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(row), nil })
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, m.Close())

	m2, mux := retiredModule(t, dir) // the daemon starts again over the same database
	t.Cleanup(func() { _ = m2.Close() })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hostconfig", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &all))
	assert.NotContains(t, all, "taskGroups")
	e, err := m2.store.Get("task_groups")
	require.NoError(t, err)
	assert.JSONEq(t, row, string(e.Value), "the row is left alone")
}
