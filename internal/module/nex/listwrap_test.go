package nex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1866 PR1a (spec 2026-10-08 §3.4, §3.8, §8 R3-1/R3-3): GET
// /api/nex/v1/executions runs Nexen's list inside the read slot and stamps
// a good page with "pdx": {epoch, ver, bseq}.

// listPage is a list body as Nexen writes it (compact, trailing newline).
const listPage = `{"items":[{"id":"exc_a","state":"idle","brief":"<i>fix</i> & ship","turn_count":2},` +
	`{"id":"exc_b","state":"running","turn_count":0}],"next_cursor":"exc_b"}` + "\n"

// newListEnv mounts a Module whose engine handler is inner, the way
// RegisterRoutes mounts an assembled engine.
func newListEnv(t *testing.T, inner http.Handler) (*Module, *http.ServeMux) {
	t.Helper()
	m := New()
	m.logf = discardLogf
	m.sys = engine{handler: inner}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	return m, mux
}

// serve runs one request through mux under ctx.
func serve(mux http.Handler, ctx context.Context, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(method, target, nil).WithContext(ctx))
	return w
}

// stampOf returns a 200 list response's pdx, checking it has exactly the
// three keys, all spelled out.
func stampOf(t *testing.T, w *httptest.ResponseRecorder) slotStamp {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "body %s", w.Body.String())
	raw, ok := decodeTop(t, w.Body.Bytes())["pdx"]
	require.True(t, ok, "no pdx in %s", w.Body.String())
	assert.Equal(t, []string{"bseq", "epoch", "ver"}, rawKeys(decodeTop(t, raw)))
	var st slotStamp
	require.NoError(t, json.Unmarshal(raw, &st))
	return st
}

