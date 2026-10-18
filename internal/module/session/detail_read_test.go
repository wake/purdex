package session

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/tmux"
)

// detailExec is a fake executor that can also list the sessions with their panes in one read (tmux.DetailLister).
type detailExec struct {
	*tmux.FakeExecutor
	calls   int
	details []tmux.SessionDetail
	trusted bool
	err     error
}

func (d *detailExec) ListSessionsDetailed(context.Context) ([]tmux.SessionDetail, bool, error) {
	d.calls++
	return d.details, d.trusted, d.err
}

func detailFor(id, name, cwd, title, win, cmd, instance string) tmux.SessionDetail {
	return tmux.SessionDetail{
		TmuxSession: tmux.TmuxSession{ID: id, Name: name, Cwd: cwd},
		Pane:        tmux.TmuxPaneMetadata{SessionID: id, SessionName: name, PaneTitle: title, WindowName: win, PaneCurrentCommand: cmd},
		Instance:    instance,
	}
}

// countReads counts the old-way reads of the fake: a list, and a pane read per session.
func countReads(fake *tmux.FakeExecutor) (lists, panes *int) {
	lists, panes = new(int), new(int)
	fake.SetReadHook(func(_ context.Context, op tmux.ReadOp, _ string) error {
		switch op {
		case tmux.ReadListSessions:
			*lists++
		case tmux.ReadPaneMetadata:
			*panes++
		}
		return nil
	})
	return lists, panes
}

// #3316: with an executor that lists the panes too, a session list is ONE tmux read: no list-sessions of its own, no instance probe,
// no display-message per session; the fields are the ones the single answer carried and the instance is the answer's.
func TestListSessions_OneReadWhenTheExecutorCanListThePanes(t *testing.T) {
	mod, _, fake := newTestModule(t)
	de := &detailExec{FakeExecutor: fake, trusted: true, details: []tmux.SessionDetail{
		detailFor("$1", "alpha", "/a", "title a", "win a", "vim", "42:1000"),
		detailFor("$2", "beta", "/b", "title b", "win b", "node", "42:1000"),
	}}
	mod.tmux = de
	lists, panes := countReads(fake)
	probes := 0
	mod.tmuxInstanceFn = func(context.Context) string { probes++; return "probed" }

	got, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, 1, de.calls)
	assert.Zero(t, *lists, "no list-sessions of its own")
	assert.Zero(t, *panes, "no display-message per session")
	assert.Zero(t, probes, "no instance probe")
	assert.Equal(t, "alpha", got[0].Name)
	assert.Equal(t, "/a", got[0].Cwd)
	assert.Equal(t, "title a", got[0].PaneTitle)
	assert.Equal(t, "win a", got[0].WindowName)
	assert.Equal(t, "vim", got[0].CurrentCommand)
	assert.Equal(t, "42:1000", got[0].TmuxInstance)
	assert.Equal(t, "node", got[1].CurrentCommand)
	assert.Equal(t, "42:1000", got[1].TmuxInstance)
}

// An answer that could not be trusted is read the old way: the list, the instance, a pane read per session — and the result is the same.
func TestListSessions_UntrustedDetailedAnswerFallsBackToTheOldReads(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSessionWithID("$1", "alpha", "/a")
	fake.SetActivePaneMetadata("alpha", tmux.TmuxPaneMetadata{PaneTitle: "old title", WindowName: "old win", PaneCurrentCommand: "zsh"})
	de := &detailExec{FakeExecutor: fake, trusted: false, details: []tmux.SessionDetail{detailFor("$9", "wrong", "/w", "x", "y", "z", "0:0")}}
	mod.tmux = de
	lists, panes := countReads(fake)
	mod.tmuxInstanceFn = func(context.Context) string { return "7:7" }

	got, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alpha", got[0].Name, "the untrusted answer is not used")
	assert.Equal(t, "old title", got[0].PaneTitle)
	assert.Equal(t, "7:7", got[0].TmuxInstance)
	assert.Equal(t, 1, de.calls)
	assert.Equal(t, 1, *lists)
	assert.Equal(t, 1, *panes)
}

// A trusted answer with no session in it has no instance to carry: it is probed, as it was.
func TestListSessions_NoSessionsInTheDetailedAnswer(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmux = &detailExec{FakeExecutor: fake, trusted: true}
	probes := 0
	mod.tmuxInstanceFn = func(context.Context) string { probes++; return "3:3" }
	got, err := mod.ListSessionsContext(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, 1, probes, "probed once, as before: the list carries the instance only when it has a session")
}

// A read that fails is the caller's error, not a quiet fallback.
func TestListSessions_DetailedReadErrorIsAnError(t *testing.T) {
	mod, _, fake := newTestModule(t)
	boom := errors.New("tmux is gone")
	mod.tmux = &detailExec{FakeExecutor: fake, err: boom}
	_, err := mod.ListSessionsContext(context.Background())
	assert.ErrorIs(t, err, boom)
}

// GetSession is the same: one read finds the session, its panes and the instance.
func TestGetSession_OneReadWhenTheExecutorCanListThePanes(t *testing.T) {
	mod, _, fake := newTestModule(t)
	de := &detailExec{FakeExecutor: fake, trusted: true, details: []tmux.SessionDetail{
		detailFor("$1", "alpha", "/a", "t1", "w1", "vim", "42:1000"),
		detailFor("$2", "beta", "/b", "t2", "w2", "node", "42:1000"),
	}}
	mod.tmux = de
	lists, panes := countReads(fake)
	probes := 0
	mod.tmuxInstanceFn = func(context.Context) string { probes++; return "probed" }

	code, err := EncodeSessionID("$2")
	require.NoError(t, err)
	info, err := mod.GetSessionContext(context.Background(), code)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "beta", info.Name)
	assert.Equal(t, "t2", info.PaneTitle)
	assert.Equal(t, "node", info.CurrentCommand)
	assert.Equal(t, "42:1000", info.TmuxInstance)
	assert.Equal(t, 1, de.calls)
	assert.Zero(t, *lists+*panes+probes, "one read, nothing else")

	missing, err := EncodeSessionID("$9")
	require.NoError(t, err)
	info, err = mod.GetSessionContext(context.Background(), missing)
	require.NoError(t, err)
	assert.Nil(t, info, "a session that is not in the answer is not found")
}
