package profiles

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testFingerprint  = hashOf("fingerprint-1")
	otherFingerprint = hashOf("fingerprint-2")
)

// hashOf returns a well-formed (64 lowercase hex) value that differs per seed.
// The daemon never recomputes a hash, so it need not match any payload.
func hashOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// sectionReq is the section PUT body of spec §4.6.
type sectionReq struct {
	ClientID    string          `json:"clientId"`
	BaseRev     int64           `json:"baseRev"`
	Hash        string          `json:"hash"`
	Fingerprint string          `json:"fingerprint"`
	Ordinal     int64           `json:"ordinal"`
	Payload     json.RawMessage `json:"payload"`
}

func defaultSectionReq(clientID string, baseRev int64, hash, payload string) sectionReq {
	return sectionReq{
		ClientID: clientID, BaseRev: baseRev, Hash: hash,
		Fingerprint: testFingerprint, Ordinal: 1, Payload: json.RawMessage(payload),
	}
}

func sectionBody(t *testing.T, clientID string, baseRev int64, hash, payload string) []byte {
	t.Helper()
	return mustJSON(t, defaultSectionReq(clientID, baseRev, hash, payload))
}

func sectionPath(profileID, section string) string {
	return "/api/profiles/" + profileID + "/sections/" + section
}

// putSection PUTs with the default shape, requires an applied 200 and returns
// the new rev.
func putSection(t *testing.T, m *Module, profileID, section, clientID string, baseRev int64, hash, payload string) int64 {
	t.Helper()
	rr := serve(m, http.MethodPut, sectionPath(profileID, section), sectionBody(t, clientID, baseRev, hash, payload))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp struct {
		Rev     int64 `json:"rev"`
		Applied *bool `json:"applied"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.NotNil(t, resp.Applied)
	require.True(t, *resp.Applied)
	return resp.Rev
}

func deleteSectionPath(profileID, section string, baseRev int64, clientID string) string {
	return fmt.Sprintf("%s?baseRev=%d&clientId=%s", sectionPath(profileID, section), baseRev, clientID)
}

// ── PUT: the §4.6 table over HTTP ──────────────────────────────────────────

func TestPutSectionAppliedThenReadBack(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")

	rr := serve(m, http.MethodPut, sectionPath(pid, "tabs.w-1_A"), sectionBody(t, clientA, 0, hashOf("1"), `{"tabs":["t1"]}`))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"rev":1,"applied":true}`, rr.Body.String())

	rr = serve(m, http.MethodPut, sectionPath(pid, "tabs.w-1_A"), sectionBody(t, clientB, 1, hashOf("2"), `{"tabs":[]}`))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"rev":2,"applied":true}`, rr.Body.String())

	rr = serve(m, http.MethodGet, sectionPath(pid, "tabs.w-1_A"), nil)
	require.Equal(t, http.StatusOK, rr.Code)
	got := decodeKeys(t, rr.Body.Bytes())
	assert.JSONEq(t, `"tabs.w-1_A"`, string(got["section"]))
	assert.JSONEq(t, `2`, string(got["rev"]))
	assert.JSONEq(t, `"`+hashOf("2")+`"`, string(got["hash"]))
	assert.JSONEq(t, `"`+testFingerprint+`"`, string(got["fingerprint"]))
	assert.JSONEq(t, `1`, string(got["ordinal"]))
	assert.JSONEq(t, `{"tabs":[]}`, string(got["payload"]))
	assert.JSONEq(t, `"`+clientB+`"`, string(got["writer"]), "writer comes from the body's clientId")
	assert.Contains(t, got, "updatedAt")
}

func TestPutSectionPayloadIsStoredByteForByte(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	// Key order and spacing a re-encode would not preserve.
	payload := `{"z": 1,   "a": [ 1 , 2 ]}`
	body := []byte(`{"clientId":"` + clientA + `","baseRev":0,"hash":"` + hashOf("1") +
		`","fingerprint":"` + testFingerprint + `","ordinal":1,"payload":` + payload + `}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, sectionPath(pid, "hosts"), body).Code)

	got, found, err := m.store.GetSection(pid, "hosts")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, payload, string(got.Payload))
}

func TestPutSectionConvergedIs200NotApplied(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "settings", clientA, 0, hashOf("1"), `{"a":1}`)
	putSection(t, m, pid, "settings", clientA, 1, hashOf("2"), `{"a":2}`)

	rr := serve(m, http.MethodPut, sectionPath(pid, "settings"), sectionBody(t, clientB, 1, hashOf("2"), `{"a":2}`))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"rev":2,"applied":false}`, rr.Body.String())
}

