package cc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

// PluginDirName is the folder under <data_dir> that holds extracted Claude
// Code plugins; the Purdex mod lives in <data_dir>/cc-plugin/purdex/.
const PluginDirName = "cc-plugin"

// PluginName is the plugin folder and the manifest's name.
const PluginName = "purdex"

// pluginDirsEnv is the settings.json env key Claude Code reads for extra
// plugin folders (platform path-list separated; spec M2).
const pluginDirsEnv = "CLAUDE_CODE_PLUGIN_DIRS"

// PluginSource is the embedded plugin tree, set by cmd/pdx at start
// (plugin.Files()). nil means "no plugin to install" (tests of the hook
// installer alone, or a build without the tree).
var PluginSource fs.FS

// PluginRoot is where the plugin is extracted for a data dir.
func PluginRoot(dataDir string) string {
	return filepath.Join(dataDir, PluginDirName, PluginName)
}

// extractMu serialises ExtractPlugin and RemovePluginDir within this
// process: two setup requests to the daemon at once must not swap the
// folder under each other. Another process (`pdx setup` beside the daemon)
// is not covered by it; the unique staging/backup names keep the two from
// writing into one tree, and a lost rename race fails with an error.
var extractMu sync.Mutex

// renameFn is os.Rename; tests swap it to fail one step of the publish.
var renameFn = os.Rename

// Sibling name patterns under <data_dir>/cc-plugin/ (os.MkdirTemp's * is
// a random suffix, so concurrent processes never share one).
const (
	stagingPattern = PluginName + ".staging-*"
	backupPattern  = PluginName + ".old-*"
)

// ExtractPlugin writes src into PluginRoot(dataDir) when the VERSION stamp
// there differs from version (or is missing), or when a managed file there
// is missing or differs from src (treeMatches), then writes VERSION and
// pdx.json {pdx, data_dir, config}. It returns the root and whether files were
// written. Extraction goes to a fresh staging sibling and is swapped into
// place (publishDir), so a session loading the folder sees the old tree or
// the new one, never a half-written or missing one.
func ExtractPlugin(src fs.FS, dataDir, version, pdxPath, cfgPath string) (root string, changed bool, err error) {
	root = PluginRoot(dataDir)
	if src == nil {
		return root, false, errors.New("plugin source is nil")
	}
	extractMu.Lock()
	defer extractMu.Unlock()
	unlock, err := lockPluginParent(filepath.Dir(root))
	if err != nil {
		return root, false, err
	}
	defer unlock()
	// A binary built without ldflags reports "unknown"; such a dev build always
	// re-extracts, so an edited mod reaches the next session without a bump.
	// A same VERSION is not proof the tree is whole: a managed file that is
	// missing or edited also re-extracts.
	if cur, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil && version != "" && version != "unknown" && strings.TrimSpace(string(cur)) == version && treeMatches(src, root) {
		if err := writePdxJSON(root, pdxPath, dataDir, cfgPath); err != nil {
			return root, false, err
		}
		return root, false, nil
	}
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return root, false, fmt.Errorf("create %s: %w", parent, err)
	}
	staging, err := os.MkdirTemp(parent, stagingPattern)
	if err != nil {
		return root, false, fmt.Errorf("create staging dir: %w", err)
	}
	if err := fillStaging(staging, src, dataDir, version, pdxPath, cfgPath); err != nil {
		_ = os.RemoveAll(staging)
		return root, false, err
	}
	if err := publishDir(staging, root); err != nil {
		_ = os.RemoveAll(staging)
		return root, false, err
	}
	return root, true, nil
}

// manifestPath is the one embedded file the extractor rewrites (the version
// stamp), so treeMatches compares it with that key set aside.
const manifestPath = ".claude-plugin/plugin.json"

// treeMatches reports whether every embedded file is at root with the same
// bytes (the manifest: present and valid JSON). Extra files at root are not
// looked at.
func treeMatches(src fs.FS, root string) bool {
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return err
		}
		if p == manifestPath {
			// The extractor stamps "version"; everything else must be the
			// embedded manifest's (critic on PR #1752: valid JSON with the
			// name changed is not a whole tree).
			want, err := fs.ReadFile(src, p)
			if err != nil {
				return err
			}
			if !sameManifest(got, want) {
				return errTreeDiffers
			}
			return nil
		}
		want, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return errTreeDiffers
		}
		return nil
	})
	return err == nil
}

var errTreeDiffers = errors.New("extracted tree differs from the embedded one")

