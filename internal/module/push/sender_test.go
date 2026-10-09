package push

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
)

// fakeBook is the sender's view of the devices: an in-memory book that records what the sender did to it.
type fakeBook struct {
	mu      sync.Mutex
	devices map[string]push.Device
	sent    map[string]int64
	errors  map[string]string
	removed []string
}

func newBook(ds ...push.Device) *fakeBook {
	b := &fakeBook{devices: map[string]push.Device{}, sent: map[string]int64{}, errors: map[string]string{}}
	for _, d := range ds {
		b.devices[d.DeviceID] = d
	}
	return b
}

func (b *fakeBook) Get(id string) (push.Device, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.devices[id]
	return d, ok
}
func (b *fakeBook) Remove(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.devices, id)
	b.removed = append(b.removed, id)
}
func (b *fakeBook) MarkSent(id string, at int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent[id] = at
}
func (b *fakeBook) MarkError(id, reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.errors[id] = reason
}

// fakeAPNS answers each Send from a script (the last answer repeats) and records what it was sent.
type fakeAPNS struct {
	mu          sync.Mutex
	script      []apns.Result
	calls       []sendCall
	invalidated int
	block       chan struct{} // when set, Send waits for it or for ctx
}

type sendCall struct {
	Env, Token string
	H          apns.Headers
	Payload    string
}

func (f *fakeAPNS) Send(ctx context.Context, env, token string, h apns.Headers, payload []byte) apns.Result {
	f.mu.Lock()
	f.calls = append(f.calls, sendCall{env, token, h, string(payload)})
	i := len(f.calls) - 1
	if i >= len(f.script) {
		i = len(f.script) - 1
	}
	res := f.script[i]
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return apns.Result{Class: apns.RetryLater, Err: "network error"}
		}
	}
	return res
}
func (f *fakeAPNS) Invalidate() { f.mu.Lock(); f.invalidated++; f.mu.Unlock() }
func (f *fakeAPNS) count() int  { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func device(token, locale, label string) push.Device {
	return push.Device{DeviceID: push.DeviceID(token), Token: token, BundleID: push.BundleID, Env: "sandbox", Platform: "ios",
		DeviceName: "iPhone", HostLabel: label, Locale: locale}
}

type slept struct {
	mu sync.Mutex
	ds []time.Duration
}

func (s *slept) sleep(ctx context.Context, d time.Duration) {
	s.mu.Lock()
	s.ds = append(s.ds, d)
	s.mu.Unlock()
}

func newTestSender(book deviceBook, a apnsClient, sl *slept) *sender {
	s := newSender(book, a, "host-1", push.BundleID)
	s.sleep = sl.sleep
	s.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	return s
}

func leadJob(ids ...string) Job {
	return Job{DeviceIDs: ids, Make: func(d push.Device) (push.Content, bool) {
		title := d.HostLabel + " lead"
		if d.Locale == "en" {
			title = d.HostLabel + " lead EN"
		}
		return push.Content{Title: title, Body: "b", Kind: "lead", ApprovalID: "ap1", CollapseID: "ap1"}, true
	}}
}

func runOne(t *testing.T, s *sender, j Job) {
	t.Helper()
	s.process(context.Background(), j)
}

func TestSender_OKRecordsLastSentAndSendsPerDeviceLocaleContent(t *testing.T) {
	zh, en := device(strings.Repeat("a1", 32), "zh-TW", "mlab"), device(strings.Repeat("b2", 32), "en", "mlab")
	book, a := newBook(zh, en), &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200, APNsID: "id1"}}}
	runOne(t, newTestSender(book, a, &slept{}), leadJob(zh.DeviceID, en.DeviceID))
	if a.count() != 2 || book.sent[zh.DeviceID] != 1_700_000_000_000 || book.sent[en.DeviceID] == 0 {
		t.Fatalf("calls %d sent %v", a.count(), book.sent)
	}
	if !strings.Contains(a.calls[0].Payload, `"title":"mlab lead"`) || !strings.Contains(a.calls[1].Payload, `"title":"mlab lead EN"`) {
		t.Fatalf("payloads = %s / %s", a.calls[0].Payload, a.calls[1].Payload)
	}
	c := a.calls[0]
	if c.Env != "sandbox" || c.Token != zh.Token || c.H.Topic != push.BundleID || c.H.CollapseID != "ap1" || c.H.Expiration.Unix() != 1_700_000_000+3600 {
		t.Fatalf("call = %+v", c)
	}
}

func TestSender_RemoveDeletesTheDevice(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	book, a := newBook(d), &fakeAPNS{script: []apns.Result{{Class: apns.Remove, Status: 410, Reason: "Unregistered"}}}
	runOne(t, newTestSender(book, a, &slept{}), leadJob(d.DeviceID))
	if len(book.removed) != 1 || book.removed[0] != d.DeviceID {
		t.Fatalf("removed = %v", book.removed)
	}
	if a.count() != 1 {
		t.Fatalf("a dead token is not retried (%d calls)", a.count())
	}
}

func TestSender_AnExpiredProviderTokenIsRenewedAndTriedOnce(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	book := newBook(d)
	a := &fakeAPNS{script: []apns.Result{{Class: apns.JWTRejected, Status: 403, Reason: "ExpiredProviderToken"}, {Class: apns.OK, Status: 200}}}
	runOne(t, newTestSender(book, a, &slept{}), leadJob(d.DeviceID))
	if a.invalidated != 1 || a.count() != 2 || book.sent[d.DeviceID] == 0 {
		t.Fatalf("invalidated %d calls %d sent %v", a.invalidated, a.count(), book.sent)
	}
	// rejected twice: one renewal, one retry, then the reason is recorded
	d2 := device(strings.Repeat("c3", 32), "en", "mlab")
	book2 := newBook(d2)
	a2 := &fakeAPNS{script: []apns.Result{{Class: apns.JWTRejected, Status: 403, Reason: "InvalidProviderToken"}}}
	runOne(t, newTestSender(book2, a2, &slept{}), leadJob(d2.DeviceID))
	if a2.count() != 2 || book2.errors[d2.DeviceID] != "InvalidProviderToken" {
		t.Fatalf("calls %d errors %v", a2.count(), book2.errors)
	}
}

