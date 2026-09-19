package profiles

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const unknownProfileID = "p_ffffffffffff"

// recordedEvent is one call to the injected broadcast func.
type recordedEvent struct {
	Type  string
	Value string
}

type eventRecorder struct{ events []recordedEvent }

func (r *eventRecorder) broadcast(eventType, value string) {
	r.events = append(r.events, recordedEvent{Type: eventType, Value: value})
}

// newTestModule builds a Module on an in-memory store whose broadcasts land in
// the returned recorder.
func newTestModule(t *testing.T) (*Module, *eventRecorder) {
	t.Helper()
	s, err := OpenStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	rec := &eventRecorder{}
	return &Module{store: s, broadcast: rec.broadcast}, rec
}

// serve runs one request through a real mux wired with RegisterRoutes, so the
// route patterns and PathValue are part of what is tested.
func serve(m *Module, method, path string, body []byte) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// decodeKeys decodes a JSON object keeping every value raw, so tests can assert
// on which keys are present as well as on their values.
func decodeKeys(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var out map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &out), string(body))
	return out
}

func createProfile(t *testing.T, m *Module, name string) string {
	t.Helper()
	rr := serve(m, http.MethodPost, "/api/profiles", mustJSON(t, map[string]string{"name": name}))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.NoError(t, validateProfileID(resp.ID))
	return resp.ID
}

func attach(t *testing.T, m *Module, profileID, clientID, deviceName string) {
	t.Helper()
	rr := serve(m, http.MethodPut, "/api/profiles/"+profileID+"/attachment",
		mustJSON(t, map[string]string{"clientId": clientID, "deviceName": deviceName}))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// ── registration ───────────────────────────────────────────────────────────

// An unregistered route answers with the mux's own plain-text 404/405. Every
// handler here answers with JSON on success, so a 200 + JSON content type can
// only have come from a handler.
func TestAllTenRoutesAreRegistered(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "Routes")
	sectionPath := "/api/profiles/" + pid + "/sections/hosts"

	steps := []struct {
		name, method, path string
		body               []byte
	}{
		{"list", http.MethodGet, "/api/profiles", nil},
		{"create", http.MethodPost, "/api/profiles", mustJSON(t, map[string]string{"name": "Another"})},
		{"rename", http.MethodPatch, "/api/profiles/" + pid, mustJSON(t, map[string]string{"name": "Renamed"})},
		{"get all", http.MethodGet, "/api/profiles/" + pid, nil},
		{"put section", http.MethodPut, sectionPath, sectionBody(t, clientA, 0, hashOf("1"), `{"a":1}`)},
		{"get section", http.MethodGet, sectionPath, nil},
		{"delete section", http.MethodDelete, sectionPath + "?baseRev=1&clientId=" + clientA, nil},
		{"put attachment", http.MethodPut, "/api/profiles/" + pid + "/attachment",
			mustJSON(t, map[string]string{"clientId": clientA, "deviceName": "Air"})},
		{"delete attachment", http.MethodDelete, "/api/profiles/" + pid + "/attachment?clientId=" + clientA, nil},
		{"delete profile", http.MethodDelete, "/api/profiles/" + pid, nil},
	}
	require.Len(t, steps, 10)
	for _, step := range steps {
		rr := serve(m, step.method, step.path, step.body)
		assert.Equal(t, http.StatusOK, rr.Code, "%s: %s", step.name, rr.Body.String())
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"), step.name)
	}
}

