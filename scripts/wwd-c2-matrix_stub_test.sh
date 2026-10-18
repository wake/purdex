#!/bin/sh
# Runs scripts/wwd-c2-matrix.sh for real (no --dry-run) against a fake world: ssh, curl, pdx and sqlite3 in PATH are stubs that answer
# like a quiet wwd pool. It checks what a dry run cannot: the quoting of the commands, the JSON the script builds and reads, the wait
# loops, PASS and FAIL and the exit code, and that the clean-up runs also after a FAIL. Only cases that send no signal are run here
# (1, 2, 3, 4, 5, 7 stop or freeze a service: those are for the hosts).
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
matrix="$script_dir/wwd-c2-matrix.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }

# ---- the fake world -----------------------------------------------------------------------------------------------------
bin="$tmp/bin"; home="$tmp/home"; stub="$tmp/stub"
mkdir -p "$bin" "$home/.config/wwd" "$stub"
printf 'stub-token-value' > "$home/.config/wwd/pool.token"

# ssh HOST CMD: the "remote" is this machine
cat > "$bin/ssh" <<'EOF'
#!/bin/sh
while [ "$1" = "-o" ]; do shift 2; done
shift
exec sh -c "$*"
EOF
cat > "$bin/curl" <<'EOF'
#!/bin/sh
# a tiny wwd: the socket (token-free) and the TCP port (needs "Bearer <contents of pool.token>")
method=GET; body=""; auth=""; fmt=""; out=""; url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -X) method=$2; shift ;;
    -d) body=$2; shift ;;
    -H) case "$2" in Authorization:*) auth=${2#Authorization: } ;; esac; shift ;;
    -w) fmt=$2; shift ;;
    -o) out=$2; shift ;;
    --unix-socket|--max-time) shift ;;
    http*) url=$1 ;;
  esac
  shift
done
path=${url#http://*/}; path=/${path#/}
tcp=0; case "$url" in http://wwd/*) ;; *) tcp=1; path=/${url#http://*:7871/} ;; esac
code=200; resp='{}'
if [ "$tcp" -eq 1 ]; then
  [ "$auth" = "Bearer $(cat "$HOME/.config/wwd/pool.token")" ] || { code=401; resp='{"error":"unauthorized"}'; }
  if [ "$code" = 200 ] && [ "$method $path" = "POST /v1/jobs" ]; then code=404; fi
else
  case "$method $path" in
    "GET /v1/info") resp='{"version":"v0-stub","proto":1,"worker":{"enabled":true}}' ;;
    "GET /v1/worker") resp='{"enabled":false,"slots":{"heavy":1,"light":1},"owner_rule":""}' ;;
    "PUT /v1/worker") echo "$body" >> "$STUB/put.log" ;;
    "GET /v1/jobs?state=queued") resp='[]' ;;
    "POST /v1/jobs") resp='{"id":"j_stub_post"}'; echo "$body" >> "$STUB/post.log" ;;
    "GET /v1/jobs/"*) resp="{\"state\":\"$STUB_STATE\",\"claim\":{\"worker\":\"air26\"},\"result\":{\"host\":\"air26\",\"reason\":\"\"}}" ;;
    "POST /v1/jobs/"*"/cancel") echo "$path" >> "$STUB/cancel.log" ;;
  esac
fi
case "$fmt" in
  *http_code*) [ "$out" = /dev/null ] || printf '%s' "$resp"; printf "$(printf '%s' "$fmt" | sed 's/%{http_code}/%s/')" "$code" ;;
  *) printf '%s' "$resp" ;;
esac
EOF
cat > "$bin/pdx" <<'EOF'
#!/bin/sh
# pdx test --detach --json TASK -> {"id":...}; pdx test c2-expire (foreground) -> exit 6
[ "$1" = test ] || exit 2
shift
if [ "$1" = --detach ]; then shift 2; echo "{\"id\":\"j_stub_$1\"}"; exit 0; fi
[ "$1" = c2-expire ] && exit 6
exit 0
EOF
cat > "$bin/sqlite3" <<'EOF'
#!/bin/sh
case "$*" in
  *"select attempts"*) echo 1 ;;
  *"select id"*) echo j_stub_exp ;;
  *) echo 0 ;;