func TestPutSectionConflictCarriesTheSOTSide(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "settings", clientA, 0, hashOf("1"), `{"a":1}`)
	putSection(t, m, pid, "settings", clientA, 1, hashOf("2"), `{"a":2}`)

	rr := serve(m, http.MethodPut, sectionPath(pid, "settings"), sectionBody(t, clientB, 1, hashOf("3"), `{"a":3}`))
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"reason":"conflict","rev":2,"hash":"`+hashOf("2")+`","payload":{"a":2}}`, rr.Body.String())

	got, _, err := m.store.GetSection(pid, "settings")
	require.NoError(t, err)
	assert.Equal(t, hashOf("2"), got.Hash, "nothing written")
}

// A conflict against a section that is absent (never existed, or a tombstone)
// is rev 0 with no hash and no payload keys at all: there is no SOT side to
// offer, and the client reads rev 0 as "deleted under you" (§4.6.3).
func TestPutSectionConflictAgainstAbsentIsRevZeroWithoutHashOrPayload(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")

	rr := serve(m, http.MethodPut, sectionPath(pid, "tabs.w1"), sectionBody(t, clientA, 3, hashOf("1"), `{}`))
	require.Equal(t, http.StatusConflict, rr.Code)
	got := decodeKeys(t, rr.Body.Bytes())
	assert.Len(t, got, 2)
	assert.JSONEq(t, `"conflict"`, string(got["reason"]))
	assert.JSONEq(t, `0`, string(got["rev"]))

	// Same over a tombstone.
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientA), nil).Code)
	rr = serve(m, http.MethodPut, sectionPath(pid, "tabs.w1"), sectionBody(t, clientB, 1, hashOf("2"), `{}`))
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.JSONEq(t, `{"reason":"conflict","rev":0}`, rr.Body.String())
}

func TestPutSectionSchemaMismatchIs409Schema(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	newer := defaultSectionReq(clientA, 0, hashOf("1"), `{}`)
	newer.Fingerprint, newer.Ordinal = otherFingerprint, 7
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, sectionPath(pid, "hosts"), mustJSON(t, newer)).Code)

	// An older client, even with the right baseRev: schema is checked first.
	rr := serve(m, http.MethodPut, sectionPath(pid, "hosts"), sectionBody(t, clientB, 1, hashOf("2"), `{}`))
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.JSONEq(t, `{"reason":"schema","fingerprint":"`+otherFingerprint+`","ordinal":7}`, rr.Body.String())
}

// A tombstone keeps the shape of the row it replaced (§4.5): an older client
// cannot recreate the section, whatever baseRev it sends, and nothing is
// announced.
func TestPutSectionOlderSchemaOverATombstoneIs409SchemaAndSilent(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	newer := defaultSectionReq(clientA, 0, hashOf("1"), `{}`)
	newer.Fingerprint, newer.Ordinal = otherFingerprint, 7
	require.Equal(t, http.StatusOK, serve(m, http.MethodPut, sectionPath(pid, "tabs.w1"), mustJSON(t, newer)).Code)
	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientA), nil).Code)
	rec.events = nil

	for _, baseRev := range []int64{0, 2} {
		rr := serve(m, http.MethodPut, sectionPath(pid, "tabs.w1"), sectionBody(t, clientB, baseRev, hashOf("2"), `{}`))
		require.Equal(t, http.StatusConflict, rr.Code, "baseRev %d", baseRev)
		assert.JSONEq(t, `{"reason":"schema","fingerprint":"`+otherFingerprint+`","ordinal":7}`, rr.Body.String())
	}
	assert.Empty(t, rec.events)

	_, found, err := m.store.GetSection(pid, "tabs.w1")
	require.NoError(t, err)
	assert.False(t, found, "still a tombstone")
}

func TestPutSectionOverATombstoneContinuesTheRevision(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientA), nil).Code)
	assert.Equal(t, int64(3), putSection(t, m, pid, "tabs.w1", clientB, 0, hashOf("2"), `{}`))
}

func TestPutSectionUnknownProfileIs404(t *testing.T) {
	m, _ := newTestModule(t)
	for _, baseRev := range []int64{0, 2} {
		rr := serve(m, http.MethodPut, sectionPath(unknownProfileID, "hosts"), sectionBody(t, clientA, baseRev, hashOf("1"), `{}`))
		assert.Equal(t, http.StatusNotFound, rr.Code, "baseRev %d", baseRev)
	}
}

// badSectionPuts is every way a section PUT can be malformed. It is shared
// with the zero-broadcast test.
func badSectionPuts(t *testing.T) map[string][]byte {
	t.Helper()
	mutate := func(f func(*sectionReq)) []byte {
		req := defaultSectionReq(clientA, 0, hashOf("1"), `{}`)
		f(&req)
		return mustJSON(t, req)
	}
	without := func(key string) []byte {
		var obj map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(sectionBody(t, clientA, 0, hashOf("1"), `{}`), &obj))
		delete(obj, key)
		return mustJSON(t, obj)
	}
	baseRevLiteral := func(literal string) []byte {
		good := string(sectionBody(t, clientA, 0, hashOf("1"), `{}`))
		require.Contains(t, good, `"baseRev":0`)
		return []byte(strings.Replace(good, `"baseRev":0`, `"baseRev":`+literal, 1))
	}
	return map[string][]byte{
		"not json":           []byte(`{`),
		"empty body":         nil,
		"bad clientId":       mutate(func(r *sectionReq) { r.ClientID = "c_XYZ" }),
		"negative baseRev":   mutate(func(r *sectionReq) { r.BaseRev = -1 }),
		"missing baseRev":    without("baseRev"),
		"null baseRev":       baseRevLiteral("null"),
		"fractional baseRev": baseRevLiteral("0.5"),
		"string baseRev":     baseRevLiteral(`"0"`),
		"short hash":         mutate(func(r *sectionReq) { r.Hash = "abc" }),
		"uppercase hash":     mutate(func(r *sectionReq) { r.Hash = strings.ToUpper(hashOf("1")) }),
		"bad fingerprint":    mutate(func(r *sectionReq) { r.Fingerprint = "fp1" }),
		"zero ordinal":       mutate(func(r *sectionReq) { r.Ordinal = 0 }),
		"missing ordinal":    without("ordinal"),
		"array payload":      mutate(func(r *sectionReq) { r.Payload = json.RawMessage(`[]`) }),
		"null payload":       mutate(func(r *sectionReq) { r.Payload = json.RawMessage(`null`) }),
		"missing payload":    without("payload"),
	}
}

func TestPutSectionBadRequests(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	for name, body := range badSectionPuts(t) {
		rr := serve(m, http.MethodPut, sectionPath(pid, "hosts"), body)
		assert.Equal(t, http.StatusBadRequest, rr.Code, "%s: %s", name, rr.Body.String())
	}

	good := sectionBody(t, clientA, 0, hashOf("1"), `{}`)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPut, sectionPath("bad-id", "hosts"), good).Code)
	for _, section := range []string{"nope", "tabs.", "tabs.a.b", "tabs." + strings.Repeat("x", 65)} {
		assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodPut, sectionPath(pid, section), good).Code, section)
	}

	sections, err := m.store.ListSections(pid)
	require.NoError(t, err)
	assert.Empty(t, sections, "a refused write stores nothing")
}

// ── PUT: size ──────────────────────────────────────────────────────────────

// payloadOfSize returns a JSON object of exactly n bytes.
func payloadOfSize(n int) string {
	const frame = len(`{"p":""}`)
	return `{"p":"` + strings.Repeat("x", n-frame) + `"}`
}

func TestPutSectionPayloadOfExactlyFiveMiBIsAccepted(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	payload := payloadOfSize(PayloadCap)
	require.Len(t, payload, PayloadCap)

	body := sectionBody(t, clientA, 0, hashOf("1"), payload)
	require.Greater(t, len(body), PayloadCap, "the envelope pushes the body past the payload cap")
	rr := serve(m, http.MethodPut, sectionPath(pid, "hosts"), body)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	got, _, err := m.store.GetSection(pid, "hosts")
	require.NoError(t, err)
	assert.Len(t, got.Payload, PayloadCap)
}

func TestPutSectionPayloadOneByteOverIs413(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	payload := payloadOfSize(PayloadCap + 1)
	body := sectionBody(t, clientA, 0, hashOf("1"), payload)
	require.LessOrEqual(t, len(body), putBodyCap, "the body fits: it is the payload check that must refuse")

	rr := serve(m, http.MethodPut, sectionPath(pid, "hosts"), body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	assert.Empty(t, rec.events)
}

func TestPutSectionBodyOverTheBodyCapIs413(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	// Not even JSON: the cap must trip before anything is parsed.
	body := bytes.Repeat([]byte("x"), putBodyCap+1)

	rr := serve(m, http.MethodPut, sectionPath(pid, "hosts"), body)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
	assert.Empty(t, rec.events)
}

func TestPutBodyCapLeavesRoomForTheEnvelope(t *testing.T) {
	assert.Equal(t, PayloadCap+64<<10, putBodyCap)
}

// ── PUT: contention ────────────────────────────────────────────────────────

func TestPutSectionContendedIs503WithRetryAfter(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	s := m.store
	// Same neighbour as the store test: every look finds the section freshly
	// recreated and deleted again. It writes the request's own shape — a
	// tombstone keeps its shape, and a different one would be a schema 409.
	neighbour := sec("tabs.w1", "hX", `{}`, clientB)
	neighbour.Fingerprint = testFingerprint
	s.afterSectionRead = func() {
		saved := s.afterSectionRead
		s.afterSectionRead = nil
		rev := mustPut(t, s, pid, neighbour, 0)
		_, err := s.DeleteSection(pid, "tabs.w1", clientB, rev)
		require.NoError(t, err)
		s.afterSectionRead = saved
	}

	rr := serve(m, http.MethodPut, sectionPath(pid, "tabs.w1"), sectionBody(t, clientA, 0, hashOf("1"), `{}`))
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	assert.Equal(t, "1", rr.Header().Get("Retry-After"))
	assert.Empty(t, rec.events)
}

// ── GET ────────────────────────────────────────────────────────────────────

func TestGetSectionFailures(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")

	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodGet, sectionPath(pid, "hosts"), nil).Code, "absent")
	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodGet, sectionPath(unknownProfileID, "hosts"), nil).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodGet, sectionPath(pid, "nope"), nil).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodGet, sectionPath("bad", "hosts"), nil).Code)

	putSection(t, m, pid, "hosts", clientA, 0, hashOf("1"), `{}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "hosts", 1, clientA), nil).Code)
	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodGet, sectionPath(pid, "hosts"), nil).Code, "tombstone reads as absent")
}

