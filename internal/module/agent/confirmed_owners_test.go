package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/wake/purdex/internal/tmux"
)

func TestConfirmedOwners_ALivePaneRunningTheSession(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w/purdex")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	got, err := m.ConfirmedOwners(context.Background(), "SESS-1") // session ids compare case-insensitively
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err %v, want one owner", got, err)
	}
	if got[0].SessionID != "sess-1" || got[0].LastSeenAt != 42 || got[0].Status == "" {
		t.Fatalf("owner = %+v", got[0])
	}
}

// Two live roots in one pane (each passed identity and ancestry), whichever was seen last: each conversation is live.
// LastSeenAt orders candidates; it is not proof that the other root stopped running.
func TestConfirmedOwners_TwoLiveRootsInOnePaneAreBothLive(t *testing.T) {
	for _, seen := range [][2]int64{{42, 99}, {99, 42}} {
		m, fake, _ := newProvenanceQueryModule(t)
		fake.AddSession("work", "/w")
		attachPane(fake, "%5", "$0", "200")
		seedIdentityFrame(t, m, "%5", "cc", 100, "t100", seen[0], "sess-1", "/w")
		seedIdentityFrame(t, m, "%5", "cc", 101, "t101", seen[1], "sess-2", "/w")
		withProcessTree(t, map[int]int{100: 200, 101: 200, 200: 1})
		withLivePids(t, map[int]string{100: "t100", 101: "t101"})

		for _, id := range []string{"sess-1", "sess-2"} {
			if got, err := m.ConfirmedOwners(context.Background(), id); err != nil || len(got) != 1 {
				t.Fatalf("last seen %v, %s: got %+v err %v, want one live owner", seen, id, got, err)
			}
		}
	}
}

func TestConfirmedOwners_NoFrameOrADeadProcessIsNobodyAndNotAnError(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	if got, err := m.ConfirmedOwners(context.Background(), "sess-1"); err != nil || got != nil {
		t.Fatalf("no frame: got %+v err %v", got, err)
	}
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{}) // the recorded process is gone
	if got, err := m.ConfirmedOwners(context.Background(), "sess-1"); err != nil || got != nil {
		t.Fatalf("dead process: got %+v err %v", got, err)
	}
	if got, err := m.ConfirmedOwners(context.Background(), ""); err != nil || got != nil {
		t.Fatalf("empty id: got %+v err %v", got, err)
	}
}

// A walk that cannot be completed is "could not tell" (an error), never "nobody".
func TestConfirmedOwners_AFailedWalkIsAnError(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	orig := provenanceTimeout
	provenanceTimeout = -1
	t.Cleanup(func() { provenanceTimeout = orig })
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	got, err := m.ConfirmedOwners(context.Background(), "sess-1")
	if err == nil || len(got) != 0 {
		t.Fatalf("got %+v err %v, want an error and no owner", got, err)
	}
}

func TestConfirmedOwners_CancelledContext(t *testing.T) {
	m, _, _ := newProvenanceQueryModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ConfirmedOwners(ctx, "sess-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// failingListExecutor fails the pane listing from the failFrom-th call on.
type failingListExecutor struct {
	*tmux.FakeExecutor
	failFrom int
	calls    int
}

func (e *failingListExecutor) ListAllPanes(ctx context.Context) ([]tmux.PaneLocation, error) {
	e.calls++
	if e.calls >= e.failFrom {
		return nil, errors.New("tmux went away")
	}
	return e.FakeExecutor.ListAllPanes(ctx)
}

// The candidate lookup worked, the owner pass could not finish: an error from the pass (not folded into "nobody").
func TestConfirmedOwners_AFailedOwnerPassIsAnError(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	m.tmux = &failingListExecutor{FakeExecutor: fake, failFrom: 2}
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	got, err := m.ConfirmedOwners(context.Background(), "sess-1")
	if err == nil || len(got) != 0 {
		t.Fatalf("got %+v err %v, want the pass's error and no owner", got, err)
	}
}

// No live candidate: tmux is not asked at all (a failing tmux would otherwise turn "nobody" into an error).
func TestConfirmedOwners_NoLiveCandidateNeverAsksTmux(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	m.tmux = &failingListExecutor{FakeExecutor: fake, failFrom: 1}
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{}) // the recorded process is gone

	if got, err := m.ConfirmedOwners(context.Background(), "sess-1"); err != nil || got != nil {
		t.Fatalf("got %+v err %v, want nobody and no error", got, err)
	}
}

// Two panes of one tmux session run different conversations, the other one newer: the session-wide winner is the
// other conversation, but this conversation's own pane is still live.
func TestConfirmedOwners_ASiblingPaneWithANewerConversationDoesNotHideIt(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	attachPane(fake, "%6", "$0", "300")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	seedIdentityFrame(t, m, "%6", "cc", 101, "t101", 99, "sess-2", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1, 101: 300, 300: 1})
	withLivePids(t, map[int]string{100: "t100", 101: "t101"})

	got, err := m.ConfirmedOwners(context.Background(), "sess-1")
	if err != nil || len(got) != 1 || got[0].TmuxPaneID != "%5" {
		t.Fatalf("got %+v err %v, want the owner in %%5", got, err)
	}
	got, err = m.ConfirmedOwners(context.Background(), "sess-2")
	if err != nil || len(got) != 1 || got[0].TmuxPaneID != "%6" {
		t.Fatalf("got %+v err %v, want the owner in %%6", got, err)
	}
}

// countingListExecutor counts the pane listings.
type countingListExecutor struct {
	*tmux.FakeExecutor
	calls int
}

func (e *countingListExecutor) ListAllPanes(ctx context.Context) ([]tmux.PaneLocation, error) {
	e.calls++
	return e.FakeExecutor.ListAllPanes(ctx)
}

// The whole query is the pass's two listings (placing and confirming), not a third one of its own.
func TestConfirmedOwners_TwoPaneListingsAtMost(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	exec := &countingListExecutor{FakeExecutor: fake}
	m.tmux = exec
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	if got, err := m.ConfirmedOwners(context.Background(), "sess-1"); err != nil || len(got) != 1 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if exec.calls != 2 {
		t.Fatalf("%d pane listings, want 2", exec.calls)
	}
}

// cancellingListExecutor cancels the request as its (successful) listing returns.
type cancellingListExecutor struct {
	*tmux.FakeExecutor
	cancel context.CancelFunc
}

func (e *cancellingListExecutor) ListAllPanes(ctx context.Context) ([]tmux.PaneLocation, error) {
	rows, err := e.FakeExecutor.ListAllPanes(ctx)
	e.cancel()
	return rows, err
}

// The deadline passes just as the first listing succeeds: that is "could not tell", not "nobody".
func TestConfirmedOwners_DeadlineAtTheFirstListingIsAnError(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.tmux = &cancellingListExecutor{FakeExecutor: fake, cancel: cancel}
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	// the frame's pane is not in the listing (it just went away): without the deadline check that reads as "nobody"
	seedIdentityFrame(t, m, "%9", "cc", 100, "t100", 42, "sess-1", "/w")
	withProcessTree(t, map[int]int{100: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100"})

	if got, err := m.ConfirmedOwners(ctx, "sess-1"); err == nil {
		t.Fatalf("got %+v with no error, want the cancellation", got)
	}
}