esac
EOF
chmod +x "$bin/ssh" "$bin/curl" "$bin/pdx" "$bin/sqlite3"

run() {   # run STATE CASE... : the script for real, in the fake world; the output is in $tmp/out, the exit code is returned
  rm -f "$stub"/*.log
  HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=$1 C2_TIMEOUT_SCALE=0 \
    sh "$matrix" --case "$2" > "$tmp/out" 2>&1
}
has() { grep -q -- "$1" "$tmp/out" || { cat "$tmp/out" >&2; fail "$2: missing '$1'"; }; }
hasnt() { if grep -q -- "$1" "$tmp/out"; then cat "$tmp/out" >&2; fail "$2: must not contain '$1'"; fi; }

# ---- cases that pass -----------------------------------------------------------------------------------------------------
run failed 12 || { cat "$tmp/out" >&2; fail "case 12 against a pool that refuses: exit code"; }
has "PASS case 12" "case 12"
has "ok   undeclared: refused (state)" "case 12"
[ "$(grep -c '"task":"c2-undeclared"' "$stub/post.log")" -eq 1 ] || fail "case 12 must post the undeclared task once"
grep -q '"args":\["../../etc/hosts"\]' "$stub/post.log" || fail "case 12 must post the path-escape argument as JSON"
grep -q 'a;touch /tmp/wwd-c2-pwned' "$stub/post.log" || fail "case 12 must post the metacharacter argument"
grep -q '"git_dir":"' "$stub/post.log" || fail "the submission must carry git_dir"

run expired 10 || { cat "$tmp/out" >&2; fail "case 10: exit code"; }
has "PASS case 10" "case 10"; has "ok   pdx test exit code" "case 10"; has "ok   state" "case 10"

run succeeded 11 || { cat "$tmp/out" >&2; fail "case 11: exit code"; }
has "PASS case 11" "case 11"
[ "$(grep -c 'ok   .* with a wrong token' "$tmp/out")" -eq 28 ] || fail "case 11 must check 7 routes on 4 hosts with a wrong token"
[ "$(grep -c 'ok   .* without a token' "$tmp/out")" -eq 28 ] || fail "case 11 must check 7 routes on 4 hosts without a token"
has "ok   mlab POST /v1/jobs on TCP is not a route" "case 11"
hasnt "stub-token-value" "case 11 (the token must never be printed)"

run succeeded 14 || { cat "$tmp/out" >&2; fail "case 14: exit code"; }
has "PASS case 14" "case 14"

# every worker setting the script changed is put back (the stub keeps what was PUT), the scratch repo is gone, the jobs are cancelled
run succeeded 11 || fail "case 11 again"
[ -s "$stub/put.log" ] || fail "the worker settings were not put back"
[ "$(grep -c '"enabled":false' "$stub/put.log")" -ge 1 ] || fail "the saved worker settings must be what GET gave"
[ -z "$(ls -A "$tmp" | grep wwd-c2 || true)" ] || fail "the scratch directory was left in TMPDIR"
[ -s "$stub/cancel.log" ] || fail "the jobs it made were not cancelled"

# ---- a case that fails -----------------------------------------------------------------------------------------------------
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=failed C2_TIMEOUT_SCALE=0 sh "$matrix" --case 10 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a case whose job ended 'failed' instead of 'expired' must make the script exit non-zero"
has "FAIL state: got 'failed', expected 'expired'" "case 10 as FAIL"
has "FAIL case 10" "case 10 as FAIL"
has "failed: 10" "the summary"
has "-- cleaning up" "clean-up after a FAIL"
[ -z "$(ls -A "$tmp" | grep wwd-c2 || true)" ] || fail "the scratch directory was left after a FAIL"

echo "ok"
