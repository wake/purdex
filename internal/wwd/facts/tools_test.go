package facts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseToolVersions(t *testing.T) {
	cases := []struct {
		tool, out, want string
	}{
		{"go", "go version go1.26.0 darwin/arm64\n", "1.26.0"},
		{"go", "go version go1.25rc1 linux/amd64", "1.25rc1"},
		{"node", "v24.14.0\n", "24.14.0"},
		{"pnpm", "10.23.0\n", "10.23.0"},
		{"php", "8.4.21", "8.4.21"},
		{"php", "7.4.33\n", "7.4.33"},
		{"claude", "2.1.306 (Claude Code)\n", "2.1.306"},
		{"claude", "2.1.306", "2.1.306"},
		{"claude", "(Claude Code)", ""},
		{"go", "garbage", ""},
		{"node", "", ""},
		{"pnpm", "  \n", ""},
		{"php", "Parse error: syntax error", ""},
	}
	for _, c := range cases {
		if got := parseToolVersion(c.tool, c.out); got != c.want {
			t.Errorf("%s %q = %q, want %q", c.tool, c.out, got, c.want)
		}
	}
}

func TestXcodeVersionFromPlist(t *testing.T) {
	plist := `<?xml version="1.0"?><plist version="1.0"><dict>
<key>CFBundleShortVersionString</key>
<string>16.4</string>
<key>ProductBuildVersion</key><string>16F6</string></dict></plist>`
	if got := xcodeVersionFromPlist([]byte(plist)); got != "16.4" {
		t.Errorf("got %q", got)
	}
	if got := xcodeVersionFromPlist([]byte("<plist/>")); got != "" {
		t.Errorf("got %q", got)
	}
}

func exe(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeRun struct {
	out   map[string]string
	fail  map[string]bool
	calls []string
}

func (f *fakeRun) run(ctx context.Context, tag, bin string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, bin+" "+strings.Join(args, " "))
	if f.fail[bin] {
		return nil, errors.New("boom")
	}
	return []byte(f.out[bin]), nil
}

func TestToolsReader(t *testing.T) {
	root := t.TempDir()
	shims := filepath.Join(root, "home", ".config", "pdx", "shims")
	brew := filepath.Join(root, "brew")
	herd := filepath.Join(root, "herd")
	goBin := exe(t, brew, "go")
	exe(t, shims, "go") // the pdx shim must never be used
	node := exe(t, brew, "node")
	pnpm := exe(t, brew, "pnpm")
	php74 := exe(t, herd, "php74")
	php84 := exe(t, herd, "php84")
	phpPlain := exe(t, herd, "php") // same version as php84: reported once
	run := &fakeRun{out: map[string]string{
		goBin: "go version go1.26.0 darwin/arm64", node: "v24.14.0", pnpm: "10.23.0",
		php74: "7.4.33", php84: "8.4.21", phpPlain: "8.4.21",
	}}
	now := t0
	r := &ToolsReader{
		Run: run.run, Now: func() time.Time { return now }, TTL: 10 * time.Minute,
		PathDirs: []string{shims, brew}, PHPDirs: []string{herd},
		XcodePlists: []string{filepath.Join(root, "none", "version.plist")},
	}
	got := r.Get()
	if got.Go == nil || *got.Go != "1.26.0" || got.Node == nil || *got.Node != "24.14.0" || got.Pnpm == nil || *got.Pnpm != "10.23.0" {
		t.Fatalf("%+v", got)
	}
	if !reflect.DeepEqual(got.PHP, []string{"7.4.33", "8.4.21"}) {
		t.Errorf("php %v", got.PHP)
	}
	if got.Xcode != nil {
		t.Errorf("xcode %v want null", *got.Xcode)
	}
	for _, c := range run.calls {
		if strings.Contains(c, "shims") {
			t.Errorf("ran a pdx shim: %s", c)
		}
	}

	// within the TTL nothing runs again
	n := len(run.calls)
	now = now.Add(5 * time.Minute)
	r.Get()
	if len(run.calls) != n {
		t.Errorf("re-ran inside the TTL: %d new calls", len(run.calls)-n)
	}

	// after the TTL a failing tool keeps its last good value, a changed one updates
	now = now.Add(10 * time.Minute)
	run.fail = map[string]bool{goBin: true}
	run.out[node] = "v24.15.0"
	got = r.Get()
	if got.Go == nil || *got.Go != "1.26.0" {
		t.Errorf("go lost on a transient failure: %v", got.Go)
	}
	if got.Node == nil || *got.Node != "24.15.0" {
		t.Errorf("node %v", got.Node)
	}
}

func TestToolsReaderMissingToolsAreNull(t *testing.T) {
	r := &ToolsReader{Run: (&fakeRun{}).run, Now: func() time.Time { return t0 }, TTL: time.Minute, PathDirs: []string{t.TempDir()}}
	got := r.Get()
	if got.Go != nil || got.Node != nil || got.Pnpm != nil || got.Xcode != nil {
		t.Errorf("%+v", got)
	}
	if got.PHP == nil || len(got.PHP) != 0 {
		t.Errorf("php must be [] not null: %#v", got.PHP)
	}
}

func TestDefaultToolDirsDropShimsAndAddNvm(t *testing.T) {
	home := t.TempDir()
	for _, v := range []string{"v16.20.2", "v24.14.0"} {
		if err := os.MkdirAll(filepath.Join(home, ".nvm", "versions", "node", v, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs := DefaultToolDirs(home, "/usr/bin:"+filepath.Join(home, ".config", "pdx", "shims")+":/opt/x")
	for _, d := range dirs {
		if strings.Contains(d, "/pdx/shims") {
			t.Errorf("shim dir kept: %s", d)
		}
	}
	want := filepath.Join(home, ".nvm", "versions", "node", "v24.14.0", "bin")
	if !contains(dirs, want) {
		t.Errorf("newest nvm node missing from %v", dirs)
	}
	if contains(dirs, filepath.Join(home, ".nvm", "versions", "node", "v16.20.2", "bin")) {
		t.Errorf("older nvm node should not be searched: %v", dirs)
	}
	if !contains(dirs, "/usr/bin") || !contains(dirs, "/opt/x") || !contains(dirs, "/opt/homebrew/bin") {
		t.Errorf("%v", dirs)
	}
}

// claude is a tool a task can need (mod-hooks runs `claude plugin test`, #3908): the facts say whether it is found on the job PATH, and which version.
func TestToolsReaderProbesClaude(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "toolchains", "claude", "bin")
	claude := exe(t, dir, "claude")
	run := &fakeRun{out: map[string]string{claude: "2.1.306 (Claude Code)\n"}}
	r := &ToolsReader{Run: run.run, Now: func() time.Time { return t0 }, TTL: time.Minute, PathDirs: []string{dir}}
	got := r.Get()
	if got.Claude == nil || *got.Claude != "2.1.306" {
		t.Fatalf("claude %v", got.Claude)
	}
	if !strings.Contains(strings.Join(run.calls, ";"), claude+" --version") {
		t.Errorf("asked with %v", run.calls)
	}
	if c := cloneTools(got); c.Claude == got.Claude || c.Claude == nil || *c.Claude != "2.1.306" {
		t.Errorf("cloneTools shares or drops claude: %v", c.Claude)
	}
}

func TestToolsReaderNoClaudeIsNull(t *testing.T) {
	r := &ToolsReader{Run: (&fakeRun{}).run, Now: func() time.Time { return t0 }, TTL: time.Minute, PathDirs: []string{t.TempDir()}}
	if got := r.Get(); got.Claude != nil {
		t.Errorf("claude %v want null", *got.Claude)
	}
}
