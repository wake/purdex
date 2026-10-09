package profiles

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
	"github.com/wake/purdex/internal/profilehash"
)

// QP-1c task 7 (spec §5.2): the phone's one write, by appending to the tabs.<ws> section of its own profile.

const (
	storedTabs = `{"order":["t1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999}},"extra":{"a":1}}`
	devID      = "d_0123456789ab"
	devClient  = "c_0123456789ab"
	goodAppend = `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1}}`
)

func sumOf(t *testing.T, payload string) string {
	t.Helper()
	h, err := profilehash.Sum([]byte(payload))
	require.NoError(t, err)
	return h
}

type appendEnv struct {
	m     *Module
	rec   *eventRecorder
	mine  string
	rev   int64
	phone *devices.Principal
}

func newAppendEnv(t *testing.T) *appendEnv {
	t.Helper()
	m, rec := newTestModule(t)
	mine := createProfile(t, m, "mine")
	rev := putSection(t, m, mine, "tabs.w1", "c_aaaaaaaaaaaa", 0, sumOf(t, storedTabs), storedTabs)
	return &appendEnv{m: m, rec: rec, mine: mine, rev: rev, phone: &devices.Principal{ID: devID, PairingID: "pair", ProfileID: mine}}
}

// put sends a device write to section with the stored baseRev; hash "" means the correct canonical hash.
func (e *appendEnv) put(t *testing.T, section, clientID, payload, hash string, mutate func(*sectionReq)) *httptest.ResponseRecorder {
	t.Helper()
	if hash == "" {
		hash = sumOf(t, payload)
	}
	req := defaultSectionReq(clientID, e.rev, hash, payload)
	if mutate != nil {
		mutate(&req)
	}
	return serveAs(e.m, e.phone, http.MethodPut, sectionPath(e.mine, section), mustJSON(t, req))
}

func (e *appendEnv) stored(t *testing.T) (string, int64) {
	t.Helper()
	sec, found, err := e.m.store.GetSection(e.mine, "tabs.w1")
	require.NoError(t, err)
	require.True(t, found)
	return string(sec.Payload), sec.Rev
}

func (e *appendEnv) untouched(t *testing.T, events int) {
	t.Helper()
	p, rev := e.stored(t)
	assert.Equal(t, storedTabs, p)
	assert.Equal(t, e.rev, rev)
	assert.Equal(t, events, len(e.rec.events), "nothing announced")
}

