package hostconfig

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/workbooksettings"
)

var _ workbooksettings.Reader = (*Module)(nil)

// WB-1b.1: the session workbook's one setting, push_wait_s (spec §7, plan D7: there is no enabled switch).
func TestWorkbook_DefaultsToEightAndRoundTrips(t *testing.T) {
	m := newTestModule(t)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	require.Equal(t, http.StatusOK, rr.Code)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"push_wait_s":8},"revision":0}`, string(got["workbook"]))
	s, err := m.WorkbookSettings()
	require.NoError(t, err)
	assert.Equal(t, 8, s.PushWaitS, "a never-written key answers the default")

	rr = serve(m, http.MethodPut, "/api/hostconfig/workbook", `{"items":{"push_wait_s":0},"baseRevision":0}`)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"items":{"push_wait_s":0},"revision":1}`, rr.Body.String())
	s, err = m.WorkbookSettings()
	require.NoError(t, err)
	assert.Equal(t, 0, s.PushWaitS, "0 = never wait, and is not read as unset")
}

func TestWorkbook_IsStrictAndBounded(t *testing.T) {
	m := newTestModule(t)
	for _, items := range []string{`{}`, `{"push_wait_s":null}`, `{"push_wait_s":"8"}`, `{"push_wait_s":8.5}`, `{"push_wait_s":-1}`,
		`{"push_wait_s":31}`, `{"push_wait_s":8,"enabled":true}`, `{"Push_wait_s":8}`, `[]`, `null`, `true`} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/workbook", `{"items":`+items+`,"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, items)
	}
	e, err := m.store.Get(KeyWorkbook)
	require.NoError(t, err)
	assert.Equal(t, int64(0), e.Revision, "nothing stored")
	for _, ok := range []string{`{"push_wait_s":0}`, `{"push_wait_s":30}`} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/workbook", `{"items":`+ok+`,"baseRevision":`+revisionOf(t, m)+`}`)
		assert.Equal(t, http.StatusOK, rr.Code, ok)
	}
}

func revisionOf(t *testing.T, m *Module) string {
	t.Helper()
	e, err := m.store.Get(KeyWorkbook)
	require.NoError(t, err)
	b, _ := json.Marshal(e.Revision)
	return string(b)
}

// A stored value that does not read is an error for the reader (the caller picks its own fallback), never a silent 8,
// and the GET shows it as invalid with the default items.
// Strictness is the same for a stored value as for a PUT body: a duplicate member is invalid, not "the last one wins".
// Mutation gate: drop rejectDuplicateKeys from normalizeWorkbook → red.
func TestWorkbook_AStoredDuplicateKeyIsInvalid(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyWorkbook, 0, func() (json.RawMessage, error) {
		return json.RawMessage(`{"push_wait_s":0,"push_wait_s":30}`), nil
	})
	require.NoError(t, err)
	_, err = m.WorkbookSettings()
	assert.Error(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"push_wait_s":8},"revision":1,"invalid":true}`, string(got["workbook"]))
	// and a duplicate PUT body is a 400
	rr = serve(m, http.MethodPut, "/api/hostconfig/workbook", `{"items":{"push_wait_s":1,"push_wait_s":2},"baseRevision":1}`)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// 8.0 and 1e1 are numbers that are not plain integers in the wire format: refused with the fractions.
func TestWorkbook_RefusesNonIntegerSpellings(t *testing.T) {
	m := newTestModule(t)
	for _, v := range []string{`8.0`, `1e1`, `99999999999999999999`, `0x8`, `"8"`} {
		rr := serve(m, http.MethodPut, "/api/hostconfig/workbook", `{"items":{"push_wait_s":`+v+`},"baseRevision":0}`)
		assert.Equal(t, http.StatusBadRequest, rr.Code, v)
	}
}

func TestWorkbook_AStoredGarbageValueIsAnError(t *testing.T) {
	m := newTestModule(t)
	_, _, err := m.store.Put(KeyWorkbook, 0, func() (json.RawMessage, error) { return json.RawMessage(`{"push_wait_s":"x"}`), nil })
	require.NoError(t, err)
	_, err = m.WorkbookSettings()
	assert.Error(t, err)
	rr := serve(m, http.MethodGet, "/api/hostconfig", "")
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	assert.JSONEq(t, `{"items":{"push_wait_s":8},"revision":1,"invalid":true}`, string(got["workbook"]))
}
