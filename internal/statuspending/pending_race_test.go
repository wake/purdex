package statuspending

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// codex R1 + attack: the compare-and-replace / compare-and-delete steps are cross-process steps, so they run under one
// directory lock; the daemon deletes only the version it loaded; a file is bounded; only real entries count toward the cap;
// a symlink is never followed.

// An older writer that passed its check before a newer writer finished must not overwrite it afterwards: the check and the
// rename are one step under the directory lock, so the newer writer waits for the older one and wins. Mutation: no lock → the
// older payload ends up on disk (red).
func TestWrite_AnOlderWriterCannotOverwriteANewerOne(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "first"), 1000)
	var wg sync.WaitGroup
	fired := false
	testHook = func(point string) {
		if point != "write-checked" || fired {
			return
		}
		fired = true
		wg.Add(1)
		go func() { defer wg.Done(); Write(dir, payload(sidA, "newer"), 3000) }()
		time.Sleep(150 * time.Millisecond) // the newer writer runs now: unlocked it would finish here
	}
	t.Cleanup(func() { testHook = nil })
	Write(dir, payload(sidA, "older"), 2000)
	wg.Wait()
	got, _ := Load(dir)
	var m struct{ Model struct{ ID string } }
	if len(got) != 1 {
		t.Fatalf("load = %+v", got)
	}
	json.Unmarshal(got[0].Raw, &m)
	if got[0].AtMs != 3000 || m.Model.ID != "newer" {
		t.Fatalf("kept at %d (%s), want the newer one", got[0].AtMs, m.Model.ID)
	}
}

// A delivery's cleanup that read the file before a newer failure was written must not delete that newer file. Mutation: no lock
// → the newer file is gone (red).
func TestCleanupOnSuccess_ANewerFailureWrittenMeanwhileSurvives(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "old"), 1000)
	var wg sync.WaitGroup
	fired := false
	testHook = func(point string) {
		if point != "cleanup-checked" || fired {
			return
		}
		fired = true
		wg.Add(1)
		go func() { defer wg.Done(); Write(dir, payload(sidA, "newer-failure"), 5000) }()
		time.Sleep(150 * time.Millisecond)
	}
	t.Cleanup(func() { testHook = nil })
	CleanupOnSuccess(dir, payload(sidA, "delivered"), 2000)
	wg.Wait()
	got, _ := Load(dir)
	if len(got) != 1 || got[0].AtMs != 5000 {
		t.Fatalf("load = %+v, want the newer failure kept", got)
	}
}

// The daemon deletes only the version it loaded: a file replaced by a newer payload since is kept. Mutations: unconditional remove
// → red; no lock → red.
func TestRemoveIfNotNewer(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "a"), 1000)
	RemoveIfNotNewer(dir, sidA, 500) // the loaded version was older than the file now there
	if got, _ := Load(dir); len(got) != 1 {
		t.Fatal("a newer file was removed")
	}
	RemoveIfNotNewer(dir, sidA, 1000)
	if got, _ := Load(dir); len(got) != 0 {
		t.Fatal("the loaded version stayed")
	}
	RemoveIfNotNewer(dir, "../x", 1) // unsafe: nothing

	Write(dir, payload(sidA, "b"), 1000)
	var wg sync.WaitGroup
	fired := false
	testHook = func(point string) {
		if point != "remove-checked" || fired {
			return
		}
		fired = true
		wg.Add(1)
		go func() { defer wg.Done(); Write(dir, payload(sidA, "newer"), 9000) }()
		time.Sleep(150 * time.Millisecond)
	}
	t.Cleanup(func() { testHook = nil })
	RemoveIfNotNewer(dir, sidA, 1000)
	wg.Wait()
	if got, _ := Load(dir); len(got) != 1 || got[0].AtMs != 9000 {
		t.Fatalf("load = %+v, want the write that came during the remove kept", got)
	}
}

// A lock someone holds for ever never stalls a render: the writer gives up (the payload is dropped, as before this package).
func TestWrite_AHeldLockDoesNotBlockForLong(t *testing.T) {
	dir := t.TempDir()
	Write(dir, payload(sidA, "x"), 1)
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = Write(dir, payload(sidA, "y"), 2)
	if err != ErrBusy || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %v, want ErrBusy quickly", err, time.Since(start))
	}
}

// Mutations: no size bound / the cap counts junk / a symlink is read → red.
func TestWrite_AFileIsBounded(t *testing.T) {
	dir := t.TempDir()
	big := []byte(fmt.Sprintf(`{"session_id":%q,"pad":%q}`, sidA, strings.Repeat("a", MaxFileBytes)))
	if err := Write(dir, big, 1); err != ErrTooBig {
		t.Fatalf("err = %v, want ErrTooBig", err)
	}
}

func TestWrite_OnlyRealEntriesCountTowardTheCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < Cap+5; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("junk%04d.json", i)), []byte("nope"), 0o600)
	}
	if err := Write(dir, payload(sidA, "m"), 1); err != nil {
		t.Fatalf("junk names blocked a real payload: %v", err)
	}
}

func TestLoad_ASymlinkIsNotFollowed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "p")
	os.MkdirAll(dir, 0o700)
	outside := filepath.Join(root, "outside.json")
	os.WriteFile(outside, []byte(`{"at_ms":1,"raw_status":`+string(payload(sidA, "m"))+`}`), 0o600)
	os.Symlink(outside, filepath.Join(dir, sidA+".json"))
	got, _ := Load(dir)
	if len(got) != 0 {
		t.Fatalf("a symlink was followed: %+v", got)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("the target outside the directory was touched")
	}
	if _, err := os.Lstat(filepath.Join(dir, sidA+".json")); err == nil {
		t.Fatal("the link itself was kept")
	}
}
