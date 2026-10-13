package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// toolEnv is the seam behind the dev subprocess PATH: tests swap the home
// directory, the environment and the filesystem so they never read the real
// ~/.nvm. The zero value uses the real ones.
//
// The daemon may be started by a boot launcher whose PATH has no nvm, so pnpm
// (a `#!/usr/bin/env node` script), node and go are resolved against this PATH
// and the child gets it too (#2812).
type toolEnv struct {
	extra   []string // [dev] extra_path, highest preference
	getenv  func(string) string
	home    func() (string, error)
	readDir func(string) ([]os.DirEntry, error)
	isDir   func(string) bool
	isExec  func(string) bool
}

func (e toolEnv) env(k string) string {
	if e.getenv != nil {
		return e.getenv(k)
	}
	return os.Getenv(k)
}

func (e toolEnv) homeDir() string {
	f := e.home
	if f == nil {
		f = os.UserHomeDir
	}
	h, err := f()
	if err != nil {
		return ""
	}
	return h
}

func (e toolEnv) dirExists(p string) bool {
	if e.isDir != nil {
		return e.isDir(p)
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func (e toolEnv) executable(p string) bool {
	if e.isExec != nil {
		return e.isExec(p)
	}
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func (e toolEnv) list(p string) []os.DirEntry {
	f := e.readDir
	if f == nil {
		f = os.ReadDir
	}
	ents, _ := f(p)
	return ents
}

// parseNodeVersion parses "v24.14.0" into its numeric parts.
func parseNodeVersion(name string) ([3]int, bool) {
	var v [3]int
	if !strings.HasPrefix(name, "v") {
		return v, false
	}
	parts := strings.Split(name[1:], ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// newestNvmBin returns the bin directory of the newest nvm-installed node, or
// "" when there is none.
func (e toolEnv) newestNvmBin() string {
	nvm := e.env("NVM_DIR")
	if nvm == "" {
		h := e.homeDir()
		if h == "" {
			return ""
		}
		nvm = filepath.Join(h, ".nvm")
	}
	root := filepath.Join(nvm, "versions", "node")
	type cand struct {
		v   [3]int
		bin string
	}
	var cands []cand
	for _, ent := range e.list(root) {
		v, ok := parseNodeVersion(ent.Name())
		if !ok {
			continue
		}
		bin := filepath.Join(root, ent.Name(), "bin")
		if e.dirExists(bin) {
			cands = append(cands, cand{v, bin})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.Slice(cands, func(i, j int) bool { return versionLess(cands[i].v, cands[j].v) })
	return cands[len(cands)-1].bin
}

// toolDirs returns the PATH directories for dev subprocesses: extra_path,
// newest nvm node, common tool locations that exist, then the daemon's own
// PATH, de-duplicated with the first occurrence winning.
func (e toolEnv) toolDirs() []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" {
			return
		}
		// The child runs with Dir=repoRoot, so a relative dir would be
		// checked here against the daemon's cwd but resolved there against
		// repoRoot: pin it to the daemon's cwd now.
		if !filepath.IsAbs(d) {
			abs, err := filepath.Abs(d)
			if err != nil {
				return
			}
			d = abs
		}
		if seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, d := range e.extra {
		add(d)
	}
	add(e.newestNvmBin())
	common := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if h := e.homeDir(); h != "" {
		common = append(common, filepath.Join(h, ".local", "bin"), filepath.Join(h, "Library", "pnpm"))
	}
	for _, d := range common {
		if e.dirExists(d) {
			add(d)
		}
	}
	for _, d := range filepath.SplitList(e.env("PATH")) {
		add(d)
	}
	return out
}

// lookTool resolves name against the tool PATH. On failure the error names
// the tool and the directories searched (one line).
func (e toolEnv) lookTool(name string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		return name, nil
	}
	dirs := e.toolDirs()
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if e.executable(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q not found; searched: %s", name, strings.Join(dirs, ":"))
}

// childEnv is the daemon's environment with PATH replaced by the tool PATH.
func (e toolEnv) childEnv() []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+strings.Join(e.toolDirs(), string(os.PathListSeparator)))
}
