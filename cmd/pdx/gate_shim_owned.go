package main

import (
	"os"
	"strings"
)

// shimScript is the shim an old `pdx setup` wrote for tool, byte for byte (the generator of internal/gate/shim as of 2026-10-21, kept
// only so setup can tell a shim it wrote from a file that merely looks like one). pdxPath is the pdx the shim called.
func shimScript(tool, pdxPath string) string {
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

// shimPdxPath is the pdx path written into a shim (the default of its PDX_BIN line); ok is false when there is no such line.
func shimPdxPath(content string) (path string, ok bool) {
	const head = `PDX_BIN="${PDX_BIN:-`
	i := strings.Index(content, head)
	if i < 0 {
		return "", false
	}
	rest := content[i+len(head):]
	j := strings.Index(rest, "}\"\n")
	if j < 0 {
		return "", false
	}
	return shUnquoteInner(rest[:j]), true
}

// isShimOf reports whether the file at p is a shim an old setup wrote for tool: its content is exactly what shimScript writes for the pdx
// path it names. A file that merely carries a shim's comment or hand-over line, or a shim someone edited, is not (setup deletes by this).
func isShimOf(p, tool string) bool {
	b, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	pdx, ok := shimPdxPath(string(b))
	return ok && shimScript(tool, pdx) == string(b)
}
