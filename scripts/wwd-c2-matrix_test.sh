#!/bin/sh
# Test for scripts/wwd-c2-matrix.sh: the list, the arguments, and what a dry run prints for each case. A dry run runs nothing.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
matrix="$script_dir/wwd-c2-matrix.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }
has() { echo "$1" | grep -q -- "$2" || fail "$3: missing '$2'"; }
hasnt() { echo "$1" | grep -q -- "$2" && fail "$3: must not contain '$2'"; return 0; }

[ -f "$matrix" ] || fail "$matrix does not exist"
sh -n "$matrix" || fail "sh -n"
if command -v dash >/dev/null 2>&1; then dash -n "$matrix" || fail "dash -n"; fi

# spies in PATH: every command that would change something or reach a host records that it was called; a dry run calls none
spy="$tmp/spy"; spylog="$tmp/spy.log"; mkdir -p "$spy"; : > "$spylog"
for c in ssh curl pdx sqlite3 launchctl systemctl mkdir rm git sleep mv cp touch chmod; do
  printf '#!/bin/sh\necho "%s $*" >> "%s"\n' "$c" "$spylog" > "$spy/$c"; chmod +x "$spy/$c"
done
workdir="$tmp/work"; mkdir -p "$workdir"
dry() { PATH="$spy:$PATH" TMPDIR="$workdir" sh "$matrix" --dry-run "$@" 2>&1; }

# the list: fourteen cases, the four that need a hook say which
out=$(sh "$matrix" --list)
[ "$(echo "$out" | grep -c .)" -eq 14 ] || fail "--list must show 14 cases: $out"
for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14; do echo "$out" | grep -q "^ *$n  " || fail "--list has no case $n"; done
has "$out" "needs fault.log_upload_fail" "list"
has "$out" "needs fault.zone" "list"
has "$out" "needs fault.owner_active_file" "list"
has "$out" "needs fault.snapshot_delay_s" "list"

# arguments
if sh "$matrix" >/dev/null 2>&1; then fail "no arguments must be refused"; fi
if sh "$matrix" --dry-run --case 99 >/dev/null 2>&1; then fail "an unknown case must be refused"; fi
if sh "$matrix" --nonsense >/dev/null 2>&1; then fail "an unknown flag must be refused"; fi

# the whole matrix in a dry run: every case passes, nothing is made on this machine
out=$(dry --all)
has "$out" "passed: 1 2 3 4 5 6 7 8 9 10 11 12 13 14" "all cases"
hasnt "$out" "FAIL" "all cases"
[ -z "$(ls -A "$workdir")" ] || fail "a dry run made files in TMPDIR: $(ls -A "$workdir")"
[ ! -s "$spylog" ] || fail "a dry run called commands: $(head -5 "$spylog")"

# nothing the brief forbids, in the script or in what it prints
for bad in pkill killall pfctl iptables "kill -9" "kill -KILL" "chmod -R" "rm -rf /"; do
  if grep -v '^#' "$matrix" | grep -q -- "$bad"; then fail "the script uses '$bad'"; fi
done
hasnt "$out" "sudoers" "all cases"
# a signal goes to an exact pid (a number above 1), never to a pattern or a group
echo "$out" | grep -E "kill -(STOP|CONT)" | grep -vqE "kill -(STOP|CONT) [0-9]+$" && fail "a kill -STOP/-CONT must name an exact pid"
echo "$out" | grep -qE "kill -(STOP|CONT) (0|-1|1)$" && fail "a signal to pid 0, 1 or -1 reaches a group or everything"
# the pool token is read on the host, never printed
hasnt "$out" "Bearer [0-9a-f]\{16\}" "all cases"

