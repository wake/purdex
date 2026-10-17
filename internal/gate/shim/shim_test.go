package shim

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeExe(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ---- Install / Uninstall ----

func TestInstall_WritesTheShimsAndOneBlockAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, "/opt/pdx/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	dir := DirIn(home)
	for _, tool := range Tools {
		fi, err := os.Stat(filepath.Join(dir, tool))
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Fatalf("shim %s: %v %v", tool, fi, err)
		}
		if s := readFile(t, filepath.Join(dir, tool)); !strings.Contains(s, `gate --auto --tool `+tool+` -- "$@"`) || !strings.Contains(s, "/opt/pdx/bin/pdx") {
			t.Fatalf("shim %s:\n%s", tool, s)
		}
	}
	z := readFile(t, filepath.Join(home, ".zshenv"))
	if strings.Count(z, blockBegin) != 1 || strings.Count(z, blockEnd) != 1 || !strings.Contains(z, dir) {
		t.Fatalf(".zshenv:\n%s", z)
	}
	// second run: nothing changes, not a byte and not a modification time
	before := map[string]time.Time{}
	for _, tool := range Tools {
		fi, _ := os.Stat(filepath.Join(dir, tool))
		before[tool] = fi.ModTime()
	}
	zfi, _ := os.Stat(filepath.Join(home, ".zshenv"))
	time.Sleep(20 * time.Millisecond)
	if err := Install(home, "/opt/pdx/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	if z2 := readFile(t, filepath.Join(home, ".zshenv")); z2 != z {
		t.Fatalf(".zshenv changed on the second run:\n%s", z2)
	}
	zfi2, _ := os.Stat(filepath.Join(home, ".zshenv"))
	if !zfi2.ModTime().Equal(zfi.ModTime()) {
		t.Fatal(".zshenv was rewritten with the same content")
	}
	for _, tool := range Tools {
		fi, _ := os.Stat(filepath.Join(dir, tool))
		if !fi.ModTime().Equal(before[tool]) {
			t.Fatalf("shim %s was rewritten with the same content", tool)
		}
	}
	// a new pdx path rewrites the shims and still leaves one block
	if err := Install(home, "/other/pdx"); err != nil {
		t.Fatal(err)
	}
	if s := readFile(t, filepath.Join(dir, "go")); !strings.Contains(s, "/other/pdx") {
		t.Fatalf("shim not updated:\n%s", s)
	}
	if z3 := readFile(t, filepath.Join(home, ".zshenv")); z3 != z {
		t.Fatalf("the block changed with the pdx path:\n%s", z3)
	}
}

func TestInstall_KeepsTheUsersZshenvAndFollowsASymlink(t *testing.T) {
	home := t.TempDir()
	dotfiles := t.TempDir()
	real := filepath.Join(dotfiles, "zshenv")
	if err := os.WriteFile(real, []byte("export FOO=1\n# mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".zshenv")); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".zshenv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the symlink was replaced: %v %v", fi, err)
	}
	got := readFile(t, real)
	if !strings.HasPrefix(got, "export FOO=1\n# mine\n"+blockBegin) {
		t.Fatalf("the user's lines were not kept in front of the block:\n%s", got)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", fi.Mode().Perm())
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, real); got != "export FOO=1\n# mine\n" {
		t.Fatalf("after uninstall:\n%q", got)
	}
}

func TestStrip(t *testing.T) {
	b := Block("/h/.config/pdx/shims")
	cases := []struct{ name, in, want string }{
		{"only the block", b, ""},
		{"content before and after", "a\n" + b + "b\n", "a\nb\n"},
		{"duplicated", "a\n" + b + "m\n" + b + "z\n", "a\nm\nz\n"},
		{"unclosed at the end", "a\n" + blockBegin + "\n" + strings.Split(b, "\n")[1] + "\n", "a\n"},
		{"unclosed with the user's lines below", "a\n" + blockBegin + "\n" + strings.Split(b, "\n")[1] + "\nkeep=1\n", "a\nkeep=1\n"},
		{"unclosed, then a good block", blockBegin + "\n" + strings.Split(b, "\n")[1] + "\nmid\n" + b, "mid\n"},
		{"begin alone", "a\n" + blockBegin + "\nuser=1\n", "a\nuser=1\n"},
		{"an end marker alone is the user's", "a\n" + blockEnd + "\n", "a\n" + blockEnd + "\n"},
		{"no block", "x\ny", "x\ny"},
	}
	for _, c := range cases {
		if got := Strip(c.in); got != c.want {
			t.Errorf("%s: Strip = %q, want %q", c.name, got, c.want)
		}
	}
}

// A ~/.zshenv that is a symlink and held only our block is emptied, not deleted: the link and its target stay.
func TestUninstall_ASymlinkedZshenvThatHeldOnlyTheBlockIsEmptiedNotRemoved(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "zshenv")
	if err := os.WriteFile(real, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(home, ".zshenv")); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".zshenv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link is gone: %v %v", fi, err)
	}
	if got := readFile(t, real); got != "" {
		t.Fatalf("the target still holds: %q", got)
	}
}

