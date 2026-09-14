#!/bin/sh
# TDD test for scripts/check-goenv.sh (spec §3.1).
#
# Runs the preflight twice under an isolated GOENV/GIT_CONFIG_GLOBAL so it
# never touches this machine's real settings:
#   1. both settings present in the isolated files -> exit 0, no output
#   2. isolated files empty                        -> exit 1, both
#      "missing:" lines printed
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
check_script="$script_dir/check-goenv.sh"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

[ -f "$check_script" ] || fail "$check_script does not exist"

# --- Case 1: both settings present -> exit 0 ---
goenv_ok="$tmp/goenv-ok"
gitconfig_ok="$tmp/gitconfig-ok"
: > "$goenv_ok"
: > "$gitconfig_ok"

GOENV="$goenv_ok" go env -w GOPRIVATE=lab.protype.tw
git config --file "$gitconfig_ok" \
  'url.ssh://git@lab.protype.tw:9079/.insteadOf' 'https://lab.protype.tw/'

set +e
out_ok=$(GOENV="$goenv_ok" GIT_CONFIG_GLOBAL="$gitconfig_ok" sh "$check_script" 2>&1)
status_ok=$?
set -e

[ "$status_ok" = 0 ] || fail "expected exit 0 with both settings present, got $status_ok (output: $out_ok)"
[ -z "$out_ok" ] || fail "expected no output on success, got: $out_ok"

# --- Case 2: isolated files empty -> exit 1, both missing: lines ---
goenv_empty="$tmp/goenv-empty"
gitconfig_empty="$tmp/gitconfig-empty"
: > "$goenv_empty"
: > "$gitconfig_empty"

set +e
out_empty=$(GOENV="$goenv_empty" GIT_CONFIG_GLOBAL="$gitconfig_empty" sh "$check_script" 2>&1)
status_empty=$?
set -e

[ "$status_empty" = 1 ] || fail "expected exit 1 with empty settings, got $status_empty (output: $out_empty)"

echo "$out_empty" | grep -qx 'missing: go env -w GOPRIVATE=lab.protype.tw' \
  || fail "missing GOPRIVATE line in output: $out_empty"
echo "$out_empty" | grep -qx 'missing: git config --global url."ssh://git@lab.protype.tw:9079/".insteadOf "https://lab.protype.tw/"' \
  || fail "missing git config line in output: $out_empty"

echo "ok: check-goenv.sh preflight (RED->GREEN verified)"
