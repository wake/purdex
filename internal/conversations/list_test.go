package conversations

import (
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// statEntry is the Entry os.Stat reports for path, under session id sid.
func statEntry(t *testing.T, sid, path string) Entry {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return Entry{
		SessionID: sid,
		Path:      path,
		Size:      fi.Size(),
		MtimeMs:   fi.ModTime().UnixMilli(),
		Inode:     fi.Sys().(*syscall.Stat_t).Ino,
	}
}

func listRoot(t *testing.T, root string) ([]Entry, []string) {
	t.Helper()
	entries, unreadable, err := ListRoot(root)
	if err != nil {
		t.Fatalf("ListRoot(%s): %v", root, err)
	}
	return entries, unreadable
}

func TestListRoot_ListsTopLevelTranscripts(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "-w-one", sidA+".jsonl")
	b := filepath.Join(root, "-w-two", sidB+".jsonl")
	writeFile(t, a, lines(t, userText("one")))
	writeFile(t, b, lines(t, userText("two"), userText("more")))
	// Not conversations: a nested subagent transcript (and its <sid> dir),
	// a non-UUID name, a directory named like a transcript, a file in the
	// root itself.
	writeFile(t, filepath.Join(root, "-w-one", sidA, "subagents", "x.jsonl"), lines(t, userText("sub")))
	writeFile(t, filepath.Join(root, "-w-one", "notes.jsonl"), lines(t, userText("notes")))
	if err := os.MkdirAll(filepath.Join(root, "-w-two", sidC+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, ".DS_Store"), []byte("x"))

	entries, unreadable := listRoot(t, root)
	want := []Entry{statEntry(t, sidA, a), statEntry(t, sidB, b)}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
	if len(unreadable) != 0 {
		t.Errorf("unreadable = %v, want none", unreadable)
	}
}

func TestListRoot_UppercaseNameIsLowercased(t *testing.T) {
	root := t.TempDir()
	upper := "0A1B2C3D-0000-4000-8000-0000000000EF"
	p := filepath.Join(root, "-w", upper+".jsonl")
	writeFile(t, p, lines(t, userText("hi")))

	entries, _ := listRoot(t, root)
	want := []Entry{statEntry(t, "0a1b2c3d-0000-4000-8000-0000000000ef", p)}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
}

func TestListRoot_FollowsSymlinkedRootAndSlug(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "vault", "projects")
	writeFile(t, filepath.Join(realRoot, "-w-real", sidA+".jsonl"), lines(t, userText("a")))
	elsewhere := filepath.Join(base, "elsewhere", "-w-linked")
	writeFile(t, filepath.Join(elsewhere, sidB+".jsonl"), lines(t, userText("b")))
	if err := os.Symlink(elsewhere, filepath.Join(realRoot, "-w-linked")); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "projects")
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}

	entries, unreadable := listRoot(t, root)
	// Paths stay under the root as given, not the resolved target.
	want := []Entry{
		statEntry(t, sidA, filepath.Join(root, "-w-real", sidA+".jsonl")),
		statEntry(t, sidB, filepath.Join(root, "-w-linked", sidB+".jsonl")),
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
	if len(unreadable) != 0 {
		t.Errorf("unreadable = %v, want none", unreadable)
	}
}

func TestListRoot_SkipsSymlinkedTranscript(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.jsonl")
	writeFile(t, target, lines(t, userText("elsewhere")))
	if err := os.MkdirAll(filepath.Join(root, "-w"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "-w", sidA+".jsonl")); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(root, "-w", sidB+".jsonl")
	writeFile(t, b, lines(t, userText("real")))

	entries, _ := listRoot(t, root)
	want := []Entry{statEntry(t, sidB, b)}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
}

