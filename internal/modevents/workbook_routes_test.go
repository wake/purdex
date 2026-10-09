package modevents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// WB-1b′-c: caps on the events wire, and the workbook job routes of the mod socket.

type fakeWB struct {
	mu       sync.Mutex
	delay    time.Duration
	job      any
	results  []WorkbookResult
	resErr   error
	more     bool
	waiting  bool
	running  atomic.Int32
	maxSeen  atomic.Int32
	gotWait  time.Duration
	ctxEnded chan struct{}
}

func (f *fakeWB) NextJob(ctx context.Context, stream, sid string, wait time.Duration) (any, bool) {
	n := f.running.Add(1)
	defer f.running.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	f.mu.Lock()
	f.gotWait = wait
	delay, job := f.delay, f.job
	f.mu.Unlock()
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		if f.ctxEnded != nil {
			close(f.ctxEnded)
		}
		return nil, false
	}
	return job, job != nil
}

func (f *fakeWB) JobResult(_ string, r WorkbookResult) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, r)
	return f.more, f.resErr
}

func (f *fakeWB) JobWaiting(string) bool { return f.waiting }

func wbHandler(f *fakeWB) http.Handler {
	return NewHandler(NewRegistry(time.Now), WithWorkbook(func() WorkbookService {
		if f == nil {
			return nil
		}
		return f
	}))
}

func nextBody(stream, sid string, wait any) string {
	return fmt.Sprintf(`{"stream":%q,"session_id":%q,"wait_ms":%v}`, stream, sid, wait)
}

