package shim

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WWD-9 S0-b: RemoveBlock takes the pdx gate block out of ~/.zshenv and nothing else, and does not write a shim.

func zshenvOf(t *testing.T, home string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".zshenv"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRemoveBlock_TakesOutTheBlockAndLeavesTheRestByteForByte(t *testing.T) {
	block := Block("/h/.config/pdx/shims")
	cases := []struct{ name, in, want string }{
		{"in the middle", "a=1\n" + block + "b=2\n", "a=1\nb=2\n"},
		{"at the start", block + "b=2\n", "b=2\n"},
		{"at the end", "a=1\n" + block, "a=1\n"},
		{"no newline after the end marker", "a=1\n" + strings.TrimSuffix(block, "\n"), "a=1\n"},
		{"the user's own text has no final newline and is kept as it is", "a=1\n" + block + "b=2", "a=1\nb=2"},
		{"two blocks from an old double install", block + "mid\n" + block, "mid\n"},
		{"CRLF lines", "a=1\r\n# >>> pdx gate >>>\r\nx\r\n# <<< pdx gate <<<\r\nb=2\r\n", "a=1\r\nb=2\r\n"},
		{"the first version's closed one-line block", "a\n" + blockBegin + "\ncase \":$PATH:\" in *\":/x:\"*) ;; *) PATH=\"/x:$PATH\" ;; esac\n" + blockEnd + "\nb\n", "a\nb\n"},
		{"only blank lines are left outside the block: they are the user's, the file stays (codex R1)", "\n" + block + "\n", "\n\n"},
		{"only spaces are left outside the block", "  \n" + block, "  \n"},
		{"blank lines around the block are the user's", "x=1\n\n" + block + "\ny=2\n", "x=1\n\n\ny=2\n"},
	}
	for _, c := range cases {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(c.in), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := RemoveBlock(home)
		if err != nil || out != BlockRemoved {
			t.Errorf("%s: %v %v", c.name, out, err)
			continue
		}
		if got := zshenvOf(t, home); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if out, err := RemoveBlock(home); err != nil || out != NoBlock {
			t.Errorf("%s: a second run: %v %v", c.name, out, err)
		}
		if got := zshenvOf(t, home); got != c.want {
			t.Errorf("%s: the second run changed the file: %q", c.name, got)
		}
	}
}

// A block with no end marker is not ours to guess at: the whole file is left as it is, also when a complete block is there too.
func TestRemoveBlock_AnUnclosedBlockLeavesTheWholeFile(t *testing.T) {
	block := Block("/h/.config/pdx/shims")
	body := strings.Split(block, "\n")[1]
	for name, in := range map[string]string{
		"unclosed alone":                 "a=1\n" + blockBegin + "\n" + body + "\nkeep=1\n",
		"unclosed at the end":            "a=1\n" + blockBegin + "\n",
		"unclosed, then a good block":    blockBegin + "\n" + body + "\nmid\n" + block,
		"a good block, then an unclosed": block + "mid\n" + blockBegin + "\n" + body + "\n",
	} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := RemoveBlock(home)
		if err != nil || out != BlockUnclosed {
			t.Errorf("%s: %v %v", name, out, err)
		}
		if got := zshenvOf(t, home); got != in {
			t.Errorf("%s: the file was changed: %q", name, got)
		}
	}
}

func TestRemoveBlock_NothingToDo(t *testing.T) {
	// no file: no file is made, and no directory either (not even the lock's)
	home := t.TempDir()
	if out, err := RemoveBlock(home); err != nil || out != NoBlock {
		t.Fatalf("%v %v", out, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("something was made in the home: %v", entries)
	}
	// a file with no block, or with an end marker and no begin, is not touched
	for name, in := range map[string]string{
		"no block":                 "a=1\nb=2\n",
		"an end marker alone":      "a=1\n" + blockEnd + "\nb=2\n",
		"empty":                    "",
		"the words in other lines": "# pdx gate is nice\n",
	} {
		if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := RemoveBlock(home); err != nil || out != NoBlock {
			t.Errorf("%s: %v %v", name, out, err)
		}
		if got := zshenvOf(t, home); got != in {
			t.Errorf("%s: changed: %q", name, got)
		}
	}
}

// A file that held nothing but the block is removed when it is a plain file, and emptied (never removed) when it is a symbolic link, so
// the link and what it points to stay where the user put them.
func TestRemoveBlock_AFileOfJustTheBlock(t *testing.T) {
	block := Block("/h/.config/pdx/shims")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := RemoveBlock(home); err != nil || out != BlockRemoved {
		t.Fatalf("%v %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshenv")); !os.IsNotExist(err) {
		t.Fatalf("a plain file of just the block is removed: %v", err)
	}

	home = t.TempDir()
	target := filepath.Join(home, "dotfiles", "zshenv")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".zshenv")); err != nil {
		t.Fatal(err)
	}
	if out, err := RemoveBlock(home); err != nil || out != BlockRemoved {
		t.Fatalf("%v %v", out, err)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".zshenv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link is still a link: %v %v", fi, err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "" {
		t.Fatalf("the target is emptied, not removed: %q %v", b, err)
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o600 {
		t.Fatalf("the target keeps its mode: %v", fi.Mode().Perm())
	}
}

// It does not write a shim and does not touch the shims that are there (the shells that started before the setup still have them first
// on PATH, and with the gate a pass-through they are harmless until 9a-1 removes the directory).
func TestRemoveBlock_LeavesTheShimsAlone(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	before := map[string]string{}
	for _, tool := range Tools {
		b, err := os.ReadFile(filepath.Join(DirIn(home), tool))
		if err != nil {
			t.Fatal(err)
		}
		before[tool] = string(b)
	}
	if out, err := RemoveBlock(home); err != nil || out != BlockRemoved {
		t.Fatalf("%v %v", out, err)
	}
	for _, tool := range Tools {
		b, err := os.ReadFile(filepath.Join(DirIn(home), tool))
		if err != nil || string(b) != before[tool] {
			t.Errorf("the shim %s changed or went: %v", tool, err)
		}
	}
	if entries, _ := os.ReadDir(DirIn(home)); len(entries) != len(Tools) {
		t.Errorf("%d files in the shim directory, want %d", len(entries), len(Tools))
	}
}

// A directory that cannot be written is an error for the caller to say; the file is as it was.
func TestRemoveBlock_AnUnwritableHomeIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	home := t.TempDir()
	in := "a=1\n" + Block("/h/.config/pdx/shims")
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	if _, err := RemoveBlock(home); err == nil {
		t.Fatal("no error for a home that cannot be written")
	}
	if got := zshenvOf(t, home); got != in {
		t.Fatalf("the file changed: %q", got)
	}
}
