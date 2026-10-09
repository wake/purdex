package push

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wake/purdex/internal/push"
	"github.com/wake/purdex/internal/push/apns"
	"github.com/wake/purdex/internal/team"
)

// fakeEvents is the team module's approval feed as the push module sees it.
type fakeEvents struct {
	mu          sync.Mutex
	fn          func(op string, a team.Approval)
	open        []team.Approval
	subscribed  int
	unsubscribe int
}

func (f *fakeEvents) SubscribeApprovals(fn func(string, team.Approval)) ([]team.Approval, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fn = fn
	f.subscribed++
	return f.open, func() { f.mu.Lock(); f.unsubscribe++; f.fn = nil; f.mu.Unlock() }
}

func (f *fakeEvents) emit(op string, a team.Approval) {
	f.mu.Lock()
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		fn(op, a)
	}
}

type presenceFake struct{ shows map[string]bool }

func (p presenceFake) ShowsName(name string) bool { return p.shows[name] }
func (p presenceFake) ShowsCode(code string) bool { return p.shows[code] }

func leadApproval(id string) team.Approval {
	raw, _ := json.Marshal(map[string]any{"reason": "split the work"})
	return team.Approval{ID: id, Kind: team.KindLead, Payload: raw,
		Origin: team.Origin{SessionID: "sid-1", Ref: "_abc123", Name: "worker-1", Tmux: "dev:@1.%2"}}
}

// triggerEnv is a ready module with a fake APNs and a fake approval feed, started.
type triggerEnv struct {
	*env
	apns   *fakeAPNS
	events *fakeEvents
}

func newTriggerEnv(t *testing.T, presence presenceChecker) *triggerEnv {
	t.Helper()
	e := newEnv(t)
	te := &triggerEnv{env: e, apns: &fakeAPNS{script: []apns.Result{{Class: apns.OK, Status: 200}}}, events: &fakeEvents{}}
	e.mod.events = te.events
	e.mod.newAPNs = func() apnsClient { return te.apns }
	if presence != nil {
		e.mod.presence = presence
	}
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.mod.Stop(context.Background()) })
	return te
}

func (te *triggerEnv) register(token, locale, label string) {
	te.do("POST", "/api/push/devices", reqBody(token, func(r *push.DeviceRequest) { r.Locale = locale; r.HostLabel = label }))
}

func (te *triggerEnv) waitSends(t *testing.T, n int) []sendCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if te.apns.count() >= n {
			return append([]sendCall(nil), te.apns.calls...)
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("sends = %d, want %d", te.apns.count(), n)
	return nil
}

func (te *triggerEnv) noSends(t *testing.T) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	if n := te.apns.count(); n != 0 {
		t.Fatalf("sends = %d, want none", n)
	}
}

func TestTrigger_AnOpenedLeadRequestGoesToEveryDeviceInItsLocale(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "zh-TW", "mlab")
	te.register(tokB, "en", "mlab-en")
	te.events.emit("opened", leadApproval("ap1"))
	calls := te.waitSends(t, 2)
	byToken := map[string]sendCall{calls[0].Token: calls[0], calls[1].Token: calls[1]}
	if !strings.Contains(byToken[tokA].Payload, "mlab：worker-1 申請成為 lead") || !strings.Contains(byToken[tokB].Payload, "mlab-en: worker-1 requests to become lead") {
		t.Fatalf("payloads = %s / %s", byToken[tokA].Payload, byToken[tokB].Payload)
	}
	if c := byToken[tokA]; c.H.CollapseID != "ap1" || c.H.Topic != push.BundleID || !strings.Contains(c.Payload, `"approval_id":"ap1"`) {
		t.Fatalf("call = %+v", c)
	}
}

func TestTrigger_OnlyOpenedOfTheThreeKindsIsPushed(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.events.emit("closed", leadApproval("ap1")) // R7: no push when an approval closes
	perm := leadApproval("ap2")
	perm.Kind = team.KindHookPermission
	te.events.emit("opened", perm)
	adopt := leadApproval("ap3")
	adopt.Kind = team.KindAdopt
	te.events.emit("opened", adopt)
	terminal := leadApproval("ap4")
	terminal.Kind = team.KindHookAsk
	terminal.Payload = json.RawMessage(`{"terminal_only":true,"questions":[{"question":"q"}]}`)
	te.events.emit("opened", terminal)
	te.noSends(t)
	ask := leadApproval("ap5")
	ask.Kind = team.KindHookAsk
	ask.Payload = json.RawMessage(`{"questions":[{"question":"red or blue?"}]}`)
	te.events.emit("opened", ask)
	calls := te.waitSends(t, 1)
	if !strings.Contains(calls[0].Payload, "red or blue?") || !strings.Contains(calls[0].Payload, `"kind":"hook_ask"`) {
		t.Fatalf("payload = %s", calls[0].Payload)
	}
}

func TestTrigger_NoDevicesNoPush(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.events.emit("opened", leadApproval("ap1"))
	te.noSends(t)
}

