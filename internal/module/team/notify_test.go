package teammod

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	peersmod "github.com/wake/purdex/internal/module/peers"
	ipeers "github.com/wake/purdex/internal/peers"
	"github.com/wake/purdex/internal/team"
)

// The notice outbox (adopt plan PL-1d1). Tests drive drainNotices directly unless they start the module.

// fakeSender records every send; err (set before the send) makes it fail, block makes it wait.
type fakeSender struct {
	mu   sync.Mutex
	sent []ipeers.SendRequest
	err  error
	// result is what a successful send reports ("" → delivered).
	result string
	block  chan struct{}
	in     chan struct{}
}

func (s *fakeSender) Send(ctx context.Context, req ipeers.SendRequest) (ipeers.SendResponse, error) {
	s.mu.Lock()
	err, block, in, result := s.err, s.block, s.in, s.result
	s.mu.Unlock()
	if result == "" {
		result = ipeers.ResultDelivered
	}
	if in != nil {
		in <- struct{}{}
	}
	if block != nil {
		<-block
	}
	if err != nil {
		return ipeers.SendResponse{}, err
	}
	s.mu.Lock()
	s.sent = append(s.sent, req)
	s.mu.Unlock()
	return ipeers.SendResponse{Result: result}, nil
}

func (s *fakeSender) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *fakeSender) calls() []ipeers.SendRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ipeers.SendRequest(nil), s.sent...)
}

// adoptedMember makes sid-1 lead a team and sid-2 an adopted member of it (the adopt notice owed); returns the key.
func (f *fixture) adoptedMember(t *testing.T) string {
	t.Helper()
	f.approveLead(uid(1))
	a := f.adoptOK(uid(10), "_def456")
	if code, body := f.decide(a.ID, "approve"); code != http.StatusOK {
		t.Fatalf("approve adopt: %d %s", code, body)
	}
	return a.ID
}

func (f *fixture) noticeOf(t *testing.T, key string) (string, int64) {
	t.Helper()
	m := memberBySpawn(t, f.m.store, key)
	return m.NoticePending, m.NoticeSince
}

func TestNotice_AdoptFromTheLeadsInbox(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.m.drainNotices()
	calls := f.sender.calls()
	if len(calls) != 1 {
		t.Fatalf("sends = %+v, want one", calls)
	}
	alias, _ := f.m.selfHost()
	want := fmt.Sprintf(team.AdoptNoticeFmt, alias+"/_abc123", uid(1), alias+"/_abc123")
	if c := calls[0]; c.OriginInbox != "/tmp/10.sock" || c.To != alias+"/_def456" || c.Text != want {
		t.Fatalf("send = %+v, want from the lead's inbox to %s/_def456 with %q", c, alias, want)
	}
	if kind, _ := f.noticeOf(t, key); kind != "" {
		t.Fatalf("notice_pending = %q after the send, want cleared", kind)
	}
	f.m.drainNotices()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("a cleared notice was sent again (%d sends)", n)
	}
}

// Decision 2's paths plus the tick: each adopt approval kicks the drain (afterApproved) and is told exactly once.
// Mutation gate: skip kickNotices in afterApproved → the kick assertion is red on every path.
func TestNotice_EveryApprovePathSends(t *testing.T) {
	paths := map[string]func(f *fixture){
		"click":  func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.decide(uid(10), "approve") },
		"create": func(f *fixture) { f.unatt.set(true); f.adoptOK(uid(10), "_def456") },
		"sweep":  func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.unatt.set(true); f.sweep() },
		"tick":   func(f *fixture) { f.adoptOK(uid(10), "_def456"); f.unatt.set(true); f.m.tick() },
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.approveLead(uid(1))
			select { // empty the slot approveLead's own tick might have filled
			case <-f.m.noticeSig:
			default:
			}
			run(f)
			select {
			case <-f.m.noticeSig:
			default:
				t.Fatal("the approval did not kick the notice drain")
			}
			f.m.drainNotices()
			if n := len(f.sender.calls()); n != 1 {
				t.Fatalf("sends = %d, want 1", n)
			}
		})
	}
	t.Run("boot", func(t *testing.T) {
		f := newFixture(t)
		f.approveLead(uid(1))
		f.adoptOK(uid(10), "_def456")
		f.unatt.set(true)
		if err := f.m.Start(context.Background()); err != nil { // the boot sweep approves; the drain goroutine sends
			t.Fatal(err)
		}
		waitFor(t, func() bool { return len(f.sender.calls()) == 1 })
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

// Mutation gate: clear the notice before the send → the failed send loses it → red.
func TestNotice_FailedSendIsRetriedByTheSweeper(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.sender.setErr(&peersmod.SendError{Status: http.StatusServiceUnavailable, API: ipeers.APIError{Error: ipeers.ErrNotReady}})
	f.m.drainNotices()
	if kind, _ := f.noticeOf(t, key); kind != team.NoticeAdopted {
		t.Fatalf("notice_pending = %q after a failed send, want kept", kind)
	}
	f.sender.setErr(nil)
	for range livenessEvery { // one liveness tick kicks the drain
		f.m.tick()
	}
	select {
	case <-f.m.noticeSig:
	default:
		t.Fatal("the liveness tick did not kick the drain")
	}
	f.m.drainNotices()
	if n := len(f.sender.calls()); n != 1 {
		t.Fatalf("sends = %d, want the retry's one", n)
	}
	if kind, _ := f.noticeOf(t, key); kind != "" {
		t.Fatalf("notice_pending = %q after the retry, want cleared", kind)
	}
}

// Mutation gate: never give up → the stale notice is sent → red.
func TestNotice_GivenUpAfterTenMinutes(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	logs := f.logs()
	f.clock.Add(int64(team.NoticeGiveUpS)*1000 + 1)
	f.m.drainNotices()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("a notice past ten minutes was sent (%d sends)", n)
	}
	if kind, _ := f.noticeOf(t, key); kind != "" {
		t.Fatalf("notice_pending = %q, want cleared when given up", kind)
	}
	if countLines(logs(), "given up after 10 min") != 1 {
		t.Fatalf("logs = %q, want one give-up line", logs())
	}
}

// Mutation gate: send without OriginInbox → red.
func TestNotice_NeedsTheLeadsInbox(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.origins.hide("sid-1") // the lead is not in the registry: no inbox, so it cannot be the sender
	f.m.drainNotices()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("sent without a lead inbox (%d sends)", n)
	}
	if kind, _ := f.noticeOf(t, key); kind != team.NoticeAdopted {
		t.Fatalf("notice_pending = %q, want kept for the retry", kind)
	}
}

