package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #2062: the relay-quota rule's switch is its own key.
func TestRelayQuota_DefaultsOffAndRoundTrips(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"rule":false},"revision":0}`, string(got["relayQuota"]))
	on, err := m.RelayQuotaRule()
	require.NoError(t, err)
	assert.False(t, on, "a never-written key is off")

	rr = serve(m, http.MethodPut, "/api/hostconfig/relay_quota", `{"items":{"rule":true},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"rule":true},"revision":1}`, rr.Body.String())
	on, err = m.RelayQuotaRule()
	require.NoError(t, err)
	assert.True(t, on)
}

func TestRelayQuota_IsStrict(t *testing.T) {
	m := newTestModule(t)
	for _, items := range []string{`{}`, `{"rule":null}`, `{"rule":"yes"}`, `{"rule":1}`, `{"rule":true,"extra":1}`, `{"Rule":true}`, `[]`, `null`, `true`} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/relay_quota", `{"items":`+items+`,"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, items)
	}
	e, err := m.store.Get(KeyRelayQuota)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

// A stored value that does not read is an error (the team module treats it as off), never "on".
func TestRelayQuota_AStoredGarbageValueIsAnErrorNotOn(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyRelayQuota, 0, func() (json.RawMessage, error) { return json.RawMessage(`{"rule":"on"}`), nil })
	require.NoError(t, err)
	on, err := m.RelayQuotaRule()
	assert.Error(t, err)
	assert.False(t, on)
}

// The reason it is its own key: an older App's full-replace PUT of `relay` (which knows nothing of the rule) must
// leave the rule as it was. Mutation gate: make the rule a field of the relay payload → red.
func TestRelayQuota_AnOlderAppsRelayPutLeavesTheRuleOn(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/hostconfig/relay_quota", `{"items":{"rule":true},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"self_solo":false},"baseRevision":0}`) // the old App's body: no rule
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	on, err := m.RelayQuotaRule()
	require.NoError(t, err)
	assert.True(t, on, "saving the relay switches reset the quota rule")
	sw, err := m.RelaySwitches()
	require.NoError(t, err)
	assert.False(t, sw.SelfSolo)
	// and the relay payload does not accept the rule: it is not a relay field
	rr = serve(m, http.MethodPut, "/api/hostconfig/relay", `{"items":{"quota_rule":true},"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
