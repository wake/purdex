#!/bin/sh
# Runs scripts/wwd-c2-matrix.sh for real (no --dry-run) against a fake world: ssh, curl, pdx and sqlite3 in PATH are stubs that answer
# like a quiet wwd pool. It checks what a dry run cannot: the quoting of the commands, the JSON the script builds and reads, the wait
# loops, PASS and FAIL and the exit code, and that the clean-up runs also after a FAIL. A signal goes only to a fake wwd process made
# here (cases 2 and 3); the cases that freeze or restart a real service (4, 5, 7 with a service) are for the hosts.
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
[ -z "${STUB:-}" ] || printf '%s: %s\n' "$STUB_HOST" "$*" >> "$STUB/ssh.log"      # what was asked of each host, for the tests that count signals
[ -z "${STUB_SSH_FAIL:-}" ] || case "$*" in *"$STUB_SSH_FAIL"*) exit 255 ;; esac
if [ -n "${STUB_SSH_PS_ONCE:-}" ]; then case "$*" in *"ps -p"*) [ ! -e "$STUB/ps-asked" ] || exit 255; : > "$STUB/ps-asked" ;; esac; fi
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
    "GET /v1/jobs?state=queued") resp='{"jobs":['${STUB_QUEUED:+'{"id":"j_other"}'}'],"cursor":"0","epoch":"e"}' ;;
    "POST /v1/jobs") if [ -n "${STUB_POST_NOID:-}" ]; then resp='{"error":"nope"}'; else resp='{"id":"j_stub_post"}'; fi; printf '%s\n' "$body" >> "$STUB/post.log" ;;
    "GET /v1/jobs/"*"/log") if [ -n "${STUB_NO_ROUNDTRIP:-}" ]; then resp='no database here'; else resp='db=wwd_0123456789abcdef0123456789abcdef read=round-trip'; fi ;;
    "GET /v1/jobs/"*)
      # a refused job says why in the one line of its summary tail: the first POST of case 12 is the undeclared task, the rest are bad arguments
      n=0; [ ! -f "$STUB/post.log" ] || n=$(wc -l < "$STUB/post.log" | tr -d ' '); if [ "$n" -le 1 ]; then word=task_unknown; else word=args_invalid; fi; word=${STUB_REFUSAL:-$word}
      resp="{\"state\":\"$STUB_STATE\",\"claim\":{\"worker\":\"air26\"},\"result\":{\"host\":\"air26\",\"reason\":\"\",\"started_at\":\"$started\",\"summary\":{\"tail\":[\"$word: detail of the refusal\"]}}}" ;;
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
# records each call; "list" gives STUB_PID
echo "$*" >> "$STUB/launchctl.log"
case "$1" in
  list) printf '{\n\t"PID" = %s;\n}\n' "$STUB_PID" ;;
esac
exit 0
EOF
cat > "$bin/sqlite3" <<'EOF'
#!/bin/sh
echo "$*" >> "$STUB/sql.log"
case "$*" in
  *"select attempts"*) echo 1 ;;
  *"claim_worker='air19'"*)   # the first question is the list at the start (empty); later ones give a new id when STUB_AIR19_LATER is set
    [ -e "$STUB/air19-asked" ] || { [ ! -e /tmp/wwd-c2-ran.log ] || : > "$STUB/order-bad"; }   # the probe log must be emptied before the list is taken
    if [ -e "$STUB/air19-asked" ]; then [ -z "${STUB_AIR19_LATER:-}" ] || echo "$STUB_AIR19_LATER"; [ -z "${STUB_AIR19_PROBE:-}" ] || echo "job=x host=air19" >> /tmp/wwd-c2-ran.log; else : > "$STUB/air19-asked"; fi ;;
  *"select id"*) echo j_stub_exp ;;
  *) echo 0 ;;
esac
EOF
cat > "$bin/mysql" <<'EOF'
#!/bin/sh
# the dev instance's admin account lists the job databases through wwdctl; STUB_MYSQL_FAIL: it cannot connect; STUB_MYSQL_DBS: what is left
echo "$*" >> "$STUB/mysql.log"
[ -z "${STUB_MYSQL_FAIL:-}" ] || exit 1
# STUB_MYSQL_GONE_AFTER=n: the database is there for the first n questions, then the drop has happened
if [ -n "${STUB_MYSQL_GONE_AFTER:-}" ] && [ "$(wc -l < "$STUB/mysql.log" | tr -d ' ')" -gt "$STUB_MYSQL_GONE_AFTER" ]; then exit 0; fi
case "$*" in *wwd_list_dbs*) [ -z "${STUB_MYSQL_DBS:-}" ] || echo "$STUB_MYSQL_DBS" ;; esac
exit 0
EOF
chmod +x "$bin/ssh" "$bin/curl" "$bin/pdx" "$bin/sqlite3" "$bin/launchctl" "$bin/mysql"

