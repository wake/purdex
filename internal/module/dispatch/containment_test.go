package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wake/purdex/internal/bridge"
	"github.com/wake/purdex/internal/config"
	"github.com/wake/purdex/internal/core"
	"github.com/wake/purdex/internal/module/execution"
	"github.com/wake/purdex/internal/module/stream"
	"github.com/wake/purdex/internal/tmux"
)

// F5 — the daemon must not run an agent in an arbitrary local checkout just
// because Ploom named it. These tests drive the REAL production wiring (config →
// buildCoordinator → Admitter) so the containment boundary is proven where it is
// actually assembled, and the rejection is proven to be reportable (F3).

func gitRepoAt(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "T")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

// newContainmentModule wires a real DispatchModule (store + gateway + seam) whose
// config carries the given allowed roots, and returns it with its store.
func newContainmentModule(t *testing.T, roots []string) (*DispatchModule, *execution.ExecutionStore) {
	t.Helper()
	t.Setenv(envPloomURL, "https://ploom.test")
	t.Setenv(envPloomToken, "tok")

	dir := t.TempDir()
	store, err := execution.OpenExecution(filepath.Join(dir, "exec.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	reg := core.NewServiceRegistry()
	reg.Register(execution.RegistryKey, store)
	reg.Register(stream.RelayGatewayKey, bridge.New())
	reg.Register(stream.TerminalSeamKey, &fakeSeam{})

	c := core.New(core.CoreDeps{
		Config: &config.Config{
			DataDir: dir, Bind: "127.0.0.1", Port: 7860, Token: "tok",
			Dispatch: config.DispatchConfig{AllowedRepoRoots: roots},
		},
		Tmux:     tmux.NewFakeExecutor(),
		Registry: reg,
	})

	m := New()
	require.NoError(t, m.Init(c))
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return m, store
}

// claimOf builds the ClaimedDispatch the worker would hand to the sink.
func claimOf(dispatchID, localDir string) ClaimedDispatch {
	return ClaimedDispatch{
		Pending: PendingDispatch{DispatchID: dispatchID, IssueID: "iss_1"},
		Detail: DispatchDetail{
			DispatchID:     dispatchID,
			Issue:          Issue{IssueID: "iss_1", Title: "Do it"},
			RepoLocation:   RepoLocation{ProjectID: "prj_1", LocalDir: localDir, IsOrigin: true},
			SandboxProfile: "workspace-write",
		},
	}
}

// requireRejected asserts the dispatch produced a backed, reportable rejection
// (F3) rather than silently launching or silently dropping.
func requireRejected(t *testing.T, store *execution.ExecutionStore, dispatchID string) {
	t.Helper()
	ids, err := store.Outbox().DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1, "the rejection must be recorded as an execution")

	row, ok, err := store.GetByID(ids[0])
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, dispatchID, row.DispatchID)
	require.Equal(t, execution.StatusFailed, row.Status)
	require.Equal(t, execution.LaunchNone, row.LaunchState, "nothing may be launched outside the boundary")
	require.Equal(t, string(execution.OutcomeRejected), row.OutcomeSource.String)

	recs, err := store.Outbox().UnackedRecords(ids[0])
	require.NoError(t, err)
	require.Len(t, recs, 2)
	require.Equal(t, "accepted", recs[0].Status)
	require.Equal(t, "failed", recs[1].Status)
	require.Contains(t, string(recs[1].Payload), "invalid_repo_location")
}

func TestContainment_NoRootsConfigured_RejectsEveryDispatch(t *testing.T) {
	repo := gitRepoAt(t, filepath.Join(t.TempDir(), "repo"))
	m, store := newContainmentModule(t, nil) // fail closed

	m.sink(context.Background(), claimOf("dsp_noroot", repo))

	requireRejected(t, store, "dsp_noroot")
}

func TestContainment_RepoOutsideRoots_Rejected(t *testing.T) {
	repo := gitRepoAt(t, filepath.Join(t.TempDir(), "repo"))
	m, store := newContainmentModule(t, []string{t.TempDir()}) // unrelated root

	m.sink(context.Background(), claimOf("dsp_outside", repo))

	requireRejected(t, store, "dsp_outside")
}

// A symlink inside an allowed root pointing at a repo outside it must not smuggle
// that repo in: both sides are symlink-resolved before the boundary check.
func TestContainment_SymlinkEscape_Rejected(t *testing.T) {
	outside := gitRepoAt(t, filepath.Join(t.TempDir(), "outside"))
	root := t.TempDir()
	link := filepath.Join(root, "inside-looking")
	require.NoError(t, os.Symlink(outside, link))

	m, store := newContainmentModule(t, []string{root})

	m.sink(context.Background(), claimOf("dsp_link", link))

	requireRejected(t, store, "dsp_link")
}

// A repo inside an allowed root passes containment and launches. The admitter is
// built from the SAME config accessor production uses (allowedRepoRoots), with
// only the launcher faked so the test does not wait on a real relay.
func TestContainment_RepoInsideRoot_Launches(t *testing.T) {
	root := t.TempDir()
	repo := gitRepoAt(t, filepath.Join(root, "repo"))

	store, err := execution.OpenExecution(filepath.Join(t.TempDir(), "exec.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	c := core.New(core.CoreDeps{
		Config: &config.Config{
			DataDir:  t.TempDir(),
			Dispatch: config.DispatchConfig{AllowedRepoRoots: []string{root}},
		},
		Tmux:     tmux.NewFakeExecutor(),
		Registry: core.NewServiceRegistry(),
	})
	coord := execution.NewCoordinator(
		execution.NewAdmitter(store, allowedRepoRoots(c)),
		store, launchReporter{},
		&sliceLauncher{result: execution.LaunchResult{SessionCode: "code_ok"}},
		execution.ProfileAsk,
	)

	(&DispatchModule{}).consumeSink(coord)(context.Background(), claimOf("dsp_inside", repo))

	ids, err := store.Outbox().DueExecutions(1 << 60)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	row, ok, err := store.GetByID(ids[0])
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "dsp_inside", row.DispatchID)
	require.Equal(t, execution.StatusRunning, row.Status)
	require.Equal(t, execution.LaunchLaunched, row.LaunchState)
	// Admission ran: the canonical repo path and the HEAD snapshot are only taken
	// inside WithRepoLock.
	require.Equal(t, mustEvalPath(t, repo), row.RepoLocation)
	require.Len(t, row.HeadAtStart, 40)
	require.Equal(t, "ask", row.SandboxProfile)
}

// The roots snapshot is a copy, so later config mutation cannot retroactively
// widen a built admitter through aliasing.
func TestAllowedRepoRoots_SnapshotsConfig(t *testing.T) {
	dir := t.TempDir()
	c := core.New(core.CoreDeps{
		Config: &config.Config{
			DataDir:  dir,
			Dispatch: config.DispatchConfig{AllowedRepoRoots: []string{"/a", "/b"}},
		},
		Registry: core.NewServiceRegistry(),
	})

	got := allowedRepoRoots(c)
	require.Equal(t, []string{"/a", "/b"}, got)

	got[0] = "/mutated"
	require.Equal(t, []string{"/a", "/b"}, c.Cfg.Dispatch.AllowedRepoRoots)
	require.Empty(t, allowedRepoRoots(core.New(core.CoreDeps{
		Config: &config.Config{DataDir: dir}, Registry: core.NewServiceRegistry(),
	})), "unset config yields no roots — admission then fails closed")
}

func mustEvalPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	require.NoError(t, err)
	return r
}
