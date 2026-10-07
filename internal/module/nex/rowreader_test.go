package nex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1a (spec 2026-10-08 §3.3): the in-process single-execution read
// the projector (PR1b) builds its deltas from.

// recordedRequest is what a fake engine handler saw.
type recordedRequest struct {
	method, path, rawPath, rawQuery, host string
	header                                http.Header
	body                                  []byte
	ctx                                   context.Context
	principal                             string
}

// recordingHandler answers every request with respond and records it.
type recordingHandler struct {
	mu      sync.Mutex
	seen    []recordedRequest
	respond func(w http.ResponseWriter, r *http.Request)
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := recordedRequest{
		method: r.Method, path: r.URL.Path, rawPath: r.URL.RawPath, rawQuery: r.URL.RawQuery, host: r.Host,
		header: r.Header.Clone(), ctx: r.Context(),
	}
	if r.Body != nil {
		rec.body, _ = io.ReadAll(r.Body)
	}
	// What the engine's own auth would name this request (build_config.go).
	rec.principal, _ = principalAuth("host1").Authenticate(r)
	h.mu.Lock()
	h.seen = append(h.seen, rec)
	h.mu.Unlock()
	h.respond(w, r)
}

func (h *recordingHandler) requests() []recordedRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]recordedRequest(nil), h.seen...)
}

// answer is a respond func writing status and body as JSON.
func answer(status int, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// singleRow is a GET /v1/executions/{id} body as Nexen writes it: compact,
// one trailing newline, carrying the two keys only the single GET has.
const singleRow = `{"id":"exc_1","state":"idle","brief":"<b>fix & ship</b>","labels":{"team":"a"},` +
	`"cost_usd":null,"duration_ms":null,"event_count":12,"observers":0,"archived":false,` +
	`"turn_count":3,"live_turn_id":"turn_9","lease":{"principal_id":"pdx:host1","expires_at":1760000000000},` +
	`"activity":{"phase":"idle"},"pending_permission":{"request_id":"perm_1","tool_name":"Bash","since":5}}` + "\n"

func TestRowReader_RequestIsFixed(t *testing.T) {
	h := &recordingHandler{respond: answer(http.StatusOK, singleRow)}
	rr := rowReader{handler: h, logf: discardLogf}

	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "projector")
	_, found, err := rr.read(ctx, "exc_01HX-y_9")
	require.NoError(t, err)
	require.True(t, found)

	reqs := h.requests()
	require.Len(t, reqs, 1)
	got := reqs[0]
	assert.Equal(t, http.MethodGet, got.method)
	assert.Equal(t, "/v1/executions/exc_01HX-y_9", got.path, "the engine handler is mounted with RoutePrefix stripped")
	assert.Empty(t, got.rawPath)
	assert.Empty(t, got.rawQuery)
	assert.Empty(t, got.host)
	assert.Empty(t, got.header, "no header may reach the engine (principal = bare host)")
	assert.Empty(t, got.body)
	assert.Equal(t, "projector", got.ctx.Value(key{}), "the read must run under the caller's context")
	assert.Equal(t, "pdx:host1", got.principal)
}

func TestRowReader_InvalidIDsNeverReachTheHandler(t *testing.T) {
	h := &recordingHandler{respond: answer(http.StatusOK, singleRow)}
	rr := rowReader{handler: h, logf: discardLogf}

	for _, id := range []string{
		"", strings.Repeat("a", 65), "../x", "a/b", "a b", "a%2Fb", "a?b", "a#b", "a.b", "é", "a\nb",
	} {
		row, found, err := rr.read(context.Background(), id)
		assert.Error(t, err, "id %q", id)
		assert.False(t, found, "id %q", id)
		assert.Nil(t, row, "id %q", id)
	}
	assert.Empty(t, h.requests(), "an invalid id reached the engine")

	// The pattern's edge: 64 characters is still an id.
	_, found, err := rr.read(context.Background(), strings.Repeat("Z", 64))
	require.NoError(t, err)
	assert.True(t, found)
	require.Len(t, h.requests(), 1)
	assert.Equal(t, "/v1/executions/"+strings.Repeat("Z", 64), h.requests()[0].path)
}