func TestNotice_CrashBetweenCommitAndSendIsResent(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)      // committed; the drain never ran
	g := f.reboot(f.titles) // a second Module over the same team.db, started
	waitFor(t, func() bool { return len(g.sender.calls()) == 1 })
}

// The release before the adopt notice was sent replaces it: the session is no member any more, and is told so.
func TestNotice_ReleaseReplacesAnUnsentAdoptNotice(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	if ok, err := f.m.store.ReleaseMember(key, "sid-2", f.clock.Load()+1); err != nil || !ok {
		t.Fatalf("release: %v %v", ok, err)
	}
	f.m.drainNotices()
	calls := f.sender.calls()
	if len(calls) != 1 {
		t.Fatalf("sends = %+v, want only the release notice", calls)
	}
	alias, _ := f.m.selfHost()
	if want := fmt.Sprintf(team.ReleaseNoticeFmt, alias+"/_abc123", uid(1)); calls[0].Text != want {
		t.Fatalf("text = %q, want %q", calls[0].Text, want)
	}
}

// A blocked sender does not block a create: nothing in the drain holds createMu.
func TestNotice_NeverUnderCreateMu(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	f.sender.mu.Lock()
	f.sender.block, f.sender.in = make(chan struct{}), make(chan struct{}, 1)
	in, block := f.sender.in, f.sender.block
	f.sender.mu.Unlock()
	go f.m.drainNotices()
	<-in // the drain is inside Send
	done := make(chan struct{})
	go func() {
		f.m.createMu.Lock()
		f.m.createMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("createMu is held across a send")
	}
	close(block)
}

func TestStop_JoinsTheDrain(t *testing.T) {
	f := newFixture(t)
	f.adoptedMember(t)
	f.sender.mu.Lock()
	f.sender.block, f.sender.in = make(chan struct{}), make(chan struct{}, 1)
	in, block := f.sender.in, f.sender.block
	f.sender.mu.Unlock()
	if err := f.m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-in
	stopped := make(chan struct{})
	go func() { _ = f.m.Stop(context.Background()); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while the drain was inside a send")
	case <-time.After(100 * time.Millisecond):
	}
	close(block)
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return after the send finished")
	}
}

// delivery_uncertain is not a delivery: the notice stays owed and is sent again (at least once).
// Mutation gate: clear on any 2xx → red.
func TestNotice_UncertainDeliveryStaysOwed(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	f.sender.mu.Lock()
	f.sender.result = ipeers.ResultDeliveryUncertain
	f.sender.mu.Unlock()
	f.m.drainNotices()
	if kind, _ := f.noticeOf(t, key); kind != team.NoticeAdopted {
		t.Fatalf("notice_pending = %q after an uncertain delivery, want kept", kind)
	}
	f.sender.mu.Lock()
	f.sender.result = ""
	f.sender.mu.Unlock()
	f.m.drainNotices()
	if kind, _ := f.noticeOf(t, key); kind != "" {
		t.Fatalf("notice_pending = %q after a confirmed delivery, want cleared", kind)
	}
}

// A member that left (gone) before it was told owes no adopt notice, and the row does not keep it forever.
func TestNotice_AdoptNoticeOfAMemberThatLeftIsDropped(t *testing.T) {
	f := newFixture(t)
	key := f.adoptedMember(t)
	if _, err := f.m.store.db.Exec(`UPDATE team_members SET state = 'gone' WHERE spawn_op = ?`, key); err != nil {
		t.Fatal(err)
	}
	f.m.drainNotices()
	if n := len(f.sender.calls()); n != 0 {
		t.Fatalf("sent %d notice(s) to a member that left", n)
	}
	if kind, _ := f.noticeOf(t, key); kind != "" {
		t.Fatalf("notice_pending = %q, want dropped", kind)
	}
}