func TestGetProfileReturnsEverySectionKeyedByNameWithoutTombstones(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")

	rr := serve(m, http.MethodGet, "/api/profiles/"+pid, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"sections":{}}`, rr.Body.String(), "an object, never null")

	putSection(t, m, pid, "hosts", clientA, 0, hashOf("h"), `{"hosts":1}`)
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("t1"), `{"tabs":1}`)
	putSection(t, m, pid, "tabs.w2", clientA, 0, hashOf("t2"), `{"tabs":2}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w2", 1, clientA), nil).Code)

	rr = serve(m, http.MethodGet, "/api/profiles/"+pid, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Sections map[string]Section `json:"sections"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.Sections, 2)
	assert.JSONEq(t, `{"hosts":1}`, string(resp.Sections["hosts"].Payload))
	assert.Equal(t, "hosts", resp.Sections["hosts"].Section)
	assert.Equal(t, hashOf("t1"), resp.Sections["tabs.w1"].Hash)
	assert.NotContains(t, resp.Sections, "tabs.w2")

	assert.Equal(t, http.StatusNotFound, serve(m, http.MethodGet, "/api/profiles/"+unknownProfileID, nil).Code)
	assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodGet, "/api/profiles/bad", nil).Code)
}

// ── DELETE ─────────────────────────────────────────────────────────────────

func TestDeleteSection(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{}`)

	rr := serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientB), nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"rev":2}`, rr.Body.String())
	assert.Equal(t, clientB, readRaw(t, m.store, pid, "tabs.w1").Writer, "writer comes from the query's clientId")

	// Idempotent: again, and for a section that never existed.
	rr = serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientA), nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"rev":2}`, rr.Body.String())
	rr = serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.never", 5, clientA), nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"rev":0}`, rr.Body.String())
}

