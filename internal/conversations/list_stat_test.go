package conversations

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

// failingEntry is a slug dir member whose lstat (Info) fails with err.
type failingEntry struct {
	name string
	err  error
}

func (e failingEntry) Name() string               { return e.name }
func (e failingEntry) IsDir() bool                { return false }
func (e failingEntry) Type() fs.FileMode          { return 0 }
func (e failingEntry) Info() (fs.FileInfo, error) { return nil, e.err }

func TestTranscriptEntry_StatErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want memberKind
	}{
		{"removed since the listing", fs.ErrNotExist, memberVanished},
		{"ENOENT", syscall.ENOENT, memberVanished},
		{"EIO", syscall.EIO, memberUnknown},
		{"permission", fs.ErrPermission, memberUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, kind := transcriptEntry("/r/-w", failingEntry{name: sidA + ".jsonl", err: c.err})
			if e != (Entry{}) || kind != c.want {
				t.Errorf("transcriptEntry = %+v, %d; want no entry, %d", e, kind, c.want)
			}
		})
	}
}

func TestAddSlug_KeepsWhatItReadAndReportsUnknown(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, sidA+".jsonl")
	writeFile(t, a, lines(t, userText("a")))
	members, err := os.ReadDir(dir)
	if err != nil || len(members) != 1 {
		t.Fatalf("ReadDir = %v, %v", members, err)
	}

	t.Run("an unknown member", func(t *testing.T) {
		byID := map[string]Entry{}
		files := []fs.DirEntry{failingEntry{name: sidB + ".jsonl", err: syscall.EIO}, members[0]}
		if !addSlug(byID, dir, files) {
			t.Error("addSlug = false, want true (a member could not be stat'ed)")
		}
		if want := map[string]Entry{sidA: statEntry(t, sidA, a)}; !reflect.DeepEqual(byID, want) {
			t.Errorf("byID = %+v, want %+v", byID, want)
		}
	})
	t.Run("a member removed since the listing", func(t *testing.T) {
		byID := map[string]Entry{}
		files := []fs.DirEntry{failingEntry{name: sidB + ".jsonl", err: fs.ErrNotExist}, members[0]}
		if addSlug(byID, dir, files) {
			t.Error("addSlug = true, want false (a removed file is gone)")
		}
		if want := map[string]Entry{sidA: statEntry(t, sidA, a)}; !reflect.DeepEqual(byID, want) {
			t.Errorf("byID = %+v, want %+v", byID, want)
		}
	})
}

func TestAddSlug_MemberVanishedWithItsSlugDir(t *testing.T) {
	// The slug dir itself went away after it was listed (renamed, unmounted,
	// removed): every member's lstat says "not exist", but that proves
	// nothing about the transcripts (R-4-7).
	base := t.TempDir()
	notADir := filepath.Join(base, "now-a-file")
	writeFile(t, notADir, []byte("x"))
	for name, dir := range map[string]string{
		"dir gone":          filepath.Join(base, "gone"),
		"dir now a file":    notADir,
		"dir behind a file": filepath.Join(notADir, "slug"),
	} {
		t.Run(name, func(t *testing.T) {
			byID := map[string]Entry{}
			files := []fs.DirEntry{failingEntry{name: sidA + ".jsonl", err: fs.ErrNotExist}}
			if !addSlug(byID, dir, files) {
				t.Error("addSlug = false, want true (the slug dir is not there to prove the file removed)")
			}
			if len(byID) != 0 {
				t.Errorf("byID = %+v, want empty", byID)
			}
		})
	}
}

func TestListRoot_SlugWhoseMembersCannotBeStatted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root stats members of a dir without search permission")
	}
	root := t.TempDir()
	// Readable (its names list) but not searchable: every lstat of a member
	// fails with EACCES.
	locked := filepath.Join(root, "-w-locked")
	writeFile(t, filepath.Join(locked, sidA+".jsonl"), lines(t, userText("a")))
	writeFile(t, filepath.Join(locked, sidC+".jsonl"), lines(t, userText("c")))
	b := filepath.Join(root, "-w-open", sidB+".jsonl")
	writeFile(t, b, lines(t, userText("b")))
	if err := os.Chmod(locked, 0o400); err != nil {
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
		t.Errorf("unreadable = %v, want %v (once)", unreadable, want)
	}
}