// decodeTop decodes a JSON object's top level, failing the test otherwise.
func decodeTop(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &m), "body %s", raw)
	require.NotNil(t, m, "body %s", raw)
	return m
}

func rawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A 200 becomes a list-shaped row: lease and live_turn_id go, every other
// value is carried byte for byte (no HTML re-escaping either).
func TestRowReader_OKBecomesAListShapedRow(t *testing.T) {
	rr := rowReader{handler: &recordingHandler{respond: answer(http.StatusOK, singleRow)}, logf: discardLogf}

	row, found, err := rr.read(context.Background(), "exc_1")
	require.NoError(t, err)
	require.True(t, found)

	in := decodeTop(t, []byte(singleRow))
	out := decodeTop(t, row)
	assert.NotContains(t, out, "lease")
	assert.NotContains(t, out, "live_turn_id")
	delete(in, "lease")
	delete(in, "live_turn_id")
	require.Equal(t, rawKeys(in), rawKeys(out))
	for k, v := range in {
		assert.Equal(t, string(v), string(out[k]), "field %s changed", k)
	}
	assert.False(t, bytes.HasSuffix(row, []byte("\n")), "the row is a bare JSON value")
}

// The single GET omits turn_count when it is 0 (omitempty, kept for wire
// compatibility, N/api/query.go:119-123) while a list row always carries
// it. A list-shaped row therefore gets the 0 spelled out.
func TestRowReader_ZeroTurnCountIsSpelledOutLikeTheList(t *testing.T) {
	rr := rowReader{handler: &recordingHandler{respond: answer(http.StatusOK,
		`{"id":"exc_new","state":"queued","lease":null}`)}, logf: discardLogf}

	row, found, err := rr.read(context.Background(), "exc_new")
	require.NoError(t, err)
	require.True(t, found)
	out := decodeTop(t, row)
	assert.Equal(t, "0", string(out["turn_count"]))
	assert.Equal(t, []string{"id", "state", "turn_count"}, rawKeys(out))
}

func TestRowReader_NotFoundIsAbsentNotAnError(t *testing.T) {
	rr := rowReader{handler: &recordingHandler{respond: answer(http.StatusNotFound,
		`{"code":"execution_not_found","error":"execution not found"}`+"\n")}, logf: discardLogf}

	row, found, err := rr.read(context.Background(), "exc_gone")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, row)
}

// Only Nexen's own execution_not_found means "this row is gone". A 404
// for any other reason (no such route, say) must never become a removal.
func TestRowReader_OtherNotFoundIsAnError(t *testing.T) {
	for _, body := range []string{"404 page not found\n", `{"error":"nope","code":"route_missing"}`} {
		rr := rowReader{handler: &recordingHandler{respond: answer(http.StatusNotFound, body)}, logf: discardLogf}
		_, found, err := rr.read(context.Background(), "exc_1")
		assert.Error(t, err, "body %q", body)
		assert.False(t, found)
	}
}

func TestRowReader_OtherStatusIsAnError(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusNoContent} {
		rr := rowReader{handler: &recordingHandler{respond: answer(status, `{"error":"database is locked"}`)}, logf: discardLogf}
		row, found, err := rr.read(context.Background(), "exc_1")
		require.Error(t, err, "status %d", status)
		assert.Contains(t, err.Error(), strconv.Itoa(status), "status %d", status)
		assert.False(t, found)
		assert.Nil(t, row)
	}
}

