package hostconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

func group(id, name string, roots ...string) map[string]any {
	return map[string]any{"id": id, "name": name, "roots": roots, "created_at": 1760000000000, "updated_at": 1760000000000}
}

func putGroups(m *Module, items any, base int64) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{"items": items, "baseRevision": base})
	return serve(m, http.MethodPut, "/api/hostconfig/task-groups", string(body))
}

type groupsAnswer struct {
	Items []struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		Color     string   `json:"color"`
		Roots     []string `json:"roots"`
		CreatedAt int64    `json:"created_at"`
		UpdatedAt int64    `json:"updated_at"`
	} `json:"items"`
	Revision int64 `json:"revision"`
	Dropped  *struct {
		Count int `json:"count"`
	} `json:"dropped"`
}

func TestTaskGroups_RoundTripAndTheFullRead(t *testing.T) {
	m := newTestModule(t)
	g := group("aaaa1111", "  Billing  ", "root-1", "root-2")
	g["color"] = "#3366cc"
	rr := putGroups(m, []any{g, group("bbbb2222", "Deploy", "root-3")}, 0)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var put groupsAnswer
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &put))
	require.Len(t, put.Items, 2)
	assert.Equal(t, "Billing", put.Items[0].Name, "the name is trimmed")
	assert.Equal(t, "#3366cc", put.Items[0].Color)
	assert.Equal(t, []string{"root-1", "root-2"}, put.Items[0].Roots)
	assert.EqualValues(t, 1, put.Revision)

	all := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, all.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(all.Body.Bytes(), &got))
	require.Contains(t, got, "taskGroups", "the groups come with the other collections")
	var groups groupsAnswer
	require.NoError(t, json.Unmarshal(got["taskGroups"], &groups))
	assert.Len(t, groups.Items, 2)
}

func TestTaskGroups_AStaleBaseIs409(t *testing.T) {
	m := newTestModule(t)
	require.Equal(t, http.StatusOK, putGroups(m, []any{group("aaaa1111", "A", "r1")}, 0).Code)
	rr := putGroups(m, []any{}, 0)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	var cur groupsAnswer
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &cur))
	assert.Len(t, cur.Items, 1, "the 409 carries the current collection")
}

func TestTaskGroups_AGroupWithoutAnIdGetsOne(t *testing.T) {
	m := newTestModule(t)
	g := group("", "Fresh", "r1")
	delete(g, "id")
	rr := putGroups(m, []any{g, group("aaaa1111", "Kept", "r2")}, 0)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var got groupsAnswer
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}$`), got.Items[0].ID)
	assert.Equal(t, "aaaa1111", got.Items[1].ID, "an id that was sent is kept")
}

// The rules of spec §7: at most 32 groups, at most 64 roots a group, a root in at most one group, the name rule of a team
// name, a colour as #rrggbb. Each is refused as a whole PUT.
// Mutation gate: skip any one of them -> that body is accepted -> red.
func TestTaskGroups_Refusals(t *testing.T) {
	many := make([]any, 0, 33)
	for i := 0; i < 33; i++ {
		many = append(many, group(fmt.Sprintf("%08x", i), fmt.Sprintf("G%d", i), fmt.Sprintf("root-%d", i)))
	}
	roots65 := make([]string, 65)
	for i := range roots65 {
		roots65[i] = fmt.Sprintf("root-%d", i)
	}
	badColor := group("aaaa1111", "A", "r1")
	badColor["color"] = "blue"
	extra := group("aaaa1111", "A", "r1")
	extra["members"] = []string{"x"}
	badTime := group("aaaa1111", "A", "r1")
	badTime["created_at"] = -1
	// the times are part of the shape: missing, null, a string, a float and an overflow are not the time 0 or a time at all
	withTime := func(field string, v any, drop bool) map[string]any {
		g := group("aaaa1111", "A", "r1")
		if drop {
			delete(g, field)
		} else {
			g[field] = v
		}
		return g
	}
	cases := map[string][]any{
		"33 groups":                  many,
		"65 roots in a group":        {group("aaaa1111", "A", roots65...)},
		"a root in two groups":       {group("aaaa1111", "A", "shared"), group("bbbb2222", "B", "shared")},
		"a root twice in one group":  {group("aaaa1111", "A", "r1", "r1")},
		"an empty name":              {group("aaaa1111", "   ", "r1")},
		"a name over 64 bytes":       {group("aaaa1111", strings.Repeat("a", 65), "r1")},
		"a name with a control char": {group("aaaa1111", "bad\x1b[31mname", "r1")},
		"an id with a space":         {group("aa aa", "A", "r1")},
		"a duplicate id":             {group("aaaa1111", "A", "r1"), group("aaaa1111", "B", "r2")},
		"a root that is not an id":   {group("aaaa1111", "A", "has space")},
		"an empty root":              {group("aaaa1111", "A", "")},
		"a bad colour":               {badColor},
		"a field that is not known":  {extra},
		"a negative time":            {badTime},
		"created_at missing":         {withTime("created_at", nil, true)},
		"created_at null":            {withTime("created_at", nil, false)},
		"updated_at missing":         {withTime("updated_at", nil, true)},
		"updated_at null":            {withTime("updated_at", nil, false)},
		"updated_at a string":        {withTime("updated_at", "1760000000000", false)},
		"created_at a float":         {withTime("created_at", 1.5, false)},
		"created_at overflows":       {withTime("created_at", json.RawMessage("99999999999999999999"), false)},
	}
	for name, items := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestModule(t)
			rr := putGroups(m, items, 0)
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			got := serve(m, http.MethodGet, "/api/hostconfig", "")
			assert.Contains(t, got.Body.String(), `"taskGroups":{"items":[],"revision":0}`, "nothing stored")
		})
	}
}

// A group may hold no root yet (the App creates it, then drags a session in); a null or missing list is an empty one.
func TestTaskGroups_AGroupMayHoldNoRoot(t *testing.T) {
	m := newTestModule(t)
	g := group("aaaa1111", "Empty")
	g["roots"] = nil
	rr := putGroups(m, []any{g, map[string]any{"id": "bbbb2222", "name": "Missing", "created_at": 1, "updated_at": 1}}, 0)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"roots":[]`, "an empty list is stored as one, not as null")
	assert.NotContains(t, rr.Body.String(), `"roots":null`)
}

