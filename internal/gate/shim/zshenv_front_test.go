package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// zshHome is a temporary HOME (also ZDOTDIR) with the shims installed, and a directory of "real" tools; the real ~/.zshenv is
// never touched.
func zshHome(t *testing.T) (zsh, home, realDir string, env []string) {
	t.Helper()
	zsh = requireTool(t, "zsh")
	root := t.TempDir()
	home = filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	realDir = filepath.Join(root, "real")
	writeExe(t, filepath.Join(realDir, "go"), "#!/bin/sh\n")
	env = []string{"HOME=" + home, "ZDOTDIR=" + home}
	return
}

// (a) a PATH that has the shim dir, but behind other directories (what a login shell leaves), has it first after `zsh -c`.
// (b) and a nested shell does not grow it.
func TestZshenvBlock_MovesAnExistingEntryToTheFrontWithoutGrowingPATH(t *testing.T) {
	zsh, home, realDir, env := zshHome(t)
	dir := DirIn(home)
	env = append(env, "PATH="+realDir+":/usr/bin:"+dir+":/bin")
	out, errOut, err := run(t, env, zsh, "-c", "command -v go; print -r -- $PATH")
	if err != nil || errOut != "" {
		t.Fatal(out, errOut, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[0] != filepath.Join(dir, "go") {
		t.Fatalf("go resolves to %s", lines[0])
	}
	if want := dir + ":" + realDir + ":/usr/bin:/bin"; lines[1] != want {
		t.Fatalf("PATH = %s, want %s", lines[1], want)
	}
	out, _, err = run(t, env, zsh, "-c", `zsh -c 'zsh -c "print -r -- \$PATH"'`)
	if err != nil || strings.TrimSpace(out) != dir+":"+realDir+":/usr/bin:/bin" {
		t.Fatalf("nested PATH = %q %v", out, err)
	}
}

// (c) .zshrc puts other directories in front (brew, nvm); the interactive shell has the shim dir first by the first prompt, and
// prints nothing of its own.
func TestZshenvBlock_InteractiveShellPutsTheShimDirBackBeforeThePrompt(t *testing.T) {
	zsh, home, realDir, env := zshHome(t)
	dir := DirIn(home)
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte("path=("+realDir+" $path)\nPS1=''\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env = append(env, "PATH=/usr/bin:/bin")
	cmd := exec.Command(zsh, "-i")
	cmd.Env = env
	cmd.Stdin = strings.NewReader("print -r -- FIRST=${path[1]}\nprint -r -- CMD=$(command -v go)\nexit\n")
	b, _ := cmd.Output()
	out := string(b)
	if !strings.Contains(out, "FIRST="+dir+"\n") || !strings.Contains(out, "CMD="+filepath.Join(dir, "go")+"\n") {
		t.Fatalf("interactive shell:\n%s", out)
	}
	// without the precmd (a non-interactive shell reading the same .zshrc through ZDOTDIR does not run it) nothing prints
	if out, errOut, err := run(t, env, zsh, "-c", ":"); err != nil || out != "" || errOut != "" {
		t.Fatalf("the block printed: %q %q %v", out, errOut, err)
	}
}

// (d) the first version's one-line block is replaced by the current one, and the text outside is not changed by a byte.
func TestInstall_UpgradesTheOldOneLineBlock(t *testing.T) {
	home := t.TempDir()
	dir := DirIn(home)
	old := blockBegin + "\n" + `case ":$PATH:" in *":` + dir + `:"*) ;; *) PATH="` + dir + `:$PATH" ;; esac` + "\n" + blockEnd + "\n"
	before, after := "export A=1\n# mine\n", "export B=2"
	if err := os.WriteFile(filepath.Join(home, ".zshenv"), []byte(before+old+after), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	z := readFile(t, filepath.Join(home, ".zshenv"))
	if z != before+after+"\n"+Block(dir) {
		t.Fatalf(".zshenv:\n%s", z)
	}
	if strings.Contains(z, "case ") || strings.Count(z, blockBegin) != 1 {
		t.Fatalf("old block left:\n%s", z)
	}
	if err := Install(home, "/p/pdx"); err != nil || readFile(t, filepath.Join(home, ".zshenv")) != z {
		t.Fatal("a second run changed the file")
	}
}

func TestStrip_OldAndNewUnclosedBlocks(t *testing.T) {
	d := "/h/.config/pdx/shims"
	oldBody := `case ":$PATH:" in *":` + d + `:"*) ;; *) PATH="` + d + `:$PATH" ;; esac` + "\n"
	body := strings.Join(blockBody(d), "\n") + "\n"
	cases := []struct{ name, in, want string }{
		{"old one-line, closed", blockBegin + "\n" + oldBody + blockEnd + "\n", ""},
		{"old one-line, unclosed, user's line below", "a\n" + blockBegin + "\n" + oldBody + "keep=1\n", "a\nkeep=1\n"},
		{"new multi-line, unclosed at the end", "a\n" + blockBegin + "\n" + body, "a\n"},
		{"new multi-line, unclosed, user's line below", "a\n" + blockBegin + "\n" + body + "keep=1\n", "a\nkeep=1\n"},
		{"new, cut short in the middle", "a\n" + blockBegin + "\n" + strings.Join(blockBody(d)[:3], "\n") + "\nkeep=1\n", "a\nkeep=1\n"},
		{"a user's own fi after an unclosed marker stays", "a\n" + blockBegin + "\nfi\n", "a\nfi\n"},
		{"new, closed, twice", Block(d) + "m\n" + Block(d), "m\n"},
	}
	for _, c := range cases {
		if got := Strip(c.in); got != c.want {
			t.Errorf("%s: Strip = %q, want %q", c.name, got, c.want)
		}
	}
}
