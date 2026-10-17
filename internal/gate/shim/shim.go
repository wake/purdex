// Package shim is the interception half of the host-load gate (spec 2026-10-15 §4): thin POSIX sh scripts named go, pnpm,
// npx, vitest and tsc in one directory that `pdx setup` puts at the front of PATH, each handing the call to `pdx gate`; the
// resolution of the real binary behind a shim; and the delimited ~/.zshenv block that puts the directory on PATH.
package shim

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Tools are the commands that get a shim. phpunit and pest are not here: they live in a project's vendor/bin, not on PATH
// (the mod prefixes those calls with `pdx gate --auto --`).
var Tools = []string{"go", "pnpm", "npx", "vitest", "tsc", "composer", "xcodebuild", "swift"}

// DirIn is the shim directory under home.
func DirIn(home string) string { return filepath.Join(home, ".config", "pdx", "shims") }

// Script is the shim for tool: it hands the call to `pdx gate --auto`, with pdx found through $PDX_BIN or, when that is not an
// executable file, the path of the pdx that wrote the shim. When neither exists (pdx moved or removed) the shim runs the real
// command by itself, found the way Resolve finds it, so the tools never stop working because pdx is gone.
func Script(tool, pdxPath string) string {
	return `#!/bin/sh
# Written by ` + "`pdx setup`" + ` (pdx gate shim for ` + tool + `); rewritten by it, removed by ` + "`pdx setup --uninstall-gate`" + `.
PDX_BIN="${PDX_BIN:-` + shQuoteInner(pdxPath) + `}"
if [ -x "$PDX_BIN" ]; then
  exec "$PDX_BIN" gate --auto --tool ` + tool + ` -- "$@"
fi
# pdx is gone: run the real ` + tool + `, the first one on PATH that is not in this directory
# this script's own real place: it may have been started through a symlink (an alias in another directory)
me=$0
n=0
while [ -L "$me" ] && [ "$n" -lt 40 ]; do
  t=$(readlink "$me") || break
  case $t in /*) me=$t ;; *) me=$(dirname "$me")/$t ;; esac
  n=$((n + 1))
done
self=$(cd "$(dirname "$me")" 2>/dev/null && pwd -P)
old_ifs=$IFS; IFS=:
for d in $PATH; do
  IFS=$old_ifs
  [ -n "$d" ] || continue
  dir=$(cd "$d" 2>/dev/null && pwd -P) || continue
  [ "$dir" = "$self" ] && continue
  p="$dir/` + tool + `"
  if [ -x "$p" ] && [ ! -d "$p" ]; then
    # a symlink that leads back into this directory is the shim again, whatever its name or directory
    n=0
    while [ -L "$p" ] && [ "$n" -lt 40 ]; do
      t=$(readlink "$p") || break
      case $t in /*) p=$t ;; *) p=$(dirname "$p")/$t ;; esac
      n=$((n + 1))
    done
    pd=$(cd "$(dirname "$p")" 2>/dev/null && pwd -P)
    if [ "$n" -lt 40 ] && [ "$pd" != "$self" ]; then
      exec "$dir/` + tool + `" "$@"
    fi
  fi
  IFS=:
done
IFS=$old_ifs
echo "` + tool + `: no ` + tool + ` on PATH besides the pdx shim" >&2
exit 127
`
}

// shUnquoteInner is the inverse of shQuoteInner.
func shUnquoteInner(s string) string {
	r := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, "$", "\\`", "`")
	return r.Replace(s)
}

// shQuoteInner makes s safe inside a double-quoted sh word (the ${VAR:-…} default).
func shQuoteInner(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return r.Replace(s)
}

// ErrNoRealBinary is Resolve's answer when PATH has nothing but the shim directory to offer.
var ErrNoRealBinary = errors.New("no real binary on PATH besides the pdx shims")

// Resolve is the real binary behind a shim: the first executable regular file named tool on pathEnv, skipping the shim
// directory and any entry that reaches it by a symlink (a candidate whose real path lies in the shim directory is refused, so
// no alias can send the gate back into its own shim). It is called on every run, from the PATH of that run, so an `nvm use`
// is followed. The returned path is the entry as PATH names it (not symlink-resolved: a program that finds its own files
// through the name it was started by keeps working).
func Resolve(tool, pathEnv, shimDir string) (string, error) {
	realShim := realPath(shimDir)
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" {
			continue // an empty entry means the current directory: never a place a shim-bypassing binary may come from
		}
		if inside(realPath(d), realShim) {
			continue
		}
		cand := filepath.Join(d, tool)
		if !isExecutable(cand) {
			continue
		}
		if inside(realPath(cand), realShim) {
			continue
		}
		return cand, nil
	}
	return "", fmt.Errorf("%s: %w", tool, ErrNoRealBinary)
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if a, err := filepath.Abs(p); err == nil {
		p = a
	}
	return filepath.Clean(p)
}

// inside reports whether p is dir or lies under it.
func inside(p, dir string) bool {
	if p == dir {
		return true
	}
	return strings.HasPrefix(p, dir+string(filepath.Separator))
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
