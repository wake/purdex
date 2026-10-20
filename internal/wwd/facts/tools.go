package facts

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wake/purdex/internal/execstat"
	"github.com/wake/purdex/internal/wwd/wire"
)

// RunFunc runs a program and returns its stdout. The default goes through
// execstat so every fork is counted.
type RunFunc func(ctx context.Context, tag, bin string, args ...string) ([]byte, error)

// ToolsReader reports the toolchain versions (spec §3.1 `tools`). Asking each
// tool forks, so the answer is cached for TTL (default 10 minutes); a tool that
// fails to answer keeps its last good value, one that was never found is null.
// The pdx shims are never used: they route through the gate, and the record
// must describe the real tools a job would run. One value belongs to one
// goroutine.
type ToolsReader struct {
	Run         RunFunc
	Now         func() time.Time
	TTL         time.Duration
	PathDirs    []string // where go, node, pnpm and a plain php are looked up
	PHPDirs     []string // where phpNN binaries live (Herd's bin)
	XcodePlists []string // Xcode's Contents/version.plist files, first that exists wins

	last time.Time
	val  wire.Tools
	php  map[string]string // phpNN binary -> its last good version
}

// probeRun is the default RunFunc (through execstat) with PATH set to the probe's own list, so a script such as pnpm (#!/usr/bin/env node) finds its interpreter
// the way a job does, not only on the daemon's PATH.
func probeRun(path string) RunFunc {
	return func(ctx context.Context, tag, bin string, args ...string) ([]byte, error) {
		cmd := execstat.CommandContext(ctx, tag, bin, args...)
		cmd.Env = append(withoutPath(os.Environ()), "PATH="+path)
		return cmd.Output()
	}
}

func withoutPath(environ []string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return out
}

// probePath is the PATH of every probe command: exactly the job PATH (ToolPath).
func probePath(home, pathEnv string, extra []string) string { return ToolPath(home, pathEnv, extra) }

// NewToolsReader is a reader over this host's usual places.
// extra is the config's [tools] extra_path after ResolveExtraDirs; the job PATH comes from the same ToolDirs call.
func NewToolsReader(home, pathEnv string, extra []string) *ToolsReader {
	return &ToolsReader{
		Run: probeRun(probePath(home, pathEnv, extra)), Now: time.Now, TTL: 10 * time.Minute,
		PathDirs:    ToolDirs(home, pathEnv, extra),
		PHPDirs:     []string{filepath.Join(home, "Library", "Application Support", "Herd", "bin"), "/opt/homebrew/bin", "/usr/local/bin"},
		XcodePlists: []string{"/Applications/Xcode.app/Contents/version.plist"},
	}
}

// DefaultToolDirs is the search path: the process PATH minus pdx shim
// directories, the usual package-manager places, and the newest nvm node.
func DefaultToolDirs(home, pathEnv string) []string {
	var dirs []string
	add := func(d string) {
		if d != "" && !contains(dirs, d) && !isShimDir(d) {
			dirs = append(dirs, d)
		}
	}
	for _, d := range filepath.SplitList(pathEnv) {
		add(d)
	}
	add(newestNvmBin(home))
	for _, d := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin", "/usr/bin"} {
		add(d)
	}
	return dirs
}

