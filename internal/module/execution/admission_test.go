package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// initGitRepo creates a real git repo in a fresh temp dir with one commit and
// returns its path. Uses real git so head/dirty snapshots exercise real output.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644))
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// admitterFor builds an Admitter backed by a real in-memory store.
func admitterFor(t *testing.T) (*Admitter, *ExecutionStore) {
	t.Helper()
	s := openTestStore(t)
	return NewAdmitter(s, nil), s
}

func TestAdmit_CleanRepoSucceeds(t *testing.T) {
	repo := initGitRepo(t)
	a, _ := admitterFor(t)

	var got *Admission
	err := a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
		got = adm
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, mustEval(t, repo), got.CanonicalPath)
	require.Len(t, got.HeadAtStart, 40, "HEAD should be a full 40-char sha")
	require.False(t, got.DirtyAtStart, "fresh committed repo is clean")
}

func TestAdmit_DirtyRepoRecordsDirty(t *testing.T) {
	repo := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x"), 0o644))
	a, _ := admitterFor(t)

	var got *Admission
	err := a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
		got = adm
		return nil
	})
	require.NoError(t, err)
	require.True(t, got.DirtyAtStart, "untracked file makes the tree dirty")
}

func TestAdmit_NonGitDirRejected(t *testing.T) {
	dir := t.TempDir() // exists but is not a git repo
	a, _ := admitterFor(t)

	called := false
	err := a.WithRepoLock(context.Background(), dir, func(*Admission) error {
		called = true
		return nil
	})
	require.Error(t, err)
	require.False(t, called, "fn must not run when the snapshot fails")
}

func TestAdmit_NonCanonicalRejected(t *testing.T) {
	a, _ := admitterFor(t)
	called := false
	err := a.WithRepoLock(context.Background(), "", func(*Admission) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, ErrCanonical)
	require.False(t, called)
}

func TestAdmit_SecondLiveRejected(t *testing.T) {
	repo := initGitRepo(t)
	a, s := admitterFor(t)

	// First admission inserts a live row inside the lock.
	err := a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
		_, _, e := s.UpsertByDispatch(newAt("dsp_1", adm.CanonicalPath))
		return e
	})
	require.NoError(t, err)

	// Second admission for the same repo must be rejected as busy.
	called := false
	err = a.WithRepoLock(context.Background(), repo, func(*Admission) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, ErrRepoBusy)
	require.False(t, called, "fn must not run when the repo already has a live execution")
}

func TestAdmit_TerminalDoesNotBlock(t *testing.T) {
	repo := initGitRepo(t)
	a, s := admitterFor(t)

	var firstID string
	require.NoError(t, a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
		e, _, err := s.UpsertByDispatch(newAt("dsp_1", adm.CanonicalPath))
		firstID = e.ExecutionID
		return err
	}))
	require.NoError(t, s.UpdateStatus(firstID, StatusRunning))
	require.NoError(t, s.UpdateStatus(firstID, StatusCompleted))

	// Repo is free after the first execution reaches a terminal status.
	called := false
	require.NoError(t, a.WithRepoLock(context.Background(), repo, func(*Admission) error {
		called = true
		return nil
	}))
	require.True(t, called)
}

// TestAdmit_SymlinkAliasBlockedByCanonicalKey proves the single-live rule can't
// be bypassed by dispatching against a symlink alias of the same repo.
func TestAdmit_SymlinkAliasBlockedByCanonicalKey(t *testing.T) {
	repo := initGitRepo(t)
	link := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(repo, link))
	a, s := admitterFor(t)

	// Admit against the REAL path, insert a live row keyed by canonical path.
	require.NoError(t, a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
		_, _, e := s.UpsertByDispatch(newAt("dsp_real", adm.CanonicalPath))
		return e
	}))

	// Admit against the SYMLINK alias — must resolve to the same canonical key
	// and be rejected as busy.
	err := a.WithRepoLock(context.Background(), link, func(*Admission) error {
		return nil
	})
	require.ErrorIs(t, err, ErrRepoBusy)
}

// TestAdmit_ConcurrentTOCTOU proves the per-repo lock serialises two concurrent
// admissions of the same repo: exactly one wins (inserts the live row) and the
// other observes it and is rejected — no TOCTOU window between check and insert.
func TestAdmit_ConcurrentTOCTOU(t *testing.T) {
	repo := initGitRepo(t)
	a, s := admitterFor(t)

	const n = 8
	var wg sync.WaitGroup
	results := make([]error, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
				_, _, e := s.UpsertByDispatch(newAt("dsp_"+itoa(idx), adm.CanonicalPath))
				return e
			})
		}(i)
	}
	close(start)
	wg.Wait()

	var success, busy int
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrRepoBusy):
			busy++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, success, "exactly one admission may create the live execution")
	require.Equal(t, n-1, busy, "all others must be rejected as busy")

	// And exactly one row exists in the store.
	var rows int
	require.NoError(t, s.db.QueryRow(`SELECT COUNT(*) FROM executions`).Scan(&rows))
	require.Equal(t, 1, rows)
}

// TestAdmit_LockReleasedAfterRejection ensures a rejected admission still frees
// the lock so a later terminal + retry can proceed.
func TestAdmit_LockReleasedAfterRejection(t *testing.T) {
	repo := initGitRepo(t)
	a, s := admitterFor(t)

	var id string
	require.NoError(t, a.WithRepoLock(context.Background(), repo, func(adm *Admission) error {
		e, _, err := s.UpsertByDispatch(newAt("dsp_1", adm.CanonicalPath))
		id = e.ExecutionID
		return err
	}))
	// Rejected while live.
	require.ErrorIs(t, a.WithRepoLock(context.Background(), repo, func(*Admission) error { return nil }), ErrRepoBusy)
	// Terminal, then a fresh admission must acquire the lock (not deadlock).
	require.NoError(t, s.UpdateStatus(id, StatusRunning))
	require.NoError(t, s.UpdateStatus(id, StatusFailed))
	require.NoError(t, a.WithRepoLock(context.Background(), repo, func(*Admission) error { return nil }))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
