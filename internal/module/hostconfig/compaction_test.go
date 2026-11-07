package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
)

const compactionPutOK = `{"solo":{"auto":false,"threshold":50,"idle":false},"team":{"auto":true,"threshold":80,"idle":true}}`

func TestCompaction_DefaultWhenNeverWritten(t *testing.T) {
	m := newTestModule(t)
	want, err := json.Marshal(DefaultCompaction)
	require.NoError(t, err)
	assert.JSONEq(t, `{"solo":{"auto":true,"threshold":70,"idle":true},"team":{"auto":true,"threshold":70,"idle":true}}`, string(want))

	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":`+string(want)+`,"revision":0}`, string(got["compaction"]))

	rr = serve(m, http.MethodGet, "/api/hostconfig/compaction", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"items":`+string(want)+`,"revision":0}`, rr.Body.String())

	d, err := m.Compaction()
	require.NoError(t, err)
	assert.Equal(t, DefaultCompaction, d)
}

func TestCompaction_PutRoundTrip(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/compaction", `{"items":`+compactionPutOK+`,"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":`+compactionPutOK+`,"revision":1}`, rr.Body.String())
	rr = serve(m, http.MethodGet, "/api/hostconfig/compaction", "")
	assert.JSONEq(t, `{"items":`+compactionPutOK+`,"revision":1}`, rr.Body.String())
	d, err := m.Compaction()
	require.NoError(t, err)
	assert.Equal(t, CompactionDefaults{
		Solo: CompactionSet{Auto: false, Threshold: 50, Idle: false},
		Team: CompactionSet{Auto: true, Threshold: 80, Idle: true},
	}, d)
}

func TestCompaction_PutRejects(t *testing.T) {
	set := `{"auto":true,"threshold":70,"idle":true}`
	cases := map[string]string{
		"missing solo":      `{"items":{"team":` + set + `},"baseRevision":0}`,
		"missing team":      `{"items":{"solo":` + set + `},"baseRevision":0}`,
		"missing threshold": `{"items":{"solo":{"auto":true,"idle":true},"team":` + set + `},"baseRevision":0}`,
		"missing auto":      `{"items":{"solo":{"threshold":70,"idle":true},"team":` + set + `},"baseRevision":0}`,
		"missing idle":      `{"items":{"solo":{"auto":true,"threshold":70},"team":` + set + `},"baseRevision":0}`,
		"threshold 0":       `{"items":{"solo":{"auto":true,"threshold":0,"idle":true},"team":` + set + `},"baseRevision":0}`,
		"threshold 101":     `{"items":{"solo":{"auto":true,"threshold":101,"idle":true},"team":` + set + `},"baseRevision":0}`,
		"threshold 70.5":    `{"items":{"solo":{"auto":true,"threshold":70.5,"idle":true},"team":` + set + `},"baseRevision":0}`,
		"threshold string":  `{"items":{"solo":{"auto":true,"threshold":"70","idle":true},"team":` + set + `},"baseRevision":0}`,
		"outer extra":       `{"items":{"solo":` + set + `,"team":` + set + `,"x":1},"baseRevision":0}`,
		"inner extra":       `{"items":{"solo":{"auto":true,"threshold":70,"idle":true,"x":1},"team":` + set + `},"baseRevision":0}`,
		"duplicate key":     `{"items":{"solo":` + set + `,"solo":` + set + `,"team":` + set + `},"baseRevision":0}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			m := newTestModule(t)
			rr := serve(m, http.MethodPut, "/api/hostconfig/compaction", body)
			assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			e, err := m.store.Get(KeyCompaction)
			require.NoError(t, err)
			assert.Equal(t, int64(0), e.Revision)
			d, err := m.Compaction()
			require.NoError(t, err)
			assert.Equal(t, DefaultCompaction, d)
		})
	}
}

func TestCompaction_StaleRevisionConflicts(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/compaction", `{"items":`+compactionPutOK+`,"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code)
	rr = serve(m, http.MethodPut, "/api/hostconfig/compaction", `{"items":`+compactionPutOK+`,"baseRevision":0}`)
	assert.Equal(t, http.StatusConflict, rr.Code)
}

func TestCompaction_StoredGarbageIsAnError(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyCompaction, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(`{"solo":{"auto":"yes"}}`), nil })
	require.NoError(t, err)
	_, err = m.Compaction()
	assert.Error(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig/compaction", "")
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"invalid":true`)
	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Contains(t, string(got["compaction"]), `"invalid":true`)
}

func TestCompaction_RelayPutLeavesItAlone(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/compaction", `{"items":`+compactionPutOK+`,"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code)
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	d, err := m.Compaction()
	require.NoError(t, err)
	assert.False(t, d.Solo.Auto)
	assert.Equal(t, 50, d.Solo.Threshold)
}

func TestInit_RegistersCompactionReader(t *testing.T) {
	m := New()
	c := core.New(core.CoreDeps{Config: &config.Config{DataDir: t.TempDir(), HostID: "mlab"}, Registry: core.NewServiceRegistry()})
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Close() })
	v, ok := c.Registry.Get(CompactionKey)
	require.True(t, ok)
	_, isReader := v.(CompactionReader)
	assert.True(t, isReader)
}
