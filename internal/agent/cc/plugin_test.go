package cc

import (
	"encoding/json"
	"errors"
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
	root, changed, err := ExtractPlugin(fakePlugin("1.0.0-alpha.530"), dataDir, "1.0.0-alpha.530", "/opt/pdx", "/etc/pdx/config.toml")
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
	if err := json.Unmarshal(b, &pj); err != nil || pj["pdx"] != "/opt/pdx" || pj["data_dir"] != dataDir || pj["config"] != "/etc/pdx/config.toml" {
		t.Fatalf("pdx.json = %s (%v)", b, err)
	}
	assertOnlyRoot(t, dataDir)
}

// assertOnlyRoot fails when <data_dir>/cc-plugin holds anything but purdex/
// (a staging or backup sibling left behind).
func assertOnlyRoot(t *testing.T, dataDir string) {
	t.Helper()
	ents, err := os.ReadDir(filepath.Dir(PluginRoot(dataDir)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != PluginName {
		t.Fatalf("cc-plugin/ holds %v, want only %s", names, PluginName)
	}
}

// assertWholeTree fails unless the extracted tree is fakePlugin(version)
// complete: every file, register.js of that version, VERSION of it.
func assertWholeTree(t *testing.T, dataDir, version string) {
	t.Helper()
	root := PluginRoot(dataDir)
	for rel := range fakePlugin(version) {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if v, _ := os.ReadFile(filepath.Join(root, "VERSION")); strings.TrimSpace(string(v)) != version {
		t.Errorf("VERSION = %q, want %s", v, version)
	}
	if js, _ := os.ReadFile(filepath.Join(root, "hooks", "register.js")); !strings.HasSuffix(string(js), "// "+version) {
		t.Errorf("register.js = %q, want version %s", js, version)
	}
}

// Attacker high: the swap must never leave the folder missing or half
// replaced. A failed staging → root rename puts the old tree back.
func TestExtractPlugin_FailedPublishRestoresTheOldTree(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	root := PluginRoot(dataDir)
	old := renameFn
	t.Cleanup(func() { renameFn = old })
	renameFn = func(from, to string) error {
		if to == root && strings.Contains(filepath.Base(from), ".staging-") {
			return os.ErrPermission
		}
		return old(from, to)
	}
	if _, changed, err := ExtractPlugin(fakePlugin("b"), dataDir, "b", "/opt/pdx", ""); err == nil || changed {
		t.Fatalf("a failed publish must error: changed=%v err=%v", changed, err)
	}
	assertWholeTree(t, dataDir, "a")
	assertOnlyRoot(t, dataDir)
}

// Attacker critical: two extractions at once in one process (two setup
// requests to the daemon) must not interleave — the result is one whole
// version, with no staging or backup left. Run with -race.
func TestExtractPlugin_ConcurrentExtractionsLeaveOneWholeTree(t *testing.T) {
	for i := 0; i < 20; i++ {
		dataDir := t.TempDir()
		if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx", ""); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for j, v := range []string{"b", "c"} {
			wg.Add(1)
			go func(j int, v string) {
				defer wg.Done()
				_, _, errs[j] = ExtractPlugin(fakePlugin(v), dataDir, v, "/opt/pdx", "")
			}(j, v)
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("round %d: errs = %v", i, errs)
		}
		v, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), "VERSION"))
		got := strings.TrimSpace(string(v))
		if got != "b" && got != "c" {
			t.Fatalf("round %d: VERSION = %q", i, got)
		}
		assertWholeTree(t, dataDir, got)
		assertOnlyRoot(t, dataDir)
	}
}

func TestRemovePluginDir_TakesLeftoverStagingAndBackupToo(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(PluginRoot(dataDir))
	for _, n := range []string{"purdex.staging-1", "purdex.old-2", "purdex.tmp"} {
		os.MkdirAll(filepath.Join(parent, n, "hooks"), 0o755)
	}
	os.MkdirAll(filepath.Join(parent, "custom"), 0o755)
	if err := RemovePluginDir(dataDir); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(parent)
	if len(ents) != 1 || ents[0].Name() != "custom" {
		t.Fatalf("cc-plugin/ after remove: %v (only someone else's folder may stay)", ents)
	}
}

