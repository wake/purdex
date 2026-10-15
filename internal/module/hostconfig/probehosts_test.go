package hostconfig

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withSSHAliases makes exactly these aliases exist in "the SSH config" for the test.
func withSSHAliases(t *testing.T, names ...string) {
	t.Helper()
	old := sshAliasOK
	sshAliasOK = func(a string) bool { return slices.Contains(names, a) }
	t.Cleanup(func() { sshAliasOK = old })
}

func putProbe(m *Module, items string, rev int64) (code int, body string) {
	rr := serve(m, http.MethodPut, "/api/hostconfig/probe-hosts", `{"items":`+items+`,"baseRevision":`+strconv.FormatInt(rev, 10)+`}`)
	return rr.Code, rr.Body.String()
}

const probeRow = `{"name":"Lab","ssh_alias":"eq14","icon":"Desktop","icon_weight":"fill","colors":{"console":{"main":{"color":"#336699","alpha":100},"middle":{"alpha":60},"light":{"color":"#aabbcc","alpha":22}}}}`

func TestProbeHosts_RoundTripGeneratesAndKeepsIds(t *testing.T) {
	withSSHAliases(t, "eq14", "other")
	m := newTestModule(t)
	code, body := putProbe(m, `[`+probeRow+`]`, 0)
	require.Equal(t, http.StatusOK, code, body)
	var got struct {
		Items    []map[string]any `json:"items"`
		Revision int64            `json:"revision"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &got))
	require.Len(t, got.Items, 1)
	id, _ := got.Items[0]["id"].(string)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}$`), id, "an empty id is generated as 8 hex")

	// A second save sends the id back: kept, not regenerated.
	row := strings.Replace(probeRow, `{"name"`, `{"id":"`+id+`","name"`, 1)
	code, body = putProbe(m, `[`+row+`]`, 1)
	require.Equal(t, http.StatusOK, code, body)
	pr, rev, err := m.ProbeHosts()
	require.NoError(t, err)
	assert.Equal(t, int64(2), rev)
	require.Len(t, pr, 1)
	assert.Equal(t, id, pr[0].ID)
	assert.Equal(t, "eq14", pr[0].SSHAlias)
	assert.Equal(t, "fill", pr[0].IconWeight)
}

func TestProbeHosts_GeneratedIdsAreDistinct(t *testing.T) {
	withSSHAliases(t, "a")
	m := newTestModule(t)
	rows := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		rows = append(rows, `{"name":"h","ssh_alias":"a"}`)
	}
	code, body := putProbe(m, `[`+strings.Join(rows, ",")+`]`, 0)
	require.Equal(t, http.StatusOK, code, body)
	pr, _, err := m.ProbeHosts()
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, r := range pr {
		seen[r.ID] = true
	}
	assert.Len(t, seen, 16)
}

func TestProbeHosts_IsStrict(t *testing.T) {
	withSSHAliases(t, "eq14")
	m := newTestModule(t)
	rowWith := func(from, to string) string {
		require.Contains(t, probeRow, from)
		return strings.Replace(probeRow, from, to, 1)
	}
	bad := map[string]string{
		"unknown row key":      rowWith(`"name"`, `"bogus":1,"name"`),
		"name empty":           rowWith(`"Lab"`, `""`),
		"name blank":           rowWith(`"Lab"`, `"   "`),
		"name 41 chars":        rowWith(`"Lab"`, `"`+strings.Repeat("x", 41)+`"`),
		"alias missing":        `{"name":"Lab"}`,
		"icon lower case":      rowWith(`"Desktop"`, `"desktop"`),
		"icon with junk":       rowWith(`"Desktop"`, `"Desk top"`),
		"weight unknown":       rowWith(`"fill"`, `"heavy"`),
		"bad id":               rowWith(`{"name"`, `{"id":"../x","name"`),
		"unknown mode":         rowWith(`"console"`, `"web"`),
		"unknown layer":        rowWith(`"middle"`, `"dark"`),
		"unknown layer key":    rowWith(`"alpha":60`, `"alpha":60,"x":1`),
		"main without color":   rowWith(`"main":{"color":"#336699","alpha":100}`, `"main":{"alpha":100}`),
		"color not hex":        rowWith(`#336699`, `red`),
		"color short hex":      rowWith(`#336699`, `#369`),
		"alpha 101":            rowWith(`"alpha":100`, `"alpha":101`),
		"alpha negative":       rowWith(`"alpha":100`, `"alpha":-1`),
		"alpha fraction":       rowWith(`"alpha":100`, `"alpha":50.5`),
		"alpha missing":        rowWith(`"main":{"color":"#336699","alpha":100}`, `"main":{"color":"#336699"}`),
		"colors not an object": rowWith(`"colors":{"console":{"main":{"color":"#336699","alpha":100},"middle":{"alpha":60},"light":{"color":"#aabbcc","alpha":22}}}`, `"colors":[]`),
		"row not an object":    `"x"`,
	}
	for why, row := range bad {
		code, body := putProbe(m, `[`+row+`]`, 0)
		assert.Equal(t, http.StatusBadRequest, code, "%s: %s", why, body)
	}
	code, _ := putProbe(m, `{}`, 0)
	assert.Equal(t, http.StatusBadRequest, code, "items must be an array")
	e, err := m.store.Get(KeyProbeHosts)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
}