# an owner_rule with spaces, a single quote, double quotes: what broke the old way of keeping the settings
worker_json='{"enabled":false,"slots":{"heavy":1,"light":1},"owner_rule":"idle 5 min; it'"'"'s \"mine\" now"}'
export STUB_WORKER_JSON="$worker_json"

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
has "ok   undeclared: refusal code" "case 12"; has "ok   bad option: refusal code" "case 12"; has "ok   shell metacharacters: refusal code" "case 12"
[ "$(grep -c '"task":"c2-undeclared"' "$stub/post.log")" -eq 1 ] || fail "case 12 must post the undeclared task once"
grep -q '"args":\["../../etc/hosts"\]' "$stub/post.log" || fail "case 12 must post the path-escape argument as JSON"
grep -q 'a;touch /tmp/wwd-c2-pwned' "$stub/post.log" || fail "case 12 must post the metacharacter argument"
grep -q '"git_dir":"' "$stub/post.log" || fail "the submission must carry git_dir"

# a refusal with another code than the case expects FAILs (every job is told args_invalid, so the undeclared task has the wrong one)
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=failed STUB_REFUSAL=args_invalid C2_TIMEOUT_SCALE=0 sh "$matrix" --case 12 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a wrong refusal code must make the exit code non-zero"
has "FAIL undeclared: refusal code: got 'args_invalid', expected 'task_unknown'" "case 12 with a wrong code"
# an origin that does not answer a submission with an id: each of the four FAILs at once and none goes on to wait for (or ask about) a job
# that is not there
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=failed STUB_POST_NOID=1 C2_TIMEOUT_SCALE=0 sh "$matrix" --case 12 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a submission without an id must make the exit code non-zero"
[ "$(grep -c 'FAIL .*: the origin took the submission' "$tmp/out")" -eq 4 ] || { cat "$tmp/out" >&2; fail "each of the four refusals must FAIL when there is no id"; }
! grep -q 'refusal code' "$tmp/out" || fail "a submission without an id must not be checked for a refusal code"

# precheck: the queue list is {"jobs":[...]}: a queued job makes the matrix refuse to start, an empty list does not
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded STUB_QUEUED=1 C2_TIMEOUT_SCALE=0 sh "$matrix" --case 14 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a queued job must stop the matrix before it starts"
has "FAIL: 1 jobs are queued already" "precheck"
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded STUB_QUEUED=1 C2_TIMEOUT_SCALE=0 sh "$matrix" --force --case 14 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "--force goes on with a queued job"; }
# pdx on air26 (case 8 submits from there): not found there stops case 8's run; the other cases do not need it
mkdir -p "$tmp/nopdx"; for c in ssh curl sqlite3 launchctl mysql; do cp "$bin/$c" "$tmp/nopdx/$c"; done
rc=0
HOME="$home" PATH="$tmp/nopdx:/usr/bin:/bin" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --case 8 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "case 8 without pdx on air26 must stop"
has "FAIL: pdx is not on air26's non-interactive PATH" "precheck pdx"
HOME="$home" PATH="$tmp/nopdx:/usr/bin:/bin" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --case 14 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "case 14 does not need pdx on air26"; }

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
grep -q "claim_worker='air19'" "$stub/sql.log" || fail "case 14 must ask for the claims of air19"
! grep -q "created_at" "$stub/sql.log" || fail "the air19 count must not depend on when a job was made (a job queued before the run counts too)"

