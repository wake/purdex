package hostconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/team"
)

var (
	appAir26 = team.Client{Kind: "app", Label: "Purdex.app @ air26", Addr: "100.64.0.4:51234"}
	appA19   = team.Client{Kind: "app", Label: "Purdex.app @ air19", Addr: "100.64.0.1:40000"}
)

// writeRaw stores value under key straight into the table, around every
// writer, as a hand edit would.
func writeRaw(t *testing.T, m *Module, key, value string) {
	t.Helper()
	_, err := m.store.db.Exec(`
		INSERT INTO host_config (key, value, revision, updated_at) VALUES (?, ?, 1, 1)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, revision = revision + 1`, key, value)
	require.NoError(t, err)
}

func unattendedRevision(t *testing.T, m *Module) int64 {
	t.Helper()
	e, err := m.store.Get(KeyUnattended)
	require.NoError(t, err)
	return e.Revision
}

// D-U23-7: a host that never wrote the switch has it off.
func TestUnattended_NeverWrittenIsOff(t *testing.T) {
	m := newTestModule(t)
	st, err := m.Unattended()
	require.NoError(t, err)
	assert.Equal(t, team.UnattendedState{}, st)
}

// Off→on sets since and changed_at; on→off keeps since (the list of
// D-U23-6 survives the switch-off) and moves changed_at; the next off→on
// starts a new since. changed_by is whoever wrote last.
func TestSetUnattended_OnSetsSinceOffKeepsIt(t *testing.T) {
	m := newTestModule(t)

	st, changed, err := m.SetUnattended(true, appAir26, 1000)
	require.NoError(t, err)
	assert.True(t, changed)
	want := team.UnattendedState{On: true, Since: 1000, ChangedAt: 1000, ChangedBy: &appAir26}
	assert.Equal(t, want, st)
	got, err := m.Unattended()
	require.NoError(t, err)
	assert.Equal(t, want, got, "stored as answered")

	st, changed, err = m.SetUnattended(false, appA19, 2000)
	require.NoError(t, err)
	assert.True(t, changed)
	want = team.UnattendedState{On: false, Since: 1000, ChangedAt: 2000, ChangedBy: &appA19}
	assert.Equal(t, want, st)
	got, err = m.Unattended()
	require.NoError(t, err)
	assert.Equal(t, want, got)

	st, changed, err = m.SetUnattended(true, appAir26, 3000)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, team.UnattendedState{On: true, Since: 3000, ChangedAt: 3000, ChangedBy: &appAir26}, st)
	assert.Equal(t, int64(3), unattendedRevision(t, m))
}

// The same value again writes nothing: changed=false, the revision, since,
// changed_at and changed_by all as they were. Off on a never-written host
// is the same value too.
func TestSetUnattended_OnAgainIsANoop(t *testing.T) {
	m := newTestModule(t)

	st, changed, err := m.SetUnattended(false, appAir26, 500)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, team.UnattendedState{}, st)
	assert.Equal(t, int64(0), unattendedRevision(t, m), "off on a never-written host writes nothing")

	_, _, err = m.SetUnattended(true, appAir26, 1000)
	require.NoError(t, err)
	_, _, err = m.SetUnattended(false, appAir26, 2000)
	require.NoError(t, err)
	_, _, err = m.SetUnattended(true, appAir26, 3000)
	require.NoError(t, err)
	require.Equal(t, int64(3), unattendedRevision(t, m))

	st, changed, err = m.SetUnattended(true, appA19, 4000)
	require.NoError(t, err)
	assert.False(t, changed)
	want := team.UnattendedState{On: true, Since: 3000, ChangedAt: 3000, ChangedBy: &appAir26}
	assert.Equal(t, want, st)
	assert.Equal(t, int64(3), unattendedRevision(t, m), "nothing written")
	got, err := m.Unattended()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// A stored value that is not the shape SetUnattended writes is an error,