func TestDeviceAppend_OneAndTwoTabsAreApplied(t *testing.T) {
	e := newAppendEnv(t)
	before := len(e.rec.events)
	rr := e.put(t, "tabs.w1", devClient, goodAppend, "", nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"applied":true`)
	p, rev := e.stored(t)
	assert.JSONEq(t, goodAppend, p)
	assert.Equal(t, e.rev+1, rev)
	assert.Equal(t, before+1, len(e.rec.events), "the usual profile event")

	two := `{"order":["t1","p1","p2"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}},"p2":{"id":"p2","pinned":false,"locked":false,"createdAt":2,"layout":{}}},"extra":{"a":1}}`
	e.rev = rev
	rr = e.put(t, "tabs.w1", devClient, two, "", nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	// two tabs at once from the first state
	e2 := newAppendEnv(t)
	both := `{"order":["t1","a","b"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"a":{"id":"a","pinned":false,"locked":false,"createdAt":3,"layout":{}},"b":{"id":"b","pinned":false,"locked":false,"createdAt":4,"layout":{}}},"extra":{"a":1}}`
	assert.Equal(t, http.StatusOK, e2.put(t, "tabs.w1", devClient, both, "", nil).Code)
}

// A number written differently but equal as JSON (1.0 vs 1, an exponent) is the same value and passes the equality gates.
func TestDeviceAppend_NumbersCompareAsJSONValues(t *testing.T) {
	e := newAppendEnv(t)
	p := `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1.789999999999e12},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1.0}}`
	assert.Equal(t, http.StatusOK, e.put(t, "tabs.w1", devClient, p, "", nil).Code)
}

func TestDeviceAppend_Refusals(t *testing.T) {
	forbidden := map[string]struct {
		payload string
		mutate  func(*sectionReq)
		client  string
	}{
		"foreign clientId": {payload: goodAppend, client: "c_ffffffffffff"},
		"changed fingerprint (a higher ordinal passes the schema gate)": {payload: goodAppend, mutate: func(r *sectionReq) { r.Fingerprint = otherFingerprint; r.Ordinal = 2 }},
		"changed ordinal":              {payload: goodAppend, mutate: func(r *sectionReq) { r.Ordinal = 2 }},
		"nothing appended":             {payload: storedTabs},
		"existing tab removed":         {payload: `{"order":["p1"],"tabs":{"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1}}`},
		"existing tab edited":          {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":false,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1}}`},
		"order reordered":              {payload: `{"order":["p1","t1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1}}`},
		"new id repeated":              {payload: `{"order":["t1","p1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1}}`},
		"new id reuses an old one":     {payload: `{"order":["t1","t1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999}},"extra":{"a":1}}`},
		"new tab is empty":             {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{}},"extra":{"a":1}}`},
		"new tab id differs from key":  {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"zz","pinned":false,"locked":false,"createdAt":1,"layout":{}}},"extra":{"a":1}}`},
		"new tab pinned not boolean":   {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":"no","locked":false,"createdAt":1,"layout":{}}},"extra":{"a":1}}`},
		"new tab createdAt a string":   {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":"1","layout":{}}},"extra":{"a":1}}`},
		"new tab layout not an object": {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1,"layout":[]}},"extra":{"a":1}}`},
		"new entry not an object":      {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":"x"},"extra":{"a":1}}`},
		"new entry missing":            {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999}},"extra":{"a":1}}`},
		"extra tab entry not in order": {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}},"zz":{}},"extra":{"a":1}}`},
		"top-level member changed":     {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":2}}`},
		"top-level member added":       {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}},"extra":{"a":1},"more":1}`},
		"top-level member dropped":     {payload: `{"order":["t1","p1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"p1":{"id":"p1","pinned":false,"locked":false,"createdAt":1790000000000,"layout":{"type":"leaf"}}}}`},
		"order holds a non-string":     {payload: `{"order":["t1",2],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999}},"extra":{"a":1}}`},
		"tabs is not an object":        {payload: `{"order":["t1","p1"],"tabs":[],"extra":{"a":1}}`},
	}
	for name, c := range forbidden {
		t.Run(name, func(t *testing.T) {
			e := newAppendEnv(t)
			before := len(e.rec.events)
			client := c.client
			if client == "" {
				client = devClient
			}
			rr := e.put(t, "tabs.w1", client, c.payload, "", c.mutate)
			assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
			assert.Contains(t, rr.Body.String(), "device_append_only")
			e.untouched(t, before)
		})
	}
}

func TestDeviceAppend_WhereGate(t *testing.T) {
	e := newAppendEnv(t)
	before := len(e.rec.events)
	other := createProfile(t, e.m, "other")
	for _, section := range []string{"settings", "workspaces", "hosts"} {
		rr := e.put(t, section, devClient, goodAppend, "", nil)
		assert.Equal(t, http.StatusForbidden, rr.Code, section)
		assert.Contains(t, rr.Body.String(), "device_append_only", section)
	}
	// A tabs section that does not exist is never created by a device.
	rr := e.put(t, "tabs.w9", devClient, goodAppend, "", func(r *sectionReq) { r.BaseRev = 0 })
	assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	_, found, _ := e.m.store.GetSection(e.mine, "tabs.w9")
	assert.False(t, found)
	// Another profile is a 404 (and the section there is untouched).
	putSection(t, e.m, other, "tabs.w1", "c_aaaaaaaaaaaa", 0, sumOf(t, storedTabs), storedTabs)
	rr = serveAs(e.m, e.phone, http.MethodPut, sectionPath(other, "tabs.w1"), mustJSON(t, defaultSectionReq(devClient, 1, sumOf(t, goodAppend), goodAppend)))
	assert.Equal(t, http.StatusNotFound, rr.Code)
	e.untouched(t, before+1) // the one event is the other profile's setup above
}

// A tombstoned section is not recreated by a device.
func TestDeviceAppend_ATombstoneIsNotRecreated(t *testing.T) {
	e := newAppendEnv(t)
	res, err := e.m.store.DeleteSection(e.mine, "tabs.w1", "c_aaaaaaaaaaaa", e.rev)
	require.NoError(t, err)
	e.rev = res.Rev
	rr := e.put(t, "tabs.w1", devClient, goodAppend, "", func(r *sectionReq) { r.BaseRev = 0 })
	assert.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
	_, found, _ := e.m.store.GetSection(e.mine, "tabs.w1")
	assert.False(t, found)
}