// R6: no push for a session a present Mac shows, matched by the tmux session name of the origin; an origin with no tmux
// is never suppressed.
func TestTrigger_ASessionAPresentMacShowsIsNotPushed(t *testing.T) {
	te := newTriggerEnv(t, presenceFake{shows: map[string]bool{"dev": true}})
	te.register(tokA, "en", "mlab")
	te.events.emit("opened", leadApproval("ap1")) // origin.tmux "dev:@1.%2" -> "dev"
	te.noSends(t)
	other := leadApproval("ap2")
	other.Origin.Tmux = "elsewhere:@3.%4"
	te.events.emit("opened", other)
	te.waitSends(t, 1)
	noTmux := leadApproval("ap3")
	noTmux.Origin.Tmux = ""
	te.events.emit("opened", noTmux)
	te.waitSends(t, 2)
}

func TestTmuxSessionOf(t *testing.T) {
	for in, want := range map[string]string{"dev:@1.%2": "dev", "a:b:c": "a", "": "", "noColon": "noColon"} {
		if got := tmuxSessionOf(in); got != want {
			t.Fatalf("tmuxSessionOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrigger_StopUnsubscribesAndEndsTheSender(t *testing.T) {
	te := newTriggerEnv(t, nil)
	te.register(tokA, "en", "mlab")
	te.mod.Stop(context.Background())
	if te.events.unsubscribe != 1 {
		t.Fatalf("unsubscribed %d times", te.events.unsubscribe)
	}
	te.events.emit("opened", leadApproval("ap1")) // nobody listens any more
	te.noSends(t)
}

func TestTrigger_ANotReadyModuleSubscribesToNothing(t *testing.T) {
	m := New()
	ev := &fakeEvents{}
	m.events = ev
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ev.subscribed != 0 {
		t.Fatal("a module that is off must not subscribe")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModule_DependsOnTeam(t *testing.T) {
	var found bool
	for _, d := range New().Dependencies() {
		found = found || d == "team"
	}
	if !found {
		t.Fatal("push must be started after team")
	}
}

// The device book the sender uses writes the store first and the cache after; a removed device is gone from both.
func TestBook_RemoveAndMarkTouchStoreAndCache(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/api/push/devices", reqBody(tokA, nil))
	id := push.DeviceID(tokA)
	e.mod.MarkSent(id, 4242)
	e.mod.MarkError(id, "boom")
	d, ok := e.mod.Get(id)
	if !ok || d.LastSentAt != 4242 || d.LastError != "boom" {
		t.Fatalf("cache = %+v", d)
	}
	stored := mustOne(t, e.mod.store)
	if stored.LastSentAt != 4242 || stored.LastError != "boom" {
		t.Fatalf("store = %+v", stored)
	}
	e.mod.Remove(id)
	if _, ok := e.mod.Get(id); ok {
		t.Fatal("still in the cache")
	}
	if list, _ := e.mod.store.List(); len(list) != 0 {
		t.Fatal("still in the store")
	}
}

// End to end through the real APNs client against a fake HTTP/2 APNs: the request on the wire, last_sent_at recorded, and
// a 410 removing the device.
func TestTrigger_EndToEndAgainstAFakeAPNs(t *testing.T) {
	var mu sync.Mutex
	var got []string
	status := 200
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path+" h"+strconv.Itoa(r.ProtoMajor)+" "+r.Header.Get("Apns-Topic")+" "+r.Header.Get("Apns-Collapse-Id")+" "+string(b))
		st := status
		mu.Unlock()
		w.Header().Set("apns-id", "ID-E2E")
		w.WriteHeader(st)
		if st == 410 {
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	e := newEnv(t)
	e.mod.events = &fakeEvents{}
	e.mod.newAPNs = func() apnsClient {
		return &apns.Client{HTTP: srv.Client(), Hosts: map[string]string{"sandbox": srv.URL}, Signer: apns.NewSigner(e.mod.key, nil)}
	}
	if err := e.mod.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.mod.Stop(context.Background())
	e.do("POST", "/api/push/devices", reqBody(tokA, nil))
	id := push.DeviceID(tokA)

	e.mod.onApproval("opened", leadApproval("ap-e2e"))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if d, _ := e.mod.Get(id); d.LastSentAt != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	d, _ := e.mod.Get(id)
	mu.Lock()
	first := append([]string(nil), got...)
	status = 410
	mu.Unlock()
	if d.LastSentAt == 0 || len(first) != 1 || !strings.HasPrefix(first[0], "POST /3/device/"+tokA+" h2 "+push.BundleID+" ap-e2e ") || !strings.Contains(first[0], `"approval_id":"ap-e2e"`) {
		t.Fatalf("last_sent_at %d, wire %v", d.LastSentAt, first)
	}

	e.mod.onApproval("opened", leadApproval("ap-gone"))
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.mod.Get(id); !ok {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, ok := e.mod.Get(id); ok {
		t.Fatal("a 410 must remove the device")
	}
	if list, _ := e.mod.store.List(); len(list) != 0 {
		t.Fatal("the device is still in the store")
	}
}

// Stop can overlap an approval callback that is already running (unsubscribing does not wait for it): the callback must
// not trip over the sender going away. Run with -race.
func TestTrigger_StopRacingAnApprovalCallbackIsSafe(t *testing.T) {
	for i := 0; i < 50; i++ {
		te := newTriggerEnv(t, nil)
		te.register(tokA, "en", "mlab")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				te.mod.onApproval("opened", leadApproval("ap-race"))
			}
		}()
		go func() {
			defer wg.Done()
			te.mod.Stop(context.Background())
		}()
		wg.Wait()
	}
}
