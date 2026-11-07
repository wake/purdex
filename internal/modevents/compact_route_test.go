package modevents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// compactWB is a fakeWB that also hands a compaction its text.
type compactWB struct {
	*fakeWB
	text     string
	delay    time.Duration
	calls    atomic.Int32
	nextSeen atomic.Int32 // fakeWB.running at the moment CompactContext ran
	ended    chan struct{}
	hold     chan struct{} // when set, the call waits until it is closed
}

func (c *compactWB) CompactContext(ctx context.Context, sid string) (string, bool) {
	c.calls.Add(1)
	c.nextSeen.Store(c.fakeWB.running.Load())
	select {
	case <-c.hold:
	case <-time.After(c.delay):
	case <-ctx.Done():
		if c.ended != nil {
			close(c.ended)
		}
		return "", false
	}
	return c.text, c.text != ""
}

func compactHandler(c *compactWB, opts ...HandlerOption) http.Handler {
	return NewHandler(capableReg(), append([]HandlerOption{WithWorkbook(func() WorkbookService { return c })}, opts...)...)
}

func compactBody(stream, sid string) string {
	return fmt.Sprintf(`{"stream":%q,"session_id":%q}`, stream, sid)
}

func TestCompactContext_Validation(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{}}
	h := compactHandler(c)
	cases := map[string]string{
		"bad stream":  compactBody("x", testSID),
		"bad session": compactBody(testStream, "nope"),
		"not json":    `nope`,
	}
	for name, body := range cases {
		if rec := post(t, h, http.MethodPost, CompactContextPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := post(t, h, http.MethodGet, CompactContextPath, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
	if c.calls.Load() != 0 {
		t.Fatal("the service ran for a bad request")
	}
}

// Mutation gate: drop the stream check → the service is called → red.
func TestCompactContext_OnlyTheSessionsOwnStream(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{}, text: "recap"}
	h := compactHandler(c)
	for _, stream := range []string{"Unknown-99", "Zz9_-other1x"} {
		if rec := post(t, h, http.MethodPost, CompactContextPath, compactBody(stream, sidB)); rec.Code != http.StatusNoContent {
			t.Errorf("%s: %d %s", stream, rec.Code, rec.Body)
		}
	}
	if c.calls.Load() != 0 {
		t.Fatal("the service was asked for a stream that is not the session's")
	}
}

func TestCompactContext_TextOr204(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{}, text: "the recap"}
	h := compactHandler(c)
	rec := post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"text":"the recap"}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c.text = ""
	if rec := post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID)); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestCompactContext_ServiceWithoutItIs503(t *testing.T) {
	if rec := post(t, wbHandler(&fakeWB{}), http.MethodPost, CompactContextPath, compactBody(testStream, testSID)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("service without it: %d", rec.Code)
	}
	if rec := post(t, wbHandler(nil), http.MethodPost, CompactContextPath, compactBody(testStream, testSID)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no module: %d", rec.Code)
	}
}

// The same stream's `next` long poll holds its gate; compact-context must still reach the service.
// Mutation gate: take h.polls in the route → it queues behind next → red.
func TestCompactContext_RunsBesideAWaitingNext(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{delay: 3 * time.Second}, text: "recap"}
	h := compactHandler(c)
	done := make(chan struct{})
	go func() {
		defer close(done)
		post(t, h, http.MethodPost, WorkbookNextPath, nextBody(testStream, testSID, 5000))
	}()
	for i := 0; c.fakeWB.running.Load() != 1; i++ {
		if i > 200 {
			t.Fatal("next never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec := post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID))
	if rec.Code != 200 || c.nextSeen.Load() != 1 {
		t.Fatalf("%d %s next running=%d", rec.Code, rec.Body, c.nextSeen.Load())
	}
	<-done
}

// Mutation gate: take h.activePolls in the route → next answers 429 → red.
func TestCompactContext_DoesNotTakeThePollSlots(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{delay: 100 * time.Millisecond}, delay: time.Minute, hold: make(chan struct{})}
	h := compactHandler(c, withMaxPolls(1))
	done := make(chan struct{})
	go func() {
		defer close(done)
		post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID))
	}()
	for i := 0; c.calls.Load() == 0; i++ {
		if i > 200 {
			t.Fatal("the service never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec := post(t, h, http.MethodPost, WorkbookNextPath, nextBody(testStream, testSID, 5000)); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("next was refused while a compact-context was held: %d %s", rec.Code, rec.Body)
	}
	close(c.hold)
	<-done
}

// Mutation gate: drop the cap → the extra request is not 429 → red.
func TestCompactContext_TooManyIs429(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{}, text: "recap", delay: time.Minute, hold: make(chan struct{})}
	h := compactHandler(c)
	var wg sync.WaitGroup
	for i := 0; i < maxCompactContexts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID))
		}()
	}
	for i := 0; int(c.calls.Load()) < maxCompactContexts; i++ {
		if i > 300 {
			t.Fatal("the held requests never all reached the service")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec := post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID)); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the cap: %d %s", rec.Code, rec.Body)
	}
	close(c.hold)
	wg.Wait()
	if rec := post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID)); rec.Code != http.StatusOK {
		t.Fatalf("after release: %d %s", rec.Code, rec.Body)
	}
}

// Mutation gate: drop SetWriteDeadline → the client gets an EOF → red.
func TestCompactContext_TheWriteDeadlineIsExtended(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{}, text: "late", delay: 900 * time.Millisecond}
	p := sockPath(t)
	l := mustListen(t, p)
	srv := NewServer(compactHandler(c))
	srv.WriteTimeout = 300 * time.Millisecond
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	res, err := unixClient(t, p).Post("http://pdx"+CompactContextPath, "application/json", strings.NewReader(compactBody(testStream, testSID)))
	if err != nil {
		t.Fatalf("the request was cut: %v", err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"late"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

func TestCompactContext_StopEndsTheWait(t *testing.T) {
	c := &compactWB{fakeWB: &fakeWB{}, text: "x", delay: time.Minute, ended: make(chan struct{})}
	stop := make(chan struct{})
	h := compactHandler(c, WithStop(stop))
	codes := make(chan int, 1)
	go func() {
		codes <- post(t, h, http.MethodPost, CompactContextPath, compactBody(testStream, testSID)).Code
	}()
	for i := 0; c.calls.Load() == 0; i++ {
		if i > 200 {
			t.Fatal("the service never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	select {
	case <-c.ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the service kept waiting after stop")
	}
	if code := <-codes; code != http.StatusNoContent {
		t.Fatalf("code %d", code)
	}
}