func TestUnregisteredMethodIsRefusedByTheMux(t *testing.T) {
	m, _ := newTestModule(t)
	rr := serve(m, http.MethodPut, "/api/profiles", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

// ── profiles ───────────────────────────────────────────────────────────────

func TestListProfilesEmptyIsAnArrayNotNull(t *testing.T) {
	m, _ := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/profiles", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"profiles":[]}`, rr.Body.String())
}

func TestListProfilesCarriesSectionIndexAndAttachments(t *testing.T) {
	m, _ := newTestModule(t)
	empty := createProfile(t, m, "Empty")
	full := createProfile(t, m, "Full")
	putSection(t, m, full, "hosts", clientA, 0, hashOf("1"), `{"secret":"payload"}`)
	attach(t, m, full, clientB, "  Air  ")

	rr := serve(m, http.MethodGet, "/api/profiles", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Profiles []map[string]json.RawMessage `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Profiles, 2)
	// Both were created within the same millisecond, where the store's order
	// falls back to the (random) id — so look them up rather than index them.
	byID := map[string]map[string]json.RawMessage{}
	for _, p := range resp.Profiles {
		var id string
		require.NoError(t, json.Unmarshal(p["id"], &id))
		byID[id] = p
	}
	require.Contains(t, byID, empty)
	require.Contains(t, byID, full)

	first := byID[empty]
	assert.JSONEq(t, `"Empty"`, string(first["name"]))
	assert.Contains(t, first, "createdAt")
	assert.Contains(t, first, "updatedAt")
	assert.JSONEq(t, `[]`, string(first["sections"]), "never null")
	assert.JSONEq(t, `[]`, string(first["attachments"]), "never null")

	second := byID[full]
	var sections []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(second["sections"], &sections))
	require.Len(t, sections, 1)
	assert.JSONEq(t, `"hosts"`, string(sections[0]["section"]))
	assert.JSONEq(t, `1`, string(sections[0]["rev"]))
	assert.JSONEq(t, `"`+hashOf("1")+`"`, string(sections[0]["hash"]))
	assert.JSONEq(t, `"`+testFingerprint+`"`, string(sections[0]["fingerprint"]))
	assert.JSONEq(t, `1`, string(sections[0]["ordinal"]))
	assert.NotContains(t, sections[0], "payload", "the index never carries payloads")
	assert.NotContains(t, rr.Body.String(), "secret")

	var attachments []Attachment
	require.NoError(t, json.Unmarshal(second["attachments"], &attachments))
	require.Len(t, attachments, 1)
	assert.Equal(t, clientB, attachments[0].ClientID)
	assert.Equal(t, full, attachments[0].ProfileID)
	assert.Equal(t, "Air", attachments[0].DeviceName, "trimmed")
}

func TestCreateProfileTrimsTheNameAndAllowsDuplicates(t *testing.T) {
	m, _ := newTestModule(t)
	a := createProfile(t, m, "  Work  ")
	b := createProfile(t, m, "Work")
	assert.NotEqual(t, a, b)

	p, found, err := m.store.GetProfile(a)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "Work", p.Name)
}

func TestCreateProfileBadRequests(t *testing.T) {
	m, _ := newTestModule(t)
	cases := map[string][]byte{
		"empty name":   mustJSON(t, map[string]string{"name": "   "}),
		"long name":    mustJSON(t, map[string]string{"name": strings.Repeat("x", 65)}),
		"control char": mustJSON(t, map[string]string{"name": "a\x00b"}),
		"no name":      []byte(`{}`),
		"not json":     []byte(`{`),
		"empty body":   nil,
	}
	for name, body := range cases {
		rr := serve(m, http.MethodPost, "/api/profiles", body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, name)
	}
	list, err := m.store.ListProfiles()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestSmallBodyRoutesRefuseOversizedBodies(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	big := mustJSON(t, map[string]string{"name": "x", "pad": strings.Repeat("x", smallBodyCap)})

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/profiles"},
		{http.MethodPatch, "/api/profiles/" + pid},
		{http.MethodPut, "/api/profiles/" + pid + "/attachment"},
	} {
		rr := serve(m, c.method, c.path, big)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code, "%s %s", c.method, c.path)
	}
}

func TestRenameProfile(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "Old")

	rr := serve(m, http.MethodPatch, "/api/profiles/"+pid, mustJSON(t, map[string]string{"name": "  New  "}))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	got := decodeKeys(t, rr.Body.Bytes())
	assert.JSONEq(t, `"`+pid+`"`, string(got["id"]))
	assert.JSONEq(t, `"New"`, string(got["name"]))

	p, _, err := m.store.GetProfile(pid)
	require.NoError(t, err)
	assert.Equal(t, "New", p.Name)
}

func TestRenameProfileFailures(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "Old")
	good := mustJSON(t, map[string]string{"name": "New"})

	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodPatch, "/api/profiles/"+unknownProfileID, good).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPatch, "/api/profiles/not-an-id", good).Code)
	assert.Equal(t, http.StatusBadRequest,
		serve(m, http.MethodPatch, "/api/profiles/"+pid, mustJSON(t, map[string]string{"name": ""})).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPatch, "/api/profiles/"+pid, []byte(`nope`)).Code)
}

func TestDeleteProfile(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "Gone")
	putSection(t, m, pid, "hosts", clientA, 0, hashOf("1"), `{}`)

	rr := serve(m, http.MethodDelete, "/api/profiles/"+pid, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"deleted":true}`, rr.Body.String())

	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodGet, "/api/profiles/"+pid, nil).Code)
	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodDelete, "/api/profiles/"+pid, nil).Code, "second delete")
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodDelete, "/api/profiles/P_BAD", nil).Code)
}