func TestProbeHosts_AtMostSixteenRows(t *testing.T) {
	withSSHAliases(t, "a")
	m := newTestModule(t)
	mk := func(n int) string {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = `{"name":"h","ssh_alias":"a"}`
		}
		return `[` + strings.Join(rows, ",") + `]`
	}
	code, body := putProbe(m, mk(17), 0)
	assert.Equal(t, http.StatusBadRequest, code, body)
	code, body = putProbe(m, mk(16), 0)
	assert.Equal(t, http.StatusOK, code, body)
}

func TestProbeHosts_NameBoundsAreInCharacters(t *testing.T) {
	withSSHAliases(t, "a")
	m := newTestModule(t)
	code, body := putProbe(m, `[{"name":"`+strings.Repeat("機", 40)+`","ssh_alias":"a"}]`, 0)
	assert.Equal(t, http.StatusOK, code, "40 characters (120 bytes) fit: "+body)
}

func TestProbeHosts_UnknownAliasIs400WithTheNamedReason(t *testing.T) {
	withSSHAliases(t, "eq14")
	m := newTestModule(t)
	code, body := putProbe(m, `[{"name":"x","ssh_alias":"gone"}]`, 0)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "unknown_ssh_alias: gone")
	code, body = putProbe(m, `[{"name":"x","ssh_alias":"a;rm"}]`, 0)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, body, "unknown_ssh_alias: a;rm")
}

func TestProbeHosts_StaleRevisionIs409WithTheCurrentCopy(t *testing.T) {
	withSSHAliases(t, "eq14")
	m := newTestModule(t)
	code, _ := putProbe(m, `[`+probeRow+`]`, 0)
	require.Equal(t, http.StatusOK, code)
	// Stale AND invalid: 409 wins, with the server copy.
	rr := serve(m, http.MethodPut, "/api/hostconfig/probe-hosts", `{"items":[{"bogus":1}],"baseRevision":0}`)
	assert.Equal(t, http.StatusConflict, rr.Code)
	var got struct {
		Items    []map[string]any `json:"items"`
		Revision int64            `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.Equal(t, int64(1), got.Revision)
	require.Len(t, got.Items, 1)
	assert.Equal(t, "Lab", got.Items[0]["name"])
}

func TestProbeHosts_GetCarriesTheFieldAndEmptyIsAList(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", ``)
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]struct {
		Items    json.RawMessage `json:"items"`
		Revision int64           `json:"revision"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Contains(t, got, "probeHosts")
	assert.JSONEq(t, `[]`, string(got["probeHosts"].Items))
	pr, rev, err := m.ProbeHosts()
	require.NoError(t, err)
	assert.Empty(t, pr)
	assert.Equal(t, int64(0), rev)
}

// A stored row whose alias is gone from the SSH config is dropped on read, with its reason; the others stay.
func TestProbeHosts_AStoredRowWithAGoneAliasIsDroppedOnRead(t *testing.T) {
	withSSHAliases(t, "eq14", "other")
	m := newTestModule(t)
	two := `[{"name":"A","ssh_alias":"eq14"},{"name":"B","ssh_alias":"other"}]`
	code, body := putProbe(m, two, 0)
	require.Equal(t, http.StatusOK, code, body)

	withSSHAliases(t, "eq14") // "other" leaves the SSH config
	rr := serve(m, http.MethodGet, "/api/hostconfig", ``)
	var got map[string]struct {
		Items   json.RawMessage `json:"items"`
		Dropped *Dropped        `json:"dropped"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	var items []map[string]any
	require.NoError(t, json.Unmarshal(got["probeHosts"].Items, &items))
	require.Len(t, items, 1)
	assert.Equal(t, "A", items[0]["name"])
	require.NotNil(t, got["probeHosts"].Dropped)
	assert.Equal(t, 1, got["probeHosts"].Dropped.Count)
	assert.Contains(t, got["probeHosts"].Dropped.Reasons[0], "unknown_ssh_alias: other")

	pr, _, err := m.ProbeHosts()
	require.NoError(t, err)
	require.Len(t, pr, 1, "the reader keeps the lenient view too")
	assert.Equal(t, "A", pr[0].Name)
}

func TestProbeHosts_AStoredValueThatIsNotAListIsInvalidInTheGet(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyProbeHosts, 0, func(Entry) (json.RawMessage, error) { return json.RawMessage(`{"a":1}`), nil })
	require.NoError(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig", ``)
	var got map[string]struct {
		Items   json.RawMessage `json:"items"`
		Invalid bool            `json:"invalid"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.True(t, got["probeHosts"].Invalid)
	assert.JSONEq(t, `[]`, string(got["probeHosts"].Items))
}
