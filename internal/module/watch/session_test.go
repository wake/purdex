package watchmod

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/watch"
	"github.com/wake/purdex/internal/watchsettings"
)

// fakeTyper stands in for the pdx-watch pane: it records what would be typed and, when it is "typed", can make the agent appear
// (the agent module's answer), which is how a start or a wake registers. Nothing here touches tmux.
type fakeTyper struct {
	mu        sync.Mutex
	lines     []string
	ensured   []string
	atShell   bool
	ensureErr error
	typeErr   error
	onType    func(line string)
}

func (f *fakeTyper) EnsureSession(_ context.Context, workdir string) (string, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, workdir)
	return "$3", "%7", false, f.ensureErr
}

func (f *fakeTyper) PaneAtShell(context.Context) (watch.PaneState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.atShell {
		return watch.PaneState{Command: "claude"}, nil
	}
	return watch.PaneState{Command: "zsh", AtShell: true}, nil
}

func (f *fakeTyper) TypeLine(_ context.Context, line string) error {
	f.mu.Lock()
	if f.typeErr != nil {
		f.mu.Unlock()
		return f.typeErr
	}
	f.lines = append(f.lines, line)
	cb := f.onType
	f.mu.Unlock()
	if cb != nil {
		cb(line)
	}
	return nil
}

func (f *fakeTyper) typed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

// sessionRig is a started module whose pane is the fake and whose agent appears when a line is typed.
type sessionRig struct {
	*recRig
	typer *fakeTyper
}

func newSessionRig(t *testing.T, registerAs string) *sessionRig {
	t.Helper()
	r := newRecRig(t)
	ft := &fakeTyper{atShell: true}
	if registerAs != "" {
		ft.onType = func(string) { r.owners.set(watcherOwner(registerAs), true, nil) }
	}
	r.m.typer = ft
	r.m.startGrace, r.m.registerWithin, r.m.pollEvery = 2*time.Second, 2*time.Second, 5*time.Millisecond
	r.m.statFile = func(string) bool { return true }
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	require.NotNil(t, r.m.session, "Start makes the session part")
	return &sessionRig{recRig: r, typer: ft}
}

func TestStart_TypesTheAgentCommandWithTheStartPromptAndSucceedsWhenAWatcherRegisters(t *testing.T) {
	r := newSessionRig(t, "f1")
	ans, err := r.m.session.Start(context.Background())
	require.NoError(t, err)
	require.Equal(t, watch.HolderAwake, ans.State)
	require.Equal(t, "sess-f1", ans.SessionID)
	require.False(t, ans.Starting)

	lines := r.typer.typed()
	require.Len(t, lines, 1)
	s := watchsettings.Default()
	require.True(t, strings.HasPrefix(lines[0], s.AgentCommand+" '"), "the agent command, then the prompt as one quoted word: %s", lines[0])
	require.NotContains(t, lines[0], "--resume")
	require.Equal(t, []string{s.Workdir}, r.typer.ensured)
	require.Nil(t, r.m.sess.Get().InProgress, "the mark is cleared")
}

func TestStart_ACustomStartPromptReplacesTheBuiltInOne(t *testing.T) {
	r := newSessionRig(t, "f1")
	r.m.settings = func() (watchsettings.Settings, error) {
		s := watchsettings.Default()
		s.StartPrompt = "do my thing"
		return s, nil
	}
	_, err := r.m.session.Start(context.Background())
	require.NoError(t, err)
	require.Contains(t, r.typer.typed()[0], "'do my thing'")
	require.NotContains(t, r.typer.typed()[0], watch.DefaultStartPrompt)
}

func TestStart_WithAWatcherAlreadyAwakeNothingIsTyped(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("up"), true, nil)
	ans, err := r.m.session.Start(context.Background())
	require.NoError(t, err)
	require.Equal(t, watch.HolderAwake, ans.State)
	require.Empty(t, r.typer.typed())
}

func TestStart_ABusyPaneIsReportedAndNothingIsTyped(t *testing.T) {
	r := newSessionRig(t, "f1")
	r.typer.atShell = false
	_, err := r.m.session.Start(context.Background())
	require.ErrorIs(t, err, watch.ErrPaneBusy)
	require.Empty(t, r.typer.typed())
	require.Nil(t, r.m.sess.Get().InProgress)
	require.True(t, r.m.sess.Get().CooldownUntil.IsZero(), "a busy pane is not a failed start: no cooldown")
}

func TestStart_ALineTheShellRefusedAtTheLastMomentIsAlsoPaneBusy(t *testing.T) {
	r := newSessionRig(t, "f1")
	r.typer.typeErr = watch.ErrPaneBusy
	_, err := r.m.session.Start(context.Background())
	require.ErrorIs(t, err, watch.ErrPaneBusy)
	require.True(t, r.m.sess.Get().CooldownUntil.IsZero())
}

