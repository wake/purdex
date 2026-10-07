package cc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/wake/purdex/internal/buildinfo"
	"github.com/wake/purdex/internal/config"
)

func fakePlugin(version string) fstest.MapFS {
	return fstest.MapFS{
		".claude-plugin/plugin.json": {Data: []byte(`{"name":"purdex","version":"` + version + `"}`)},
		"hooks/hooks.json":           {Data: []byte(`{"modules":["./register.js"]}`)},
		"hooks/register.js":          {Data: []byte("export function register(on) {} // " + version)},
		"skills/pdx-team/SKILL.md":   {Data: []byte("---\nname: pdx-team\n---\n")},
	}
}

func envDirs(t *testing.T, settings map[string]any) (string, bool) {
	t.Helper()
	env, ok := settings["env"].(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := env["CLAUDE_CODE_PLUGIN_DIRS"].(string)
	return v, ok
}

func TestExtractPlugin_WritesTreeVersionAndPdxJSON(t *testing.T) {
	dataDir := t.TempDir()
	root, changed, err := ExtractPlugin(fakePlugin("1.0.0-alpha.530"), dataDir, "1.0.0-alpha.530", "/opt/pdx")
	if err != nil || !changed {
		t.Fatalf("first extract: changed=%v err=%v", changed, err)
	}
	if root != filepath.Join(dataDir, "cc-plugin", "purdex") {
		t.Fatalf("root = %s", root)
	}
	for _, rel := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.js", "skills/pdx-team/SKILL.md", "VERSION", "pdx.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
	v, _ := os.ReadFile(filepath.Join(root, "VERSION"))
	if strings.TrimSpace(string(v)) != "1.0.0-alpha.530" {
		t.Fatalf("VERSION = %q", v)
	}
	var pj map[string]string
	b, _ := os.ReadFile(filepath.Join(root, "pdx.json"))
	if err := json.Unmarshal(b, &pj); err != nil || pj["pdx"] != "/opt/pdx" || pj["data_dir"] != dataDir {
		t.Fatalf("pdx.json = %s (%v)", b, err)
	}
	if _, err := os.Stat(root + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("the .tmp sibling must not remain")
	}
}

func TestExtractPlugin_SameVersionIsNoop_NewVersionReplaces(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx"); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(PluginRoot(dataDir), "hooks", "stale.js")
	os.WriteFile(stale, []byte("old"), 0o644)
	_, changed, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx")
	if err != nil || changed {
		t.Fatalf("same version: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("a same-version extract must not touch the tree")
	}
	// …but it refreshes pdx.json: a binary moved since the last install is
	// found by the mod (the rule's one exception).
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), "pdx.json")); !strings.Contains(string(b), `"/usr/local/bin/pdx"`) {
		t.Fatalf("same-version extract must refresh pdx.json: %s", b)
	}
	_, changed, err = ExtractPlugin(fakePlugin("b"), dataDir, "b", "/opt/pdx")
	if err != nil || !changed {
		t.Fatalf("new version: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("a re-extract must replace the whole tree, not merge into it")
	}
	js, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), "hooks", "register.js"))
	if !strings.HasSuffix(strings.TrimSpace(string(js)), "// b") {
		t.Fatalf("register.js not replaced: %q", js)
	}
}