func TestTaskGroups_32GroupsAnd64RootsAreAccepted(t *testing.T) {
	m := newTestModule(t)
	roots64 := make([]string, 64)
	for i := range roots64 {
		roots64[i] = fmt.Sprintf("r-%d", i)
	}
	items := []any{group("aaaa0000", "Full", roots64...)}
	for i := 1; i < 32; i++ {
		items = append(items, group(fmt.Sprintf("%08x", i), fmt.Sprintf("G%d", i), fmt.Sprintf("x-%d", i)))
	}
	require.Equal(t, http.StatusOK, putGroups(m, items, 0).Code)
}

// A stored value edited by hand is read like a PUT would take it, row by row: a group whose root an earlier group already
// holds is left out and counted (the lenient view, #1889).
// Mutation gate: no cross-row check in the lenient read -> both groups are kept -> red.
func TestTaskGroups_TheLenientReadDropsASecondOwnerOfARoot(t *testing.T) {
	m := newTestModule(t)
	raw := `[{"id":"aaaa1111","name":"A","roots":["shared"],"created_at":1,"updated_at":1},{"id":"bbbb2222","name":"B","roots":["shared","own"],"created_at":1,"updated_at":1}]`
	_, ok, err := m.store.Put(KeyTaskGroups, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(raw), nil })
	require.NoError(t, err)
	require.True(t, ok)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &all))
	var got groupsAnswer
	require.NoError(t, json.Unmarshal(all["taskGroups"], &got))
	require.Len(t, got.Items, 1)
	assert.Equal(t, "aaaa1111", got.Items[0].ID)
	require.NotNil(t, got.Dropped)
	assert.Equal(t, 1, got.Dropped.Count)
}

// A duplicate key inside a row is refused by the PUT (the shared handler checks the whole body), so the lenient read must not
// keep the last value of it either: the row is dropped and counted (NP-5 R2).
// Mutation gate: no duplicate-key check in the row rules -> the read keeps the row -> red.
func TestTaskGroups_ADuplicateKeyInARowIsRefusedByTheReadToo(t *testing.T) {
	m := newTestModule(t)
	row := `{"id":"aaaa1111","id":"bbbb2222","name":"A","roots":[],"created_at":1,"updated_at":1}`
	_, ok, err := m.store.Put(KeyTaskGroups, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage("[" + row + "]"), nil })
	require.NoError(t, err)
	require.True(t, ok)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	var all map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &all))
	var got groupsAnswer
	require.NoError(t, json.Unmarshal(all["taskGroups"], &got))
	assert.Empty(t, got.Items, "the read kept a row with a duplicate key")
	require.NotNil(t, got.Dropped)
	assert.Equal(t, 1, got.Dropped.Count)

	put := serve(m, http.MethodPut, "/api/hostconfig/task-groups", `{"items":[`+row+`],"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, put.Code, "the PUT took the same row")
}

// The real module registered in a real core, asked through GET /api/info (the lesson of fleet.v1): task_groups.v1 is declared
// while the module is up and gone after Stop. Mutation gate: do not declare it -> red.
func TestInfo_AnnouncesTaskGroupsV1ForTheRealModule(t *testing.T) {
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}, Registry: core.NewServiceRegistry()})
	c.AddModule(m)
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Close() })
	mux := http.NewServeMux()
	c.RegisterCoreRoutes(mux)
	caps := func() []any {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var body map[string]any
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
		list, _ := body["capabilities"].([]any)
		return list
	}
	require.Contains(t, caps(), "task_groups.v1")
	require.NoError(t, m.Stop(context.Background()))
	require.NotContains(t, caps(), "task_groups.v1")
}
