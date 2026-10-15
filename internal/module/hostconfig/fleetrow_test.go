package hostconfig

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ FleetConfigReader = (*Module)(nil)

func putFleet(m *Module, items string, rev string) (int, string) {
	rr := serve(m, http.MethodPut, "/api/hostconfig/fleet", `{"items":`+items+`,"baseRevision":`+rev+`}`)
	return rr.Code, rr.Body.String()
}

func TestFleet_DefaultRowWhenNeverWritten(t *testing.T) {
	m := newTestModule(t)
	cfg, rev, err := m.FleetConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(0), rev)
	assert.Equal(t, FleetConfig{Order: []string{"air26", "mlab"}, PeerSSHAlias: "", Enabled: true}, cfg)

	rr := serve(m, http.MethodGet, "/api/hostconfig", ``)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"order":["air26","mlab"],"peer_ssh_alias":"","enabled":true},"revision":0}`, string(got["fleet"]))
}

func TestFleet_RoundTripAndDefaultsForLeftOutFields(t *testing.T) {
	withSSHAliases(t, "air-2026")
	m := newTestModule(t)
	code, body := putFleet(m, `{"order":["mlab","air26"],"peer_ssh_alias":"air-2026"}`, "0")
	require.Equal(t, http.StatusOK, code, body)
	cfg, rev, err := m.FleetConfig()
	require.NoError(t, err)
	assert.Equal(t, int64(1), rev)
	assert.Equal(t, FleetConfig{Order: []string{"mlab", "air26"}, PeerSSHAlias: "air-2026", Enabled: true}, cfg, "enabled left out = true")

	code, body = putFleet(m, `{"order":["mlab"],"enabled":false}`, "1")
	require.Equal(t, http.StatusOK, code, body)
	cfg, _, err = m.FleetConfig()
	require.NoError(t, err)
	assert.Equal(t, FleetConfig{Order: []string{"mlab"}, PeerSSHAlias: "", Enabled: false}, cfg)
}

func TestFleet_IsStrictAndBounded(t *testing.T) {
	withSSHAliases(t, "air-2026")
	m := newTestModule(t)
	bad := map[string]string{
		"not an object":      `[]`,
		"null":               `null`,
		"order missing":      `{"enabled":true}`,
		"order empty":        `{"order":[]}`,
		"order null":         `{"order":null}`,
		"order five":         `{"order":["a","b","c","d","e"]}`,
		"order not strings":  `{"order":[1]}`,
		"alias with slash":   `{"order":["a/b"]}`,
		"alias with semi":    `{"order":["a;rm"]}`,
		"alias empty":        `{"order":[""]}`,
		"alias dot":          `{"order":["."]}`,
		"alias dotdot":       `{"order":[".."]}`,
		"alias too long":     `{"order":["` + strings.Repeat("a", 65) + `"]}`,
		"duplicate":          `{"order":["mlab","mlab"]}`,
		"duplicate by case":  `{"order":["mlab","MLAB"]}`,
		"unknown key":        `{"order":["mlab"],"bogus":1}`,
		"enabled not bool":   `{"order":["mlab"],"enabled":"yes"}`,
		"enabled null":       `{"order":["mlab"],"enabled":null}`,
		"peer alias unknown": `{"order":["mlab"],"peer_ssh_alias":"gone"}`,
		"peer alias bad":     `{"order":["mlab"],"peer_ssh_alias":"a;rm"}`,
		"peer alias number":  `{"order":["mlab"],"peer_ssh_alias":5}`,
		"trailing":           `{"order":["mlab"]}{}`,
	}
	for why, items := range bad {
		code, body := putFleet(m, items, "0")
		assert.Equal(t, http.StatusBadRequest, code, "%s: %s", why, body)
	}
	code, body := putFleet(m, `{"order":["a","b","c","d"]}`, "0")
	assert.Equal(t, http.StatusOK, code, "four entries fit: "+body)
	e, err := m.store.Get(KeyFleet)
	require.NoError(t, err)
	assert.Equal(t, int64(1), e.Revision, "only the good one stored")
}

func TestFleet_TheConfigSavesWhileAPeerIsUnknown(t *testing.T) {
	// Own-alias membership and peer resolution are the module's run-time checks, not the config's.
	m := newTestModule(t)
	code, body := putFleet(m, `{"order":["nobody-knows","mlab"]}`, "0")
	assert.Equal(t, http.StatusOK, code, body)
}

func TestFleet_StaleRevisionIs409WithTheCurrentCopy(t *testing.T) {
	m := newTestModule(t)
	code, _ := putFleet(m, `{"order":["mlab"]}`, "0")
	require.Equal(t, http.StatusOK, code)
	code, body := putFleet(m, `{"bogus":1}`, "0")
	assert.Equal(t, http.StatusConflict, code, "stale wins over invalid")
	assert.Contains(t, body, `"revision":1`)
	assert.Contains(t, body, `"order":["mlab"]`)
}

func TestFleet_AStoredBadValueIsAnErrorAndInvalidInTheGet(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyFleet, 0, func(Entry) (json.RawMessage, error) {
		return json.RawMessage(`{"order":["a","a"]}`), nil
	})
	require.NoError(t, err)
	_, _, err = m.FleetConfig()
	assert.Error(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig", ``)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"order":["air26","mlab"],"peer_ssh_alias":"","enabled":true},"revision":1,"invalid":true}`, string(got["fleet"]))
}
