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

// The tmux session's owner is another conversation (the pane's newer frame): this one is not live.
func TestConfirmedOwners_TheSessionsOwnerIsAnotherConversation(t *testing.T) {
	m, fake, _ := newProvenanceQueryModule(t)
	fake.AddSession("work", "/w")
	attachPane(fake, "%5", "$0", "200")
	seedIdentityFrame(t, m, "%5", "cc", 100, "t100", 42, "sess-1", "/w")
	seedIdentityFrame(t, m, "%5", "cc", 101, "t101", 99, "sess-2", "/w")
	withProcessTree(t, map[int]int{100: 200, 101: 200, 200: 1})
	withLivePids(t, map[int]string{100: "t100", 101: "t101"})

	if got, err := m.ConfirmedOwners(context.Background(), "sess-1"); err != nil || len(got) != 0 {
		t.Fatalf("sess-1: got %+v err %v, want none", got, err)
	}
	if got, err := m.ConfirmedOwners(context.Background(), "sess-2"); err != nil || len(got) != 1 {
		t.Fatalf("sess-2: got %+v err %v, want one", got, err)
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