func TestDeleteSectionStaleRevIs409Conflict(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{"a":1}`)
	putSection(t, m, pid, "tabs.w1", clientA, 1, hashOf("2"), `{"a":2}`)

	rr := serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientB), nil)
	require.Equal(t, http.StatusConflict, rr.Code)
	assert.JSONEq(t, `{"reason":"conflict","rev":2,"hash":"`+hashOf("2")+`","payload":{"a":2}}`, rr.Body.String())
	_, found, err := m.store.GetSection(pid, "tabs.w1")
	require.NoError(t, err)
	assert.True(t, found)
}

// badSectionDeletes maps a label to a full request path.
func badSectionDeletes(pid string) map[string]string {
	base := sectionPath(pid, "tabs.w1")
	return map[string]string{
		"no query":         base,
		"no baseRev":       base + "?clientId=" + clientA,
		"no clientId":      base + "?baseRev=1",
		"bad clientId":     base + "?baseRev=1&clientId=nope",
		"negative baseRev": base + "?baseRev=-1&clientId=" + clientA,
		"textual baseRev":  base + "?baseRev=one&clientId=" + clientA,
		"bad section":      sectionPath(pid, "nope") + "?baseRev=1&clientId=" + clientA,
		"bad profile id":   sectionPath("bad", "tabs.w1") + "?baseRev=1&clientId=" + clientA,
	}
}

func TestDeleteSectionFailures(t *testing.T) {
	m, _ := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{}`)

	for name, path := range badSectionDeletes(pid) {
		assert.Equal(t, http.StatusBadRequest, serve(m, http.MethodDelete, path, nil).Code, name)
	}
	assert.Equal(t, http.StatusNotFound,
		serve(m, http.MethodDelete, deleteSectionPath(unknownProfileID, "tabs.w1", 1, clientA), nil).Code)

	_, found, err := m.store.GetSection(pid, "tabs.w1")
	require.NoError(t, err)
	assert.True(t, found)
}