func TestExtractPlugin_UnknownVersionAlwaysReextracts_AndSemverStampsManifest(t *testing.T) {
	dataDir := t.TempDir()
	for i := 0; i < 2; i++ {
		_, changed, err := ExtractPlugin(fakePlugin("x"), dataDir, "unknown", "/opt/pdx")
		if err != nil || !changed {
			t.Fatalf("run %d with version unknown: changed=%v err=%v (a dev build must always re-extract)", i, changed, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
	if !strings.Contains(string(b), `"version":"unknown"`) && strings.Contains(string(b), `"unknown"`) {
		t.Fatalf("unknown must not be stamped into plugin.json: %s", b)
	}
	if _, _, err := ExtractPlugin(fakePlugin("x"), dataDir, "1.0.0-alpha.530", "/opt/pdx"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["version"] != "1.0.0-alpha.530" || m["name"] != "purdex" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
}

func TestExtractPlugin_NilSourceErrors(t *testing.T) {
	if _, _, err := ExtractPlugin(nil, t.TempDir(), "v", "/opt/pdx"); err == nil {
		t.Fatal("nil source must error")
	}
}

func TestMergePluginDirs_CreatesEnvAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	for i := 0; i < 2; i++ {
		if err := mergePluginDirs(path, root, false); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	v, ok := envDirs(t, readSettings(t, path))
	if !ok || v != root {
		t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS = %q ok=%v, want exactly %q once", v, ok, root)
	}
}

func TestMergePluginDirs_AppendsToExistingListAndKeepsOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	sep := string(os.PathListSeparator)
	os.WriteFile(path, []byte(`{"env":{"FOO":"1","CLAUDE_CODE_PLUGIN_DIRS":"/Users/x/mods/a`+sep+`/Users/x/mods/b"},"hooks":{}}`), 0o644)
	if err := mergePluginDirs(path, root, false); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, path)
	v, _ := envDirs(t, s)
	if v != "/Users/x/mods/a"+sep+"/Users/x/mods/b"+sep+root {
		t.Fatalf("got %q", v)
	}
	if s["env"].(map[string]any)["FOO"] != "1" {
		t.Fatal("other env keys must be kept")
	}
	if _, ok := s["hooks"]; !ok {
		t.Fatal("other settings keys must be kept")
	}
}

// R1 P2 + attacker: a Purdex entry is any …/cc-plugin/purdex, wherever the
// data dir was (a moved data dir leaves one behind); anything else — even a
// sibling under this data dir's cc-plugin/ — belongs to someone else.
func TestMergePluginDirs_OwnsEveryCcPluginPurdexEntry_KeepsEverythingElse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	sep := string(os.PathListSeparator)
	old := filepath.Join(dir, "old", "cc-plugin", "purdex")
	custom := filepath.Join(dataDir, "cc-plugin", "custom")
	seed := `{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"` + old + sep + custom + sep + `/Users/x/mods/a` + sep + root + `/"}}`

	os.WriteFile(path, []byte(seed), 0o644)
	if err := mergePluginDirs(path, root, false); err != nil {
		t.Fatal(err)
	}
	if v, _ := envDirs(t, readSettings(t, path)); v != custom+sep+"/Users/x/mods/a"+sep+root {
		t.Fatalf("install: got %q", v)
	}

	os.WriteFile(path, []byte(seed), 0o644)
	if err := mergePluginDirs(path, root, true); err != nil {
		t.Fatal(err)
	}
	if v, _ := envDirs(t, readSettings(t, path)); v != custom+sep+"/Users/x/mods/a" {
		t.Fatalf("remove: got %q", v)
	}
}

func TestMergePluginDirs_RemoveKeepsOthersAndDeletesEmptyEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	root := PluginRoot(dataDir)
	sep := string(os.PathListSeparator)
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"/Users/x/mods/a`+sep+root+`"}}`), 0o644)
	if err := mergePluginDirs(path, root, true); err != nil {
		t.Fatal(err)
	}
	v, _ := envDirs(t, readSettings(t, path))
	if v != "/Users/x/mods/a" {
		t.Fatalf("got %q", v)
	}
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"`+root+`"},"other":true}`), 0o644)
	if err := mergePluginDirs(path, root, true); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, path)
	if _, ok := s["env"]; ok {
		t.Fatalf("env block must go when empty: %v", s["env"])
	}
	if s["other"] != true {
		t.Fatal("other keys kept")
	}
}

func TestMergePluginDirs_RemoveOnMissingFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := mergePluginDirs(path, PluginRoot(filepath.Join(dir, "pdx")), true); err != nil {
		t.Fatal(err)
	}
	s := readSettings(t, path)
	if len(s) != 0 {
		t.Fatalf("got %v", s)
	}
}