// lockFileName is the cross-process lock beside the extracted tree: the
// daemon's setup route and a `pdx setup` run without the daemon are two
// processes, and extractMu covers only one (critic on PR #1752). flock is
// per open file description, so it also serialises two goroutines of one
// process; extractMu stays as the cheap in-process guard.
const lockFileName = ".purdex.lock"

// lockPluginParent takes an exclusive flock on <parent>/.purdex.lock,
// creating parent and the file as needed; the returned func releases it.
func lockPluginParent(parent string) (func(), error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", parent, err)
	}
	f, err := os.OpenFile(filepath.Join(parent, lockFileName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open plugin lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock plugin dir: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// fillStaging writes the whole tree into staging: the embedded files,
// VERSION, pdx.json and the stamped manifest.
func fillStaging(staging string, src fs.FS, dataDir, version, pdxPath, cfgPath string) error {
	// MkdirTemp makes 0700; the published folder keeps the 0755 it always had.
	if err := os.Chmod(staging, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", staging, err)
	}
	if err := copyFS(staging, src); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(staging, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		return fmt.Errorf("write VERSION: %w", err)
	}
	if err := writePdxJSON(staging, pdxPath, dataDir, cfgPath); err != nil {
		return err
	}
	return stampManifest(staging, version)
}

// publishDir swaps staging in as root: an existing root is first renamed
// to a unique backup sibling, then staging is renamed to root. When that
// second rename fails the backup is renamed back, so root is never left
// missing; on success the backup is deleted. The caller removes staging
// when an error is returned.
func publishDir(staging, root string) error {
	var backup string
	if _, err := os.Lstat(root); err == nil {
		b, err := os.MkdirTemp(filepath.Dir(root), backupPattern)
		if err != nil {
			return fmt.Errorf("reserve backup name: %w", err)
		}
		if err := os.Remove(b); err != nil {
			return fmt.Errorf("reserve backup name: %w", err)
		}
		if err := renameFn(root, b); err != nil {
			return fmt.Errorf("move %s aside: %w", root, err)
		}
		backup = b
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", root, err)
	}
	if err := renameFn(staging, root); err != nil {
		err = fmt.Errorf("publish %s: %w", root, err)
		if backup != "" {
			if rerr := renameFn(backup, root); rerr != nil {
				return errors.Join(err, fmt.Errorf("restore %s from %s: %w", root, backup, rerr))
			}
		}
		return err
	}
	if backup != "" {
		_ = os.RemoveAll(backup)
	}
	return nil
}

// stampManifest sets .claude-plugin/plugin.json "version" to the pdx version
// when it is a semver (1.0.0-alpha.527); "unknown" leaves the file as embedded.
func stampManifest(root, version string) error {
	if !semverRe.MatchString(version) {
		return nil
	}
	path := filepath.Join(root, filepath.FromSlash(manifestPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plugin.json: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse plugin.json: %w", err)
	}
	m["version"] = version
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// writePdxJSON writes what the mod needs to call back: the pdx binary, the
// data dir and — when known — the config file of the daemon that installed
// it, which the mod passes as `pdx relay --config` so it reaches that
// daemon rather than whatever the default config names.
func writePdxJSON(root, pdxPath, dataDir, cfgPath string) error {
	m := map[string]string{"pdx": pdxPath, "data_dir": dataDir}
	if cfgPath != "" {
		m["config"] = cfgPath
	}
	b, _ := json.Marshal(m)
	// Written beside and renamed in, so a session starting meanwhile reads
	// the old file or the new one, never a half-written one (which the mod
	// would read as absent: pdx from PATH, no --config).
	f, err := os.CreateTemp(root, ".pdx.json-*")
	if err != nil {
		return fmt.Errorf("write pdx.json: %w", err)
	}
	tmp := f.Name()
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write pdx.json: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write pdx.json: %w", err)
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write pdx.json: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(root, "pdx.json")); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write pdx.json: %w", err)
	}
	return nil
}

func copyFS(dst string, src fs.FS) error {
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", p, err)
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// RemovePluginDir deletes the extracted tree and any staging or backup
// sibling a crashed extraction left (plus the pre-review .tmp one). Other
// folders under cc-plugin/ are not ours and stay.
func RemovePluginDir(dataDir string) error {
	extractMu.Lock()
	defer extractMu.Unlock()
	root := PluginRoot(dataDir)
	if _, err := os.Stat(filepath.Dir(root)); os.IsNotExist(err) {
		return nil // never installed: nothing to lock, nothing to remove
	}
	unlock, err := lockPluginParent(filepath.Dir(root))
	if err != nil {
		return err
	}
	defer unlock()
	leftovers := []string{root + ".tmp"}
	for _, pat := range []string{stagingPattern, backupPattern} {
		m, _ := filepath.Glob(filepath.Join(filepath.Dir(root), pat))
		leftovers = append(leftovers, m...)
	}
	for _, p := range leftovers {
		_ = os.RemoveAll(p)
	}
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove %s: %w", root, err)
	}
	return nil
}

// mergePluginDirs takes every Purdex entry out of
// settings.env.CLAUDE_CODE_PLUGIN_DIRS and, when remove=false, appends
// pluginRoot. Other entries are kept in order; the env block and the key are
// created or deleted as needed. A Purdex entry is any …/cc-plugin/purdex
// (isPurdexPluginDir), not only this data dir's, so a moved data dir's stale
// entry is still ours to replace; a sibling such as <data_dir>/cc-plugin/custom
// is not ours and is kept.
func mergePluginDirs(settingsPath, pluginRoot string, remove bool) error {
	settings, err := loadSettings(settingsPath)
	if err != nil {
		return err
	}
	env, err := envMapForMerge(settings)
	if err != nil {
		return err
	}
	var current string
	if v, ok := env[pluginDirsEnv]; ok && v != nil {
		s, isStr := v.(string)
		if !isStr {
			return fmt.Errorf("claude env %s has unsupported value shape", pluginDirsEnv)
		}
		current = s
	}
	kept := make([]string, 0, 4)
	for _, p := range strings.Split(current, string(os.PathListSeparator)) {
		if p == "" || isPurdexPluginDir(p) {
			continue
		}
		kept = append(kept, p)
	}
	if !remove {
		kept = append(kept, pluginRoot)
	}
	if len(kept) == 0 {
		delete(env, pluginDirsEnv)
	} else {
		env[pluginDirsEnv] = strings.Join(kept, string(os.PathListSeparator))
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	return writeSettingsAtomic(settingsPath, settings)
}

// readPluginDirs returns settings.env.CLAUDE_CODE_PLUGIN_DIRS as it is (and
// whether it is set at all); a missing settings file is "not set".
func readPluginDirs(settingsPath string) (string, bool, error) {
	settings, err := loadSettings(settingsPath)
	if err != nil {
		return "", false, err
	}
	env, err := envMapForMerge(settings)
	if err != nil {
		return "", false, err
	}
	v, ok := env[pluginDirsEnv]
	if !ok || v == nil {
		return "", false, nil
	}
	s, isStr := v.(string)
	if !isStr {
		return "", false, fmt.Errorf("claude env %s has unsupported value shape", pluginDirsEnv)
	}
	return s, true, nil
}

// restorePluginDirs puts settings.env.CLAUDE_CODE_PLUGIN_DIRS back to a value
// readPluginDirs returned (unset when present is false), touching nothing
// else.
func restorePluginDirs(settingsPath, value string, present bool) error {
	settings, err := loadSettings(settingsPath)
	if err != nil {
		return err
	}
	env, err := envMapForMerge(settings)
	if err != nil {
		return err
	}
	if present {
		env[pluginDirsEnv] = value
	} else {
		delete(env, pluginDirsEnv)
	}
	if len(env) == 0 {
		delete(settings, "env")
	} else {
		settings["env"] = env
	}
	return writeSettingsAtomic(settingsPath, settings)
}

// isPurdexPluginDir reports whether a CLAUDE_CODE_PLUGIN_DIRS entry is a
// Purdex extraction: its last two elements are cc-plugin/purdex, under any
// data dir.
func isPurdexPluginDir(p string) bool {
	p = filepath.Clean(p)
	return filepath.Base(p) == PluginName && filepath.Base(filepath.Dir(p)) == PluginDirName
}

func envMapForMerge(settings map[string]any) (map[string]any, error) {
	v, ok := settings["env"]
	if !ok || v == nil {
		return make(map[string]any), nil
	}
	env, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("claude settings env has unsupported value shape")
	}
	return env, nil
}

// sameManifest reports whether two plugin.json documents are equal once the
// "version" key the extractor stamps is set aside.
func sameManifest(got, want []byte) bool {
	var g, w map[string]any
	if json.Unmarshal(got, &g) != nil || json.Unmarshal(want, &w) != nil {
		return false
	}
	delete(g, "version")
	delete(w, "version")
	return reflect.DeepEqual(g, w)
}
