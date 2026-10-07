package modevents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func post(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

func TestHandler_AcksAndDedupes(t *testing.T) {
	reg := NewRegistry(time.Now)
	rec := &recorder{}
	reg.Subscribe(rec.fn)
	p := sockPath(t)
	serve(t, mustListen(t, p), NewHandler(reg)) // the real peer-uid check
	c := unixClient(t, p)

	body := batchJSON(1, testStream, evs(ev(1, testSID, "turn.start"), ev(2, testSID, "turn.complete")))
	for i := range 2 {
		res, err := c.Post("http://pdx/mod/v1/events", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || string(b) != `{"ack":2}` || res.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("post %d: %d %s (%s)", i, res.StatusCode, b, res.Header.Get("Content-Type"))
		}
	}
	if got := rec.list(); len(got) != 2 {
		t.Fatalf("deliveries = %v; the resent batch must not be delivered again", got)
	}
	info := streamInfo(t, reg, testStream)
	if info.LastSeq != 2 || info.Counts["turn.start"] != 1 || info.SID != testSID || info.CCVersion != "2.1.293" {
		t.Fatalf("info = %+v", info)
	}
}

func TestHandler_ErrorCodes(t *testing.T) {
	h := NewHandler(NewRegistry(time.Now))
	codes := map[string]string{
		CodeBadJSON:            `{"v":1,`,
		CodeUnsupportedVersion: batchJSON(2, testStream, evs(ev(1, testSID, "turn.start"))),
		CodeBadStream:          batchJSON(1, "bad", evs(ev(1, testSID, "turn.start"))),
		CodeBadEvents:          batchJSON(1, testStream, `[]`),
		CodeBadSeq:             batchJSON(1, testStream, evs(ev(2, testSID, "turn.start"), ev(1, testSID, "turn.start"))),
		CodeBadSID:             batchJSON(1, testStream, evs(ev(1, "nope", "turn.start"))),
	}
	for code, body := range codes {
		res := post(t, h, http.MethodPost, "/mod/v1/events", body)
		var got map[string]string
		if res.Code != http.StatusBadRequest || json.Unmarshal(res.Body.Bytes(), &got) != nil || got["error"] != code {
			t.Errorf("%s: %d %s", code, res.Code, res.Body.String())
		}
		if ct := res.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: content type %q", code, ct)
		}
	}

	res := post(t, h, http.MethodGet, "/mod/v1/events", "")
	if res.Code != http.StatusMethodNotAllowed || res.Header().Get("Allow") != http.MethodPost || res.Header().Get("Content-Type") != "application/json" {
		t.Errorf("GET: %d allow=%q", res.Code, res.Header().Get("Allow"))
	}
	for _, target := range []string{"/", "/mod/v1/events/x", "/api/mod/streams", "/mod/v2/events"} {
		if res := post(t, h, http.MethodPost, target, "{}"); res.Code != http.StatusNotFound || res.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: %d", target, res.Code)
		}
	}

	big := `{"v":1,"stream":"` + testStream + `","pad":"` + strings.Repeat("x", MaxBody) + `"}`
	res = post(t, h, http.MethodPost, "/mod/v1/events", big)
	if res.Code != http.StatusRequestEntityTooLarge || strings.TrimSpace(res.Body.String()) != `{"error":"too_large"}` {
		t.Errorf("too large: %d %s", res.Code, res.Body.String())
	}
}

func TestHandler_RejectCountsOnValidStream(t *testing.T) {
	reg := NewRegistry(time.Now)
	h := NewHandler(reg)
	res := post(t, h, http.MethodPost, "/mod/v1/events", batchJSON(1, testStream, evs(ev(1, testSID, "turn.start"), ev(1, testSID, "turn.start"))))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("bad_seq: %d", res.Code)
	}
	if info := streamInfo(t, reg, testStream); info.Rejected != 1 || info.LastSeq != 0 {
		t.Fatalf("info = %+v; want rejected 1", info)
	}
	post(t, h, http.MethodPost, "/mod/v1/events", batchJSON(1, "bad", evs(ev(1, testSID, "turn.start"))))
	post(t, h, http.MethodPost, "/mod/v1/events", `{"v":1,`)
	if n := len(reg.Streams()); n != 1 {
		t.Fatalf("a bad stream id or bad JSON counts nowhere: %d streams", n)
	}
}

// A batch for a new stream while every one of MaxStreams streams is pinned
// is 503 registry_full (spec §6.2); the mod backs off and resends.
func TestHandler_RegistryFull503(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	rec, release := pinAll(t, reg, clk)
	p := sockPath(t)
	serve(t, mustListen(t, p), NewHandler(reg))
	c := unixClient(t, p)

	res, err := c.Post("http://pdx/mod/v1/events", "application/json",
		strings.NewReader(batchJSON(1, testStream, evs(ev(1, testSID, "turn.start")))))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || string(b) != `{"error":"registry_full"}` || res.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("full registry: %d %s (%s)", res.StatusCode, b, res.Header.Get("Content-Type"))
	}
	if _, ok := reg.Events(testStream, 0); ok {
		t.Fatal("a refused stream must not be added")
	}
	if got := rec.list(); len(got) != 0 {
		t.Fatalf("deliveries = %v: a refused batch must not be delivered", got)
	}
	release()
}

func TestNewServer_Timeouts(t *testing.T) {
	s := NewServer(http.NotFoundHandler())
	if s.ReadHeaderTimeout != 5*time.Second || s.ReadTimeout != 10*time.Second || s.WriteTimeout != 10*time.Second || s.MaxHeaderBytes != 16<<10 {
		t.Fatalf("server = %+v", s)
	}
}