func TestUninstall_RemovesTheBlockTheShimsAndAnOwnFileButNotAForeignOne(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	dir := DirIn(home)
	writeExe(t, filepath.Join(dir, "mytool"), "#!/bin/sh\n") // not ours
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".zshenv")); !os.IsNotExist(err) {
		t.Fatalf(".zshenv held only our block and should be gone: %v", err)
	}
	for _, tool := range Tools {
		if _, err := os.Stat(filepath.Join(dir, tool)); !os.IsNotExist(err) {
			t.Fatalf("shim %s left: %v", tool, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "mytool")); err != nil {
		t.Fatalf("a foreign file in the shim dir was removed: %v", err)
	}
	if err := Uninstall(home); err != nil { // nothing left to do is not an error
		t.Fatal(err)
	}
}

// ---- Resolve ----

func TestResolve(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	writeExe(t, filepath.Join(shimDir, "go"), "#!/bin/sh\n")
	realA := filepath.Join(root, "a")
	writeExe(t, filepath.Join(realA, "go"), "#!/bin/sh\n")

	got, err := Resolve("go", shimDir+":"+realA, shimDir)
	if err != nil || got != filepath.Join(realA, "go") {
		t.Fatalf("plain: %q %v", got, err)
	}

	// a symlink alias of the shim is refused, whatever its name or directory
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(alias, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(shimDir, "go"), filepath.Join(alias, "go")); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve("go", alias+":"+realA, shimDir); err != nil || got != filepath.Join(realA, "go") {
		t.Fatalf("symlinked shim: %q %v", got, err)
	}
	// a directory that is a symlink to the shim directory is the shim directory
	link := filepath.Join(root, "linkdir")
	if err := os.Symlink(shimDir, link); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve("go", link+":"+realA, shimDir); err != nil || got != filepath.Join(realA, "go") {
		t.Fatalf("symlinked dir: %q %v", got, err)
	}
	// nothing but the shims: an error, never the shim
	if got, err := Resolve("go", link+":"+shimDir+":"+alias, shimDir); !errors.Is(err, ErrNoRealBinary) || got != "" {
		t.Fatalf("only shims: %q %v", got, err)
	}
	// empty entries (= the current directory), directories named like the tool and non-executables are not binaries
	cwd := filepath.Join(root, "cwd")
	writeExe(t, filepath.Join(cwd, "go"), "#!/bin/sh\n")
	t.Chdir(cwd)
	noexec := filepath.Join(root, "noexec")
	if err := os.MkdirAll(noexec, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noexec, "go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dirgo", "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve("go", ":"+noexec+":"+filepath.Join(root, "dirgo")+":"+realA, shimDir); err != nil || got != filepath.Join(realA, "go") {
		t.Fatalf("skips: %q %v", got, err)
	}
}