func TestExtractPlugin_SameVersionIsNoop_NewVersionReplaces(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	// A file of our own beside the managed ones may stay (the same-version
	// check compares the embedded files only)…
	stale := filepath.Join(PluginRoot(dataDir), "hooks", "stale.js")
	os.WriteFile(stale, []byte("old"), 0o644)
	_, changed, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx", "")
	if err != nil || changed {
		t.Fatalf("same version: changed=%v err=%v", changed, err)
	}
	// …but a managed file that went missing is put back (attacker high).
	os.Remove(filepath.Join(PluginRoot(dataDir), "hooks", "register.js"))
	_, changed, err = ExtractPlugin(fakePlugin("a"), dataDir, "a", "/opt/pdx", "")
	if err != nil || !changed {
		t.Fatalf("same version, register.js missing: changed=%v err=%v", changed, err)
	}
	assertWholeTree(t, dataDir, "a")
	// …and it refreshes pdx.json: a binary moved since the last install is
	// found by the mod (the rule's one exception).
	if _, _, err := ExtractPlugin(fakePlugin("a"), dataDir, "a", "/usr/local/bin/pdx", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), "pdx.json")); !strings.Contains(string(b), `"/usr/local/bin/pdx"`) {
		t.Fatalf("same-version extract must refresh pdx.json: %s", b)
	}
	_, changed, err = ExtractPlugin(fakePlugin("b"), dataDir, "b", "/opt/pdx", "")
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

// Attacker high: a same VERSION is not proof the tree is whole. Every
// embedded file must be there byte for byte — plugin.json only present and
// valid JSON, since the extractor stamps the version into it — or the
// tree is re-extracted (through the atomic publish).
func TestExtractPlugin_SameVersionVerifiesTheTree(t *testing.T) {
	const v = "1.0.0-alpha.600" // semver: plugin.json is stamped, so it differs from the embedded bytes
	cases := []struct {
		name    string
		damage  func(root string)
		changed bool
	}{
		{"intact (stamped plugin.json is fine)", func(string) {}, false},
		{"register.js deleted", func(root string) { os.Remove(filepath.Join(root, "hooks", "register.js")) }, true},
		{"hooks.json corrupted", func(root string) {
			os.WriteFile(filepath.Join(root, "hooks", "hooks.json"), []byte(`{"modules":[]}`), 0o644)
		}, true},
		{"SKILL.md deleted", func(root string) { os.Remove(filepath.Join(root, "skills", "pdx-team", "SKILL.md")) }, true},
		{"plugin.json not JSON", func(root string) {
			os.WriteFile(filepath.Join(root, ".claude-plugin", "plugin.json"), []byte(`{"name":`), 0o644)
		}, true},
		{"plugin.json deleted", func(root string) { os.Remove(filepath.Join(root, ".claude-plugin", "plugin.json")) }, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dataDir := t.TempDir()
			if _, _, err := ExtractPlugin(fakePlugin(v), dataDir, v, "/opt/pdx", ""); err != nil {
				t.Fatal(err)
			}
			c.damage(PluginRoot(dataDir))
			_, changed, err := ExtractPlugin(fakePlugin(v), dataDir, v, "/usr/local/bin/pdx", "")
			if err != nil || changed != c.changed {
				t.Fatalf("changed=%v err=%v, want changed=%v", changed, err, c.changed)
			}
			assertWholeTree(t, dataDir, v)
			assertOnlyRoot(t, dataDir)
			if hj, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), "hooks", "hooks.json")); string(hj) != `{"modules":["./register.js"]}` {
				t.Fatalf("hooks.json = %s", hj)
			}
			var m map[string]any
			pj, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
			if err := json.Unmarshal(pj, &m); err != nil || m["version"] != v {
				t.Fatalf("plugin.json = %s (%v)", pj, err)
			}
			if readPdxJSON(t, dataDir)["pdx"] != "/usr/local/bin/pdx" {
				t.Fatal("pdx.json must be refreshed either way")
			}
		})
	}
}

