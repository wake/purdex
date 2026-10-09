package convfeed

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

const sidR = "11111111-2222-3333-4444-555555555555"

type fakeOwners struct {
	owners []Owner
	err    error
}

func (f fakeOwners) LiveSessions(context.Context, string) ([]Owner, error) { return f.owners, f.err }

type fakeIndex struct {
	path string
	ok   bool
	err  error
}

func (f fakeIndex) TranscriptPath(context.Context, string) (string, bool, error) {
	return f.path, f.ok, f.err
}

// resEnv is a home with ~/.claude/projects, and a way to put a transcript under a slug.
type resEnv struct {
	t    *testing.T
	home string
	root string // symlink-resolved projects root
}

func newResEnv(t *testing.T) *resEnv {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(proj)
	if err != nil {
		t.Fatal(err)
	}
	return &resEnv{t: t, home: home, root: root}
}

func (e *resEnv) put(slug, content string) string {
	e.t.Helper()
	p := filepath.Join(e.root, slug, sidR+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func readAll(t *testing.T, s Source) string {
	t.Helper()
	n, err := s.File.Size()
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, n)
	if _, err := s.File.ReadAt(b, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	return string(b)
}

func resolve(t *testing.T, r *Resolver) (Source, error) {
	t.Helper()
	s, err := r.Resolve(context.Background(), sidR)
	if err == nil && s.Closer != nil {
		t.Cleanup(func() { s.Closer.Close() })
	}
	return s, err
}

func TestResolve_LivePaneWinsOverIndexOverLookup(t *testing.T) {
	e := newResEnv(t)
	byPane := e.put("-pane", "pane")
	byIndex := e.put("-index", "index")
	e.put("-lookup", "lookup")

	r := &Resolver{Home: e.home,
		Owners: fakeOwners{owners: []Owner{{TranscriptPath: byPane, Status: "running", SeenAt: 1}}},
		Index:  fakeIndex{path: byIndex, ok: true}}
	s, err := resolve(t, r)
	if err != nil || readAll(t, s) != "pane" || !s.Live || s.Backend != "terminal" || s.Status != "running" {
		t.Fatalf("live pane: %+v err %v", s, err)
	}

	r.Owners = fakeOwners{}
	s, err = resolve(t, r)
	if err != nil || readAll(t, s) != "index" || s.Live || s.Backend != "" || s.Status != "ended" {
		t.Fatalf("index: %+v err %v", s, err)
	}

	r.Index = fakeIndex{}
	s, err = resolve(t, r)
	if err != nil || readAll(t, s) == "" {
		t.Fatalf("lookup: %+v err %v", s, err)
	}
}

func TestResolve_SeveralPanesTheOneSeenLastWins(t *testing.T) {
	e := newResEnv(t)
	a := e.put("-a", "a")
	b := e.put("-b", "b")
	r := &Resolver{Home: e.home, Owners: fakeOwners{owners: []Owner{
		{TranscriptPath: a, Status: "running", SeenAt: 3}, {TranscriptPath: b, Status: "idle", SeenAt: 9}, {TranscriptPath: a, Status: "running", SeenAt: 5}}}}
	s, err := resolve(t, r)
	if err != nil || readAll(t, s) != "b" || s.Status != "idle" {
		t.Fatalf("got %+v err %v, want the pane seen last", s, err)
	}
}

func TestResolve_BadCandidatesAreSkippedAndTheLookupContinues(t *testing.T) {
	e := newResEnv(t)
	found := e.put("-real", "real")
	outside := filepath.Join(t.TempDir(), sidR+".jsonl")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	txt := filepath.Join(e.root, "-real", "notes.txt")
	if err := os.WriteFile(txt, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(e.root, "-link", sidR+".jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	for name, cand := range map[string]string{
		"missing":      filepath.Join(e.root, "-gone", sidR+".jsonl"),
		"outside root": outside,
		"symlink out":  link,
		"a directory":  filepath.Join(e.root, "-real"),
		"not .jsonl":   txt,
	} {
		r := &Resolver{Home: e.home, Index: fakeIndex{path: cand, ok: true},
			Owners: fakeOwners{owners: []Owner{{TranscriptPath: cand, Status: "idle"}}}}
		s, err := resolve(t, r)
		if err != nil || readAll(t, s) != "real" {
			t.Fatalf("%s: got %+v err %v, want the lookup to find %s", name, s, err, found)
		}
	}
}

func TestResolve_OwnerErrorStatusUnknownContentServed(t *testing.T) {
	e := newResEnv(t)
	e.put("-x", "content")
	r := &Resolver{Home: e.home, Owners: fakeOwners{err: errors.New("boom")}}
	s, err := resolve(t, r)
	if err != nil || s.Status != "unknown" || s.Live || readAll(t, s) != "content" {
		t.Fatalf("got %+v err %v", s, err)
	}
}

func TestResolve_NothingIsNotFound(t *testing.T) {
	e := newResEnv(t)
	r := &Resolver{Home: e.home}
	if _, err := resolve(t, r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	for _, id := range []string{"", "..", "a/b", `a\b`} {
		if _, err := r.Resolve(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %q: err = %v", id, err)
		}
	}
}

// A directory swapped for a symlink between the containment check and the open must not be followed.
func TestResolve_DirectorySwappedAfterTheCheckDoesNotEscape(t *testing.T) {
	e := newResEnv(t)
	cand := e.put("-victim", "inside")
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, sidR+".jsonl"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{Home: e.home, Index: fakeIndex{path: cand, ok: true}}
	r.afterCheck = func() {
		dir := filepath.Join(e.root, "-victim")
		os.RemoveAll(dir)
		if err := os.Symlink(outsideDir, dir); err != nil {
			t.Error(err)
		}
	}
	s, err := resolve(t, r)
	if err == nil && readAll(t, s) == "SECRET" {
		t.Fatal("the swapped directory was followed out of the projects root")
	}
}

func TestResolve_IdentityChangesWhenThePathIsAnotherFile(t *testing.T) {
	e := newResEnv(t)
	p := e.put("-x", "one")
	r := &Resolver{Home: e.home, Index: fakeIndex{path: p, ok: true}}
	s1, err := resolve(t, r)
	if err != nil {
		t.Fatal(err)
	}
	s1b, _ := resolve(t, r)
	if s1.Identity == "" || s1.Identity != s1b.Identity {
		t.Fatalf("same file, identity %q vs %q", s1.Identity, s1b.Identity)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	e.put("-x", "two")
	s2, err := resolve(t, r)
	if err != nil || s2.Identity == s1.Identity {
		t.Fatalf("replaced file kept identity %q (err %v)", s2.Identity, err)
	}
}

func TestResolve_LookupIsBoundedByDirectoryCount(t *testing.T) {
	e := newResEnv(t)
	for i := 0; i < 10; i++ {
		if err := os.MkdirAll(filepath.Join(e.root, fmt.Sprintf("-d%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 7; i++ { // plain files in the root cost nothing
		if err := os.WriteFile(filepath.Join(e.root, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	look := func(max int) (*os.File, int) {
		r := &Resolver{Home: e.home, MaxLookupDirs: max}
		f, _, n := r.lookup(context.Background(), e.root, sidR)
		if f != nil {
			t.Cleanup(func() { f.Close() })
		}
		return f, n
	}
	if f, n := look(4); f != nil || n != 4 {
		t.Fatalf("bound 4 over 10 directories: file %v, visited %d, want nothing after exactly 4", f != nil, n)
	}
	if f, n := look(0); f != nil || n != 10 { // the default is far above 10: all directories, no file
		t.Fatalf("default bound: file %v, visited %d, want all 10 directories", f != nil, n)
	}
	e.put("-zzz", "late")
	if f, _ := look(11); f == nil {
		t.Fatal("bound above the directory count did not find the file")
	}
}

func TestResolve_LookupIsBoundedByTimeAndContext(t *testing.T) {
	e := newResEnv(t)
	for i := 0; i < 300; i++ {
		if err := os.MkdirAll(filepath.Join(e.root, fmt.Sprintf("-d%03d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.put("-zzz", "late")
	r := &Resolver{Home: e.home, LookupTime: time.Nanosecond}
	if _, err := resolve(t, r); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired lookup budget: err = %v, want ErrNotFound", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&Resolver{Home: e.home}).Resolve(ctx, sidR); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled request: err = %v, want context.Canceled", err)
	}
}

// One resolver serves concurrent requests (go test -race).
func TestResolve_ConcurrentResolutionsShareOneResolver(t *testing.T) {
	e := newResEnv(t)
	e.put("-x", "content")
	r := &Resolver{Home: e.home, Owners: fakeOwners{}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := r.Resolve(context.Background(), sidR)
			if err != nil {
				t.Error(err)
				return
			}
			s.Closer.Close()
		}()
	}
	wg.Wait()
}

// The request is cancelled while the scan runs: the answer is the cancellation, not a 404.
func TestResolve_CancelledDuringTheScanIsNotNotFound(t *testing.T) {
	e := newResEnv(t)
	for i := 0; i < 300; i++ {
		if err := os.MkdirAll(filepath.Join(e.root, fmt.Sprintf("-d%03d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	r := &Resolver{Home: e.home, onDir: func() {
		if n++; n == 3 {
			cancel()
		}
	}}
	if _, err := r.Resolve(ctx, sidR); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A FIFO named like the transcript (as a candidate and in the lookup) neither blocks nor is returned.
func TestResolve_FIFOIsNeitherOpenedNorBlocksTheLookup(t *testing.T) {
	e := newResEnv(t)
	fifo := filepath.Join(e.root, "-fifo", sidR+".jsonl")
	if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		r := &Resolver{Home: e.home, Index: fakeIndex{path: fifo, ok: true},
			Owners: fakeOwners{owners: []Owner{{TranscriptPath: fifo, Status: "idle"}}}}
		s, err := r.Resolve(context.Background(), sidR)
		if err == nil {
			s.Closer.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the resolver blocked on a FIFO")
	}
}

// Cancelled just before a candidate that exists would be opened: still the cancellation, never a success.
func TestResolve_CancelledBeforeTheOpenOfAnExistingCandidate(t *testing.T) {
	e := newResEnv(t)
	e.put("-only", "content")
	ctx, cancel := context.WithCancel(context.Background())
	r := &Resolver{Home: e.home, onDir: cancel}
	s, err := r.Resolve(ctx, sidR)
	if err == nil {
		s.Closer.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// An owner or index answer naming another session's file is skipped, and the later sources still run.
func TestResolve_CandidateOfAnotherSessionIsSkipped(t *testing.T) {
	e := newResEnv(t)
	wrong := filepath.Join(e.root, "-other", "99999999-9999-4999-8999-999999999999.jsonl")
	if err := os.MkdirAll(filepath.Dir(wrong), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrong, []byte("not this one"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.put("-right", "this one")
	r := &Resolver{Home: e.home, Index: fakeIndex{path: wrong, ok: true},
		Owners: fakeOwners{owners: []Owner{{TranscriptPath: wrong, Status: "idle"}}}}
	s, err := resolve(t, r)
	if err != nil || readAll(t, s) != "this one" || filepath.Base(s.Path) != sidR+".jsonl" {
		t.Fatalf("got %+v err %v, want the file named after the session", s, err)
	}
}