// The binary is looked up at every run from that run's PATH: a switch of node versions is followed.
func TestResolve_FollowsAChangedPath(t *testing.T) {
	root := t.TempDir()
	shimDir := filepath.Join(root, "shims")
	writeExe(t, filepath.Join(shimDir, "pnpm"), "#!/bin/sh\n")
	n20 := filepath.Join(root, "node20", "bin")
	n24 := filepath.Join(root, "node24", "bin")
	writeExe(t, filepath.Join(n20, "pnpm"), "#!/bin/sh\n")
	writeExe(t, filepath.Join(n24, "pnpm"), "#!/bin/sh\n")
	if got, _ := Resolve("pnpm", shimDir+":"+n20, shimDir); got != filepath.Join(n20, "pnpm") {
		t.Fatalf("node 20: %q", got)
	}
	if got, _ := Resolve("pnpm", shimDir+":"+n24+":"+n20, shimDir); got != filepath.Join(n24, "pnpm") {
		t.Fatalf("node 24: %q", got)
	}
}

// ---- the shim script and a real zsh ----

func requireTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("no %s", name)
	}
	return p
}

func run(t *testing.T, env []string, name string, args ...string) (string, string, error) {
	t.Helper()
	// a shim that execs itself again forever must fail the test, not hang it
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// The shim hands the call to `pdx gate --auto --tool <name> -- args`; when pdx is gone it runs the real command itself.
func TestScript_HandsOverToGateAndFallsBackToTheRealBinary(t *testing.T) {
	sh := requireTool(t, "sh")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	fakePdx := filepath.Join(root, "bin", "pdx")
	writeExe(t, fakePdx, "#!/bin/sh\necho \"pdx $*\"\n")
	if err := Install(home, fakePdx); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	writeExe(t, filepath.Join(realDir, "go"), "#!/bin/sh\necho \"real go $*\"\n")
	shimGo := filepath.Join(DirIn(home), "go")
	env := []string{"PATH=" + DirIn(home) + ":" + realDir + ":/usr/bin:/bin", "HOME=" + home}

	out, _, err := run(t, env, sh, shimGo, "build", "./...")
	if err != nil || strings.TrimSpace(out) != "pdx gate --auto --tool go -- build ./..." {
		t.Fatalf("with pdx: %q %v", out, err)
	}
	// PDX_BIN wins over the path written into the shim
	other := filepath.Join(root, "other-pdx")
	writeExe(t, other, "#!/bin/sh\necho \"other $*\"\n")
	out, _, _ = run(t, append(env, "PDX_BIN="+other), sh, shimGo, "version")
	if strings.TrimSpace(out) != "other gate --auto --tool go -- version" {
		t.Fatalf("PDX_BIN: %q", out)
	}
	// pdx removed: the real go, not the shim again, and its exit code and arguments are kept
	if err := os.Remove(fakePdx); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, env, sh, shimGo, "test", "-run", "X y")
	if err != nil || strings.TrimSpace(out) != "real go test -run X y" {
		t.Fatalf("fallback: %q %v", out, err)
	}
	// pdx removed and an alias of the shim earlier on PATH (a symlink in another directory): skipped, not run again
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(alias, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shimGo, filepath.Join(alias, "go")); err != nil {
		t.Fatal(err)
	}
	aliasEnv := []string{"PATH=" + alias + ":" + DirIn(home) + ":" + realDir + ":/usr/bin:/bin", "HOME=" + home}
	out, _, err = run(t, aliasEnv, sh, shimGo, "env")
	if err != nil || strings.TrimSpace(out) != "real go env" {
		t.Fatalf("alias fallback: %q %v", out, err)
	}
	// nothing else on PATH: a clear failure (127), not a loop
	_, errOut, err := run(t, []string{"PATH=" + DirIn(home) + ":/usr/bin:/bin", "HOME=" + home}, sh, shimGo)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 127 || !strings.Contains(errOut, "no go on PATH") {
		t.Fatalf("no real go: %v %q", err, errOut)
	}
}

