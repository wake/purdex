#!/bin/sh
# Test for scripts/wwd-deploy.sh: the dry run prints the commands a deploy would
# run, for each host, and runs none of them.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
deploy="$script_dir/wwd-deploy.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }

[ -f "$deploy" ] || fail "$deploy does not exist"

# a bin dir with the four builds, empty files are enough for a dry run
bin="$tmp/bin"
mkdir -p "$bin"
for t in darwin-arm64 darwin-amd64 linux-amd64; do : > "$bin/wwd-$t"; done

run() { WWD_BIN_DIR="$bin" sh "$deploy" "$@"; }

# each host: the right build, the right ssh target, the install command with the alias
out=$(run --dry-run mserv)
echo "$out" | grep -q "wwd-linux-amd64" || fail "mserv must ship the linux build: $out"
echo "$out" | grep -q "ssh mserv" || fail "mserv: ssh target: $out"
echo "$out" | grep -q "install --alias mserv" || fail "mserv: install command: $out"

out=$(run --dry-run air19)
echo "$out" | grep -q "wwd-darwin-amd64" || fail "air19 must ship the Intel build: $out"
echo "$out" | grep -q "ssh air19" || fail "air19: ssh target: $out"

out=$(run --dry-run air26)
echo "$out" | grep -q "wwd-darwin-arm64" || fail "air26 must ship the arm64 build: $out"
echo "$out" | grep -q "ssh air26" || fail "air26: ssh target: $out"

# mlab is this host: no ssh, no copy
out=$(run --dry-run mlab)
echo "$out" | grep -q "ssh" && fail "mlab is local, no ssh: $out"
echo "$out" | grep -q "install --alias mlab" || fail "mlab: install command: $out"

# flags pass through
out=$(run --dry-run --no-restart air26)
echo "$out" | grep -q -- "--no-restart" || fail "--no-restart not passed on: $out"

# a token file goes to the remote as a 0600 temporary, is passed by path and removed; the token value is never printed
echo "tok-secret-value" > "$tmp/tok"
out=$(run --dry-run --token-file "$tmp/tok" air26)
echo "$out" | grep -q -- "--token-file" || fail "token file not passed: $out"
echo "$out" | grep -q "chmod 600" || fail "the remote token copy must be 0600: $out"
echo "$out" | grep -q "rm -f" || fail "the remote token copy must be removed: $out"
echo "$out" | grep -q "tok-secret-value" && fail "the token value was printed"

# unknown host and missing build are refused
if run --dry-run elsewhere >/dev/null 2>&1; then fail "unknown host accepted"; fi
rm "$bin/wwd-linux-amd64"
if run --dry-run mserv >/dev/null 2>&1; then fail "a missing build was accepted"; fi

echo "ok"