func TestDeviceAppend_HashGate(t *testing.T) {
	e := newAppendEnv(t)
	before := len(e.rec.events)
	for name, hash := range map[string]string{
		"the stored row's hash resent": sumOf(t, storedTabs),
		"an unrelated hash":            hashOf("nope"),
	} {
		rr := e.put(t, "tabs.w1", devClient, goodAppend, hash, nil)
		assert.Equal(t, http.StatusBadRequest, rr.Code, name)
		assert.Contains(t, rr.Body.String(), "hash_mismatch", name)
	}
	e.untouched(t, before)
	// A payload the port cannot reproduce (a lone surrogate in a new tab) is the same refusal.
	lone := strings.Replace(goodAppend, `"type":"leaf"`, `"type":"le\ud800af"`, 1)
	rr := e.put(t, "tabs.w1", devClient, lone, hashOf("x"), nil)
	assert.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	e.untouched(t, before)
}

// A Mac write landing between the phone's read and write: the phone gets the 409 with the SOT payload and nothing is appended.
func TestDeviceAppend_AStaleBaseRevIsAConflict(t *testing.T) {
	e := newAppendEnv(t)
	macPayload := `{"order":["t1","m1"],"tabs":{"t1":{"id":"t1","pinned":true,"createdAt":1789999999999},"m1":{"id":"m1","pinned":false,"locked":false,"createdAt":5,"layout":{}}},"extra":{"a":1}}`
	putSection(t, e.m, e.mine, "tabs.w1", "c_bbbbbbbbbbbb", e.rev, sumOf(t, macPayload), macPayload)
	before := len(e.rec.events)
	rr := e.put(t, "tabs.w1", devClient, goodAppend, "", nil) // still on the old baseRev
	assert.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"reason":"conflict"`)
	assert.Contains(t, rr.Body.String(), "m1")
	p, rev := e.stored(t)
	assert.JSONEq(t, macPayload, p)
	assert.Equal(t, e.rev+1, rev)
	assert.Equal(t, before, len(e.rec.events))
}

// A stale baseRev whose content equals what is stored is converged (nothing written), even for a device.
func TestDeviceAppend_ConvergedWhenTheStoredRowAlreadyHoldsIt(t *testing.T) {
	e := newAppendEnv(t)
	putSection(t, e.m, e.mine, "tabs.w1", "c_bbbbbbbbbbbb", e.rev, sumOf(t, goodAppend), goodAppend)
	before := len(e.rec.events)
	rr := e.put(t, "tabs.w1", devClient, goodAppend, "", nil)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Contains(t, rr.Body.String(), `"applied":false`)
	assert.Equal(t, before, len(e.rec.events))
}

// The admin's writes carry no guard: the same body that a device cannot send is applied for the admin.
func TestDeviceAppend_AdminWritesAreNotGuarded(t *testing.T) {
	e := newAppendEnv(t)
	removing := `{"order":["z"],"tabs":{"z":{}},"extra":2}`
	rr := serveAs(e.m, nil, http.MethodPut, sectionPath(e.mine, "tabs.w1"), mustJSON(t, defaultSectionReq("c_aaaaaaaaaaaa", e.rev, hashOf("any"), removing)))
	assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

// The schema gate runs first, as for every writer: a different fingerprint without a higher ordinal is the ordinary 409
// `schema` (nothing written); a payload that is not an object never reaches the guard (400).
func TestDeviceAppend_TheOrdinaryGatesStillRunFirst(t *testing.T) {
	e := newAppendEnv(t)
	before := len(e.rec.events)
	rr := e.put(t, "tabs.w1", devClient, goodAppend, "", func(r *sectionReq) { r.Fingerprint = otherFingerprint })
	assert.Equal(t, http.StatusConflict, rr.Code)
	assert.Contains(t, rr.Body.String(), `"reason":"schema"`)
	assert.Equal(t, http.StatusBadRequest, e.put(t, "tabs.w1", devClient, `[1,2]`, hashOf("x"), nil).Code)
	e.untouched(t, before)
}