// ── broadcast: the wire contract with P2b ──────────────────────────────────

func TestAppliedPutBroadcastsExactlyOneEventWithTheContractKeys(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	require.Empty(t, rec.events, "creating a profile is not a section event")

	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{"never":"on the wire"}`)
	require.Len(t, rec.events, 1)
	assert.Equal(t, "profile", rec.events[0].Type)

	got := decodeKeys(t, []byte(rec.events[0].Value))
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"profileId", "section", "rev", "hash", "writerClientId"}, keys,
		"exact key set; deleted is omitted when false, and the payload is never sent")
	assert.JSONEq(t, `"`+pid+`"`, string(got["profileId"]))
	assert.JSONEq(t, `"tabs.w1"`, string(got["section"]))
	assert.JSONEq(t, `1`, string(got["rev"]))
	assert.JSONEq(t, `"`+hashOf("1")+`"`, string(got["hash"]))
	assert.JSONEq(t, `"`+clientA+`"`, string(got["writerClientId"]))

	putSection(t, m, pid, "tabs.w1", clientB, 1, hashOf("2"), `{}`)
	require.Len(t, rec.events, 2)
	second := decodeKeys(t, []byte(rec.events[1].Value))
	assert.JSONEq(t, `2`, string(second["rev"]))
	assert.JSONEq(t, `"`+clientB+`"`, string(second["writerClientId"]))
}

func TestAppliedDeleteBroadcastsOneEventWithDeletedTrue(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{}`)
	rec.events = nil

	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientB), nil).Code)
	require.Len(t, rec.events, 1)
	assert.Equal(t, "profile", rec.events[0].Type)
	got := decodeKeys(t, []byte(rec.events[0].Value))
	assert.Len(t, got, 6)
	assert.JSONEq(t, `"`+pid+`"`, string(got["profileId"]))
	assert.JSONEq(t, `"tabs.w1"`, string(got["section"]))
	assert.JSONEq(t, `2`, string(got["rev"]), "the tombstone's rev")
	assert.JSONEq(t, `""`, string(got["hash"]), "a tombstone has no hash")
	assert.JSONEq(t, `"`+clientB+`"`, string(got["writerClientId"]))
	assert.JSONEq(t, `true`, string(got["deleted"]))
}

