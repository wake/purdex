package profiles

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/devices"
)

// QP-1b-ii task 6: a paired phone reaches only the profile its token names; everything else is the 404 of a profile that
// does not exist. (The append-only write of spec §5.2 is QP-1c; until then a phone writes nothing.)

func serveAs(m *Module, p *devices.Principal, method, path string, body []byte) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if p != nil {
		req = req.WithContext(devices.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestOwnership_APhoneReadsOnlyItsOwnProfile(t *testing.T) {
	m, _ := newTestModule(t)
	mine, other := createProfile(t, m, "mine"), createProfile(t, m, "other")
	putSection(t, m, mine, "tabs.w1", "c_0123456789ab", 0, hashOf("a"), `{"order":[],"tabs":{}}`)
	putSection(t, m, other, "tabs.w1", "c_0123456789ab", 0, hashOf("b"), `{"order":[],"tabs":{}}`)
	phone := &devices.Principal{ID: "d_aaaaaaaaaaaa", ProfileID: mine}
	noProfile := &devices.Principal{ID: "d_bbbbbbbbbbbb"}

	for _, path := range []string{"/api/profiles/" + mine, sectionPath(mine, "tabs.w1")} {
		assert.Equal(t, http.StatusOK, serveAs(m, phone, "GET", path, nil).Code, path)
		assert.Equal(t, http.StatusOK, serveAs(m, nil, "GET", path, nil).Code, "admin "+path)
		assert.Equal(t, http.StatusNotFound, serveAs(m, noProfile, "GET", path, nil).Code, "a token with no profile: "+path)
	}
	for _, path := range []string{"/api/profiles/" + other, sectionPath(other, "tabs.w1"), "/api/profiles/p_ffffffffffff", sectionPath("p_ffffffffffff", "tabs.w1")} {
		rr := serveAs(m, phone, "GET", path, nil)
		assert.Equal(t, http.StatusNotFound, rr.Code, path)
		assert.NotContains(t, rr.Body.String(), "order", "no part of another profile leaks")
		assert.Equal(t, http.StatusOK, serveAs(m, nil, "GET", "/api/profiles/"+other, nil).Code)
	}
}

func TestOwnership_APhoneWritesNothingYetAndNotAnotherProfileAtAll(t *testing.T) {
	m, rec := newTestModule(t)
	mine, other := createProfile(t, m, "mine"), createProfile(t, m, "other")
	phone := &devices.Principal{ID: "d_aaaaaaaaaaaa", ProfileID: mine}
	before := len(rec.events)

	body := sectionBody(t, "c_aaaaaaaaaaaa", 0, hashOf("a"), `{"order":[],"tabs":{}}`)
	assert.Equal(t, http.StatusNotFound, serveAs(m, phone, "PUT", sectionPath(other, "tabs.w1"), body).Code, "another profile")
	rr := serveAs(m, phone, "PUT", sectionPath(mine, "tabs.w1"), body)
	assert.Equal(t, http.StatusForbidden, rr.Code, "its own, until QP-1c")
	assert.Contains(t, rr.Body.String(), "device_append_only")
	assert.Equal(t, before, len(rec.events), "nothing was written, nothing announced")
	require.Equal(t, http.StatusNotFound, serveAs(m, nil, "GET", sectionPath(mine, "tabs.w1"), nil).Code, "the section was not created")
	// The admin still writes.
	assert.Equal(t, http.StatusOK, serveAs(m, nil, "PUT", sectionPath(mine, "tabs.w1"), body).Code)
}
