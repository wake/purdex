package session

// CreateSession / SessionExists / ValidateCwd on the provider (exec-to-
// terminal spec §4.1 steps 4 and 6, plan T1): the create path of
// handleCreate as a Go call other modules can make, with a typed error
// that says which stage failed and whether a tmux session was left behind.

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wake/purdex/internal/tmux"
)

// listFailingExecutor is the fake with ListSessions replaced by an error:
// tmux new-session succeeded, the follow-up list did not.
type listFailingExecutor struct {
	tmux.Executor
	err error
}

func (e *listFailingExecutor) ListSessions() ([]tmux.TmuxSession, error) { return nil, e.err }

func TestCreateSession_ReturnsInfo(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	mod.tmuxInstanceFn = func() string { return "4471:1788740000" }
	dir := t.TempDir()

	info, err := mod.CreateSession("proj-1", dir)
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "proj-1", info.Name)
	assert.NotEmpty(t, info.Code)
	assert.Equal(t, "$0", info.TmuxID)
	assert.True(t, info.Exists)
	assert.Equal(t, "terminal", info.Mode)
	assert.Equal(t, dir, info.Cwd)
	assert.Equal(t, "4471:1788740000", info.TmuxInstance, "stamped by hand, as the HTTP create is")

	assert.True(t, fake.HasSession("proj-1"))
	m, err := meta.GetMeta("$0")
	require.NoError(t, err)
	require.NotNil(t, m, "meta row written")
	assert.Equal(t, "terminal", m.Mode)
	assert.Equal(t, dir, m.Cwd)
}

func TestCreateSession_ExpandsTilde(t *testing.T) {
	mod, _, fake := newTestModule(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	info, err := mod.CreateSession("home-1", "~")
	require.NoError(t, err)
	assert.True(t, fake.HasSession("home-1"))
	assert.Equal(t, home, info.Cwd, "~ expanded before tmux saw it")
}

func TestCreateSession_Exists(t *testing.T) {
	mod, _, fake := newTestModule(t)
	fake.AddSession("taken", "/tmp")

	info, err := mod.CreateSession("taken", t.TempDir())
	assert.Nil(t, info)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSessionExists), "%v", err)
	var ce *CreateError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, CreateStageExists, ce.Stage)
	assert.Equal(t, "taken", ce.Name)
	assert.False(t, ce.SessionAlive(), "nothing was created by us")
	sessions, _ := fake.ListSessions()
	assert.Len(t, sessions, 1, "the existing session is untouched, no second one")
}

func TestCreateSession_BadName(t *testing.T) {
	mod, _, fake := newTestModule(t)
	for _, bad := range []string{"", "has space", "bad@name", "a/b"} {
		info, err := mod.CreateSession(bad, t.TempDir())
		assert.Nil(t, info, "name %q", bad)
		require.Error(t, err, "name %q", bad)
		assert.True(t, errors.Is(err, ErrInvalidSessionName), "name %q: %v", bad, err)
		var ce *CreateError
		require.True(t, errors.As(err, &ce))
		assert.Equal(t, CreateStageInvalidName, ce.Stage)
		assert.False(t, ce.SessionAlive())
	}
	sessions, _ := fake.ListSessions()
	assert.Empty(t, sessions, "nothing created")
}

func TestCreateSession_BadCwd(t *testing.T) {
	mod, _, fake := newTestModule(t)
	missing := filepath.Join(t.TempDir(), "nope")
	for _, bad := range []string{missing, "relative/path"} {
		info, err := mod.CreateSession("cwd-1", bad)
		assert.Nil(t, info, "cwd %q", bad)
		require.Error(t, err, "cwd %q", bad)
		assert.True(t, errors.Is(err, ErrInvalidCwd), "cwd %q: %v", bad, err)
		var ce *CreateError
		require.True(t, errors.As(err, &ce))
		assert.Equal(t, CreateStageInvalidCwd, ce.Stage)
		assert.False(t, ce.SessionAlive())
		assert.NotEmpty(t, ce.Err.Error(), "carries the resolveCwd reason for the HTTP text")
	}
	assert.False(t, fake.HasSession("cwd-1"))
}

func TestCreateSession_ListFails_SessionAlive(t *testing.T) {
	mod, _, fake := newTestModule(t)
	mod.tmux = &listFailingExecutor{Executor: fake, err: errors.New("tmux list exploded")}

	info, err := mod.CreateSession("proj-2", t.TempDir())
	assert.Nil(t, info)
	require.Error(t, err)
	var ce *CreateError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, CreateStageList, ce.Stage)
	assert.Equal(t, "proj-2", ce.Name)
	assert.True(t, ce.SessionAlive(), "tmux new-session already ran")
	assert.Contains(t, err.Error(), "tmux list exploded")
	assert.True(t, fake.HasSession("proj-2"), "the session is left for the caller to handle")
	assert.False(t, errors.Is(err, ErrSessionExists))
	assert.False(t, errors.Is(err, ErrInvalidSessionName))
	assert.False(t, errors.Is(err, ErrInvalidCwd))
}

func TestCreateSession_MetaFails_SessionAlive(t *testing.T) {
	mod, meta, fake := newTestModule(t)
	require.NoError(t, meta.Close()) // every SetMeta from here on fails

	info, err := mod.CreateSession("proj-3", t.TempDir())
	assert.Nil(t, info)
	require.Error(t, err)
	var ce *CreateError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, CreateStageMeta, ce.Stage)
	assert.True(t, ce.SessionAlive())
	assert.True(t, fake.HasSession("proj-3"))
}

func TestCreateSession_InvalidatesNameCache(t *testing.T) {
	mod, _, _ := newTestModule(t)
	_, ok := mod.LookupCodeByName("later")
	assert.False(t, ok, "nothing yet; the name cache is now warm and empty")

	info, err := mod.CreateSession("later", t.TempDir())
	require.NoError(t, err)
	code, ok := mod.LookupCodeByName("later")
	assert.True(t, ok, "create invalidated the name cache")
	assert.Equal(t, info.Code, code)
}

func TestSessionExists(t *testing.T) {
	mod, _, fake := newTestModule(t)
	assert.False(t, mod.SessionExists("x"))
	fake.AddSession("x", "/tmp")
	assert.True(t, mod.SessionExists("x"))
}

func TestValidateCwd(t *testing.T) {
	mod, _, _ := newTestModule(t)
	assert.NoError(t, mod.ValidateCwd(t.TempDir()))
	err := mod.ValidateCwd(filepath.Join(t.TempDir(), "gone"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidCwd), "%v", err)
	assert.Error(t, mod.ValidateCwd("not/absolute"))
}

func TestValidSessionName(t *testing.T) {
	assert.True(t, ValidSessionName("proj-1"))
	assert.True(t, ValidSessionName("A_b-9"))
	assert.False(t, ValidSessionName(""))
	assert.False(t, ValidSessionName("has space"))
	assert.False(t, ValidSessionName("a.b"))
}

// The provider interface carries the three new methods (plan T1); the
// assertion fails to compile if one is dropped.
var _ SessionProvider = (*SessionModule)(nil)