func TestIdempotentDeleteBroadcastsNothing(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "tabs.w1", clientA, 0, hashOf("1"), `{}`)
	require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, deleteSectionPath(pid, "tabs.w1", 1, clientA), nil).Code)
	rec.events = nil

	// The exact retry — same baseRev, and the no-op reports the same rev the
	// real delete did — plus other baseRevs, plus a section that never was.
	for _, path := range []string{
		deleteSectionPath(pid, "tabs.w1", 1, clientA),
		deleteSectionPath(pid, "tabs.w1", 2, clientB),
		deleteSectionPath(pid, "tabs.w1", 0, clientB),
		deleteSectionPath(pid, "tabs.never", 0, clientA),
	} {
		require.Equal(t, http.StatusOK, serve(m, http.MethodDelete, path, nil).Code, path)
	}
	assert.Empty(t, rec.events)
}

func TestNonWritesBroadcastNothing(t *testing.T) {
	m, rec := newTestModule(t)
	pid := createProfile(t, m, "P")
	putSection(t, m, pid, "hosts", clientA, 0, hashOf("1"), `{}`)
	putSection(t, m, pid, "hosts", clientA, 1, hashOf("2"), `{}`)
	rec.events = nil

	expect := func(name string, want int, method, path string, body []byte) {
		t.Helper()
		rr := serve(m, method, path, body)
		require.Equal(t, want, rr.Code, "%s: %s", name, rr.Body.String())
		assert.Empty(t, rec.events, name)
	}

	expect("converged", http.StatusOK, http.MethodPut, sectionPath(pid, "hosts"), sectionBody(t, clientB, 1, hashOf("2"), `{}`))
	expect("conflict", http.StatusConflict, http.MethodPut, sectionPath(pid, "hosts"), sectionBody(t, clientB, 1, hashOf("3"), `{}`))
	schema := defaultSectionReq(clientB, 2, hashOf("3"), `{}`)
	schema.Fingerprint = otherFingerprint
	expect("schema", http.StatusConflict, http.MethodPut, sectionPath(pid, "hosts"), mustJSON(t, schema))
	expect("put 404", http.StatusNotFound, http.MethodPut, sectionPath(unknownProfileID, "hosts"), sectionBody(t, clientA, 0, hashOf("1"), `{}`))
	expect("delete conflict", http.StatusConflict, http.MethodDelete, deleteSectionPath(pid, "hosts", 1, clientB), nil)
	expect("delete 404", http.StatusNotFound, http.MethodDelete, deleteSectionPath(unknownProfileID, "hosts", 1, clientB), nil)
	for name, body := range badSectionPuts(t) {
		expect("put 400 "+name, http.StatusBadRequest, http.MethodPut, sectionPath(pid, "hosts"), body)
	}
	for name, path := range badSectionDeletes(pid) {
		expect("delete 400 "+name, http.StatusBadRequest, http.MethodDelete, path, nil)
	}

	// Neither do the non-section routes.
	expect("list", http.StatusOK, http.MethodGet, "/api/profiles", nil)
	expect("get", http.StatusOK, http.MethodGet, sectionPath(pid, "hosts"), nil)
	expect("rename", http.StatusOK, http.MethodPatch, "/api/profiles/"+pid, mustJSON(t, map[string]string{"name": "N"}))
	expect("attach", http.StatusOK, http.MethodPut, "/api/profiles/"+pid+"/attachment",
		mustJSON(t, map[string]string{"clientId": clientA, "deviceName": "Air"}))
	expect("detach", http.StatusOK, http.MethodDelete, "/api/profiles/"+pid+"/attachment?clientId="+clientA, nil)

	got, _, err := m.store.GetSection(pid, "hosts")
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.Rev, "and none of it moved the section")
}
