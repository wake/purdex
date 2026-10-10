package modevents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// #2420: a daemon restart must not wait for the mods' parked long polls (workbook and prompt `next`, up to 15 s each).
// With WithStop, closing the channel answers them at once; without it they run their wait out.

func parked(t *testing.T, h http.Handler, path, body string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		out <- rec
	}()
	time.Sleep(100 * time.Millisecond) // let it park
	return out
}

func answered(t *testing.T, what string, ch <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-ch:
		return rec
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: still parked after the stop signal", what)
		return nil
	}
}

// Mutation gate: pollContext ignores stop → both red.
func TestStop_PromptNextAnswersAtOnce(t *testing.T) {
	stop := make(chan struct{})
	f := &parkedPrompt{}
	h := NewHandler(promptReg(), WithPrompt(func() PromptService { return f }), WithStop(stop))
	ch := parked(t, h, PromptNextPath, nextBody(testStream, testSID, 15000))
	close(stop)
	if rec := answered(t, "prompt next", ch); rec.Code != http.StatusNoContent {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body.String())
	}
}

func TestStop_WorkbookNextAnswersAtOnce(t *testing.T) {
	stop := make(chan struct{})
	f := &fakeWB{delay: time.Minute}
	h := wbHandler(f, WithStop(stop))
	ch := parked(t, h, WorkbookNextPath, nextBody(testStream, testSID, 15000))
	close(stop)
	if rec := answered(t, "workbook next", ch); rec.Code != http.StatusNoContent {
		t.Fatalf("answer = %d %s", rec.Code, rec.Body.String())
	}
}

// A poll queued behind its stream's earlier poll (the per-stream gate) is released by the stop too.
func TestStop_AQueuedPollIsReleased(t *testing.T) {
	stop := make(chan struct{})
	f := &parkedPrompt{}
	h := NewHandler(promptReg(), WithPrompt(func() PromptService { return f }), WithStop(stop))
	first := parked(t, h, PromptNextPath, nextBody(testStream, testSID, 15000))
	second := parked(t, h, PromptNextPath, nextBody(testStream, testSID, 15000))
	close(stop)
	answered(t, "first", first)
	answered(t, "queued", second)
}

// No stop channel: nothing changes, the poll runs its wait (a 0 ms one answers immediately).
func TestStop_NoChannelNoChange(t *testing.T) {
	f := &parkedPrompt{}
	h := NewHandler(promptReg(), WithPrompt(func() PromptService { return f }))
	if rec := post(t, h, http.MethodPost, PromptNextPath, nextBody(testStream, testSID, 0)); rec.Code != http.StatusNoContent {
		t.Fatalf("answer = %d", rec.Code)
	}
}
