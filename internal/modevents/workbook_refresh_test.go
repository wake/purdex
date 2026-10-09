package modevents

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// WB-2b-i (b): the mod socket's refresh route and the registry's capable-session list.

// refreshReg: testStream announced workbook.v2 and workbook.refresh, "Zz9_-other1" workbook.v2 only; both are on testSID.
func refreshReg() *Registry {
	reg := NewRegistry(time.Now)
	_, _ = reg.Apply(Batch{V: 1, Stream: testStream, Agent: "cc", Caps: []string{CapWorkbookV2, CapWorkbookRefresh},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	_, _ = reg.Apply(Batch{V: 1, Stream: "Zz9_-other1", Agent: "cc", Caps: []string{CapWorkbookV2},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	return reg
}

func refreshHandler(f *fakeWB) http.Handler {
	return NewHandler(refreshReg(), WithWorkbook(func() WorkbookService { return f }))
}

func refreshBody(stream, sid string) string {
	b, _ := json.Marshal(map[string]string{"stream": stream, "session_id": sid})
	return string(b)
}

// 202 with the entry id; the daemon is asked with the caller's stream and session.
func TestWorkbookRefresh_Accepted(t *testing.T) {
	f := &fakeWB{refreshID: 42}
	rec := post(t, refreshHandler(f), http.MethodPost, WorkbookRefreshPath, refreshBody(testStream, testSID))
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"entry_id":42`) {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if len(f.refreshAsked) != 1 || f.refreshAsked[0] != testStream+"/"+testSID {
		t.Fatalf("asked = %v", f.refreshAsked)
	}
}

// The two 409s; anything else is a 500.
func TestWorkbookRefresh_Conflicts(t *testing.T) {
	for _, c := range []struct {
		err  error
		code int
		body string
	}{
		{ErrNotLive, http.StatusConflict, "not_live"},
		{ErrRefreshPending, http.StatusConflict, "refresh_pending"},
		{errors.New("boom"), http.StatusInternalServerError, "internal"},
	} {
		rec := post(t, refreshHandler(&fakeWB{refreshErr: c.err}), http.MethodPost, WorkbookRefreshPath, refreshBody(testStream, testSID))
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.body) {
			t.Fatalf("%v: %d %s", c.err, rec.Code, rec.Body.String())
		}
	}
}

// A stream that did not announce workbook.refresh (a WB-1c mod), or is not the session's, never reaches the daemon: 409
// not_live. Mutation gate: drop the StreamCapable check → red.
func TestWorkbookRefresh_OnlyARefreshCapableStreamOfTheSession(t *testing.T) {
	f := &fakeWB{refreshID: 1}
	h := refreshHandler(f)
	for name, body := range map[string]string{
		"v2 only":       refreshBody("Zz9_-other1", testSID),
		"other session": refreshBody(testStream, "99999999-aaaa-bbbb-cccc-dddddddddddd"),
		"unknown":       refreshBody("Zz9_-unknown", testSID),
	} {
		rec := post(t, h, http.MethodPost, WorkbookRefreshPath, body)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not_live") {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	if len(f.refreshAsked) != 0 {
		t.Fatalf("the daemon was asked: %v", f.refreshAsked)
	}
}

func TestWorkbookRefresh_Validation(t *testing.T) {
	h := refreshHandler(&fakeWB{})
	for name, body := range map[string]string{
		"bad stream":  refreshBody("x", testSID),
		"bad session": refreshBody(testStream, "nope"),
		"not json":    `nope`,
	} {
		if rec := post(t, h, http.MethodPost, WorkbookRefreshPath, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", name, rec.Code)
		}
	}
	if rec := post(t, h, http.MethodGet, WorkbookRefreshPath, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("get: %d", rec.Code)
	}
	nilSvc := NewHandler(refreshReg(), WithWorkbook(func() WorkbookService { return nil }))
	if rec := post(t, nilSvc, http.MethodPost, WorkbookRefreshPath, refreshBody(testStream, testSID)); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no service: %d", rec.Code)
	}
}

// CapableSessions lists the live streams that named the capability within the window, newest announcement per stream.
func TestRegistry_CapableSessions(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := &now
	reg := NewRegistry(func() time.Time { return *clock })
	_, _ = reg.Apply(Batch{V: 1, Stream: testStream, Agent: "cc", Caps: []string{CapWorkbookRefresh},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	_, _ = reg.Apply(Batch{V: 1, Stream: "Zz9_-other1", Agent: "cc", Caps: []string{CapWorkbookV2},
		Events: []Event{{Seq: 1, SID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	got := reg.CapableSessions(CapWorkbookRefresh, CapsFresh)
	if len(got) != 1 || got[0].SID != testSID || !got[0].At.Equal(now) {
		t.Fatalf("got = %+v", got)
	}
	later := now.Add(CapsFresh + time.Second)
	*clock = later
	if got := reg.CapableSessions(CapWorkbookRefresh, CapsFresh); len(got) != 0 {
		t.Fatalf("a stale announcement counted: %+v", got)
	}
}
