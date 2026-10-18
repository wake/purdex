package clones

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func mkRepo(t *testing.T, gitAsFile bool) string {
	t.Helper()
	dir := t.TempDir()
	g := filepath.Join(dir, ".git")
	var err error
	if gitAsFile { // a linked worktree has a .git file
		err = os.WriteFile(g, []byte("gitdir: "+t.TempDir()+"\n"), 0o644)
	} else {
		err = os.Mkdir(g, 0o755)
	}
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestVerifiedKeepsOnlyOwnedGitCheckouts(t *testing.T) {
	plain := t.TempDir() // a directory that is not a repo
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	good, worktree := mkRepo(t, false), mkRepo(t, true)
	got := Verified(map[string]string{
		"purdex":   good,
		"wt":       worktree,
		"plain":    plain,
		"missing":  filepath.Join(t.TempDir(), "nope"),
		"notadir":  file,
		"relative": "some/where",
		"empty":    "",
	})
	if len(got) != 2 || got["purdex"] != good || got["wt"] != worktree {
		t.Fatalf("%v", got)
	}
}

// A .git file counts only as a linked worktree's pointer: "gitdir: <dir>" whose
// target is a directory (relative to the checkout).
func TestGitFileMustPointToARealGitDir(t *testing.T) {
	target := t.TempDir()
	for _, tc := range []struct {
		name, content string
		want          bool
	}{
		{"pointer", "gitdir: " + target + "\n", true},
		{"empty", "", false},
		{"plain text", "hello\n", false},
		{"missing target", "gitdir: " + filepath.Join(target, "nope") + "\n", false},
		{"target is a file", "gitdir: " + filepath.Join(t.TempDir(), "f") + "\n", false},
		{"no value", "gitdir:\n", false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte(tc.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := len(Verified(map[string]string{"r": dir})) == 1; got != tc.want {
			t.Errorf("%s: verified=%v want %v", tc.name, got, tc.want)
		}
	}
	// a relative pointer is read from the checkout
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: meta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(Verified(map[string]string{"r": dir})) != 1 {
		t.Error("a relative gitdir pointer")
	}
}

// Whatever the config holds, a path the owner does not own is not a prepared clone.
func TestOwnedByThisUser(t *testing.T) {
	d := mkRepo(t, false)
	if !ownedByMe(d) {
		t.Fatal("a directory this process made is its own")
	}
	if ownedByMe("/") && os.Getuid() != 0 {
		t.Fatal("/ is not owned by a normal user")
	}
}

func TestSourceNamesAreSortedAndRecheckedEveryCall(t *testing.T) {
	a, b := mkRepo(t, false), mkRepo(t, false)
	s := NewSource(map[string]string{"zeta": a, "alpha": b})
	if got := s.Names(); !slices.Equal(got, []string{"alpha", "zeta"}) {
		t.Fatalf("%v", got)
	}
	// the clone is removed: the label goes with it, no error
	if err := os.RemoveAll(filepath.Join(a, ".git")); err != nil {
		t.Fatal(err)
	}
	if got := s.Names(); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("after removal %v", got)
	}
	if got := NewSource(nil).Names(); len(got) != 0 {
		t.Fatalf("no repos configured: %v", got)
	}
}