func TestListWrapper_StampsAGoodPageAndKeepsItsValues(t *testing.T) {
	inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Nex-Probe", "inner")
		answer(http.StatusOK, listPage)(w, r)
	}}
	m, mux := newListEnv(t, inner)

	w := serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")
	st := stampOf(t, w)
	assert.Equal(t, slotStamp{Epoch: m.reads().epoch, Ver: 1, Bseq: 0}, st)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{16}$`), st.Epoch)
	assert.Contains(t, w.Body.String(), `"bseq":0`, "a zero bseq must be spelled out")
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, "inner", w.Header().Get("X-Nex-Probe"), "the engine's headers were not copied")

	in, out := decodeTop(t, []byte(listPage)), decodeTop(t, w.Body.Bytes())
	assert.Equal(t, []string{"items", "next_cursor", "pdx"}, rawKeys(out))
	assert.Equal(t, string(in["items"]), string(out["items"]), "items changed")
	assert.Equal(t, string(in["next_cursor"]), string(out["next_cursor"]), "next_cursor changed")

	reqs := inner.requests()
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodGet, reqs[0].method)
	assert.Equal(t, "/v1/executions", reqs[0].path, "the engine must see the same stripped path the mount gives it")
}

// A Module built as a struct literal (as several test envs do) still gets
// a working slot on first use.
func TestListWrapper_StructLiteralModuleBuildsItsSlot(t *testing.T) {
	m := &Module{sys: engine{handler: &recordingHandler{respond: answer(http.StatusOK, listPage)}}, logf: discardLogf}
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver)
}

func TestListWrapper_QueryStringReachesTheEngineIntact(t *testing.T) {
	inner := &recordingHandler{respond: answer(http.StatusOK, listPage)}
	_, mux := newListEnv(t, inner)
	const q = "limit=100&cursor=exc_0042&include_archived=true&state=idle&label.team=a%20b&label.k%2Fx=v%26w" +
		"&session_id=0f8fad5b-d9cb-469f-a165-70867728950e"

	stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions?"+q))
	require.Len(t, inner.requests(), 1)
	assert.Equal(t, q, inner.requests()[0].rawQuery)
}

// Rule V across callers: a page and a projector row read are ordered by
// completion, whichever of them held the slot first.
func TestListWrapper_VerOrdersPagesAndRowReads(t *testing.T) {
	entered, unblock := make(chan struct{}, 1), make(chan struct{})
	block := false
	inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) {
		if block {
			entered <- struct{}{}
			<-unblock
		}
		answer(http.StatusOK, listPage)(w, r)
	}}
	m, mux := newListEnv(t, inner)
	ctx := context.Background()
	rowRead := func() uint64 {
		st, err := m.reads().read(ctx, "row", 0, okRead)
		require.NoError(t, err)
		return st.Ver
	}

	assert.Equal(t, uint64(1), stampOf(t, serve(mux, ctx, http.MethodGet, "/api/nex/v1/executions")).Ver)
	assert.Equal(t, uint64(2), rowRead())
	_, err := m.reads().read(ctx, "row", 0, func(context.Context) error { return errors.New("500") })
	require.Error(t, err)
	assert.Equal(t, uint64(3), stampOf(t, serve(mux, ctx, http.MethodGet, "/api/nex/v1/executions?cursor=exc_b")).Ver,
		"a failed row read consumed a ver")

	// A page inside the slot, a row read queued behind it.
	block = true
	pageDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { pageDone <- serve(mux, ctx, http.MethodGet, "/api/nex/v1/executions") }()
	<-entered
	rowDone := make(chan uint64, 1)
	go func() { rowDone <- rowRead() }()
	close(unblock)
	pageVer := stampOf(t, <-pageDone).Ver
	assert.Less(t, pageVer, <-rowDone, "the row read queued behind the page was stamped before it")
	block = false

	// A row read inside the slot, a page queued behind it.
	rowEntered, rowUnblock := make(chan struct{}), make(chan struct{})
	rowVer := make(chan uint64, 1)
	go func() {
		st, err := m.reads().read(ctx, "row", 0, func(context.Context) error {
			close(rowEntered)
			<-rowUnblock
			return nil
		})
		assert.NoError(t, err)
		rowVer <- st.Ver
	}()
	<-rowEntered
	go func() { pageDone <- serve(mux, ctx, http.MethodGet, "/api/nex/v1/executions") }()
	close(rowUnblock)
	first := <-rowVer
	assert.Greater(t, stampOf(t, <-pageDone).Ver, first, "the page queued behind the row read was stamped before it")
}

// Any answer but a 200 JSON object goes out exactly as the engine wrote it,
// unstamped, and consumes no ver.
func TestListWrapper_NonOKPassesThroughUnstamped(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{http.StatusBadRequest, `{"code":"bad_state","error":"unknown state: x"}` + "\n"},
		{http.StatusInternalServerError, `{"error":"database is locked"}` + "\n"},
		{http.StatusOK, "[]\n"},
		{http.StatusOK, "null\n"},
		{http.StatusOK, "not json"},
	}
	var respond func(http.ResponseWriter, *http.Request)
	inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) { respond(w, r) }}
	_, mux := newListEnv(t, inner)
	for _, tc := range cases {
		respond = func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Nex-Probe", "inner")
			answer(tc.status, tc.body)(w, r)
		}
		w := serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions?state=x")
		assert.Equal(t, tc.status, w.Code, tc.body)
		assert.Equal(t, tc.body, w.Body.String(), "body rewritten")
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"), tc.body)
		assert.Equal(t, "inner", w.Header().Get("X-Nex-Probe"), tc.body)
	}
	respond = answer(http.StatusOK, listPage)
	assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver,
		"a passed-through answer consumed a ver")
}

// nexErrorOf decodes a Nexen-shaped error body.
func nexErrorOf(t *testing.T, w *httptest.ResponseRecorder) (msg, code string) {
	t.Helper()
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e), "body %s", w.Body.String())
	return e.Error, e.Code
}

// An engine that panicked never yields a stamped page — also when it had
// already written a 200 and a complete JSON object, which recoverer's
// WriteHeader(500) can no longer change and which would otherwise read as a
// good page. The client gets 500 nex_list_panicked instead of whatever was
// buffered, the panic is logged, and no ver is consumed.
func TestListWrapper_EnginePanicIs500AndNeverStamped(t *testing.T) {
	cases := map[string]func(http.ResponseWriter, *http.Request){
		"before writing": func(http.ResponseWriter, *http.Request) { panic("store exploded") },
		"after a partial body": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"items":[{"id":"exc_a","state":"idle"`)
			panic("mid-page explosion")
		},
		"after a complete 200": func(w http.ResponseWriter, r *http.Request) {
			answer(http.StatusOK, listPage)(w, r)
			panic("late explosion")
		},
	}
	for name, explode := range cases {
		t.Run(name, func(t *testing.T) {
			panicking := false
			inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) {
				if panicking {
					explode(w, r)
					return
				}
				answer(http.StatusOK, listPage)(w, r)
			}}
			m, mux := newListEnv(t, inner)
			logs := &logRecorder{}
			m.logf = logs.logf
			assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver)

			panicking = true
			w := serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")
			assert.Equal(t, http.StatusInternalServerError, w.Code)
			msg, code := nexErrorOf(t, w)
			assert.Equal(t, "nex_list_panicked", code)
			assert.Equal(t, "nex list failed", msg)
			assert.NotContains(t, w.Body.String(), "exc_a", "the buffered body reached the client")
			assert.NotContains(t, w.Body.String(), "pdx")
			lines := logs.all()
			require.Len(t, lines, 1, "%q", lines)
			assert.Contains(t, lines[0], "nex: panic recovered: method=GET path=/v1/executions ",
				"recoverer must see the stripped path, as the mount gives it")
			require.True(t, readSlotFree(m.reads()), "the panicked page left the slot held")

			panicking = false
			assert.Equal(t, uint64(2), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver,
				"the panicked page consumed a ver")
		})
	}
}