func TestSender_RetryLaterWaitsTwoSecondsOnceThenGivesUp(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	book := newBook(d)
	a := &fakeAPNS{script: []apns.Result{{Class: apns.RetryLater, Status: 503, Reason: "ServiceUnavailable"}, {Class: apns.OK, Status: 200}}}
	sl := &slept{}
	runOne(t, newTestSender(book, a, sl), leadJob(d.DeviceID))
	if len(sl.ds) != 1 || sl.ds[0] != 2*time.Second || a.count() != 2 || book.sent[d.DeviceID] == 0 {
		t.Fatalf("sleeps %v calls %d sent %v", sl.ds, a.count(), book.sent)
	}
	book2, a2, sl2 := newBook(d), &fakeAPNS{script: []apns.Result{{Class: apns.RetryLater, Status: 0, Err: "network error"}}}, &slept{}
	runOne(t, newTestSender(book2, a2, sl2), leadJob(d.DeviceID))
	if a2.count() != 2 || book2.errors[d.DeviceID] != "network error" {
		t.Fatalf("calls %d errors %v", a2.count(), book2.errors)
	}
}

func TestSender_OtherRejectionsAreRecordedWithoutRetry(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	book, a := newBook(d), &fakeAPNS{script: []apns.Result{{Class: apns.Rejected, Status: 413, Reason: "PayloadTooLarge"}}}
	runOne(t, newTestSender(book, a, &slept{}), leadJob(d.DeviceID))
	if a.count() != 1 || book.errors[d.DeviceID] != "PayloadTooLarge" {
		t.Fatalf("calls %d errors %v", a.count(), book.errors)
	}
}

func TestSender_ADeviceThatIsGoneOrHasNoContentIsSkipped(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	book, a := newBook(d), &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}
	s := newTestSender(book, a, &slept{})
	runOne(t, s, leadJob("ffffffffffffffff", d.DeviceID)) // the first id is not registered (any more)
	if a.count() != 1 {
		t.Fatalf("calls = %d", a.count())
	}
	runOne(t, s, Job{DeviceIDs: []string{d.DeviceID}, Make: func(push.Device) (push.Content, bool) { return push.Content{}, false }})
	if a.count() != 1 {
		t.Fatalf("a job with no content sent something (%d calls)", a.count())
	}
}

// Enqueue never blocks: a full queue drops and counts.
func TestSender_AFullQueueDropsWithoutBlocking(t *testing.T) {
	s := newTestSender(newBook(), &fakeAPNS{script: []apns.Result{{Class: apns.OK}}}, &slept{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < queueCap+44; i++ {
			s.Enqueue(leadJob("x"))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked on a full queue")
	}
	if s.Dropped() != 44 {
		t.Fatalf("dropped = %d, want 44", s.Dropped())
	}
}

func TestSender_RunProcessesJobsAndStopReturnsEvenWithASendInFlight(t *testing.T) {
	d := device(strings.Repeat("a1", 32), "en", "mlab")
	book := newBook(d)
	a := &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}, block: make(chan struct{})}
	s := newTestSender(book, a, &slept{})
	s.Start(context.Background())
	s.Enqueue(leadJob(d.DeviceID))
	deadline := time.Now().Add(2 * time.Second)
	for a.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if a.count() == 0 {
		t.Fatal("the job was never picked up")
	}
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return with a send in flight")
	}
	// a worked example of the happy path through the goroutine
	book2, a2 := newBook(d), &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}
	s2 := newTestSender(book2, a2, &slept{})
	s2.Start(context.Background())
	s2.Enqueue(leadJob(d.DeviceID))
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		book2.mu.Lock()
		n := len(book2.sent)
		book2.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s2.Stop()
	if len(book2.sent) != 1 {
		t.Fatalf("sent = %v", book2.sent)
	}
}

// Logs carry the masked token, the reason and the apns-id; never the full token.
func TestSender_LogsNeverCarryTheFullToken(t *testing.T) {
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	defer log.SetOutput(os.Stderr)
	tok := strings.Repeat("a1", 32)
	d := device(tok, "en", "mlab")
	for _, res := range []apns.Result{
		{Class: apns.OK, Status: 200, APNsID: "APNS-ID-9"},
		{Class: apns.Remove, Status: 410, Reason: "Unregistered", APNsID: "APNS-ID-9"},
		{Class: apns.Rejected, Status: 400, Reason: "BadTopic"},
		{Class: apns.RetryLater, Err: "network error"},
	} {
		book := newBook(d)
		runOne(t, newTestSender(book, &fakeAPNS{script: []apns.Result{res}}, &slept{}), leadJob(d.DeviceID))
	}
	out := logs.String()
	if strings.Contains(out, tok) {
		t.Fatal("a log line carries the full device token")
	}
	if !strings.Contains(out, push.MaskToken(tok)) || !strings.Contains(out, "APNS-ID-9") || !strings.Contains(out, "Unregistered") {
		t.Fatalf("logs lack the masked token / apns-id / reason:\n%s", out)
	}
}
