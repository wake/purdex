package dev

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mkdirs(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		if err := os.MkdirAll(filepath.Join(root, r), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func testEnv(home, path string) toolEnv {
	return toolEnv{
		getenv: func(k string) string {
			if k == "PATH" {
				return path
			}
			return ""
		},
		home: func() (string, error) { return home, nil },
		// Hide the machine's real /opt/homebrew and /usr/local from tests.
		isDir: func(p string) bool {
			if strings.HasPrefix(p, "/opt/") || strings.HasPrefix(p, "/usr/local") {
				return false
			}
			st, err := os.Stat(p)
			return err == nil && st.IsDir()
		},
	}
}

func TestNewestNvmBin(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".nvm/versions/node/v9.11.2/bin", ".nvm/versions/node/v24.2.0/bin", ".nvm/versions/node/v24.14.0/bin", ".nvm/versions/node/vjunk/bin")
	got := testEnv(home, "").newestNvmBin()
	want := filepath.Join(home, ".nvm/versions/node/v24.14.0/bin")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNewestNvmBinIgnoresVersionWithoutBin(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".nvm/versions/node/v30.0.0", ".nvm/versions/node/v20.0.0/bin")
	got := testEnv(home, "").newestNvmBin()
	if want := filepath.Join(home, ".nvm/versions/node/v20.0.0/bin"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNvmDirEnvOverridesHome(t *testing.T) {
	home, nvm := t.TempDir(), t.TempDir()
	mkdirs(t, home, ".nvm/versions/node/v1.0.0/bin")
	mkdirs(t, nvm, "versions/node/v2.0.0/bin")
	e := testEnv(home, "")
	e.getenv = func(k string) string {
		if k == "NVM_DIR" {
			return nvm
		}
		return ""
	}
	if got, want := e.newestNvmBin(), filepath.Join(nvm, "versions/node/v2.0.0/bin"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMissingNvmDir(t *testing.T) {
	e := testEnv(t.TempDir(), "/usr/bin")
	if got := e.newestNvmBin(); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := e.toolDirs(); !reflect.DeepEqual(got, []string{"/usr/bin"}) {
		t.Fatalf("got %v", got)
	}
}

func TestToolDirsOrderAndDedup(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".nvm/versions/node/v24.14.0/bin", ".local/bin")
	extra := t.TempDir()
	e := testEnv(home, "/usr/bin:"+extra+":/usr/bin")
	e.extra = []string{extra}
	got := e.toolDirs()
	want := []string{extra, filepath.Join(home, ".nvm/versions/node/v24.14.0/bin"), filepath.Join(home, ".local/bin"), "/usr/bin"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func fakeTool(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestChildEnvCarriesToolPath(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "faketool", `echo "PATH=$PATH"`)
	e := testEnv(t.TempDir(), "/usr/bin:/bin")
	e.extra = []string{bin}
	m := New(t.TempDir())
	m.tools = e
	s := newBuildSession()
	if err := m.streamTool(context.Background(), s, "p", "", "faketool"); err != nil {
		t.Fatal(err)
	}
	_, replay, unsub := s.subscribe()
	defer unsub()
	var found bool
	for _, ev := range replay {
		if strings.HasPrefix(ev.Line, "PATH="+bin+":") {
			found = true
		}
	}
	if !found {
		t.Fatalf("child PATH did not start with extra dir: %+v", replay)
	}
}

func TestToolNotFoundError(t *testing.T) {
	e := testEnv(t.TempDir(), "/nonexistent-a:/nonexistent-b")
	e.extra = []string{"/nonexistent-x"}
	m := New(t.TempDir())
	m.tools = e
	err := m.streamTool(context.Background(), newBuildSession(), "p", "", "no-such-tool-xyz")
	if err == nil {
		t.Fatal("want error")
	}
	for _, w := range []string{"no-such-tool-xyz", "/nonexistent-x", "/nonexistent-a", "/nonexistent-b"} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q lacks %q", err, w)
		}
	}
	if strings.Contains(err.Error(), "\n") {
		t.Fatal("must be one line")
	}
}

func TestBuildBinaryUsesToolPath(t *testing.T) {
	bin := t.TempDir()
	fakeTool(t, bin, "go", `echo "PATH=$PATH"`)
	e := testEnv(t.TempDir(), "/usr/bin:/bin")
	e.extra = []string{bin}
	m := New(t.TempDir())
	m.tools = e
	var lines []string
	if err := m.buildBinary(context.Background(), buildTarget{}, "h", "v", filepath.Join(t.TempDir(), "out"), func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "PATH="+bin+":") {
		t.Fatalf("go child PATH wrong: %v", lines)
	}
}

// A relative extra_path is checked against the daemon's cwd, but the child
// runs with Dir=repoRoot; the resolved executable must be absolute so the file
// that was checked is the file that runs.
func TestRelativeToolDirsBecomeAbsolute(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	e := testEnv(t.TempDir(), "rel/path")
	e.extra = []string{"rel/extra"}
	for _, d := range e.toolDirs() {
		if !filepath.IsAbs(d) {
			t.Fatalf("non-absolute dir %q", d)
		}
	}
	if got := e.toolDirs()[0]; got != filepath.Join(cwd, "rel/extra") {
		t.Fatalf("got %q", got)
	}
}