// never "off with no error" and never "on": the team module then treats the
// switch as off and logs (fail closed, PU-1b2). Every field is required and
// typed, changed_by names a client, and an on switch has its since: a bare
// {"on":true} would decode as on with since 0 — auto-approving with an empty
// list. The App's next write repairs every one of them, the same value
// included: an unreadable value never takes the "nothing to write" exit.
func TestUnattended_CorruptValueIsAnError(t *testing.T) {
	const by = `"changed_by":{"kind":"app","label":"x"}`
	for _, raw := range []string{
		`{"on":"yes"}`, `null`, `{"on":null}`, `{}`, `[]`, `true`, `"on"`, ``, `{`,
		`{"on":true,"extra":1}`, `{"on":true,"since":1,"changed_at":1,"changed_by":{"kind":"app","label":"x","who":1}}`,
		`{"on":true,"since":"1"}`, `{"on":false,"on":true}`, `{"on":true} {}`,
		// a field missing
		`{"on":true}`,
		`{"on":true,"changed_at":1000,` + by + `}`,
		`{"on":false,"changed_at":1000,` + by + `}`,
		`{"on":true,"since":1000,` + by + `}`,
		`{"on":false,"since":0,` + by + `}`,
		`{"on":true,"since":1000,"changed_at":1000}`,
		`{"on":false,"since":0,"changed_at":1000}`,
		// a field null
		`{"on":true,"since":null,"changed_at":1000,` + by + `}`,
		`{"on":false,"since":null,"changed_at":1000,` + by + `}`,
		`{"on":true,"since":1000,"changed_at":null,` + by + `}`,
		`{"on":false,"since":0,"changed_at":1000,"changed_by":null}`,
		// a field of the wrong type or out of range
		`{"on":true,"since":1.5,"changed_at":1000,` + by + `}`,
		`{"on":false,"since":-1,"changed_at":1000,` + by + `}`,
		`{"on":false,"since":0,"changed_at":0,` + by + `}`,
		`{"on":false,"since":0,"changed_at":1000,"changed_by":"app"}`,
		`{"on":false,"since":0,"changed_at":1000,"changed_by":{}}`,
		`{"on":false,"since":0,"changed_at":1000,"changed_by":{"kind":"app","label":"  "}}`,
		`{"on":false,"since":0,"changed_at":1000,"changed_by":{"kind":"","label":"x"}}`,
		// on without its since
		`{"on":true,"since":0,"changed_at":1000,` + by + `}`,
	} {
		t.Run(raw, func(t *testing.T) {
			m := newTestModule(t)
			writeRaw(t, m, KeyUnattended, raw)
			st, err := m.Unattended()
			assert.Error(t, err)
			assert.False(t, st.On)

			st, changed, err := m.SetUnattended(true, appAir26, 5000)
			require.NoError(t, err)
			assert.True(t, changed, "an unreadable value is overwritten, even with on again")
			want := team.UnattendedState{On: true, Since: 5000, ChangedAt: 5000, ChangedBy: &appAir26}
			assert.Equal(t, want, st)
			got, err := m.Unattended()
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

// What SetUnattended writes reads back: off with since 0 (written over a
// value nobody could read) or with the since of the last on, and a client
// with or without addr.
func TestUnattended_WrittenShapesRead(t *testing.T) {
	for raw, want := range map[string]team.UnattendedState{
		`{"on":false,"since":0,"changed_at":2000,"changed_by":{"kind":"app","label":"x"}}`:                       {On: false, Since: 0, ChangedAt: 2000, ChangedBy: &team.Client{Kind: "app", Label: "x"}},
		`{"on":false,"since":1000,"changed_at":2000,"changed_by":{"kind":"app","label":"x","addr":"1.2.3.4:5"}}`: {On: false, Since: 1000, ChangedAt: 2000, ChangedBy: &team.Client{Kind: "app", Label: "x", Addr: "1.2.3.4:5"}},
		`{"on":true,"since":1000,"changed_at":1000,"changed_by":{"kind":"app","label":"x"}}`:                     {On: true, Since: 1000, ChangedAt: 1000, ChangedBy: &team.Client{Kind: "app", Label: "x"}},
	} {
		m := newTestModule(t)
		writeRaw(t, m, KeyUnattended, raw)
		got, err := m.Unattended()
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
}

// SetUnattended never writes what Unattended would refuse: a client without
// a kind or a label, or a time that is not a unix ms, is an error and
// nothing is stored.
func TestSetUnattended_RefusesWhatItCouldNotReadBack(t *testing.T) {
	m := newTestModule(t)
	for _, c := range []struct {
		by  team.Client
		now int64
	}{
		{team.Client{Kind: "app", Label: ""}, 1000},
		{team.Client{Kind: "app", Label: "  "}, 1000},
		{team.Client{Kind: "", Label: "x"}, 1000},
		{appAir26, 0},
		{appAir26, -5},
	} {
		_, changed, err := m.SetUnattended(true, c.by, c.now)
		assert.Error(t, err, "%+v", c)
		assert.False(t, changed)
	}
	assert.Equal(t, int64(0), unattendedRevision(t, m), "nothing stored")
}

// The App's write heals a value nobody can read: it is the person's
// explicit choice, and refusing it would leave the switch stuck until the
// row is edited by hand. Off over it has no since to keep.
func TestSetUnattended_OverwritesACorruptValue(t *testing.T) {
	m := newTestModule(t)
	writeRaw(t, m, KeyUnattended, `{"on":"yes"}`)
	st, changed, err := m.SetUnattended(false, appAir26, 2000)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, team.UnattendedState{On: false, Since: 0, ChangedAt: 2000, ChangedBy: &appAir26}, st)

	writeRaw(t, m, KeyUnattended, `null`)
	st, changed, err = m.SetUnattended(true, appAir26, 3000)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, team.UnattendedState{On: true, Since: 3000, ChangedAt: 3000, ChangedBy: &appAir26}, st)
	got, err := m.Unattended()
	require.NoError(t, err)
	assert.Equal(t, st, got)
}

// A writer that lands between SetUnattended's read and its write wins the
// CAS; SetUnattended re-reads, recomputes from what is now stored and
// writes again.
func TestSetUnattended_RetriesALostCAS(t *testing.T) {
	m := newTestModule(t)
	puts := 0
	m.beforeUnattendedPut = func() {
		puts++
		if puts > 1 {
			return
		}
		// Another writer: off, with a since of its own, at revision 1.
		_, ok, err := m.store.Put(KeyUnattended, 0, func() (json.RawMessage, error) {
			return json.RawMessage(`{"on":false,"since":500,"changed_at":1500,"changed_by":{"kind":"app","label":"other"}}`), nil
		})
		require.NoError(t, err)
		require.True(t, ok)
	}
	st, changed, err := m.SetUnattended(true, appAir26, 2000)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, team.UnattendedState{On: true, Since: 2000, ChangedAt: 2000, ChangedBy: &appAir26}, st)
	assert.Equal(t, 2, puts, "one lost write, one retry")
	assert.Equal(t, int64(2), unattendedRevision(t, m))

	// The retry recomputes: a racer that turned it on already leaves
	// nothing to write.
	m2 := newTestModule(t)
	m2.beforeUnattendedPut = func() {
		_, _, err := m2.store.Put(KeyUnattended, 0, func() (json.RawMessage, error) {
			return json.RawMessage(`{"on":true,"since":1500,"changed_at":1500,"changed_by":{"kind":"app","label":"other"}}`), nil
		})
		require.NoError(t, err)
	}
	st, changed, err = m2.SetUnattended(true, appAir26, 2000)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, team.UnattendedState{On: true, Since: 1500, ChangedAt: 1500, ChangedBy: &team.Client{Kind: "app", Label: "other"}}, st)
	assert.Equal(t, int64(1), unattendedRevision(t, m2))
}

// A writer that keeps winning makes SetUnattended give up after three
// retries with an error, rather than loop.
func TestSetUnattended_GivesUpAfterThreeRetries(t *testing.T) {
	m := newTestModule(t)
	puts := 0
	m.beforeUnattendedPut = func() {
		puts++
		e, err := m.store.Get(KeyUnattended)
		require.NoError(t, err)
		_, ok, err := m.store.Put(KeyUnattended, e.Revision, func() (json.RawMessage, error) {
			return json.RawMessage(`{"on":false,"since":0,"changed_at":1,"changed_by":{"kind":"app","label":"other"}}`), nil
		})
		require.NoError(t, err)
		require.True(t, ok)
	}
	_, changed, err := m.SetUnattended(true, appAir26, 2000)
	assert.Error(t, err)
	assert.False(t, changed)
	assert.Equal(t, 4, puts, "the first write and three retries")
	st, err := m.Unattended()
	require.NoError(t, err)
	assert.False(t, st.On, "never written by the loser")
}

// PU-1a rule 4: the switch has no generic route. GET /api/hostconfig never
// answers it and no PUT /api/hostconfig/* writes it; the team module's
// route is its only writer (it runs the D-U23-3 sweep with the write).
func TestHostConfig_GetAndPutNeverSeeUnattended(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.SetUnattended(true, appAir26, 1000)
	require.NoError(t, err)

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &fields))
	for k := range fields {
		assert.NotContains(t, []string{"unattended", KeyUnattended}, k)
	}
	assert.JSONEq(t, `{
		"projects":{"items":[],"revision":0},
		"commands":{"items":[],"revision":0},
		"resumeTemplates":{"items":{},"revision":0},
		"quickReplies":{"items":[],"revision":0},
		"relay":{"items":{"self_solo":true,"self_lead":true},"revision":0},
		"team":{"items":{"member_command":"claude --dangerously-skip-permissions"},"revision":0},
		"resources":{"items":{"mode":"lease","kinds":{"build":35,"lint-full":10,"test-full":35,"test-pkg":15},"deadline_s":300,"warmup_s":20,"floor_pct":50,"max_hold_s":3600,"ewma_half_life_s":15,"heavy_min_weight":30},"revision":0}
	}`, rr.Body.String())
	_, hasReader := readers[KeyUnattended]
	assert.False(t, hasReader, "no lenient reader: nothing generic reads the key")

	for _, path := range []string{"/api/hostconfig/unattended", "/api/hostconfig/" + KeyUnattended} {
		rr = serve(m, http.MethodPut, path, `{"items":{"on":false},"baseRevision":1}`)
		assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rr.Code, path)
	}
	st, err := m.Unattended()
	require.NoError(t, err)
	assert.True(t, st.On, "no PUT reached the key")
	assert.Equal(t, int64(1), unattendedRevision(t, m))
}

// Init publishes the module under UnattendedKey as the UnattendedStore the
// team module type-asserts (PU-1b2); the other tests build their Module
// without Init, so this is where dropping the registration goes red.
func TestInit_RegistersUnattendedStore(t *testing.T) {
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir()}})
	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { m.Stop(context.Background()) })
	svc, ok := c.Registry.Get(UnattendedKey)
	require.True(t, ok, "Init must register under UnattendedKey")
	store, ok := svc.(UnattendedStore)
	require.True(t, ok, "registry value must be an UnattendedStore, got %T", svc)
	st, err := store.Unattended()
	require.NoError(t, err)
	assert.Equal(t, team.UnattendedState{}, st)
}
