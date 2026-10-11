package gitbranch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t testing.TB, p, s string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(s), 0o644))
}

func TestBranch(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "repo/.git/HEAD"), "ref: refs/heads/feat/x\n")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "repo/a/b"), 0o755))
	assert.Equal(t, "feat/x", Branch(filepath.Join(root, "repo"), root))
	assert.Equal(t, "feat/x", Branch(filepath.Join(root, "repo/a/b"), root), "nested dir")

	write(t, filepath.Join(root, "det/.git/HEAD"), "abc1234def5678abc1234def5678abc1234def56\n")
	assert.Equal(t, "@abc1234", Branch(filepath.Join(root, "det"), root))

	// worktree: .git file with relative gitdir
	write(t, filepath.Join(root, "main/.git/worktrees/w/HEAD"), "ref: refs/heads/wt\n")
	write(t, filepath.Join(root, "wt/.git"), "gitdir: ../main/.git/worktrees/w\n")
	assert.Equal(t, "wt", Branch(filepath.Join(root, "wt"), root))
	// absolute gitdir
	write(t, filepath.Join(root, "wt2/.git"), "gitdir: "+filepath.Join(root, "main/.git/worktrees/w")+"\n")
	assert.Equal(t, "wt", Branch(filepath.Join(root, "wt2"), root))

	// not a repo / empty / unreadable
	require.NoError(t, os.MkdirAll(filepath.Join(root, "plain"), 0o755))
	assert.Equal(t, "", Branch(filepath.Join(root, "plain"), root))
	assert.Equal(t, "", Branch("", root))
	assert.Equal(t, "", Branch("relative/path", root))
	write(t, filepath.Join(root, "bad/.git/HEAD"), "garbage")
	assert.Equal(t, "", Branch(filepath.Join(root, "bad"), root))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nohead/.git"), 0o755))
	assert.Equal(t, "", Branch(filepath.Join(root, "nohead"), root))
	write(t, filepath.Join(root, "badptr/.git"), "nonsense")
	assert.Equal(t, "", Branch(filepath.Join(root, "badptr"), root))
}

func TestStopAtBoundary(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git/HEAD"), "ref: refs/heads/outer\n")
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(filepath.Join(home, "p"), 0o755))
	assert.Equal(t, "", Branch(filepath.Join(home, "p"), home), "never looks above stopAt")
	assert.Equal(t, "outer", Branch(filepath.Join(home, "p"), root))
}

func TestHeadContentRules(t *testing.T) {
	sha40 := strings.Repeat("a", 40)
	sha64 := strings.Repeat("B", 64)
	cases := []struct {
		name, head, want string
	}{
		{"plain", "ref: refs/heads/main\n", "main"},
		{"no trailing newline", "ref: refs/heads/main", "main"},
		{"nested", "ref: refs/heads/feat/a-b_c.d\n", "feat/a-b_c.d"},
		{"unicode name", "ref: refs/heads/功能\n", "功能"},
		{"sha40", sha40 + "\n", "@aaaaaaa"},
		{"sha64", sha64 + "\n", "@BBBBBBB"},
		{"huge 100KB", "ref: refs/heads/" + strings.Repeat("a", 100*1024) + "\n", ""},
		{"just over 1KiB", "ref: refs/heads/" + strings.Repeat("a", 1100), ""},
		{"multi-line", "ref: refs/heads/main\nextra\n", ""},
		{"two newlines", "ref: refs/heads/main\n\n", ""},
		{"CR", "ref: refs/heads/ma\rin\n", ""},
		{"NUL", "ref: refs/heads/ma\x00in\n", ""},
		{"ESC", "ref: refs/heads/\x1b[31mred\n", ""},
		{"DEL", "ref: refs/heads/a\x7fb\n", ""},
		{"bidi RLO", "ref: refs/heads/a‮b\n", ""},
		{"bidi isolate", "ref: refs/heads/a⁦b\n", ""},
		{"LRM", "ref: refs/heads/a‎b\n", ""},
		{"dotdot", "ref: refs/heads/a..b\n", ""},
		{"space", "ref: refs/heads/a b\n", ""},
		{"tilde", "ref: refs/heads/a~1\n", ""},
		{"caret", "ref: refs/heads/a^\n", ""},
		{"colon", "ref: refs/heads/a:b\n", ""},
		{"question", "ref: refs/heads/a?\n", ""},
		{"star", "ref: refs/heads/a*\n", ""},
		{"bracket", "ref: refs/heads/a[\n", ""},
		{"backslash", "ref: refs/heads/a\\b\n", ""},
		{"leading slash", "ref: refs/heads//a\n", ""},
		{"trailing slash", "ref: refs/heads/a/\n", ""},
		{"leading dot", "ref: refs/heads/.a\n", ""},
		{"trailing dot", "ref: refs/heads/a.\n", ""},
		{"lock suffix", "ref: refs/heads/a.lock\n", ""},
		{"empty name", "ref: refs/heads/\n", ""},
		{"overlong name", "ref: refs/heads/" + strings.Repeat("a", 201) + "\n", ""},
		{"max name ok", "ref: refs/heads/" + strings.Repeat("a", 200) + "\n", strings.Repeat("a", 200)},
		{"remotes ref", "ref: refs/remotes/origin/x\n", ""},
		{"tags ref", "ref: refs/tags/v1\n", ""},
		{"bad sha length", strings.Repeat("a", 39) + "\n", ""},
		{"bad sha 41", strings.Repeat("a", 41) + "\n", ""},
		{"non-hex sha", strings.Repeat("g", 40) + "\n", ""},
		{"invalid utf8", "ref: refs/heads/a\xffb\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "r/.git/HEAD"), c.head)
			assert.Equal(t, c.want, Branch(filepath.Join(root, "r"), root))
		})
	}
}