// http.ErrAbortHandler keeps its net/http contract: recoverer re-panics it,
// and the wrapper lets it propagate (net/http aborts the response) — nothing
// is written, the slot is free afterwards and no ver is consumed.
func TestListWrapper_AbortHandlerPropagates(t *testing.T) {
	aborting := false
	inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) {
		if aborting {
			answer(http.StatusOK, listPage)(w, r)
			panic(http.ErrAbortHandler)
		}
		answer(http.StatusOK, listPage)(w, r)
	}}
	m, mux := newListEnv(t, inner)
	assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver)

	aborting = true
	w := httptest.NewRecorder()
	var rec any
	func() {
		defer func() { rec = recover() }()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/nex/v1/executions", nil))
	}()
	assert.Equal(t, http.ErrAbortHandler, rec, "the abort did not propagate")
	assert.Empty(t, w.Body.String(), "an aborted page reached the client")
	require.True(t, readSlotFree(m.reads()), "the aborted page left the slot held")

	aborting = false
	assert.Equal(t, uint64(2), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver,
		"the aborted page consumed a ver")
}

func TestListWrapper_SlotBusyIs503NexBusy(t *testing.T) {
	inner := &recordingHandler{respond: answer(http.StatusOK, listPage)}
	m, mux := newListEnv(t, inner)
	m.listWait = 30 * time.Millisecond
	require.NoError(t, m.reads().acquire(context.Background(), 0))

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions") }()
	var w *httptest.ResponseRecorder
	select {
	case w = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a page behind a held slot never gave up")
	}
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	msg, code := nexErrorOf(t, w)
	assert.Equal(t, "nex_busy", code)
	assert.Equal(t, "nex list busy", msg)
	assert.Empty(t, inner.requests(), "a page that never got the slot reached the engine")

	m.reads().release()
	assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver)
}

// The default wait is the spec's 2 s.
func TestListWrapper_DefaultWaitIsTwoSeconds(t *testing.T) {
	assert.Equal(t, 2*time.Second, (&Module{}).listSlotWait())
	assert.Equal(t, 5*time.Millisecond, (&Module{listWait: 5 * time.Millisecond}).listSlotWait())
}