func TestWorkbookNext_Validation(t *testing.T) {
	h := wbHandler(&fakeWB{})
	cases := map[string]string{
		"bad stream":     nextBody("x", testSID, 0),
		"bad session":    nextBody(testStream, "nope", 0),
		"wait too big":   nextBody(testStream, testSID, 15001),
		"wait negative":  nextBody(testStream, testSID, -1),
		"wait missing":   fmt.Sprintf(`{"stream":%q,"session_id":%q}`, testStream, testSID),
		"not json":       `nope`,
		"wait not a num": nextBody(testStream, testSID, `"5"`),
	}
	for name, body := range cases {
		if rec := post(t, h, http.MethodPost, WorkbookNextPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if rec := post(t, h, http.MethodGet, WorkbookNextPath, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
	if rec := post(t, wbHandler(nil), http.MethodPost, WorkbookNextPath, nextBody(testStream, testSID, 0)); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no module: %d", rec.Code)
	}
}

func TestWorkbookNext_JobOr204(t *testing.T) {
	f := &fakeWB{job: map[string]string{"id": "j1"}}
	h := wbHandler(f)
	rec := post(t, h, http.MethodPost, WorkbookNextPath, nextBody(testStream, testSID, 1500))
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"job":{"id":"j1"}}` || f.gotWait != 1500*time.Millisecond {
		t.Fatalf("%d %s wait=%v", rec.Code, rec.Body, f.gotWait)
	}
	f.job = nil
	if rec := post(t, h, http.MethodPost, WorkbookNextPath, nextBody(testStream, testSID, 0)); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

// A long poll answers after the server's own WriteTimeout would have cut it: the route extends its write deadline to the
// wait + 5 s. Here the server's timeout is shortened so the test is quick.
// Mutation gate: drop SetWriteDeadline → the client gets an EOF → red.
func TestWorkbookNext_TheWriteDeadlineIsExtended(t *testing.T) {
	f := &fakeWB{delay: 900 * time.Millisecond, job: map[string]string{"id": "late"}}
	p := sockPath(t)
	l := mustListen(t, p)
	srv := NewServer(wbHandler(f))
	srv.WriteTimeout = 300 * time.Millisecond
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	res, err := unixClient(t, p).Post("http://pdx"+WorkbookNextPath, "application/json", strings.NewReader(nextBody(testStream, testSID, 1000)))
	if err != nil {
		t.Fatalf("the long poll was cut: %v", err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), `"late"`) {
		t.Fatalf("%d %s", res.StatusCode, b)
	}
}

// A client that goes away ends the wait.
func TestWorkbookNext_ClientGoneEndsTheWait(t *testing.T) {
	f := &fakeWB{delay: time.Minute, ctxEnded: make(chan struct{})}
	p := sockPath(t)
	serve(t, mustListen(t, p), wbHandler(f))
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://pdx"+WorkbookNextPath, strings.NewReader(nextBody(testStream, testSID, 15000)))
	go func() { _, _ = unixClient(t, p).Do(req) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case <-f.ctxEnded:
	case <-time.After(2 * time.Second):
		t.Fatal("the service kept waiting for a client that was gone")
	}
}

// One long poll per stream at a time; another stream is independent.
// Mutation gate: drop the gate → the max concurrency is 2 → red.
func TestWorkbookNext_OnePollPerStream(t *testing.T) {
	f := &fakeWB{delay: 250 * time.Millisecond}
	h := wbHandler(f)
	var wg sync.WaitGroup
	poll := func(stream string) {
		defer wg.Done()
		post(t, h, http.MethodPost, WorkbookNextPath, nextBody(stream, testSID, 5000))
	}
	wg.Add(2)
	go poll(testStream)
	go poll(testStream)
	wg.Wait()
	if m := f.maxSeen.Load(); m != 1 {
		t.Fatalf("two polls of one stream ran together (max %d)", m)
	}
	f.maxSeen.Store(0)
	wg.Add(2)
	go poll(testStream)
	go poll("Zz9_-other1")
	wg.Wait()
	if m := f.maxSeen.Load(); m != 2 {
		t.Fatalf("two streams must poll together (max %d)", m)
	}
}

func TestWorkbookResult(t *testing.T) {
	f := &fakeWB{more: true}
	h := wbHandler(f)
	ok := fmt.Sprintf(`{"stream":%q,"job_id":"wbj-1","answered":true,"text":"{}","usage":{"input":10,"output":2,"cache_read":7},"latency_ms":900}`, testStream)
	rec := post(t, h, http.MethodPost, WorkbookResultPath, ok)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"more":true}` {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if r := f.results[0]; r.JobID != "wbj-1" || !r.Answered || r.Usage.CacheRead != 7 || r.LatencyMS != 900 || r.Stream != testStream {
		t.Fatalf("result = %+v", r)
	}
	f.resErr = ErrNotLeased
	if rec := post(t, h, http.MethodPost, WorkbookResultPath, ok); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "not_leased") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f.resErr = nil
	for name, body := range map[string]string{
		"no job":       fmt.Sprintf(`{"stream":%q}`, testStream),
		"bad stream":   `{"stream":"x","job_id":"j"}`,
		"negative":     fmt.Sprintf(`{"stream":%q,"job_id":"j","latency_ms":-1}`, testStream),
		"not json":     `{`,
		"long job id":  fmt.Sprintf(`{"stream":%q,"job_id":%q}`, testStream, strings.Repeat("a", 65)),
		"long text":    fmt.Sprintf(`{"stream":%q,"job_id":"j","text":%q}`, testStream, strings.Repeat("a", maxResultText+1)),
		"neg usage":    fmt.Sprintf(`{"stream":%q,"job_id":"j","usage":{"input":-1}}`, testStream),
		"long error":   fmt.Sprintf(`{"stream":%q,"job_id":"j","error":%q}`, testStream, strings.Repeat("e", 129)),
		"long reason":  fmt.Sprintf(`{"stream":%q,"job_id":"j","reason":%q}`, testStream, strings.Repeat("r", 65)),
		"wrong type":   fmt.Sprintf(`{"stream":%q,"job_id":5}`, testStream),
		"answered str": fmt.Sprintf(`{"stream":%q,"job_id":"j","answered":"yes"}`, testStream),
	} {
		if rec := post(t, h, http.MethodPost, WorkbookResultPath, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := post(t, wbHandler(nil), http.MethodPost, WorkbookResultPath, ok); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no module: %d", rec.Code)
	}
}

// The events answer carries workbook:true only while a job waits.
// Mutation gate: always add the hint, or never → red.
func TestEvents_AnswerHintsAWaitingJob(t *testing.T) {
	f := &fakeWB{}
	h := wbHandler(f)
	body := batchJSON(1, testStream, evs(ev(1, testSID, "heartbeat")))
	if rec := post(t, h, http.MethodPost, EventsPath, body); strings.TrimSpace(rec.Body.String()) != `{"ack":1}` {
		t.Fatalf("no job: %s", rec.Body)
	}
	f.waiting = true
	rec := post(t, h, http.MethodPost, EventsPath, batchJSON(1, testStream, evs(ev(2, testSID, "heartbeat"))))
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["ack"] != float64(2) || got["workbook"] != true {
		t.Fatalf("waiting: %s", rec.Body)
	}
}

func capBatch(stream string, seq int64, sid string, caps string) string {
	return fmt.Sprintf(`{"v":1,"stream":%q,"agent":"cc","cc_version":"2.1.293","mod_version":"1.0.0","dropped_total":0,"caps":%s,"events":%s}`,
		stream, caps, evs(ev(seq, sid, "heartbeat")))
}

func TestDecodeBatch_Caps(t *testing.T) {
	for name, caps := range map[string]string{
		"upper":    `["Workbook.v2"]`,
		"spaces":   `["a b"]`,
		"too many": `["a","b","c","d","e","f","g","h","i"]`,
		"long":     `["` + strings.Repeat("a", 33) + `"]`,
		"not str":  `[1]`,
	} {
		_, err := DecodeBatch(strings.NewReader(capBatch(testStream, 1, testSID, caps)))
		var we *WireError
		if err == nil || !asWire(err, &we) || (name != "not str" && we.Code != CodeBadCap) {
			t.Errorf("%s: %v", name, err)
		}
	}
	b, err := DecodeBatch(strings.NewReader(capBatch(testStream, 1, testSID, `["workbook.v2","workbook.refresh"]`)))
	if err != nil || len(b.Caps) != 2 {
		t.Fatalf("%v %+v", err, b.Caps)
	}
	if b, err := DecodeBatch(strings.NewReader(batchJSON(1, testStream, evs(ev(1, testSID, "heartbeat"))))); err != nil || len(b.Caps) != 0 {
		t.Fatalf("an older mod: %v %+v", err, b.Caps)
	}
}

func asWire(err error, target **WireError) bool {
	we, ok := err.(*WireError)
	*target = we
	return ok
}

// A capability counts for 30 s after the batch that named it, for the stream's current session only, and not once the
// stream ended; an older mod (no caps) is never capable.
// Mutation gate: ignore the age, the sid, or Ended → red.
func TestRegistry_SessionCapable(t *testing.T) {
	clk := newFakeClock()
	reg := NewRegistry(clk.Now)
	apply := func(b Batch) {
		t.Helper()
		if _, err := reg.Apply(b); err != nil {
			t.Fatal(err)
		}
	}
	b := mkBatch("stream-aaaa", 0, mkEvent(1, "heartbeat"))
	apply(b)
	if reg.SessionCapable(sidA, "workbook.v2", CapsFresh) {
		t.Fatal("an older mod is not capable")
	}
	b = mkBatch("stream-aaaa", 0, mkEvent(2, "heartbeat"))
	b.Caps = []string{"workbook.v2"}
	apply(b)
	if !reg.SessionCapable(sidA, "workbook.v2", CapsFresh) || reg.SessionCapable(sidA, "workbook.refresh", CapsFresh) || reg.SessionCapable(sidB, "workbook.v2", CapsFresh) {
		t.Fatal("capability by name and by current session")
	}
	clk.Advance(29 * time.Second)
	if !reg.SessionCapable(sidA, "workbook.v2", CapsFresh) {
		t.Fatal("still fresh at 29 s")
	}
	clk.Advance(2 * time.Second)
	if reg.SessionCapable(sidA, "workbook.v2", CapsFresh) {
		t.Fatal("stale at 31 s")
	}
	b = mkBatch("stream-aaaa", 0, Event{Seq: 3, SID: sidB, Type: "heartbeat", Data: json.RawMessage(`{}`)})
	b.Caps = []string{"workbook.v2"}
	apply(b)
	if reg.SessionCapable(sidA, "workbook.v2", CapsFresh) || !reg.SessionCapable(sidB, "workbook.v2", CapsFresh) {
		t.Fatal("the capability follows the stream's current session")
	}
	end := mkBatch("stream-aaaa", 0, Event{Seq: 4, SID: sidB, Type: TypeSessionEnd, Data: json.RawMessage(`{"reason":"exit"}`)})
	end.Caps = []string{"workbook.v2"}
	apply(end)
	if reg.SessionCapable(sidB, "workbook.v2", CapsFresh) {
		t.Fatal("an ended stream is not capable")
	}
}