// A fresh `zsh -c` resolves the tool to the shim, a nested one does not grow PATH, and the block prints nothing.
func TestZshenvBlock_WithARealZsh(t *testing.T) {
	zsh := requireTool(t, "zsh")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, "/p/pdx"); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	writeExe(t, filepath.Join(realDir, "go"), "#!/bin/sh\n")
	env := []string{"HOME=" + home, "ZDOTDIR=" + home, "PATH=" + realDir + ":/usr/bin:/bin"}

	out, errOut, err := run(t, env, zsh, "-c", "command -v go")
	if err != nil || strings.TrimSpace(out) != filepath.Join(DirIn(home), "go") || errOut != "" {
		t.Fatalf("command -v go: %q %q %v", out, errOut, err)
	}
	out, errOut, err = run(t, env, zsh, "-c", ":")
	if err != nil || out != "" || errOut != "" {
		t.Fatalf("the block printed: %q %q %v", out, errOut, err)
	}
	out, _, err = run(t, env, zsh, "-c", `zsh -c 'zsh -c "print -r -- \$PATH"'`)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, DirIn(home)); n != 1 {
		t.Fatalf("the shim dir is on PATH %d times after nested shells: %s", n, out)
	}
	// after the uninstall a fresh zsh finds the user's own go again
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	out, _, _ = run(t, env, zsh, "-c", "command -v go")
	if strings.TrimSpace(out) != filepath.Join(realDir, "go") {
		t.Fatalf("after uninstall: %q", out)
	}
}

// ---- review fixes ----

// A wrapper of the user's own with a shim's name (or a symlink there) is not overwritten: Install says which and writes nothing.
func TestInstall_RefusesAForeignFileOrSymlinkBeforeWritingAnything(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		home := t.TempDir()
		dir := DirIn(home)
		writeExe(t, filepath.Join(dir, "pnpm"), "#!/bin/sh\necho mine\n")
		if kind == "symlink" {
			mine := filepath.Join(t.TempDir(), "my-pnpm")
			writeExe(t, mine, "#!/bin/sh\necho mine\n")
			if err := os.Remove(filepath.Join(dir, "pnpm")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(mine, filepath.Join(dir, "pnpm")); err != nil {
				t.Fatal(err)
			}
		}
		err := Install(home, "/p/pdx")
		var ce *ConflictError
		if !errors.As(err, &ce) || len(ce.Paths) != 1 || ce.Paths[0] != filepath.Join(dir, "pnpm") {
			t.Fatalf("%s: err = %v", kind, err)
		}
		for _, tool := range Tools {
			if tool == "pnpm" {
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, tool)); !os.IsNotExist(err) {
				t.Fatalf("%s: shim %s written although another target conflicts", kind, tool)
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".zshenv")); !os.IsNotExist(err) {
			t.Fatalf("%s: .zshenv written although a target conflicts", kind)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "pnpm")); err != nil || !strings.Contains(string(b), "echo mine") {
			t.Fatalf("%s: the user's file is gone: %q %v", kind, b, err)
		}
		// and Uninstall leaves it alone too
		if err := Uninstall(home); err != nil {
			t.Fatal(err)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "pnpm")); err != nil || !strings.Contains(string(b), "echo mine") {
			t.Fatalf("%s: uninstall removed the user's file: %q %v", kind, b, err)
		}
	}
}

