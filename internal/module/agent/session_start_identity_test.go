package agent

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/wake/purdex/internal/store"
)

func withIdentityWrite(t *testing.T, fn func(s *store.FramesStore, frameID, sessionID, cwd string, seq int64) error) {
	t.Helper()
	old := updateSessionIdentityFn
	updateSessionIdentityFn = fn
	t.Cleanup(func() { updateSessionIdentityFn = old })
}

// A failed identity write must not publish: a Q1 subscriber's re-check would
// find no frame holding the session id and skip, and the event is never
// replayed. The envelope itself is unaffected.
func TestSessionStartSubscription_NoEventWhenIdentityWriteFails(t *testing.T) {
	m := newSessionStartTestModule(t)
	got := make(chan SessionStartEvent, 4)
	defer m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })()
	withIdentityWrite(t, func(*store.FramesStore, string, string, string, int64) error {
		return errors.New("disk I/O error")
	})

	rec := postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"startup"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("hook answered %d", rec.Code)
	}
	select {
	case ev := <-got:
		t.Fatalf("published despite failed identity write: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSessionStartIdentityFailure_EnvelopeStillAttached(t *testing.T) {
	m := newSessionStartTestModule(t)
	withIdentityWrite(t, func(*store.FramesStore, string, string, string, int64) error {
		return errors.New("disk I/O error")
	})
	req := EventRequest{
		TmuxPaneID: "%5", AgentType: "cc", SenderPID: 200,
		SenderStartTime: "t200", PurdexName: "PdxSessionStart",
		RawEvent: []byte(`{"session_id":"S1","cwd":"/w/p","source":"startup"}`),
	}
	withProcessTree(t, map[int]int{200: 999})
	ev := m.buildNormalizedForTest(t, req)
	if _, ok := ev.Detail["pdx_provenance"]; !ok {
		t.Fatalf("envelope must stay attached: detail=%+v", ev.Detail)
	}
}

func TestSessionStartSubscription_EventWhenIdentityOutOfOrder(t *testing.T) {
	m := newSessionStartTestModule(t)
	got := make(chan SessionStartEvent, 4)
	defer m.SubscribeSessionStart(func(ev SessionStartEvent) { got <- ev })()
	withIdentityWrite(t, func(*store.FramesStore, string, string, string, int64) error {
		return store.ErrIdentityOutOfOrder
	})

	postRootSessionStart(t, m, `{"session_id":"S","cwd":"/w","source":"startup"}`)
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("out-of-order write must still publish")
	}
}