func TestStart_ThatNeverRegistersFailsAndKeepsTheNextAwayForThirtyMinutes(t *testing.T) {
	r := newSessionRig(t, "") // the agent never appears
	r.m.registerWithin = 60 * time.Millisecond
	r.m.startGrace = 2 * time.Second
	_, err := r.m.session.Start(context.Background())
	require.Error(t, err)
	require.Nil(t, r.m.sess.Get().InProgress)
	until := r.m.sess.Get().CooldownUntil
	require.True(t, until.Sub(r.now) >= watch.StartCooldown-time.Second, "cooldown until %v", until)
	require.Len(t, r.typer.typed(), 1)

	_, err = r.m.session.Start(context.Background())
	var ce *watch.CooldownError
	require.ErrorAs(t, err, &ce)
	require.Len(t, r.typer.typed(), 1, "nothing is typed during the cooldown")

	r.now = r.now.Add(watch.StartCooldown + time.Minute)
	r.typer.onType = func(string) { r.owners.set(watcherOwner("late"), true, nil) }
	ans, err := r.m.session.Start(context.Background())
	require.NoError(t, err, "after the cooldown a start is tried again")
	require.Equal(t, "sess-late", ans.SessionID)
}

func TestStart_AnswersInProgressAfterItsGraceAndGoesOnInTheBackground(t *testing.T) {
	r := newSessionRig(t, "")
	r.m.startGrace = 30 * time.Millisecond
	r.m.registerWithin = 2 * time.Second
	ans, err := r.m.session.Start(context.Background())
	require.NoError(t, err)
	require.True(t, ans.Starting, "not registered within the grace: in progress")
	require.NotNil(t, r.m.sess.Get().InProgress)

	again, err := r.m.session.Start(context.Background())
	require.NoError(t, err)
	require.True(t, again.Starting, "a second ask does not start a second one")
	require.Len(t, r.typer.typed(), 1)

	r.owners.set(watcherOwner("later"), true, nil) // the agent comes up after the caller left
	require.Eventually(t, func() bool { return r.m.sess.Get().InProgress == nil }, 3*time.Second, 5*time.Millisecond)
	require.Equal(t, watch.HolderAwake, r.m.hs().Get().State)
	require.True(t, r.m.sess.Get().CooldownUntil.IsZero(), "it registered: no cooldown")
}

func TestStart_ADaemonThatRestartedMidStartDoesNotStartASecondOne(t *testing.T) {
	r := newSessionRig(t, "f1")
	require.NoError(t, r.m.sess.Update(func(st *watch.SessionState) {
		st.InProgress = &watch.InProgress{Kind: "start", Since: r.now.Add(-30 * time.Second)}
	}))
	ans, err := r.m.session.Start(context.Background())
	require.NoError(t, err)
	require.True(t, ans.Starting)
	require.Empty(t, r.typer.typed(), "the start that was under way is waited for, not repeated")

	// a mark older than the time a start has: that start failed
	require.NoError(t, r.m.sess.Update(func(st *watch.SessionState) {
		st.InProgress = &watch.InProgress{Kind: "start", Since: r.now.Add(-time.Hour)}
	}))
	_, err = r.m.session.Start(context.Background())
	var ce *watch.CooldownError
	require.ErrorAs(t, err, &ce, "a stale mark counts as a failed start: cooldown")
	require.Empty(t, r.typer.typed())
}

func TestStart_TwoAtOnceTypeOnce(t *testing.T) {
	r := newSessionRig(t, "f1")
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = r.m.session.Start(context.Background()) }()
	}
	wg.Wait()
	require.Len(t, r.typer.typed(), 1)
}

func restingRig(t *testing.T, registerAs string) *sessionRig {
	r := newSessionRig(t, registerAs)
	require.NoError(t, r.m.holder.Update(func(h *watch.Holder) {
		h.State, h.SessionID, h.TranscriptPath = watch.HolderResting, "11111111-2222-3333-4444-555555555555", "/t/kept.jsonl"
	}))
	return r
}

func TestWake_ResumesTheKeptSessionWithTheWakePromptAndSucceedsWhenThatSessionRegisters(t *testing.T) {
	r := restingRig(t, "")
	r.typer.onType = func(string) {
		o := watcherOwner("w")
		o.SessionID = "11111111-2222-3333-4444-555555555555"
		r.owners.set(o, true, nil)
	}
	ans, err := r.m.session.Wake(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, watch.HolderAwake, ans.State)
	require.Equal(t, "11111111-2222-3333-4444-555555555555", ans.SessionID)
	line := r.typer.typed()[0]
	require.Contains(t, line, " --resume 11111111-2222-3333-4444-555555555555")
	require.Contains(t, line, "'"+watch.DefaultWakePrompt+"'", "the wake prompt alone: no trailing space")
}