// The shim started through a symlink in another directory (pdx gone) still finds the real tool and does not exec itself again.
func TestScript_StartedThroughAnAliasWithoutPdxRunsTheRealTool(t *testing.T) {
	sh := requireTool(t, "sh")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := Install(home, filepath.Join(root, "no-such-pdx")); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(root, "real")
	writeExe(t, filepath.Join(realDir, "go"), "#!/bin/sh\necho \"real go $*\"\n")
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(alias, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(DirIn(home), "go"), filepath.Join(alias, "go")); err != nil {
		t.Fatal(err)
	}
	// a second alias: with the script's place taken from the name it was started by, the two would start each other forever
	alias2 := filepath.Join(root, "alias2")
	if err := os.MkdirAll(alias2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(DirIn(home), "go"), filepath.Join(alias2, "go")); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + alias2 + ":" + alias + ":" + DirIn(home) + ":" + realDir + ":/usr/bin:/bin", "HOME=" + home}
	out, _, err := run(t, env, sh, filepath.Join(alias, "go"), "version")
	if err != nil || strings.TrimSpace(out) != "real go version" {
		t.Fatalf("started through the alias: %q %v", out, err)
	}
}

// A ~/.zshenv that is a dangling symlink (the dotfiles repo is not cloned yet) keeps being a link: its target is created.
func TestInstall_ADanglingZshenvSymlinkKeepsTheLinkAndCreatesTheTarget(t *testing.T) {
	for _, relative := range []bool{false, true} {
		home := t.TempDir()
		dot := filepath.Join(home, "dotfiles")
		target := filepath.Join(dot, "zshenv")
		link := target
		if relative {
			link = filepath.Join("dotfiles", "zshenv")
		}
		if err := os.Symlink(link, filepath.Join(home, ".zshenv")); err != nil {
			t.Fatal(err)
		}
		if err := Install(home, "/p/pdx"); err != nil {
			t.Fatalf("relative=%v: %v", relative, err)
		}
		if fi, err := os.Lstat(filepath.Join(home, ".zshenv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("relative=%v: the link was replaced: %v %v", relative, fi, err)
		}
		if got := readFile(t, target); !strings.Contains(got, blockBegin) {
			t.Fatalf("relative=%v: the target has no block: %q", relative, got)
		}
		if err := Uninstall(home); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Lstat(filepath.Join(home, ".zshenv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("relative=%v: uninstall removed the link: %v %v", relative, fi, err)
		}
	}
}

// Two `pdx setup` at once, with different pdx paths, end with shims that all name one of them, never a mixture.
func TestInstall_ConcurrentSetupsLeaveOneConsistentSet(t *testing.T) {
	for round := 0; round < 10; round++ {
		home := t.TempDir()
		done := make(chan error, 2)
		for _, p := range []string{"/a/pdx", "/b/pdx"} {
			go func(p string) { done <- Install(home, p) }(p)
		}
		for i := 0; i < 2; i++ {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}
		seen := map[string]bool{}
		for _, tool := range Tools {
			s := readFile(t, filepath.Join(DirIn(home), tool))
			for _, p := range []string{"/a/pdx", "/b/pdx"} {
				if strings.Contains(s, p) {
					seen[p] = true
				}
			}
		}
		if len(seen) != 1 {
			t.Fatalf("round %d: the shims name %v", round, seen)
		}
		if z := readFile(t, filepath.Join(home, ".zshenv")); strings.Count(z, blockBegin) != 1 {
			t.Fatalf("round %d: .zshenv:\n%s", round, z)
		}
	}
}

// Ownership is "byte for byte what pdx writes": a file that only carries a shim's comment, or a shim someone edited, is the
// user's. A pdx path with every character the quoting handles is still recognized on the next run.
func TestOwnership_IsExactNotAMarker(t *testing.T) {
	home := t.TempDir()
	weird := "/p a/\"q\"/$x/`y`/\\z/pdx"
	if err := Install(home, weird); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, weird); err != nil { // recognized as its own, not a conflict
		t.Fatalf("second install: %v", err)
	}
	p := filepath.Join(DirIn(home), "go")
	if !owned(p, "go") {
		t.Fatal("a shim pdx wrote for a path with quotes and dollars is not recognized")
	}
	if got, ok := pdxPathOf(readFile(t, p)); !ok || got != weird {
		t.Fatalf("pdxPathOf = %q, %v", got, ok)
	}
	if _, ok := pdxPathOf("#!/bin/sh\necho nothing\n"); ok {
		t.Fatal("a file without the PDX_BIN line has a pdx path")
	}
	if err := Install(t.TempDir(), ""); err == nil {
		t.Fatal("an empty pdx path was accepted")
	}
	// a user's file with the marker comment in it
	forged := "#!/bin/sh\n# (pdx gate shim for go) -- my own wrapper, keep\nexec /usr/local/go/bin/go \"$@\"\n"
	if err := os.WriteFile(p, []byte(forged), 0o755); err != nil {
		t.Fatal(err)
	}
	var ce *ConflictError
	if err := Install(home, weird); !errors.As(err, &ce) {
		t.Fatalf("a forged marker was taken for a shim: %v", err)
	}
	if err := Uninstall(home); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != forged {
		t.Fatalf("uninstall touched the user's file: %q", got)
	}
	// a pdx shim somebody edited
	if err := os.WriteFile(p, []byte(Script("go", weird)+"echo edited\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Install(home, weird); !errors.As(err, &ce) {
		t.Fatalf("an edited shim was overwritten: %v", err)
	}
}

// Herd keeps composer in "~/Library/Application Support/Herd/bin": a directory with a space in its name must resolve, and the
// shim's own fallback must find the tool there too (word splitting on spaces would lose it).
func TestSpacesInPaths(t *testing.T) {
	sh := requireTool(t, "sh")
	root := t.TempDir()
	home := filepath.Join(root, "my home") // spaces in the shim directory's path as well
	if err := Install(home, filepath.Join(root, "no such pdx")); err != nil {
		t.Fatal(err)
	}
	herd := filepath.Join(root, "Library", "Application Support", "Herd", "bin")
	writeExe(t, filepath.Join(herd, "composer"), "#!/bin/sh\necho \"herd composer $*\"\n")

	pathEnv := DirIn(home) + ":" + herd + ":/usr/bin:/bin"
	got, err := Resolve("composer", pathEnv, DirIn(home))
	if err != nil || got != filepath.Join(herd, "composer") {
		t.Fatalf("Resolve: %q %v", got, err)
	}
	// the shim without pdx runs it, with the arguments as they came
	out, _, err := run(t, []string{"PATH=" + pathEnv, "HOME=" + home}, sh, filepath.Join(DirIn(home), "composer"), "test", "--", "--filter", "Foo Bar")
	if err != nil || strings.TrimSpace(out) != "herd composer test -- --filter Foo Bar" {
		t.Fatalf("shim fallback: %q %v", out, err)
	}
	// and with a pdx whose path has a space in it too
	fakePdx := filepath.Join(root, "pdx dir", "pdx")
	writeExe(t, fakePdx, "#!/bin/sh\necho \"pdx $*\"\n")
	if err := Install(home, fakePdx); err != nil {
		t.Fatal(err)
	}
	out, _, err = run(t, []string{"PATH=" + pathEnv, "HOME=" + home}, sh, filepath.Join(DirIn(home), "composer"), "install")
	if err != nil || strings.TrimSpace(out) != "pdx gate --auto --tool composer -- install" {
		t.Fatalf("with pdx: %q %v", out, err)
	}
}

func TestToolsHaveComposerAndNotThePHPRunners(t *testing.T) {
	have := map[string]bool{}
	for _, tool := range Tools {
		have[tool] = true
	}
	for _, want := range []string{"go", "pnpm", "npx", "vitest", "tsc", "composer", "xcodebuild", "swift", "swift-build", "swift-test"} {
		if !have[want] {
			t.Errorf("no shim for %s", want)
		}
	}
	for _, not := range []string{"phpunit", "pest"} {
		if have[not] {
			t.Errorf("%s lives in vendor/bin, not on PATH: no shim", not)
		}
	}
}