func TestListRoot_RootErrors(t *testing.T) {
	base := t.TempDir()
	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "gone"), dangling); err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"missing":          filepath.Join(base, "missing"),
		"dangling symlink": dangling,
	} {
		t.Run(name, func(t *testing.T) {
			entries, unreadable, err := ListRoot(root)
			if err == nil {
				t.Fatalf("ListRoot(%s) = %v, %v, nil; want an error", root, entries, unreadable)
			}
		})
	}
}

func TestListRoot_UnreadableSlugIsReportedNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a chmod 000 dir")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "-w-locked")
	writeFile(t, filepath.Join(locked, sidA+".jsonl"), lines(t, userText("a")))
	b := filepath.Join(root, "-w-open", sidB+".jsonl")
	writeFile(t, b, lines(t, userText("b")))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	entries, unreadable, err := ListRoot(root)
	if err != nil {
		t.Fatalf("ListRoot: %v", err)
	}
	if want := []Entry{statEntry(t, sidB, b)}; !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
	if want := []string{locked}; !reflect.DeepEqual(unreadable, want) {
		t.Errorf("unreadable = %v, want %v", unreadable, want)
	}
}

func TestListRoot_DanglingSlugSymlinkIsUnreadable(t *testing.T) {
	// A slug that is a symlink to a missing dir (an unmounted volume, say)
	// cannot be listed; it is reported so that nothing in it is taken as
	// gone (R-4-7).
	root := t.TempDir()
	slug := filepath.Join(root, "-w-unmounted")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), slug); err != nil {
		t.Fatal(err)
	}
	entries, unreadable := listRoot(t, root)
	if len(entries) != 0 {
		t.Errorf("entries = %+v, want none", entries)
	}
	if want := []string{slug}; !reflect.DeepEqual(unreadable, want) {
		t.Errorf("unreadable = %v, want %v", unreadable, want)
	}
}

func TestListRoot_DuplicateIDNewerMtimeWins(t *testing.T) {
	root := t.TempDir()
	older := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	// sidA: the newer copy is in the first slug; sidB: in the second.
	aFirst := filepath.Join(root, "-w-1", sidA+".jsonl")
	aSecond := filepath.Join(root, "-w-2", sidA+".jsonl")
	bFirst := filepath.Join(root, "-w-1", sidB+".jsonl")
	bSecond := filepath.Join(root, "-w-2", sidB+".jsonl")
	for path, mtime := range map[string]time.Time{aFirst: newer, aSecond: older, bFirst: older, bSecond: newer} {
		writeFile(t, path, lines(t, userText(path)))
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	entries, _ := listRoot(t, root)
	want := []Entry{statEntry(t, sidA, aFirst), statEntry(t, sidB, bSecond)}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
}

func TestOpenTranscript_RegularFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), sidA+".jsonl")
	writeFile(t, p, lines(t, userText("hello")))

	f, e, err := OpenTranscript(p)
	if err != nil {
		t.Fatalf("OpenTranscript: %v", err)
	}
	defer f.Close()
	if want := statEntry(t, sidA, p); e != want {
		t.Errorf("entry = %+v, want %+v", e, want)
	}
}

func TestOpenTranscript_RefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.jsonl")
	writeFile(t, target, lines(t, userText("hello")))
	link := filepath.Join(dir, sidA+".jsonl")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if f, _, err := OpenTranscript(link); err == nil {
		f.Close()
		t.Fatal("OpenTranscript(symlink) succeeded, want an error")
	}
}

func TestOpenTranscript_RefusesFIFOWithoutBlocking(t *testing.T) {
	p := filepath.Join(t.TempDir(), sidA+".jsonl")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, _, err := OpenTranscript(p)
		if err == nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("OpenTranscript(FIFO) succeeded, want an error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OpenTranscript(FIFO) blocked")
	}
}

func TestOpenTranscript_Missing(t *testing.T) {
	if f, _, err := OpenTranscript(filepath.Join(t.TempDir(), sidA+".jsonl")); err == nil {
		f.Close()
		t.Fatal("OpenTranscript(missing) succeeded, want an error")
	}
}