func TestWake_ThePromptIsAddedAfterTheWakePromptByASpace(t *testing.T) {
	for name, tc := range map[string]struct{ extra, want string }{
		"empty":     {"", "'" + watch.DefaultWakePrompt + "'"},
		"something": {"check the disk", "'" + watch.DefaultWakePrompt + " check the disk'"},
	} {
		t.Run(name, func(t *testing.T) {
			r := restingRig(t, "")
			r.typer.onType = func(string) {
				o := watcherOwner("w")
				o.SessionID = "11111111-2222-3333-4444-555555555555"
				r.owners.set(o, true, nil)
			}
			_, err := r.m.session.Wake(context.Background(), tc.extra)
			require.NoError(t, err)
			require.Contains(t, r.typer.typed()[0], tc.want)
		})
	}
}

func TestWake_ABadPromptIsRefusedBeforeAnythingIsTyped(t *testing.T) {
	r := restingRig(t, "")
	for name, extra := range map[string]string{"a newline": "a\nb", "a control": "x\x1b", "over 4 KB": strings.Repeat("a", 4100)} {
		_, err := r.m.session.Wake(context.Background(), extra)
		require.ErrorIs(t, err, watch.ErrBadPrompt, name)
	}
	require.Empty(t, r.typer.typed())
	require.Nil(t, r.m.sess.Get().InProgress)
}

func TestWake_WithTheTranscriptGoneOrNoKeptSessionItIsAStart(t *testing.T) {
	r := restingRig(t, "fresh")
	r.m.statFile = func(string) bool { return false }
	ans, err := r.m.session.Wake(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "sess-fresh", ans.SessionID)
	require.NotContains(t, r.typer.typed()[0], "--resume")

	r2 := newSessionRig(t, "fresh2") // nothing kept at all
	ans, err = r2.m.session.Wake(context.Background(), "")
	require.NoError(t, err)
	require.NotContains(t, r2.typer.typed()[0], "--resume")
	require.Equal(t, "sess-fresh2", ans.SessionID)
}

func TestWake_AnAwakeWatcherNeedsNothing(t *testing.T) {
	r := newSessionRig(t, "")
	r.owners.set(watcherOwner("up"), true, nil)
	ans, err := r.m.session.Wake(context.Background(), "x")
	require.NoError(t, err)
	require.Equal(t, watch.HolderAwake, ans.State)
	require.Empty(t, r.typer.typed())
}

func TestWake_ThatDoesNotComeBackIsFailedWithACooldown(t *testing.T) {
	r := restingRig(t, "")
	r.m.registerWithin = 60 * time.Millisecond
	_, err := r.m.session.Wake(context.Background(), "")
	require.Error(t, err)
	require.False(t, r.m.sess.Get().CooldownUntil.IsZero())
}

func TestSession_AStatFileThatIsMissingIsNotATranscript(t *testing.T) {
	dir := t.TempDir()
	require.True(t, fileExists(filepath.Join(dir)))
	require.False(t, fileExists(filepath.Join(dir, "nope.jsonl")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "t.jsonl"), []byte("x"), 0o600))
	require.True(t, fileExists(filepath.Join(dir, "t.jsonl")))
	require.False(t, fileExists(""))
	_ = errors.New
}

func TestStartAndWake_WithoutAPaneAreRefusedAsNoSession(t *testing.T) {
	r := newRecRig(t)
	require.NoError(t, r.m.Start(context.Background()))
	t.Cleanup(func() { _ = r.m.Stop(context.Background()) })
	r.m.typer = nil
	c := &sessionCtl{m: r.m}
	_, err := c.Start(context.Background())
	require.ErrorIs(t, err, watch.ErrNoSession)
	_, err = c.Wake(context.Background(), "")
	require.ErrorIs(t, err, watch.ErrNoSession)
}

func TestStart_AMarkLeftByARestartIsClearedWhenTheWatcherIsAwakeAndNoCooldownFollows(t *testing.T) {
	for name, since := range map[string]time.Duration{"within its time": 30 * time.Second, "past its time": time.Hour} {
		t.Run(name, func(t *testing.T) {
			r := newSessionRig(t, "")
			r.owners.set(watcherOwner("f1"), true, nil)
			require.NoError(t, r.m.sess.Update(func(st *watch.SessionState) {
				st.InProgress = &watch.InProgress{Kind: "start", Since: r.now.Add(-since)}
			}))
			ans, err := r.m.session.Start(context.Background())
			require.NoError(t, err)
			require.Equal(t, watch.HolderAwake, ans.State)
			require.False(t, ans.Starting)
			require.Nil(t, r.m.sess.Get().InProgress)
			require.True(t, r.m.sess.Get().CooldownUntil.IsZero())
			require.Empty(t, r.typer.typed())
		})
	}
}

func TestStart_ACallWaitingForTheGateLeavesWhenItsContextEnds(t *testing.T) {
	r := newSessionRig(t, "f1")
	c := r.m.session.(*sessionCtl)
	require.NoError(t, c.acquire(context.Background())) // a decision that is slow in Reconcile
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	began := time.Now()
	_, err := c.Start(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(began), time.Second)
	require.Empty(t, r.typer.typed())
	c.release()
	_, err = c.Start(context.Background())
	require.NoError(t, err, "the gate is free again")
}