# case 13 with --keep: the scratch repo stays, and the PHP probe really uses its database
rm -f "$stub"/*.log
# the stub's "mserv" is this machine: a real socket stands in for the docker socket the case asks the host for
python3 -I -c 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1])' "$tmp/docker.sock" || fail "make a stand-in socket"
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 C2_DOCKER_SOCK="$tmp/docker.sock" C2_DOCKER_PING="echo OK" sh "$matrix" --keep --case 13 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "case 13: exit code"; }
has "PASS case 13" "case 13"
grep -q 'defaults-extra-file=.*mysql-admin.cnf .*wwd_list_dbs' "$stub/mysql.log" || fail "case 13 must list the databases with the admin account through wwdctl: $(cat "$stub/mysql.log")"
has "ok   the job really had a database" "case 13 control"
kept=$(sed -n 's/^kept //p' "$tmp/out")
[ -d "$kept/repo" ] || fail "--keep must leave the scratch repo"
grep -q 'new PDO' "$kept/repo/scripts/db_probe.php" || fail "the PHP probe must connect to the database"
grep -q "CREATE TABLE" "$kept/repo/scripts/db_probe.php" || fail "the PHP probe must write to the database"
grep -q "exit(\$v === 'round-trip' ? 0 : 1)" "$kept/repo/scripts/db_probe.php" || fail "the PHP probe must exit non-zero when the round trip fails"
grep -q 'db = "per-job"' "$kept/repo/.wwd/tasks.toml" || fail "the php task must ask for a per-job database"
rm -rf "$kept"

# case 13 when the databases cannot be listed: ? is not 0, so the case FAILs instead of passing on nothing
for var in STUB_MYSQL_FAIL=1 STUB_MYSQL_DBS=wwd_0123456789abcdef0123456789abcdef STUB_NO_ROUNDTRIP=1; do
  rc=0
  env HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 C2_DOCKER_SOCK="$tmp/docker.sock" C2_DOCKER_PING="echo OK" "$var" sh "$matrix" --case 13 > "$tmp/out" 2>&1 || rc=$?
  [ "$rc" -ne 0 ] || fail "case 13 with $var must FAIL"
done
env HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 C2_DOCKER_SOCK="$tmp/docker.sock" C2_DOCKER_PING="echo OK" STUB_MYSQL_FAIL=1 sh "$matrix" --case 13 > "$tmp/out" 2>&1 || true
has "FAIL its database is gone (within 45 s of the result): got '?', expected '0'" "case 13 with a mysql that cannot be asked"
env HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 C2_DOCKER_SOCK="$tmp/docker.sock" C2_DOCKER_PING="echo OK" STUB_NO_ROUNDTRIP=1 sh "$matrix" --case 13 > "$tmp/out" 2>&1 || true
has "FAIL the job really had a database" "case 13 control"

# the database is removed after the result is posted: case 13 asks again until it is gone (here at the third question, 6 s)
rm -f "$stub"/*.log
env HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=1 C2_DOCKER_SOCK="$tmp/docker.sock" C2_DOCKER_PING="echo OK" \
  STUB_MYSQL_DBS=wwd_0123456789abcdef0123456789abcdef STUB_MYSQL_GONE_AFTER=2 sh "$matrix" --case 13 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "case 13 must wait for the drop"; }
has "PASS case 13" "case 13 with a late drop"
[ "$(wc -l < "$stub/mysql.log" | tr -d ' ')" -ge 3 ] || fail "case 13 must ask again while the database is there: $(cat "$stub/mysql.log")"

# every worker setting the script changed is put back (the stub keeps what was PUT), the scratch repo is gone, the jobs are cancelled
run succeeded 11 || fail "case 11 again"
# case 11 sets three workers (mlab and air26 on, mserv off) and puts back three: six PUTs, the last three are what GET gave
[ "$(wc -l < "$stub/put.log" | tr -d ' ')" -eq 6 ] || fail "expected 6 worker PUTs (3 set, 3 put back), got $(wc -l < "$stub/put.log")"
[ "$(tail -3 "$stub/put.log" | grep -c -F "$worker_json")" -eq 3 ] || fail "the last three PUTs must be, byte for byte, the settings GET gave (spaces and quotes in the owner_rule included): $(tail -3 "$stub/put.log")"
[ -z "$(ls -A "$tmp" | grep wwd-c2 || true)" ] || fail "the scratch directory was left in TMPDIR"
[ -s "$stub/cancel.log" ] || fail "the jobs it made were not cancelled"

# ---- case 2 freezes air26's wwd twice and thaws it twice, and stops nothing --------------------------------------------------------------
# a fake wwd (the pid the "service manager" gives): the signals the script sends are read from what it asked of air26
mkdir -p "$tmp/wwd/bin"
printf '#!/bin/sh\nwhile :; do sleep 1; done\n' > "$tmp/wwd/bin/wwd"; chmod +x "$tmp/wwd/bin/wwd"
"$tmp/wwd/bin/wwd" & fake2=$!
rm -f "$stub"/*.log
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_PID="$fake2" STUB_STATE=queued C2_TIMEOUT_SCALE=0 sh "$matrix" --case 2 > "$tmp/out" 2>&1 || rc=$?
kill -CONT "$fake2" 2>/dev/null || true; kill -KILL "$fake2" 2>/dev/null || true
[ "$(grep -c "^air26: kill -STOP $fake2\$" "$stub/ssh.log")" -eq 2 ] || { cat "$stub/ssh.log" >&2; fail "case 2 freezes air26's wwd twice (an exact pid)"; }
[ "$(grep -c "^air26: kill -CONT $fake2\$" "$stub/ssh.log")" -eq 2 ] || { cat "$stub/ssh.log" >&2; fail "case 2 thaws air26's wwd twice"; }
if [ -s "$stub/launchctl.log" ] && grep -qv '^list' "$stub/launchctl.log"; then fail "case 2 must not stop or start a service: $(cat "$stub/launchctl.log")"; fi
has "ok   attempts after the lapse" "case 2"
has "FAIL state after two lapses: got 'queued', expected 'failed'" "case 2 (the stub's job never fails: the check is made)"

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

# an owner-active file that was there before the run is not ours: it is still there afterwards
mkdir -p "$(dirname "$faultfile")"; : > "$faultfile"
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_FAULT=1 STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 7 > "$tmp/out" 2>&1 || true
has "was already on air26" "case 7 with a file that was there"
[ -e "$faultfile" ] || fail "a file that was there before the run was removed by the clean-up"
rm -f "$faultfile"

# a clean-up that cannot do its work does not end as a success: the host is named and the exit code is not 0 (see the thaw tests below)

# the question "is the file there?" fails (ssh down): the script stops, makes nothing, and the file that was there stays
mkdir -p "$(dirname "$faultfile")"; : > "$faultfile"
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_FAULT=1 STUB_SSH_FAIL="set -C" STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 7 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a failed probe must stop the script with a non-zero exit"
has "could not make" "case 7 with a failed probe"
[ -e "$faultfile" ] || fail "a file that could not be probed was removed by the clean-up"
rm -f "$faultfile"

# a wwd the script froze whose pid cannot be asked about at clean-up (ps fails over ssh) is reported, not called thawed
mkdir -p "$tmp/wwd/bin"
printf '#!/bin/sh\nwhile :; do sleep 1; done\n' > "$tmp/wwd/bin/wwd"; chmod +x "$tmp/wwd/bin/wwd"
"$tmp/wwd/bin/wwd" & fakewwd=$!
rm -f "$stub"/*.log "$stub/ps-asked"
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_PID="$fakewwd" STUB_SSH_PS_ONCE=1 STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 3 > "$tmp/out" 2>&1 || rc=$?
kill -CONT "$fakewwd" 2>/dev/null || true; kill -KILL "$fakewwd" 2>/dev/null || true
[ "$rc" -ne 0 ] || fail "a wwd that may still be frozen must make the exit code non-zero"
has "the clean-up could not put back:.*thaw:air26" "thaw with a failed ps"

# ssh works but ps itself fails at clean-up (exit 3): that is "could not ask", not "not wwd": the record stays and the exit is non-zero
mkdir -p "$tmp/psbin"
printf '#!/bin/sh\nif [ -e "$STUB/ps-asked" ]; then exit 3; fi\n: > "$STUB/ps-asked"\nexec /bin/ps "$@"\n' > "$tmp/psbin/ps"; chmod +x "$tmp/psbin/ps"
"$tmp/wwd/bin/wwd" & fakewwd=$!
rm -f "$stub"/*.log "$stub/ps-asked"
rc=0
HOME="$home" PATH="$tmp/psbin:$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_PID="$fakewwd" STUB_STATE=running C2_TIMEOUT_SCALE=0 sh "$matrix" --case 3 > "$tmp/out" 2>&1 || rc=$?
kill -CONT "$fakewwd" 2>/dev/null || true; kill -KILL "$fakewwd" 2>/dev/null || true
[ "$rc" -ne 0 ] || fail "a ps that fails must make the exit code non-zero"
has "the clean-up could not put back:.*thaw:air26" "thaw with a ps that fails"

# air19 claims a job after the run began (a job made before it, no matter): case 14 FAILs
rm -f "$stub"/*.log "$stub/air19-asked"
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_AIR19_LATER=j_new STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --case 14 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a claim by air19 must make the exit code non-zero"
has "FAIL air19 claimed nothing since this run began: got '1', expected '0'" "case 14 with a claim by air19"
# a line in air19's probe log (it ran a job, even if the job was taken back afterwards) counts too, with no row in wwd.db
rm -f "$stub"/*.log "$stub/air19-asked"
rc=0
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_AIR19_PROBE=1 STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --case 14 > "$tmp/out" 2>&1 || rc=$?
[ "$rc" -ne 0 ] || fail "a job that ran on air19 must make the exit code non-zero"
has "FAIL air19 claimed nothing since this run began: got '1', expected '0'" "case 14 with a probe line on air19"

# the probe log of air19 is emptied BEFORE the list of its claims is taken (a probe line written in between would be lost)
rm -f "$stub"/*.log "$stub/air19-asked" "$stub/order-bad"
echo "job=old host=air19" > /tmp/wwd-c2-ran.log
HOME="$home" PATH="$bin:$PATH" TMPDIR="$tmp" STUB="$stub" STUB_STATE=succeeded C2_TIMEOUT_SCALE=0 sh "$matrix" --case 14 > "$tmp/out" 2>&1 || { cat "$tmp/out" >&2; fail "case 14 on a quiet pool"; }
[ ! -e "$stub/order-bad" ] || fail "the list of air19's claims was taken before its probe log was emptied"

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