// ToolDirs is DefaultToolDirs with the configured extra directories in front (an explicit setting wins). It is the one list both the
// tool probe and a job's PATH are built from, so a tool the facts report is a tool a job finds.
func ToolDirs(home, pathEnv string, extra []string) []string {
	var dirs []string
	for _, d := range extra {
		if !isShimDir(d) && !contains(dirs, d) { // a shim routes through the gate: never a real tool, for the probe or for a job
			dirs = append(dirs, d)
		}
	}
	for _, d := range DefaultToolDirs(home, pathEnv) {
		if !contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return dirs
}

// ToolPath is ToolDirs as a PATH value.
func ToolPath(home, pathEnv string, extra []string) string {
	return strings.Join(ToolDirs(home, pathEnv, extra), string(os.PathListSeparator))
}

// ResolveExtraDirs turns the config's extra_path entries (absolute, or ~/ for home) into the directories that exist. A missing
// entry, or one that is not a directory, is skipped and logged once.
func ResolveExtraDirs(home string, entries []string, logf func(format string, args ...any)) []string {
	var out []string
	for _, e := range entries {
		p := e
		if strings.TrimSpace(p) == "" { // Clean("") is ".": the daemon's working directory would lead every PATH
			logf("wwd: tools.extra_path: skipping an empty entry")
			continue
		}
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
		p = filepath.Clean(p)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			logf("wwd: tools.extra_path: skipping %s (not a directory on this host)", p)
			continue
		}
		if !contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// isShimDir is a pdx shim directory (~/.config/pdx/shims): its go, pnpm, … route
// through the gate and are not the real tools.
func isShimDir(d string) bool {
	if strings.Contains(filepath.ToSlash(d), "/pdx/shims") {
		return true
	}
	// a link to the shim directory, or to a shim, is a shim as well
	real, err := filepath.EvalSymlinks(d)
	return err == nil && strings.Contains(filepath.ToSlash(real), "/pdx/shims")
}

func newestNvmBin(home string) string {
	ents, err := os.ReadDir(filepath.Join(home, ".nvm", "versions", "node"))
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range ents {
		if e.IsDir() && (best == "" || semverLess(best, e.Name())) {
			best = e.Name()
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(home, ".nvm", "versions", "node", best, "bin")
}

var reDigits = regexp.MustCompile(`\d+`)

// semverLess orders version strings by their numeric parts.
func semverLess(a, b string) bool {
	pa, pb := reDigits.FindAllString(a, -1), reDigits.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

var (
	reGoVersion   = regexp.MustCompile(`^go version go(\S+)`)
	reDotted      = regexp.MustCompile(`^v?(\d+(?:\.\d+)+\S*)$`)
	reXcodeBundle = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)
)

// parseToolVersion extracts the version out of a tool's answer; "" when the
// answer is not one.
func parseToolVersion(tool, out string) string {
	out = strings.TrimSpace(out)
	if tool == "go" {
		if m := reGoVersion.FindStringSubmatch(out); m != nil {
			return m[1]
		}
		return ""
	}
	if m := reDotted.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

func xcodeVersionFromPlist(b []byte) string {
	if m := reXcodeBundle.FindSubmatch(b); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

func isExec(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0o111 != 0
}

func lookIn(dirs []string, name string) string {
	for _, d := range dirs {
		if isShimDir(d) {
			continue
		}
		if p := filepath.Join(d, name); isExec(p) && !isShimDir(p) {
			return p
		}
	}
	return ""
}

var rePHPName = regexp.MustCompile(`^php(\d*)$`)

// Get returns the tools record, refreshing it when older than TTL.
func (r *ToolsReader) Get() wire.Tools {
	now := r.Now()
	if !r.last.IsZero() && now.Sub(r.last) < r.TTL {
		return cloneTools(r.val)
	}
	r.last = now
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	one := func(tool string, prev *string, args ...string) *string {
		bin := lookIn(r.PathDirs, tool)
		if bin == "" {
			return nil // not installed
		}
		cctx, c := context.WithTimeout(ctx, 5*time.Second)
		defer c()
		out, err := r.Run(cctx, "wwd.tool."+tool, bin, args...)
		if v := parseToolVersion(tool, string(out)); err == nil && v != "" {
			return &v
		}
		return prev // no answer this time: keep what was known
	}
	r.val.Go = one("go", r.val.Go, "version")
	r.val.Node = one("node", r.val.Node, "-v")
	r.val.Pnpm = one("pnpm", r.val.Pnpm, "-v")
	r.val.PHP = r.phpVersions(ctx)
	r.val.Xcode = r.xcode(r.val.Xcode)
	return cloneTools(r.val)
}

// phpVersions probes every phpNN binary once (the same file listed in both
// search lists is one binary) and reports the distinct versions. Each binary
// keeps its own last good answer, so one transient failure does not drop a
// version; a binary that is gone takes its version with it.
func (r *ToolsReader) phpVersions(ctx context.Context) []string {
	var bins []string
	seenBin := map[string]bool{}
	for _, d := range append(append([]string{}, r.PHPDirs...), r.PathDirs...) {
		if isShimDir(d) {
			continue
		}
		ents, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range ents {
			p := filepath.Join(d, e.Name())
			if rePHPName.MatchString(e.Name()) && !seenBin[p] && isExec(p) && !isShimDir(p) {
				seenBin[p] = true
				bins = append(bins, p)
			}
		}
	}
	known := make(map[string]string, len(bins))
	for _, b := range bins {
		cctx, c := context.WithTimeout(ctx, 5*time.Second)
		o, err := r.Run(cctx, "wwd.tool.php", b, "-r", "echo PHP_VERSION;")
		c()
		if v := parseToolVersion("php", string(o)); err == nil && v != "" {
			known[b] = v
		} else if old, ok := r.php[b]; ok {
			known[b] = old
		}
	}
	r.php = known
	seen := map[string]bool{}
	out := []string{}
	for _, v := range known {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return semverLess(out[i], out[j]) })
	return out
}

func (r *ToolsReader) xcode(prev *string) *string {
	for _, p := range r.XcodePlists {
		if b, err := os.ReadFile(p); err == nil {
			if v := xcodeVersionFromPlist(b); v != "" {
				return &v
			}
			return prev
		}
	}
	return nil
}

func cloneTools(t wire.Tools) wire.Tools {
	t.Go, t.Node, t.Pnpm, t.Xcode = clonePtr(t.Go), clonePtr(t.Node), clonePtr(t.Pnpm), clonePtr(t.Xcode)
	t.PHP = append([]string{}, t.PHP...)
	return t
}
