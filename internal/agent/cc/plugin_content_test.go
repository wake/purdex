package cc

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wake/purdex/internal/buildinfo"
)

// #2403: a release that does not touch the mod writes nothing into the watched plugin folder (every open Claude Code
// session would print "reloaded" for each file it sees change). ExtractPlugin decides by CONTENT — the managed files
// against the embedded tree, our own VERSION / pdx.json and the engine's own files set aside — not by the daemon version.
// Mutation gates: decide by VERSION again / ignore stray files / rewrite pdx.json unconditionally → red.

var longAgo = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ageAll sets every file and folder under root to longAgo, so any later write shows as a newer mtime.
func ageAll(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, longAgo, longAgo)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// writtenSince lists the regular files under root with an mtime after longAgo (written since ageAll).
func writtenSince(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(longAgo) {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const (
	vOld = "1.0.0-alpha.600"
	vNew = "1.0.0-alpha.601"
)

// A new daemon version over the same mod tree: not one file is written, VERSION and the manifest keep the version at
// which the tree last changed.
func TestExtractPlugin_SameTreeNewDaemonVersionWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	src := fakePlugin("same")
	if _, _, err := ExtractPlugin(src, dataDir, vOld, "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	root := PluginRoot(dataDir)
	ageAll(t, root)
	_, changed, err := ExtractPlugin(src, dataDir, vNew, "/opt/pdx", "")
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v, want an untouched folder", changed, err)
	}
	if w := writtenSince(t, root); len(w) != 0 {
		t.Fatalf("files written for an unchanged mod: %v", w)
	}
	if v, _ := os.ReadFile(filepath.Join(root, "VERSION")); strings.TrimSpace(string(v)) != vOld {
		t.Fatalf("VERSION = %q, want the version the tree last changed at (%s)", v, vOld)
	}
	assertOnlyRoot(t, dataDir)
}

// A changed module: the whole folder is replaced once, and VERSION moves.
func TestExtractPlugin_ChangedModuleReplacesTheWholeFolderOnce(t *testing.T) {
	dataDir := t.TempDir()
	if _, _, err := ExtractPlugin(fakePlugin("one"), dataDir, vOld, "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	_, changed, err := ExtractPlugin(fakePlugin(vNew), dataDir, vNew, "/opt/pdx", "")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	assertWholeTree(t, dataDir, vNew)
	assertOnlyRoot(t, dataDir)
}

// Only pdx.json's content differs (the binary moved): only pdx.json is written.
func TestExtractPlugin_OnlyPdxJSONDiffersWritesOnlyPdxJSON(t *testing.T) {
	dataDir := t.TempDir()
	src := fakePlugin("same")
	if _, _, err := ExtractPlugin(src, dataDir, vOld, "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	root := PluginRoot(dataDir)
	ageAll(t, root)
	_, changed, err := ExtractPlugin(src, dataDir, vNew, "/usr/local/bin/pdx", "")
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if w := writtenSince(t, root); len(w) != 1 || w[0] != "pdx.json" {
		t.Fatalf("written = %v, want only pdx.json", w)
	}
	if readPdxJSON(t, dataDir)["pdx"] != "/usr/local/bin/pdx" {
		t.Fatal("pdx.json was not refreshed")
	}
}

// A file the new mod no longer ships (a removed skill) must not stay behind for Claude Code to load.
func TestExtractPlugin_ARemovedFileOfTheModIsClearedAway(t *testing.T) {
	dataDir := t.TempDir()
	old := fakePlugin(vNew) // the same files as the new mod, plus one skill it dropped
	old["skills/gone/SKILL.md"] = old["skills/pdx-team/SKILL.md"]
	if _, _, err := ExtractPlugin(old, dataDir, vOld, "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	_, changed, err := ExtractPlugin(fakePlugin(vNew), dataDir, vNew, "/opt/pdx", "")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v: a tree with a file the embedded one lacks is a different tree", changed, err)
	}
	if _, err := os.Stat(filepath.Join(PluginRoot(dataDir), "skills", "gone", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("the removed skill is still in the folder")
	}
	assertWholeTree(t, dataDir, vNew)
	// and the same holds when the daemon version did not move at all
	stray := filepath.Join(PluginRoot(dataDir), "skills", "stray.md")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := ExtractPlugin(fakePlugin(vNew), dataDir, vNew, "/opt/pdx", ""); err != nil || !changed {
		t.Fatalf("same version, stray file: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("the stray file stayed")
	}
}

// The engine's own files (.claude-plugin/types/…, tsconfig.json) and our VERSION / pdx.json are not part of the comparison.
func TestExtractPlugin_EngineFilesAreNotADifference(t *testing.T) {
	dataDir := t.TempDir()
	src := fakePlugin("same")
	if _, _, err := ExtractPlugin(src, dataDir, vOld, "/opt/pdx", ""); err != nil {
		t.Fatal(err)
	}
	root := PluginRoot(dataDir)
	for rel, body := range map[string]string{".claude-plugin/types/hooks.d.ts": "declare {}", ".claude-plugin/types/deep/x.d.ts": "x", "tsconfig.json": "{}"} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ageAll(t, root)
	_, changed, err := ExtractPlugin(src, dataDir, vNew, "/opt/pdx", "")
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v: the engine's files made the tree differ", changed, err)
	}
	if w := writtenSince(t, root); len(w) != 0 {
		t.Fatalf("files written: %v", w)
	}
	for _, rel := range []string{".claude-plugin/types/hooks.d.ts", "tsconfig.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("%s was removed: %v", rel, err)
		}
	}
}

// The hooks status judges the mod the same way: a release that left the mod alone is not "outdated".
func TestPluginIssue_JudgesTheModByContent(t *testing.T) {
	p, home, dataDir := pluginProvider(t, vOld)
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	_ = home
	root := PluginRoot(dataDir)
	buildinfo.Version = vNew // a release that did not touch the mod; PluginSource is the same tree
	if status, _ := p.CheckHooks(); !status.Installed || len(pluginIssues(status.Issues)) != 0 {
		t.Fatalf("same mod, newer daemon: Installed=%v issues=%v", status.Installed, status.Issues)
	}
	if err := os.WriteFile(filepath.Join(root, "hooks", "register.js"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _ := p.CheckHooks()
	if got := pluginIssues(status.Issues); status.Installed || len(got) != 1 || !strings.Contains(got[0], "outdated") {
		t.Fatalf("an edited managed file: Installed=%v issues=%v", status.Installed, status.Issues)
	}
	if err := p.InstallHooks("/usr/local/bin/pdx"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "stray.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, _ = p.CheckHooks()
	if got := pluginIssues(status.Issues); status.Installed || len(got) != 1 || !strings.Contains(got[0], "outdated") {
		t.Fatalf("a stray file: Installed=%v issues=%v", status.Installed, status.Issues)
	}
}
