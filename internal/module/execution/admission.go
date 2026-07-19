package execution

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// ErrRepoBusy is returned when a dispatch targets a canonical repo that already
// has a live execution (status ∈ {accepted, running}). Single-live per canonical
// repo is the M0 data-safety guard in the absence of worktree isolation (spec
// §7.2); the caller turns this into a `failed` outcome for the dispatch.
var ErrRepoBusy = errors.New("repo already has a live execution")

// Admission is the immutable snapshot captured while holding the per-repo lock
// (spec §7.4). The caller (launch, P.6) writes these onto the execution row:
// CanonicalPath becomes repo_location, and Head/Dirty become the diff base and
// audit fields head_at_start / dirty_at_start.
type Admission struct {
	CanonicalPath string
	HeadAtStart   string
	DirtyAtStart  bool
}

// liveChecker is the read-only slice of the execution store admission needs: is
// there already a live execution for this canonical repo? (Satisfied by
// *ExecutionStore.)
type liveChecker interface {
	HasLiveByRepo(ctx context.Context, canonicalPath string) (bool, error)
}

// Admitter enforces the M0 admission rule (spec §7): canonical repo key, a
// per-canonical-repo lock held across accept→launch (not a point check), the
// status-based single-live guard, and the head/dirty snapshot.
//
// # Known residual (spec §7.5, deliberate M0 tradeoff, NOT a defect)
//
// M0 has no worktree isolation (that is M3). While an execution runs, an
// external process editing the same repo (git pull, manual edits) will be folded
// into the terminal `git diff` against head_at_start. M0 mitigates this with the
// single-live rule plus the dirty_at_start audit field only; full isolation is
// deferred to M3.
type Admitter struct {
	store        liveChecker
	allowedRoots []string
	locks        *keyedMutex
	// runGit runs `git -C dir args...` and returns trimmed stdout. Overridable
	// in tests; defaults to the real git binary.
	runGit func(ctx context.Context, dir string, args ...string) (string, error)
}

// NewAdmitter builds an Admitter over the execution store. allowedRoots, when
// non-empty, restricts admissible repos to paths within those roots (spec §7.1);
// nil/empty imposes no containment restriction.
func NewAdmitter(store liveChecker, allowedRoots []string) *Admitter {
	return &Admitter{
		store:        store,
		allowedRoots: allowedRoots,
		locks:        newKeyedMutex(),
		runGit:       runGitCmd,
	}
}

// WithRepoLock is the admission entry point and the seam the launch step (P.6)
// composes into. It atomically, under a single per-canonical-repo lock:
//
//  1. canonicalises repoLocation (reject → ErrCanonical, fn NOT run);
//  2. checks the status-based single-live rule (busy → ErrRepoBusy, fn NOT run);
//  3. snapshots HEAD + dirty into an Admission;
//  4. invokes fn(adm) WHILE STILL HOLDING THE LOCK.
//
// P.6 wraps its whole durable cut inside fn — insert the execution row (which
// makes the repo live), enqueue the accepted report, spawn the session, write
// launch_state — so no second admission can slip between this check and the row
// becoming live. There is no TOCTOU window: a concurrent admission for the same
// repo blocks on the lock, then observes the row fn inserted and is rejected.
// The lock is always released when WithRepoLock returns (fn returned, or an
// error short-circuited before fn), so a rejected or failed admission never
// leaks the lock.
func (a *Admitter) WithRepoLock(ctx context.Context, repoLocation string, fn func(*Admission) error) error {
	canonical, err := Canonicalize(repoLocation, a.allowedRoots)
	if err != nil {
		return err
	}

	unlock := a.locks.lock(canonical)
	defer unlock()

	live, err := a.store.HasLiveByRepo(ctx, canonical)
	if err != nil {
		return fmt.Errorf("admission live check: %w", err)
	}
	if live {
		return fmt.Errorf("%w: %s", ErrRepoBusy, canonical)
	}

	head, dirty, err := a.snapshot(ctx, canonical)
	if err != nil {
		return err
	}
	return fn(&Admission{
		CanonicalPath: canonical,
		HeadAtStart:   head,
		DirtyAtStart:  dirty,
	})
}

// snapshot captures HEAD commit and dirty state while the per-repo lock is held.
// A non-git directory or a repo with no commits fails rev-parse and is rejected
// (a diff base cannot be established).
func (a *Admitter) snapshot(ctx context.Context, canonical string) (head string, dirty bool, err error) {
	head, err = a.runGit(ctx, canonical, "rev-parse", "HEAD")
	if err != nil {
		return "", false, fmt.Errorf("snapshot HEAD for %q: %w", canonical, err)
	}
	porcelain, err := a.runGit(ctx, canonical, "status", "--porcelain")
	if err != nil {
		return "", false, fmt.Errorf("snapshot dirty for %q: %w", canonical, err)
	}
	return head, strings.TrimSpace(porcelain) != "", nil
}

// runGitCmd runs `git -C dir args...` and returns trimmed stdout, following the
// existing daemon convention (internal/module/dev). stderr is folded into the
// error for diagnosis.
func runGitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("git %v: %v: %s", args, err, msg)
		}
		return "", fmt.Errorf("git %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// keyedMutex is a per-key mutex: distinct keys never contend, the same key
// serialises. Entries are never removed (bounded by the number of distinct repos
// on a single daemon — negligible for M0), so a returned unlock always refers to
// a live mutex.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newKeyedMutex() *keyedMutex {
	return &keyedMutex{locks: make(map[string]*sync.Mutex)}
}

// lock acquires the mutex for key and returns its unlock func.
func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	m, ok := k.locks[key]
	if !ok {
		m = &sync.Mutex{}
		k.locks[key] = m
	}
	k.mu.Unlock()

	m.Lock()
	return m.Unlock
}
