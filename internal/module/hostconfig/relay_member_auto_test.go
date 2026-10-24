package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// member_auto (member-relay-switch spec §2, §3): default true, written to the wire and the store only while false.
func TestRelayMemberAuto_PutAndStore(t *testing.T) {
	m := newTestModule(t)

	// Left out: reads true, the stored row does not carry the key.
	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":true},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":true,"self_lead":true},"revision":1}`, rr.Body.String())
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.True(t, sw.MemberAuto)
	e, err := m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.NotContains(t, string(e.Value), "member_auto")

	// false: stored and read back as false.
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"member_auto":false},"baseRevision":1}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"self_solo":true,"self_lead":true,"member_auto":false},"revision":2}`, rr.Body.String())
	e, err = m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.Contains(t, string(e.Value), `"member_auto":false`)
	sw, err = m.RelaySwitches()
	require.NoError(t, err)
	assert.False(t, sw.MemberAuto)

	// GET carries false.
	rr = serve(m, http.MethodGet, "/api/hostconfig", "")
	assert.Contains(t, rr.Body.String(), `"relay":{"items":{"self_solo":true,"self_lead":true,"member_auto":false},"revision":2}`)

	// A PUT that names only a self switch replaces the whole row: member_auto goes back to true.
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false},"baseRevision":2}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	sw, err = m.RelaySwitches()
	require.NoError(t, err)
	assert.True(t, sw.MemberAuto)

	// An explicit true is left out of the stored row.
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"member_auto":true},"baseRevision":3}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	e, err = m.store.Get(KeyRelay)
	require.NoError(t, err)
	assert.NotContains(t, string(e.Value), "member_auto")

	// null, a string and a misspelt key are 400.
	for _, body := range []string{`{"items":{"member_auto":null},"baseRevision":4}`, `{"items":{"member_auto":"no"},"baseRevision":4}`,
		`{"items":{"member_autoo":false},"baseRevision":4}`} {
		assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPut, "/api/hostconfig/relay", body).Code, body)
	}
}

func TestRelayMemberAuto_GetDefaultsAndBadRow(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	assert.Contains(t, rr.Body.String(), `"relay":{"items":{"self_solo":true,"self_lead":true},"revision":0}`)

	r := readRelay(json.RawMessage(`{"self_solo":true,"bogus":1}`))
	assert.Error(t, r.invalid)
	assert.False(t, r.items.(RelaySwitches).MemberAuto)
	assert.JSONEq(t, `{"self_solo":false,"self_lead":false,"member_auto":false}`, jsonOf(t, r.items))
}

// The formal gate reads RelaySwitches(), not the GET: a stored member_auto that is not a boolean is an error there.
func TestRelayMemberAuto_ReaderErrorsOnBadStoredValue(t *testing.T) {
	for _, raw := range []string{`{"member_auto":null}`, `{"member_auto":"no"}`} {
		m := newTestModule(t)
		_, _, err := m.store.Put(KeyRelay, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(raw), nil })
		require.NoError(t, err, raw)
		_, err = m.RelaySwitches()
		assert.Error(t, err, raw)
	}
}

func TestRelayMemberAuto_PromptsSurviveAFalsePut(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"prompt_write":"W {{path}}","member_auto":false},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	p, err := m.RelayPrompts()
	require.NoError(t, err)
	assert.Equal(t, "W {{path}}", p.Write)
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.False(t, sw.MemberAuto)
}