func TestExtractPlugin_UnknownVersionAlwaysReextracts_AndSemverStampsManifest(t *testing.T) {
	dataDir := t.TempDir()
	for i := 0; i < 2; i++ {
		_, changed, err := ExtractPlugin(fakePlugin("x"), dataDir, "unknown", "/opt/pdx", "")
		if err != nil || !changed {
			t.Fatalf("run %d with version unknown: changed=%v err=%v (a dev build must always re-extract)", i, changed, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
	if !strings.Contains(string(b), `"version":"unknown"`) && strings.Contains(string(b), `"unknown"`) {
		t.Fatalf("unknown must not be stamped into plugin.json: %s", b)
	}
	if _, _, err := ExtractPlugin(fakePlugin("x"), dataDir, "1.0.0-alpha.530", "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(PluginRoot(dataDir), ".claude-plugin", "plugin.json"))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m["version"] != "1.0.0-alpha.530" || m["name"] != "purdex" {
		t.Fatalf("plugin.json = %s (%v)", b, err)
	}
}

func TestExtractPlugin_NilSourceErrors(t *testing.T) {
	if _, _, err := ExtractPlugin(nil, t.TempDir(), "v", "/opt/pdx", ""); err == nil {
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

func readPdxJSON(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	var pj map[string]string
	b, err := os.ReadFile(filepath.Join(PluginRoot(dataDir), "pdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &pj); err != nil {
		t.Fatalf("pdx.json = %s: %v", b, err)
	}
	return pj
}

// Attacker high: the mod must reach the daemon that installed it, not
// whatever the default config names — pdx.json carries that daemon's config
// path (the provider's cfg.Path; the default config's without one).
func TestInstallHooks_PdxJSONNamesTheInstallingConfig(t *testing.T) {
	p, home, dataDir := pluginProvider(t, "1.0.0-alpha.600")
	p.cfg.Path = "/srv/pdx-b/config.toml"
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	if got := readPdxJSON(t, dataDir)["config"]; got != "/srv/pdx-b/config.toml" {
		t.Fatalf("daemon provider: pdx.json config = %q", got)
	}
	// Same version, another config: the refresh rewrites it too.
	p.cfg.Path = "/srv/pdx-c/config.toml"
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	if got := readPdxJSON(t, dataDir)["config"]; got != "/srv/pdx-c/config.toml" {
		t.Fatalf("same-version refresh: pdx.json config = %q", got)
	}

	// `pdx setup` builds the provider without a config: the default one.
	bare := NewProvider(nil, nil, nil, nil)
	if err := bare.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	defDataDir := filepath.Join(home, ".config", "pdx")
	if got, want := readPdxJSON(t, defDataDir)["config"], filepath.Join(defDataDir, "config.toml"); got != want {
		t.Fatalf("no-config provider: pdx.json config = %q, want %q", got, want)
	}
}

// failHooks / failEnv swap a settings.json writer for one that fails when
// remove == onRemove (restored at cleanup), to break one step.
func failHooks(t *testing.T, onRemove bool) {
	t.Helper()
	old := mergeHooksFn
	t.Cleanup(func() { mergeHooksFn = old })
	mergeHooksFn = func(path, pdxPath string, remove bool) error {
		if remove == onRemove {
			return errors.New("injected hooks write failure")
		}
		return old(path, pdxPath, remove)
	}
}

func failEnv(t *testing.T, onRemove bool) {
	t.Helper()
	old := mergeEnvFn
	t.Cleanup(func() { mergeEnvFn = old })
	mergeEnvFn = func(path, root string, remove bool) error {
		if remove == onRemove {
			return errors.New("injected env write failure")
		}
		return old(path, root, remove)
	}
}

func hasPdxHook(t *testing.T, settingsPath string) bool {
	t.Helper()
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return false
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	hooks, _ := s["hooks"].(map[string]any)
	for _, entries := range hooks {
		for _, e := range toEntrySlice(entries) {
			if entryIsPdx(e) {
				return true
			}
		}
	}
	return false
}

// Attacker high: install is plugin → env → hooks, and a failed hooks write
// takes the env entry back out, so a failure never leaves the plugin named
// without its hooks (nor the hooks without the plugin).
func TestInstallHooks_PartialFailureLeavesNoHalfInstall(t *testing.T) {
	t.Run("hooks write fails: env is taken back out, other entries kept", func(t *testing.T) {
		p, home, _ := pluginProvider(t, "1.0.0-alpha.600")
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		os.MkdirAll(filepath.Dir(settingsPath), 0o755)
		os.WriteFile(settingsPath, []byte(`{"env":{"CLAUDE_CODE_PLUGIN_DIRS":"/Users/x/mods/a"}}`), 0o644)
		failHooks(t, false)
		if err := p.InstallHooks("/usr/local/bin/pdx"); err == nil {
			t.Fatal("a failed hooks write must fail the install")
		}
		if v, _ := envDirs(t, readSettings(t, settingsPath)); v != "/Users/x/mods/a" {
			t.Fatalf("CLAUDE_CODE_PLUGIN_DIRS = %q, want the Purdex entry taken back out", v)
		}
	})
	t.Run("extraction fails: settings.json is not touched", func(t *testing.T) {
		p, home, dataDir := pluginProvider(t, "1.0.0-alpha.600")
		// A file where cc-plugin/ must go makes the extraction fail.
		os.WriteFile(filepath.Join(dataDir, PluginDirName), []byte("x"), 0o644)
		if err := p.InstallHooks("/usr/local/bin/pdx"); err == nil {
			t.Fatal("a failed extraction must fail the install")
		}
		if hasPdxHook(t, filepath.Join(home, ".claude", "settings.json")) {
			t.Fatal("hooks must not be installed without the plugin")
		}
	})
}

// Attacker high: remove tries all three steps (hooks, env, plugin dir) and
// joins the errors; one failing never strands the others.
func TestRemoveHooks_TriesEveryStepAndJoinsErrors(t *testing.T) {
	install := func(t *testing.T) (*Provider, string, string) {
		p, home, dataDir := pluginProvider(t, "1.0.0-alpha.600")
		if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
			t.Fatal(err)
		}
		return p, filepath.Join(home, ".claude", "settings.json"), dataDir
	}
	t.Run("env write fails: hooks and plugin dir still go", func(t *testing.T) {
		p, settingsPath, dataDir := install(t)
		failEnv(t, true)
		if err := p.RemoveHooks("/usr/local/bin/pdx"); err == nil {
			t.Fatal("a failed env write must fail the remove")
		}
		if hasPdxHook(t, settingsPath) {
			t.Fatal("hooks must still be removed")
		}
		if _, err := os.Stat(PluginRoot(dataDir)); !os.IsNotExist(err) {
			t.Fatalf("plugin dir must still be removed (%v)", err)
		}
	})
	t.Run("hooks write fails: env and plugin dir still go", func(t *testing.T) {
		p, settingsPath, dataDir := install(t)
		failHooks(t, true)
		if err := p.RemoveHooks("/usr/local/bin/pdx"); err == nil {
			t.Fatal("a failed hooks write must fail the remove")
		}
		if _, ok := envDirs(t, readSettings(t, settingsPath)); ok {
			t.Fatal("env must still be cleaned")
		}
		if _, err := os.Stat(PluginRoot(dataDir)); !os.IsNotExist(err) {
			t.Fatalf("plugin dir must still be removed (%v)", err)
		}
	})
}
