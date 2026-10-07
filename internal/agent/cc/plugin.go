package cc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// ExtractPlugin writes src into PluginRoot(dataDir) when the VERSION stamp
// there differs from version (or is missing), then writes VERSION and
// pdx.json {pdx, data_dir}. It returns the root and whether files were
// written. Extraction goes to a sibling .tmp dir and is renamed into place
// so a session loading the folder never sees a half-written tree.
func ExtractPlugin(src fs.FS, dataDir, version, pdxPath string) (root string, changed bool, err error) {
	root = PluginRoot(dataDir)
	if src == nil {
		return root, false, errors.New("plugin source is nil")
	}
	// A binary built without ldflags reports "unknown"; such a dev build always
	// re-extracts, so an edited mod reaches the next session without a bump.
	if cur, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil && version != "" && version != "unknown" && strings.TrimSpace(string(cur)) == version {
		if err := writePdxJSON(root, pdxPath, dataDir); err != nil {
			return root, false, err
		}
		return root, false, nil
	}
	tmp := root + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return root, false, fmt.Errorf("create %s: %w", tmp, err)
	}
	if err := copyFS(tmp, src); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, fmt.Errorf("write VERSION: %w", err)
	}
	if err := writePdxJSON(tmp, pdxPath, dataDir); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, err
	}
	if err := stampManifest(tmp, version); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, err
	}
	_ = os.RemoveAll(root)
	if err := os.Rename(tmp, root); err != nil {
		_ = os.RemoveAll(tmp)
		return root, false, fmt.Errorf("rename %s: %w", tmp, err)
	}
	return root, true, nil
}

// stampManifest sets .claude-plugin/plugin.json "version" to the pdx version
// when it is a semver (1.0.0-alpha.527); "unknown" leaves the file as embedded.
func stampManifest(root, version string) error {
	if !semverRe.MatchString(version) {
		return nil
	}
	path := filepath.Join(root, ".claude-plugin", "plugin.json")
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

func writePdxJSON(root, pdxPath, dataDir string) error {
	b, _ := json.Marshal(map[string]string{"pdx": pdxPath, "data_dir": dataDir})
	return os.WriteFile(filepath.Join(root, "pdx.json"), append(b, '\n'), 0o644)
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

// RemovePluginDir deletes the extracted tree (and its .tmp sibling).
func RemovePluginDir(dataDir string) error {
	root := PluginRoot(dataDir)
	_ = os.RemoveAll(root + ".tmp")
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove %s: %w", root, err)
	}
	return nil
}

// mergePluginDirs adds pluginRoot to settings.env.CLAUDE_CODE_PLUGIN_DIRS
// (remove=false) or takes every entry under <data_dir>/cc-plugin/ out of it
// (remove=true). Other entries are kept in order; the env block and the
// key are created or deleted as needed. Entries are recognised by the
// cc-plugin prefix, not by equality, so a moved data dir's stale entry is
// still ours to replace.
func mergePluginDirs(settingsPath, dataDir, pluginRoot string, remove bool) error {
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
	prefix := filepath.Join(dataDir, PluginDirName) + string(filepath.Separator)
	kept := make([]string, 0, 4)
	for _, p := range strings.Split(current, string(os.PathListSeparator)) {
		if p == "" || isUnderPrefix(p, prefix) {
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

func isUnderPrefix(p, prefix string) bool {
	return strings.HasPrefix(filepath.Clean(p)+string(filepath.Separator), prefix)
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