func TestMergePluginDirs_UnsupportedShapesError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	dataDir := filepath.Join(dir, "pdx")
	os.WriteFile(path, []byte(`{"env":[]}`), 0o644)
	if err := mergePluginDirs(path, PluginRoot(dataDir), false); err == nil {
		t.Fatal("env array must error")
	}
	os.WriteFile(path, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":["/a"]}}`), 0o644)
	if err := mergePluginDirs(path, PluginRoot(dataDir), false); err == nil {
		t.Fatal("non-string value must error")
	}
}

// pluginProvider sets PluginSource and buildinfo.Version for one test and
// returns a provider whose data dir is a temp dir, with $HOME redirected.
func pluginProvider(t *testing.T, version string) (p *Provider, home, dataDir string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	dataDir = t.TempDir()
	oldSrc, oldVer := PluginSource, buildinfo.Version
	PluginSource = fakePlugin(version)
	buildinfo.Version = version
	t.Cleanup(func() { PluginSource, buildinfo.Version = oldSrc, oldVer })
	var mu sync.RWMutex
	return NewProvider(nil, nil, &config.Config{DataDir: dataDir}, &mu), home, dataDir
}

func pluginIssues(issues []string) []string {
	var out []string
	for _, s := range issues {
		if strings.Contains(s, "Purdex plugin") {
			out = append(out, s)
		}
	}
	return out
}

// R1 P1: a user who installed the hooks before the plugin shipped must see
// Install again after upgrading — the hooks alone are not "installed".
func TestCCCheckHooks_PluginMissingOrOutdatedIsNotInstalled(t *testing.T) {
	p, home, dataDir := pluginProvider(t, "1.0.0-alpha.600")
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := mergeClaudeHooks(settingsPath, "/usr/local/bin/pdx", false); err != nil {
		t.Fatal(err)
	}
	status, err := p.CheckHooks()
	if err != nil {
		t.Fatal(err)
	}
	if got := pluginIssues(status.Issues); status.Installed || len(got) != 1 || !strings.Contains(got[0], "not installed") {
		t.Fatalf("hooks without the plugin: Installed=%v issues=%v", status.Installed, status.Issues)
	}

	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	status, _ = p.CheckHooks()
	if !status.Installed || len(pluginIssues(status.Issues)) != 0 {
		t.Fatalf("after install: Installed=%v issues=%v", status.Installed, status.Issues)
	}

	root := PluginRoot(dataDir)
	os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.0.0-alpha.599\n"), 0o644)
	status, _ = p.CheckHooks()
	if got := pluginIssues(status.Issues); status.Installed || len(got) != 1 || !strings.Contains(got[0], "outdated (installed 1.0.0-alpha.599, want 1.0.0-alpha.600)") {
		t.Fatalf("stale VERSION: Installed=%v issues=%v", status.Installed, status.Issues)
	}

	// A dev build ("unknown") accepts whatever VERSION is there.
	buildinfo.Version = "unknown"
	status, _ = p.CheckHooks()
	if !status.Installed {
		t.Fatalf("dev build with any VERSION: issues=%v", status.Issues)
	}

	os.Remove(filepath.Join(root, "hooks", "register.js"))
	status, _ = p.CheckHooks()
	if status.Installed || len(pluginIssues(status.Issues)) != 1 {
		t.Fatalf("register.js missing: Installed=%v issues=%v", status.Installed, status.Issues)
	}

	buildinfo.Version = "1.0.0-alpha.600"
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	if err := mergePluginDirs(settingsPath, root, true); err != nil {
		t.Fatal(err)
	}
	status, _ = p.CheckHooks()
	if status.Installed || len(pluginIssues(status.Issues)) != 1 {
		t.Fatalf("env no longer names the plugin: Installed=%v issues=%v", status.Installed, status.Issues)
	}
}