func TestGitFilePointerRules(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "g/HEAD"), "ref: refs/heads/wt\n")
	good := "gitdir: " + filepath.Join(root, "g")
	cases := []struct {
		name, content, want string
	}{
		{"good", good + "\n", "wt"},
		{"multi-line", good + "\nmore\n", ""},
		{"control char", "gitdir: " + filepath.Join(root, "g") + "\x01\n", ""},
		{"huge", "gitdir: " + strings.Repeat("a", 100*1024) + "\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := filepath.Join(root, "w-"+strings.ReplaceAll(c.name, " ", "-"))
			write(t, filepath.Join(d, ".git"), c.content)
			assert.Equal(t, c.want, Branch(d, root))
		})
	}
}

func TestFifoHeadDoesNotBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mkfifo")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "r/.git"), 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "r/.git/HEAD"), 0o644))
	// .git itself a FIFO too.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "q"), 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "q/.git"), 0o644))

	done := make(chan [2]string, 1)
	go func() {
		done <- [2]string{Branch(filepath.Join(root, "r"), root), Branch(filepath.Join(root, "q"), root)}
	}()
	select {
	case got := <-done:
		assert.Equal(t, [2]string{"", ""}, got)
	case <-time.After(2 * time.Second):
		t.Fatal("Branch blocked on a FIFO")
	}
}

func TestNoLocationCache_NearerRepoAndGitdirRedirect(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "outer/.git/HEAD"), "ref: refs/heads/outer\n")
	cwd := filepath.Join(root, "outer/sub")
	require.NoError(t, os.MkdirAll(cwd, 0o755))
	assert.Equal(t, "outer", Branch(cwd, root))
	write(t, filepath.Join(cwd, ".git/HEAD"), "ref: refs/heads/inner\n")
	assert.Equal(t, "inner", Branch(cwd, root), "a nearer repo wins on the next lookup")

	write(t, filepath.Join(root, "g1/HEAD"), "ref: refs/heads/one\n")
	write(t, filepath.Join(root, "g2/HEAD"), "ref: refs/heads/two\n")
	wt := filepath.Join(root, "wt")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(root, "g1")+"\n")
	assert.Equal(t, "one", Branch(wt, root))
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(root, "g2")+"\n")
	assert.Equal(t, "two", Branch(wt, root), "a redirected gitdir is followed on the next lookup")
}

func BenchmarkBranch13(b *testing.B) {
	root := b.TempDir()
	var dirs []string
	for i := 0; i < 13; i++ {
		n := string(rune('a' + i))
		d := filepath.Join(root, "d", n, "src", "deep")
		require.NoError(b, os.MkdirAll(d, 0o755))
		switch {
		case i < 6:
			write(b, filepath.Join(root, "d", n, ".git/HEAD"), "ref: refs/heads/main\n")
		case i < 9:
			write(b, filepath.Join(root, "g", n, "HEAD"), "ref: refs/heads/wt\n")
			write(b, filepath.Join(root, "d", n, ".git"), "gitdir: "+filepath.Join(root, "g", n)+"\n")
		}
		dirs = append(dirs, d)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, d := range dirs {
			Branch(d, root)
		}
	}
}