# case by case: the fault each one makes, and what it expects at the end
out=$(dry --case 1);  has "$out" "fakeA" "case 1"; has "$out" "fakeB" "case 1"; has "$out" "expect: 200 409" "case 1"
out=$(dry --case 2);  has "$out" "launchctl bootout" "case 2"; has "$out" "launchctl bootstrap" "case 2"; has "$out" "expect: lost" "case 2"; has "$out" "expect: failed" "case 2"
out=$(dry --case 3);  has "$out" "kill -STOP" "case 3"; has "$out" "kill -CONT" "case 3"; has "$out" "heartbeat of the old claim (expect: 409)" "case 3"; has "$out" "result of the old claim (expect: 409)" "case 3"
out=$(dry --case 4);  has "$out" "launchctl kickstart -k" "case 4"; has "$out" "expect: air26" "case 4"
out=$(dry --case 5);  has "$out" "5b long" "case 5"; has "$out" "5c" "case 5"; has "$out" "needs hook fault.snapshot_delay_s" "case 5"
out=$(dry --case 6);  has "$out" "needs hook fault.log_upload_fail" "case 6"
out=$(dry --case 7);  has "$out" "fault/owner-active" "case 7"; hasnt "$out" ">> move the mouse" "case 7"
out=$(dry --manual --case 7); has "$out" ">> move the mouse on air26" "case 7 --manual"
out=$(dry --case 8);  has "$out" "needs hook fault.zone" "case 8"; has "$out" "two origins" "case 8"; has "$out" "started first, on the same worker (expect: mlab)" "case 8"
out=$(dry --case 9);  has "$out" "superseded_by:" "case 9"; has "$out" "c2-sleep-noint" "case 9"
out=$(dry --case 10); has "$out" "pdx test c2-expire" "case 10"; has "$out" "pdx test exit code (expect: 6)" "case 10"
out=$(dry --case 11)
for ip in 100.64.0.2 100.64.0.4 100.64.0.6 100.64.0.1; do has "$out" "$ip:7871/v1/info" "case 11 $ip"; done
has "$out" "Bearer c2-wrong-token" "case 11"; has "$out" "(expect: 401)" "case 11"
out=$(dry --case 12); has "$out" "c2-undeclared" "case 12"; has "$out" "--bad-option" "case 12"; has "$out" "../../etc/hosts" "case 12"; has "$out" "a;touch /tmp/wwd-c2-pwned" "case 12"
out=$(dry --case 13); has "$out" "show databases" "case 13"; has "$out" "systemctl --failed" "case 13"; has "$out" "sandbox probe" "case 13"
out=$(dry --case 14); has "$out" "air19 claimed nothing since this run began (expect: 0)" "case 14"; has "$out" "air19's worker is off" "case 14"

# the counted attempts: 0 for a job that succeeded at once or was reclaimed, 1 after one lapse, 2 after two (then failed/lost)
has "$(dry --case 1)" "attempts (expect: 0)" "case 1 attempts"
has "$(dry --case 2)" "attempts after the lapse (expect: 1)" "case 2 attempts"
has "$(dry --case 2)" "attempts (expect: 2)" "case 2 attempts"
has "$(dry --case 3)" "attempts (expect: 1)" "case 3 attempts"
has "$(dry --case 4)" "attempts (expect: 0)" "case 4 attempts"
has "$(dry --case 5)" "5a an infra release or a lapse was counted (expect: 1)" "case 5a attempts"
has "$(dry --case 5)" "5b short: attempts (expect: 0)" "case 5b short attempts"
has "$(dry --case 5)" "5b long: the lease lapsed once (expect: 1)" "case 5b long attempts"
has "$(dry --case 7)" "the reclaim did not count an attempt (expect: 0)" "case 7 attempts"

# every case other than 14 also checks air19, and ends by putting back what it changed
out=$(dry --case 4)
has "$out" "air19 claimed nothing during case 4" "air19 rule"
has "$out" "-- cleaning up" "cleanup"; has "$out" "rm -rf" "cleanup"; has "$out" "PUT" "cleanup (worker settings)"

# only the cases asked for run
out=$(dry --case 2,4)
has "$out" "== case 2:" "selection"; has "$out" "== case 4:" "selection"; hasnt "$out" "== case 3:" "selection"

echo "ok"
