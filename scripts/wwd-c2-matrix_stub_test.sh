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
STUB_HOST=$1; export STUB_HOST
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
    -d) if [ "$2" = "@-" ]; then body=$(cat); else body=$2; fi; shift ;;
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
started=""; case "${STUB_HOST:-mlab}" in air26) started=$STUB_START_AIR26 ;; *) started=$STUB_START_MLAB ;; esac
if [ "$tcp" -eq 1 ]; then
  printf '%s\n' "$method $path $body" >> "$STUB/tcp.log"
  [ "$auth" = "Bearer $(cat "$HOME/.config/wwd/pool.token")" ] || { code=401; resp='{"error":"unauthorized"}'; }
  if [ "$code" = 200 ] && [ "$method $path" = "POST /v1/jobs" ]; then code=404; fi
else
  case "$method $path" in
    "GET /v1/info") resp='{"version":"v0-stub","proto":1,"worker":{"enabled":true}'${STUB_FAULT:+',"fault":{"owner_active_file":true}'}${STUB_ZONE:+',"fault":{"zone":true}'}'}' ;;
    "GET /v1/worker") resp=$STUB_WORKER_JSON ;;
    "PUT /v1/worker") printf '%s\n' "$body" >> "$STUB/put.log" ;;
    "GET /v1/jobs?state=queued") resp='[]' ;;
    "POST /v1/jobs") resp='{"id":"j_stub_post"}'; printf '%s\n' "$body" >> "$STUB/post.log" ;;
    "GET /v1/jobs/"*) resp="{\"state\":\"$STUB_STATE\",\"claim\":{\"worker\":\"air26\"},\"result\":{\"host\":\"air26\",\"reason\":\"\",\"started_at\":\"$started\"}}" ;;
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
cat > "$bin/launchctl" <<'EOF'
#!/bin/sh
# records each call; the first bootstrap fails (a service that does not come up at once); "list" gives STUB_PID
echo "$*" >> "$STUB/launchctl.log"
case "$1" in
  list) printf '{\n\t"PID" = %s;\n}\n' "$STUB_PID" ;;
  bootstrap) [ -e "$STUB/bootstrap-failed-once" ] || { : > "$STUB/bootstrap-failed-once"; exit 1; } ;;
esac
exit 0
EOF
cat > "$bin/sqlite3" <<'EOF'
#!/bin/sh
echo "$*" >> "$STUB/sql.log"
case "$*" in
  *"select attempts"*) echo 1 ;;
  *"select id"*) echo j_stub_exp ;;
  *) echo 0 ;;
esac
EOF
chmod +x "$bin/ssh" "$bin/curl" "$bin/pdx" "$bin/sqlite3" "$bin/launchctl"

# an owner_rule with spaces, a single quote, double quotes: what broke the old way of keeping the settings
worker_json='{"enabled":false,"slots":{"heavy":1,"light":1},"owner_rule":"idle 5 min; it'"'"'s \"mine\" now"}'
export STUB_WORKER_JSON="$worker_json"

