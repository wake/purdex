package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Task 2: the lockfile helper -----------------------------------------

func TestPathLock_AcquireReleaseAndPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".local", "bin")

	lock, err := acquirePathLock(dir, lockPolicy{})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	want := filepath.Join(dir, ".pdx-path.lock")
	if lock.path != want {
		t.Errorf("lock path = %q, want %q (one lock, shared by link and add-to-shell)", lock.path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("lockfile not present while held: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Errorf("release: %v", err)
	}
	// The file deliberately outlives the lock: removing it on release is
	// what let the old implementation delete a lock another process had
	// since taken. What must be true after release is that the lock is
	// available, which is what this asserts.
	assertLockFree(t, dir)
}

func TestPathLock_DefaultPolicy(t *testing.T) {
	p := lockPolicy{}.withDefaults()
	if p.retryEvery != 50*time.Millisecond {
		t.Errorf("retryEvery = %v, want 50ms", p.retryEvery)
	}
	if p.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", p.timeout)
	}
}

func TestPathLock_HeldTimesOutNamingTheLock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".pdx-path.lock")
	held, err := acquirePathLock(dir, lockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	defer held.release()

	_, err = acquirePathLock(dir, lockPolicy{retryEvery: 10 * time.Millisecond, timeout: 120 * time.Millisecond})
	if err == nil {
		t.Fatal("acquire succeeded while the lock was held")
	}
	if !strings.Contains(err.Error(), lockPath) {
		t.Errorf("error must name the lock path %q: %v", lockPath, err)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Errorf("a held lock must not be stolen: %v", statErr)
	}
}

// A lockfile left by a process that died holding the lock must not block
// anyone. The old implementation guessed at this from the file's mtime, and
// round 2's attack review found both ways that guess breaks: a slow-but-alive
// holder gets its lock stolen, and its later release then unlinks the lock the
// thief now owns. flock removes the guess — the kernel drops the lock when the
// descriptor closes, so a leftover file is simply an unlocked file.
func TestPathLock_FileLeftByADeadHolderDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".pdx-path.lock")
	if err := os.WriteFile(lockPath, []byte("99999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * time.Second)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	lock, err := acquirePathLock(dir, lockPolicy{retryEvery: 10 * time.Millisecond, timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatalf("a lockfile with no live holder must not block: %v", err)
	}
	if err := lock.release(); err != nil {
		t.Fatal(err)
	}
}

// The two failure modes of the mtime-staleness design, asserted to be gone:
// a holder that is merely slow keeps its lock however long it takes, and when
// it finally releases, the lock is free for the next caller — not deleted out
// from under one.
func TestPathLock_ASlowHolderIsNeverStolenFrom(t *testing.T) {
	dir := t.TempDir()
	held, err := acquirePathLock(dir, lockPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	// Age the file well past any staleness window the old design would have
	// used. The holder is still very much alive.
	lockPath := filepath.Join(dir, ".pdx-path.lock")
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := acquirePathLock(dir, lockPolicy{retryEvery: 5 * time.Millisecond, timeout: 60 * time.Millisecond}); err == nil {
		t.Fatal("an old-looking lock that is still held must not be stolen")
	}
	if err := held.release(); err != nil {
		t.Fatal(err)
	}
	assertLockFree(t, dir)
}

// TestPathCommandsShareOneLock asserts spec §3.2's "one lock, not two":
// add-to-shell cannot proceed while link holds it.
func TestPathCommandsShareOneLock(t *testing.T) {
	env := shellFixture(t, "/bin/zsh", "darwin")
	env.lock = lockPolicy{retryEvery: 5 * time.Millisecond, timeout: 60 * time.Millisecond}

	var innerCode int
	var innerErr bytes.Buffer
	env.afterLock = func() {
		inner := env
		inner.afterLock = nil
		var out bytes.Buffer
		innerCode = runPathCmd(inner, []string{"add-to-shell"}, &out, &innerErr)
	}

	if code, out, errOut := runPathT(t, env, "link"); code != 0 {
		t.Fatalf("link exit = %d\n%s%s", code, out, errOut)
	}
	if innerCode != 1 {
		t.Errorf("add-to-shell exited %d while link held the lock, want 1", innerCode)
	}
	if !strings.Contains(innerErr.String(), pathLockName) {
		t.Errorf("the contention must name the shared lock:\n%s", innerErr.String())
	}
	if _, err := os.Stat(filepath.Join(env.home, ".zshrc")); err == nil {
		t.Error("the blocked add-to-shell still wrote the rc file")
	}
}
