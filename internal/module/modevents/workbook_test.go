package modeventsmod

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/modevents"
)

type fakeWBService struct{ waiting bool }

func (f *fakeWBService) NextJob(context.Context, string, string, time.Duration) (any, bool) {
	return map[string]string{"id": "j1"}, true
}
func (f *fakeWBService) JobResult(string, modevents.WorkbookResult) (bool, error) { return true, nil }
func (f *fakeWBService) JobWaiting(string) bool                                   { return f.waiting }
func (f *fakeWBService) RequestRefresh(string, string) (int64, error)             { return 0, nil }

func socketPost(t *testing.T, path, target, body string) (int, string) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}
	defer tr.CloseIdleConnections()
	res, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Post("http://pdx"+target, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

const (
	wbStream = "Ab3_-xyz09"
	wbSID    = "0f8e2c1a-1b2c-4d3e-8f90-a1b2c3d4e5f6"
)

// The workbook module is found at request time (it need not be up when the socket starts): 503 before, the job after.
func TestWorkbookRoutes_ServedOnTheSocketFromTheWorkbookService(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	next := `{"stream":"` + wbStream + `","session_id":"` + wbSID + `","wait_ms":0}`
	if code, body := socketPost(t, m.path, modevents.WorkbookNextPath, next); code != 503 {
		t.Fatalf("before the workbook module: %d %s", code, body)
	}
	c.Registry.Register(workbookJobsKey, modevents.WorkbookService(&fakeWBService{}))
	if code, _ := socketPost(t, m.path, modevents.WorkbookNextPath, next); code != 204 {
		t.Fatalf("a stream that never announced the capability: %d", code)
	}
	if _, err := m.reg.Apply(modevents.Batch{V: 1, Stream: wbStream, Agent: "cc", Caps: []string{modevents.CapWorkbookV2},
		Events: []modevents.Event{{Seq: 1, SID: wbSID, Type: "heartbeat", Data: []byte(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	code, body := socketPost(t, m.path, modevents.WorkbookNextPath, next)
	if code != 200 || strings.TrimSpace(body) != `{"job":{"id":"j1"}}` {
		t.Fatalf("%d %s", code, body)
	}
}

type offWBService struct{ fakeWBService }

func (offWBService) Ready() bool { return false }

// A workbook module that is registered but off answers 503, not "no job" (codex R1).
// Mutation gate: ignore Ready → red.
func TestWorkbookRoutes_ARegisteredButOffModuleIs503(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	c.Registry.Register(workbookJobsKey, modevents.WorkbookService(&offWBService{}))
	next := `{"stream":"` + wbStream + `","session_id":"` + wbSID + `","wait_ms":0}`
	if code, body := socketPost(t, m.path, modevents.WorkbookNextPath, next); code != 503 {
		t.Fatalf("%d %s", code, body)
	}
}

// The job routes exist on the mod socket only: the daemon's TCP mux (token-guarded, remote) does not serve them.
// Mutation gate: add the routes to RegisterRoutes → red.
func TestWorkbookRoutes_NotServedOnTheTCPMux(t *testing.T) {
	m, c, _ := started(t, shortDir(t))
	c.Registry.Register(workbookJobsKey, modevents.WorkbookService(&fakeWBService{}))
	mux := http.NewServeMux()
	m.RegisterRoutes(mux)
	for _, target := range []string{modevents.WorkbookNextPath, modevents.WorkbookResultPath, "/api/mod/workbook/next"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{}`)))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 404/405", target, rec.Code)
		}
	}
}
