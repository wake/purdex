package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/statuspending"
)

// #2545: a payload the proxy could not deliver is kept (the newest per session) for the daemon's next boot; a delivery removes
// its own session's file unless a newer failure wrote it; the render path costs nothing when there is nothing pending.

const pendSid = "0a1b2c3d-0000-4000-8000-0000000000aa"

func pendPayload(sid string) statuslinePayload {
	return statuslinePayload{TmuxSession: "t", AgentType: "cc",
		RawStatus: json.RawMessage(fmt.Sprintf(`{"session_id":%q,"model":{"id":"claude-opus-5-5"},"effort":{"level":"xhigh"}}`, sid))}
}

func downURL(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := ts.URL
	ts.Close() // nothing listens there any more: a daemon that is down
	return url + "/api/agent/status"
}

func upURL(t *testing.T, status int) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(ts.Close)
	return ts.URL + "/api/agent/status"
}

// Mutations: nothing written on failure → red; written for a delivery that worked → red; the time not the render's → red.
func TestDeliverStatus_AFailedPostIsKept(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	at := time.UnixMilli(5000)
	deliverStatus(cfg, downURL(t), "", pendPayload(pendSid), at)
	got, err := statuspending.Load(statuspending.DirFor(cfg))
	if err != nil || len(got) != 1 || got[0].SessionID != pendSid || got[0].AtMs != 5000 {
		t.Fatalf("pending = %+v (%v)", got, err)
	}
	if st, _ := os.Stat(statuspending.DirFor(cfg)); st == nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode: %v", st)
	}
}

// A 4xx/5xx answer is a failed delivery too (the daemon did not take it).
func TestDeliverStatus_ARefusedPostIsKept(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	deliverStatus(cfg, upURL(t, http.StatusServiceUnavailable), "", pendPayload(pendSid), time.UnixMilli(7000))
	if got, _ := statuspending.Load(statuspending.DirFor(cfg)); len(got) != 1 {
		t.Fatalf("pending = %+v", got)
	}
}

// A delivery clears an older pending file of the session, leaves a newer one, and writes nothing itself.
func TestDeliverStatus_ADeliveryClearsItsOwnOlderFile(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	dir := statuspending.DirFor(cfg)
	deliverStatus(cfg, downURL(t), "", pendPayload(pendSid), time.UnixMilli(1000))
	deliverStatus(cfg, upURL(t, http.StatusOK), "", pendPayload(pendSid), time.UnixMilli(900)) // an older render got through
	if got, _ := statuspending.Load(dir); len(got) != 1 {
		t.Fatal("a newer failure's file was removed by an older delivery")
	}
	deliverStatus(cfg, upURL(t, http.StatusOK), "", pendPayload(pendSid), time.UnixMilli(2000))
	if got, _ := statuspending.Load(dir); len(got) != 0 {
		t.Fatalf("the file stayed after a newer delivery: %+v", got)
	}
}

// An unsafe session id, no config, or the daemon's own self-test traffic: nothing is written, nothing breaks.
func TestDeliverStatus_NothingToKeep(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir()}
	deliverStatus(cfg, downURL(t), "", pendPayload("../escape"), time.UnixMilli(1))
	deliverStatus(nil, downURL(t), "", pendPayload(pendSid), time.UnixMilli(1))
	t.Setenv("PDX_STATUSLINE_TEST_SESSION", "pdx-statusline-test-nonce")
	deliverStatus(cfg, downURL(t), "", pendPayload(pendSid), time.UnixMilli(1))
	if got, _ := statuspending.Load(statuspending.DirFor(cfg)); len(got) != 0 {
		t.Fatalf("pending = %+v", got)
	}
}

// What the render path costs when nothing is pending: the delivery plus ONE stat of a missing directory. Reported in the PR.
func BenchmarkDeliverStatus_NothingPending(b *testing.B) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()
	cfg := &config.Config{DataDir: b.TempDir()}
	p := pendPayload(pendSid)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deliverStatus(cfg, ts.URL, "", p, time.Now())
	}
}

func BenchmarkDeliverStatus_DirectoryExistsNoFile(b *testing.B) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()
	cfg := &config.Config{DataDir: b.TempDir()}
	_ = os.MkdirAll(statuspending.DirFor(cfg), 0o700)
	p := pendPayload(pendSid)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deliverStatus(cfg, ts.URL, "", p, time.Now())
	}
}