func TestDeleteProfileWhileAttachedIs409WithTheAttachments(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "Held")
	attach(t, m, pid, clientA, "Air")

	rr := serve(m, http.MethodDelete, "/api/profiles/"+pid, nil)
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	got := decodeKeys(t, rr.Body.Bytes())
	assert.JSONEq(t, `"attached"`, string(got["reason"]))
	var attachments []Attachment
	require.NoError(t, json.Unmarshal(got["attachments"], &attachments))
	require.Len(t, attachments, 1)
	assert.Equal(t, clientA, attachments[0].ClientID)
	assert.Equal(t, "Air", attachments[0].DeviceName)

	_, found, err := m.store.GetProfile(pid)
	require.NoError(t, err)
	assert.True(t, found, "refused means untouched")
}

// ── attachments ────────────────────────────────────────────────────────────

func TestPutAttachment(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")

	rr := serve(m, http.MethodPut, "/api/profiles/"+pid+"/attachment",
		mustJSON(t, map[string]string{"clientId": clientA, "deviceName": " Air "}))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"attached":true}`, rr.Body.String())

	list, err := m.store.ListAttachments(pid)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Air", list[0].DeviceName)
}

func TestPutAttachmentFailures(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	path := "/api/profiles/" + pid + "/attachment"
	good := mustJSON(t, map[string]string{"clientId": clientA, "deviceName": "Air"})

	assert.Equal(t, http.StatusNotFound,
		serve(m, http.MethodPut, "/api/profiles/"+unknownProfileID+"/attachment", good).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPut, "/api/profiles/bad/attachment", good).Code)
	assert.Equal(t, http.StatusBadRequest,
		serve(m, http.MethodPut, path, mustJSON(t, map[string]string{"clientId": "nope", "deviceName": "Air"})).Code)
	assert.Equal(t, http.StatusBadRequest,
		serve(m, http.MethodPut, path, mustJSON(t, map[string]string{"clientId": clientA, "deviceName": " "})).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPut, path, []byte(`[`)).Code)

	list, err := m.store.ListAttachments(pid)
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestDeleteAttachmentOnlyDetachesFromThePathProfile(t *testing.T) {
	m, _ := newTestModule(t)
	mine := createProfile(t, m, "Mine")
	other := createProfile(t, m, "Other")
	attach(t, m, mine, clientA, "Air")

	rr := serve(m, http.MethodDelete, "/api/profiles/"+other+"/attachment?clientId="+clientA, nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"detached":false}`, rr.Body.String())
	list, err := m.store.ListAttachments(mine)
	require.NoError(t, err)
	assert.Len(t, list, 1, "a detach aimed at another profile is nobody's detach")

	rr = serve(m, http.MethodDelete, "/api/profiles/"+mine+"/attachment?clientId="+clientA, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"detached":true}`, rr.Body.String())

	rr = serve(m, http.MethodDelete, "/api/profiles/"+mine+"/attachment?clientId="+clientA, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"detached":false}`, rr.Body.String(), "idempotent")
}

func TestDeleteAttachmentFailures(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")

	assert.Equal(t, http.StatusBadRequest,
		serve(m, http.MethodDelete, "/api/profiles/"+pid+"/attachment", nil).Code, "clientId is required")
	assert.Equal(t, http.StatusBadRequest,
		serve(m, http.MethodDelete, "/api/profiles/"+pid+"/attachment?clientId=nope", nil).Code)
	assert.Equal(t, http.StatusBadRequest,
		serve(m, http.MethodDelete, "/api/profiles/bad/attachment?clientId="+clientA, nil).Code)
	assert.Equal(t, http.StatusNotFound,
		serve(m, http.MethodDelete, "/api/profiles/"+unknownProfileID+"/attachment?clientId="+clientA, nil).Code)
}

// ── misc ───────────────────────────────────────────────────────────────────

func TestANilBroadcastIsTolerated(t *testing.T) {
	m, _ := newTestModule(t)
	m.broadcast = nil
	pid := createProfile(t, m, "P")
	rr := serve(m, http.MethodPut, "/api/profiles/"+pid+"/sections/hosts",
		sectionBody(t, clientA, 0, hashOf("1"), `{}`))
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func TestStoreFailureIs500WithoutLeakingTheError(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	require.NoError(t, m.store.Close())

	for _, c := range []struct {
		method, path string
		body         []byte
	}{
		{http.MethodGet, "/api/profiles", nil},
		{http.MethodGet, "/api/profiles/" + pid, nil},
		{http.MethodPut, "/api/profiles/" + pid + "/sections/hosts", sectionBody(t, clientA, 0, hashOf("1"), `{}`)},
		{http.MethodDelete, "/api/profiles/" + pid + "/sections/hosts?baseRev=0&clientId=" + clientA, nil},
	} {
		rr := serve(m, c.method, c.path, c.body)
		assert.Equal(t, http.StatusInternalServerError, rr.Code, "%s %s", c.method, c.path)
		assert.NotContains(t, rr.Body.String(), "sql", "%s %s", c.method, c.path)
	}
	assert.Empty(t, rec.events)
}