run() {   # run STATE CASE... : the script for real, in the fake world; the output is in $tmp/out, the exit code is returned
  rm -f "$stub"/*.log "$stub"/bootstrap-failed-once
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
grep -q "claim_worker='air19' and created_at >=" "$stub/sql.log" || fail "case 14 must count only the claims since this run began"

# case 13 with --keep: the scratch repo stays, and the PHP probe really uses its database
rm -f "$stub"/*.log
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --keep --case 13 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "case 13: exit code"; }
has "PASS case 13" "case 13"
kept=$(sed -n 's/^kept //p' "$tmp/out")
[ -d "$kept/repo" ] || fail "--keep must leave the scratch repo"
grep -q 'new PDO' "$kept/repo/scripts/db_probe.php" || fail "the PHP probe must connect to the database"
grep -q "CREATE TABLE" "$kept/repo/scripts/db_probe.php" || fail "the PHP probe must write to the database"
grep -q "exit(\$v === 'round-trip' ? 0 : 1)" "$kept/repo/scripts/db_probe.php" || fail "the PHP probe must exit non-zero when the round trip fails"
grep -q 'db = "per-job"' "$kept/repo/.wwd/tasks.toml" || fail "the php task must ask for a per-job database"
rm -rf "$kept"

# every worker setting the script changed is put back (the stub keeps what was PUT), the scratch repo is gone, the jobs are cancelled
run succeeded 11 || fail "case 11 again"
# case 11 sets three workers (mlab and air26 on, mserv off) and puts back three: six PUTs, the last three are what GET gave
[ "$(wc -l < "$stub/put.log" | tr -d ' ')" -eq 6 ] || fail "expected 6 worker PUTs (3 set, 3 put back), got $(wc -l < "$stub/put.log")"
[ "$(tail -3 "$stub/put.log" | grep -c -F "$worker_json")" -eq 3 ] || fail "the last three PUTs must be, byte for byte, the settings GET gave (spaces and quotes in the owner_rule included): $(tail -3 "$stub/put.log")"
[ -z "$(ls -A "$tmp" | grep wwd-c2 || true)" ] || fail "the scratch directory was left in TMPDIR"
[ -s "$stub/cancel.log" ] || fail "the jobs it made were not cancelled"

# ---- a service this script took down comes back, also when the script stops in between ---------------------------------------------
# case 2 takes air26's wwd down, then brings it up: the stub refuses the first bootstrap, so the script stops there (set -e)
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 2 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "case 2 must stop when the service does not come up"
[ "$(grep -c '^bootout' "$stub/launchctl.log")" -eq 1 ] || fail "bootout should have been called once: $(cat "$stub/launchctl.log")"
[ "$(grep -c '^bootstrap' "$stub/launchctl.log")" -eq 2 ] || fail "the clean-up must bootstrap the service again: $(cat "$stub/launchctl.log")"

# the same case when every service comes up: two down, two up, and the clean-up brings nothing up again
rm -f "$stub"/*.log; : > "$stub/bootstrap-failed-once"      # the stub's launchctl never fails now
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 2 > "$tmp/out" 2>&1 || rc=$?
[ "$(grep -c '^bootout' "$stub/launchctl.log")" -eq 2 ] || fail "case 2 takes the service down twice: $(cat "$stub/launchctl.log")"
[ "$(grep -c '^bootstrap' "$stub/launchctl.log")" -eq 2 ] || fail "the clean-up must not bring up a service that is up: $(cat "$stub/launchctl.log")"

# the pid of a service must be a number above 1 and the wwd binary: 0 would signal a group
for badpid in 0 1 "" abc 99999999; do
  rc=0
  HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_PID="$badpid" STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 3 > "$tmp/out" 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || fail "case 3 must stop when the pid is '$badpid'"
  has "is not the pid of wwd on air26" "pid '$badpid'"
done

# a ps that calls every pid the wwd binary: then only the number check can refuse 0 and 1 (a signal to 0 reaches a whole group)
fakeps="$tmp/fakeps"; mkdir -p "$fakeps"
printf '#!/bin/sh\necho /x/wwd/bin/wwd\n' > "$fakeps/ps"; chmod +x "$fakeps/ps"
for badpid in 0 1 -1; do
  rc=0
  HOME="$home" PATH="$fakeps:$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_PID="$badpid" STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 3 > "$tmp/out" 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || fail "case 3 must stop when the pid is '$badpid', also if ps calls it wwd"
  has "is not the pid of wwd on air26" "pid '$badpid' with a ps that agrees"
done

# the fake workers claim with the labels of a real worker (the job's labels must be a subset), also in the exact part of case 1
rm -f "$stub"/*.log
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --case 1 > "$tmp/out" 2>&1 || true
[ "$(grep -c ' /v1/jobs/.*/claim ' "$stub/tcp.log")" -ge 2 ] || fail "case 1 must send claims over TCP: $(cat "$stub/tcp.log" 2>/dev/null)"
[ "$(grep ' /v1/jobs/.*/claim ' "$stub/tcp.log" | grep -c '"labels":\["os:darwin","arch:arm64"\]')" -eq "$(grep -c ' /v1/jobs/.*/claim ' "$stub/tcp.log")" ] || fail "every claim must carry the worker labels: $(cat "$stub/tcp.log")"

# case 8 compares the two start times: the job of mlab (the origin under pressure) must be first; if air26's came first, it FAILs
rm -f "$stub"/*.log
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded STUB_ZONE=1 STUB_START_MLAB=2026-10-18T10:00:05Z STUB_START_AIR26=2026-10-18T10:00:01Z C2_TIMEOUT_SCALE=0 sh "$matrix" --case 8 > "$tmp/out" 2>&1 && fail "case 8 must FAIL when air26's job started first"
has "FAIL" "case 8, air26 first"
rm -f "$stub"/*.log
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded STUB_ZONE=1 STUB_START_MLAB=2026-10-18T10:00:01Z STUB_START_AIR26=2026-10-18T10:00:05Z C2_TIMEOUT_SCALE=0 sh "$matrix" --case 8 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "case 8 must PASS when mlab's job started first"; }

# terminated while the owner-active file exists on air26 (case 7, not --manual): the file is gone afterwards
faultfile="$home/.local/share/wwd/fault/owner-active"
rm -f "$faultfile"
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_FAULT=1 STUB_STATE=running C2_TIMEOUT_SCALE=1 sh "$matrix" --case 7 > "$tmp/out" 2>&1 &
pid=$!
n=0; while [ ! -e "$faultfile" ] && [ "$n" -lt 100 ]; do sleep 0.2; n=$((n + 1)); done
[ -e "$faultfile" ] || { kill "$pid" 2>/dev/null || true; cat "$tmp/out" >&2; fail "case 7 never made the owner-active file"; }
kill -TERM "$pid"          # TERM: a background sh ignores INT
wait "$pid" || true
[ ! -e "$faultfile" ] || fail "the owner-active file was left on air26 after the script was terminated"

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