func TestRowReader_NonObjectOKIsAnError(t *testing.T) {
	for _, body := range []string{"null", "[]", `"exc_1"`, "42", "not json", `{"id":"exc_1"} trailing`, ""} {
		rr := rowReader{handler: &recordingHandler{respond: answer(http.StatusOK, body)}, logf: discardLogf}
		row, found, err := rr.read(context.Background(), "exc_1")
		assert.Error(t, err, "body %q", body)
		assert.False(t, found, "body %q", body)
		assert.Nil(t, row, "body %q", body)
	}
}

// objectOfSize is a JSON object exactly n bytes long.
func objectOfSize(n int) string {
	const head, tail = `{"pad":"`, `"}`
	return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
}

func TestRowReader_BodyCappedAtOneMiB(t *testing.T) {
	rr := rowReader{handler: &recordingHandler{respond: answer(http.StatusOK, objectOfSize(rowBodyLimit))}, logf: discardLogf}
	_, found, err := rr.read(context.Background(), "exc_1")
	require.NoError(t, err, "a body of exactly the cap is fine")
	assert.True(t, found)

	rr = rowReader{handler: &recordingHandler{respond: answer(http.StatusOK, objectOfSize(rowBodyLimit+1))}, logf: discardLogf}
	row, found, err := rr.read(context.Background(), "exc_1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
	assert.False(t, found)
	assert.Nil(t, row)
}

// A panicking engine handler is an error for the caller and a log line,
// never a crash — also when it panicked after writing a complete 200, and
// for http.ErrAbortHandler, which recoverer re-panics for net/http (there
// is no net/http above an in-process read to catch it).
func TestRowReader_HandlerPanicIsAnError(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"before writing": func(http.ResponseWriter, *http.Request) { panic("store exploded") },
		"after a full 200": func(w http.ResponseWriter, r *http.Request) {
			answer(http.StatusOK, singleRow)(w, r)
			panic("late explosion")
		},
		"abort handler": func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) },
	}
	for name, respond := range cases {
		t.Run(name, func(t *testing.T) {
			logs := &logRecorder{}
			rr := rowReader{handler: &recordingHandler{respond: respond}, logf: logs.logf}
			row, found, err := rr.read(context.Background(), "exc_1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "panic")
			assert.False(t, found)
			assert.Nil(t, row)
			if name != "abort handler" {
				lines := logs.all()
				require.Len(t, lines, 1)
				assert.Contains(t, lines[0], "nex: panic recovered: method=GET path=/v1/executions/exc_1")
			}
		})
	}
}

// Through the REAL engine: an unknown id is Nexen's execution_not_found,
// and a live row (holding a lease, so the single GET carries "lease") is
// returned in exactly the list's shape.
func TestRowReader_RealEngine(t *testing.T) {
	f := newMountFixture(t)
	rr := rowReader{handler: f.m.sys.handler, logf: discardLogf}

	row, found, err := rr.read(context.Background(), "exc_nope")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Nil(t, row)

	id := f.delegate(t)
	f.waitTurnDone(t, id, 1)
	var lease struct {
		LeaseID string `json:"lease_id"`
	}
	f.doJSON(t, http.MethodPost, "/api/nex/v1/executions/"+id+"/attach", map[string]string{"mode": "control"}, &lease)
	require.NotEmpty(t, lease.LeaseID)
	require.Contains(t, f.summary(t, id), "lease", "the single GET should carry the lease this test relies on")

	row, found, err = rr.read(context.Background(), id)
	require.NoError(t, err)
	require.True(t, found)
	got := decodeTop(t, row)

	var page struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	f.doJSON(t, http.MethodGet, "/api/nex/v1/executions", nil, &page)
	var listRow map[string]json.RawMessage
	for _, it := range page.Items {
		if string(it["id"]) == `"`+id+`"` {
			listRow = it
		}
	}
	require.NotNil(t, listRow, "execution %s missing from the list", id)
	assert.Equal(t, rawKeys(listRow), rawKeys(got), "the row read is not list-shaped")
	assert.Equal(t, string(listRow["state"]), string(got["state"]))
}
