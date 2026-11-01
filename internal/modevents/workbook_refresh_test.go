package modevents

import (
	"encoding/json"
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

// Refresh is retired: every well-formed request is a 409 "disabled", whatever the stream announced and whether or not the
// workbook service is there. The daemon is never asked (the service has no refresh method any more).
func TestWorkbookRefresh_AlwaysDisabled(t *testing.T) {
	nilSvc := NewHandler(refreshReg(), WithWorkbook(func() WorkbookService { return nil }))
	for name, h := range map[string]http.Handler{"service": refreshHandler(&fakeWB{}), "no service": nilSvc, "no workbook option": NewHandler(refreshReg())} {
		for sname, body := range map[string]string{
			"refresh-capable stream": refreshBody(testStream, testSID),
			"v2 only":                refreshBody("Zz9_-other1", testSID),
			"other session":          refreshBody(testStream, "99999999-aaaa-bbbb-cccc-dddddddddddd"),
			"unknown":                refreshBody("Zz9_-unknown", testSID),
		} {
			rec := post(t, h, http.MethodPost, WorkbookRefreshPath, body)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"error":"disabled"`) {
				t.Fatalf("%s / %s: %d %s", name, sname, rec.Code, rec.Body.String())
			}
		}
	}
}

// A stream that announced only workbook.refresh (an older mod) may still poll `next`: CapWorkbookRefresh stays in streamMayPoll.
func TestWorkbookNext_RefreshOnlyStreamStillPolls(t *testing.T) {
	reg := NewRegistry(time.Now)
	_, _ = reg.Apply(Batch{V: 1, Stream: "Refr-0001", Agent: "cc", Caps: []string{CapWorkbookRefresh},
		Events: []Event{{Seq: 1, SID: testSID, Type: "heartbeat", Data: json.RawMessage(`{}`)}}})
	f := &fakeWB{job: map[string]string{"id": "j1"}}
	h := NewHandler(reg, WithWorkbook(func() WorkbookService { return f }))
	if rec := post(t, h, http.MethodPost, WorkbookNextPath, nextBody("Refr-0001", testSID, 0)); rec.Code != http.StatusOK {
		t.Fatalf("a refresh-only stream: %d %s", rec.Code, rec.Body.String())
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