func TestListWrapper_OversizePageIs502(t *testing.T) {
	chunk := strings.Repeat("x", 1<<20)
	inner := &recordingHandler{respond: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":["`)
		for i := 0; i <= listBodyLimit>>20; i++ {
			if _, err := io.WriteString(w, chunk); err != nil {
				return
			}
		}
	}}
	_, mux := newListEnv(t, inner)

	w := serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")
	assert.Equal(t, http.StatusBadGateway, w.Code)
	_, code := nexErrorOf(t, w)
	assert.Equal(t, "nex_list_too_large", code)
	assert.Less(t, w.Body.Len(), 1024, "the oversize page leaked into the response")
}

func TestListWrapper_CancelWhileWaitingWritesNothing(t *testing.T) {
	inner := &recordingHandler{respond: answer(http.StatusOK, listPage)}
	m, mux := newListEnv(t, inner)
	require.NoError(t, m.reads().acquire(context.Background(), 0))
	defer m.reads().release()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- serve(mux, ctx, http.MethodGet, "/api/nex/v1/executions") }()
	cancel()
	select {
	case w := <-done:
		assert.Empty(t, w.Body.String(), "a response was written for a client that is gone")
		assert.Empty(t, w.Header().Get("Content-Type"))
	case <-time.After(5 * time.Second):
		t.Fatal("a waiting page did not give up when its request ended")
	}
	assert.Empty(t, inner.requests())
}

// A client that goes away mid-page frees the slot (the next acquire takes
// it at once) and consumes no ver.
func TestListWrapper_CancelMidPageReleasesTheSlot(t *testing.T) {
	entered := make(chan struct{}, 1)
	blocking := true
	inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) {
		if blocking {
			entered <- struct{}{}
			<-r.Context().Done()
			answer(http.StatusInternalServerError, `{"error":"context canceled"}`)(w, r) // what Nexen's store error looks like
			return
		}
		answer(http.StatusOK, listPage)(w, r)
	}}
	m, mux := newListEnv(t, inner)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		serve(mux, ctx, http.MethodGet, "/api/nex/v1/executions")
		close(done)
	}()
	<-entered
	cancel()
	<-done
	require.True(t, readSlotFree(m.reads()), "the cancelled page left the slot held")

	blocking = false
	assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver,
		"the cancelled page consumed a ver")
}

// Only GET /v1/executions itself is wrapped: the single-row GET, the
// delegate POST, the trailing-slash path and every other route reach the
// engine as before, unstamped, and never touch the slot's counter.
func TestListWrapper_OtherRoutesReachTheEngineUnchanged(t *testing.T) {
	inner := &recordingHandler{respond: func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/executions" {
			answer(http.StatusOK, listPage)(w, r)
			return
		}
		answer(http.StatusOK, `{"id":"exc_a","path":"`+r.URL.Path+`"}`+"\n")(w, r)
	}}
	_, mux := newListEnv(t, inner)

	for _, tc := range []struct{ method, target, innerPath string }{
		{http.MethodGet, "/api/nex/v1/executions/exc_a", "/v1/executions/exc_a"},
		{http.MethodPost, "/api/nex/v1/executions", "/v1/executions"},
		{http.MethodGet, "/api/nex/v1/executions/", "/v1/executions/"},
		{http.MethodGet, "/api/nex/v1/executions/exc_a/events", "/v1/executions/exc_a/events"},
		{http.MethodGet, "/api/nex/v1/capabilities", "/v1/capabilities"},
	} {
		w := serve(mux, context.Background(), tc.method, tc.target)
		assert.Equal(t, http.StatusOK, w.Code, tc.target)
		assert.Equal(t, `{"id":"exc_a","path":"`+tc.innerPath+`"}`+"\n", w.Body.String(), "%s %s", tc.method, tc.target)
	}
	reqs := inner.requests()
	require.Len(t, reqs, 5)
	assert.Equal(t, http.MethodPost, reqs[1].method)
	assert.Equal(t, uint64(1), stampOf(t, serve(mux, context.Background(), http.MethodGet, "/api/nex/v1/executions")).Ver)
}

// The wrapper exists only with an engine: a soft-failed Init keeps the 503
// nex_unavailable fallback for the list path too.
func TestListWrapper_SoftFailedEngineStaysNexUnavailable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", launchdPath)
	cfg := baseConfig(t)
	m := New()
	m.logf = discardLogf
	m.assemble = newFakeAssemble(&fakeAssembleRecord{}, engine{}, errors.New("boom: store locked"))
	require.NoError(t, m.Init(newTestCore(&cfg)))
	require.Error(t, m.initErr)

	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, target := range []string{"/api/nex/v1/executions", "/api/nex/v1/executions?limit=100"} {
		w := serve(mux, context.Background(), http.MethodGet, target)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code, target)
		_, code := nexErrorOf(t, w)
		assert.Equal(t, "nex_unavailable", code, target)
	}
}

// Through the REAL engine: a list page is 200 with a valid pdx and an items
// array, consecutive pages get consecutive vers, and the single-row route
// still answers Nexen's own 404.
func TestListWrapper_RealEngine(t *testing.T) {
	f := newMountFixture(t)

	status, body := f.do(t, http.MethodGet, "/api/nex/v1/executions", nil)
	require.Equal(t, http.StatusOK, status, "body %s", body)
	var page struct {
		Items []json.RawMessage `json:"items"`
		Pdx   *slotStamp        `json:"pdx"`
	}
	require.NoError(t, json.Unmarshal(body, &page))
	require.NotNil(t, page.Pdx, "no pdx in %s", body)
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{16}$`), page.Pdx.Epoch)
	assert.NotZero(t, page.Pdx.Ver)
	assert.Contains(t, string(body), `"bseq":0`)
	assert.True(t, strings.HasPrefix(string(decodeTop(t, body)["items"]), "["), "items is not an array: %s", body)

	status, body = f.do(t, http.MethodGet, "/api/nex/v1/executions?limit=100&include_archived=true", nil)
	require.Equal(t, http.StatusOK, status, "body %s", body)
	var next struct {
		Pdx slotStamp `json:"pdx"`
	}
	require.NoError(t, json.Unmarshal(body, &next))
	assert.Equal(t, page.Pdx.Epoch, next.Pdx.Epoch)
	assert.Equal(t, page.Pdx.Ver+1, next.Pdx.Ver)

	status, body = f.do(t, http.MethodGet, "/api/nex/v1/executions/exc_nope", nil)
	require.Equal(t, http.StatusNotFound, status, "body %s", body)
	assert.Equal(t, "execution_not_found", errorCode(body))
	assert.NotContains(t, decodeTop(t, body), "pdx")

	status, body = f.do(t, http.MethodGet, "/api/nex/v1/executions?state=bogus", nil)
	require.Equal(t, http.StatusBadRequest, status, "body %s", body)
	assert.Equal(t, "bad_state", errorCode(body))
	assert.NotContains(t, decodeTop(t, body), "pdx")
}
