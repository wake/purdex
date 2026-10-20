#!/bin/sh
# Test for scripts/release-gate.sh: the gate sends every release task for one commit, from a clean clone of the remote, and says in one table
# what happened to each. The remote is a local repository and pdx is a stand-in that records how it is called.
set -eu

script_dir=$(cd "$(dirname "$0")" && pwd)
gate="$script_dir/release-gate.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

fail() { echo "FAIL: $1" >&2; exit 1; }
has() { echo "$1" | grep -q -- "$2" || fail "$3: wanted '$2' in: $1"; }
hasnt() { echo "$1" | grep -q -- "$2" && fail "$3: did not want '$2' in: $1" || true; }

[ -f "$gate" ] || fail "$gate does not exist"
command -v jq >/dev/null || { echo "ok (skipped: jq is not installed)"; exit 0; }

g() { d=$1; shift; git -C "$d" -c user.email=t@t -c user.name=t -c commit.gpgsign=false "$@" >/dev/null 2>&1; }

# a remote with one commit, and a second one for the "a new cut" case
remote="$tmp/remote"
git init -q -b main "$remote"
echo one > "$remote/a.txt"
g "$remote" add -A; g "$remote" commit -q -m one
c1=$(git -C "$remote" rev-parse HEAD)

# pdx stand-in: logs "<cwd> <args>", answers a JSON report per task; FAKE_RC_<task> (with dashes as _) sets the exit code, FAKE_CACHE names a
# task answered from the cache, FAKE_NOREPORT names a task that prints nothing
cat > "$tmp/pdx" <<'EOF'
#!/bin/sh
echo "$(pwd) $*" >> "$FAKE_LOG"
[ "$1" = test ] || exit 9
task=$2
var=FAKE_RC_$(echo "$task" | tr '-' '_')
rc=$(eval "echo \${$var:-0}")
[ "${FAKE_NOREPORT:-}" = "$task" ] && exit 3
cache=false; [ "${FAKE_CACHE:-}" = "$task" ] && cache=true
state=succeeded; [ "$rc" != 0 ] && state=failed
[ -n "${FAKE_MOVE_TO:-}" ] && [ "$task" = go-all ] && git checkout -q --detach "$FAKE_MOVE_TO"
# reports that a gate must not read as a pass although pdx exited 0
[ "${FAKE_EMPTY:-}" = "$task" ] && { echo '{}'; exit 0; }
[ "${FAKE_BADSTATE:-}" = "$task" ] && state=failed
[ "${FAKE_WRONGTASK:-}" = "$task" ] && task=another-task
host='"host":"air26",'; [ "${FAKE_NOHOST:-}" = "$task" ] && host=''
printf '{"id":"job-%s","task":"%s","state":"%s","exit":%s,%s"cache_hit":%s,"duration_s":12.7}\n' "$task" "$task" "$state" "$rc" "$host" "$cache"
exit "$rc"
EOF
chmod +x "$tmp/pdx"

clone="$tmp/gate-clone"
run() { # run <args...>: the gate with the stand-in; sets out and rc
	: > "$tmp/log"
	set +e
	out=$(FAKE_LOG="$tmp/log" sh "$gate" --pdx "$tmp/pdx" --clone "$clone" --remote "$remote" "$@" 2>&1)
	rc=$?
	set -e
}
ALL="go-all spa-full tsc-force e2e lint wwd-linux wwd-linux-vet wwd-scripts-mysql wwd-scripts-c2 wwd-scripts-c2-stub wwd-scripts-deploy wwd-scripts-gate"

# 1. all pass: exit 0, every task sent from the clone at the commit, one row each
export FAKE_CACHE=lint
run "$c1"
[ "$rc" = 0 ] || fail "all pass: exit $rc: $out"
for t in $ALL; do
	grep -q " test $t --json\$" "$tmp/log" || fail "task $t was not sent: $(cat "$tmp/log")"
	has "$out" "$t" "table row of $t"
done
hasnt "$(cat "$tmp/log")" "mod-all" "mod-all is not sent without --mod"
grep -q "^$clone test" "$tmp/log" || fail "pdx must run in the gate clone: $(cat "$tmp/log")"
[ "$(git -C "$clone" rev-parse HEAD)" = "$c1" ] || fail "the clone is not at the commit"
[ "$(git -C "$clone" remote get-url origin)" = "$remote" ] || fail "the clone's origin is not the remote"
has "$out" "job-go-all" "the job id is in the table"
has "$out" "air26" "the host is in the table"
echo "$out" | grep "lint" | grep -q "cache" || fail "the cache hit of lint is not marked: $out"
echo "$out" | grep "go-all" | grep -q "cache" && fail "go-all ran and must not be marked as cache: $out"
has "$out" "12 passed" "the summary counts"
has "$out" "1 from cache" "the summary counts the cache"
unset FAKE_CACHE

# 2. a task that fails: exit 1, marked
export FAKE_RC_spa_full=1
run "$c1"
unset FAKE_RC_spa_full
[ "$rc" = 1 ] || fail "a failed task: exit $rc"
echo "$out" | grep "spa-full" | grep -q "FAIL" || fail "spa-full must be marked FAIL: $out"
has "$out" "1 failed" "failure count"

# 3. a task with no report (pdx died): a failure, not a pass
export FAKE_NOREPORT=e2e
run "$c1"
unset FAKE_NOREPORT
[ "$rc" = 1 ] || fail "no report: exit $rc"
echo "$out" | grep "e2e" | grep -q "FAIL" || fail "a task with no report must be marked FAIL: $out"
has "$out" "no report" "the reason"

# 4. --skip: not sent, written as skipped (never as a pass); everything else passing is incomplete (3), a failure is still 1
run --skip wwd-linux --skip wwd-linux-vet "$c1"
[ "$rc" = 3 ] || fail "skips: exit $rc, want 3 (incomplete): $out"
grep -q "wwd-linux" "$tmp/log" && fail "a skipped task was sent: $(cat "$tmp/log")"
echo "$out" | grep "wwd-linux-vet" | grep -q "跳過" || fail "a skipped task must say 跳過: $out"
has "$out" "10 passed" "skipped tasks are not counted as passed"
has "$out" "2 skipped" "skip count"
has "$out" "INCOMPLETE" "the table says the gate is not whole"
export FAKE_RC_lint=1
run --skip wwd-linux "$c1"
unset FAKE_RC_lint
[ "$rc" = 1 ] || fail "a failure beats a skip: exit $rc"
run --skip no-such-task "$c1"
[ "$rc" = 2 ] || fail "an unknown --skip task: exit $rc"
# a task that is not part of this run cannot be skipped: it would vanish from the table and the gate would read as whole (#3708 R1)
run --skip mod-all "$c1"
[ "$rc" = 2 ] || fail "--skip mod-all without --mod: exit $rc, want 2: $out"
has "$out" "--mod" "the refusal says why"
run --mod --skip mod-all --skip wwd-linux --skip wwd-linux-vet "$c1"
[ "$rc" = 3 ] || fail "--mod with --skip mod-all: exit $rc, want 3: $out"
echo "$out" | grep "mod-all" | grep -q "跳過" || fail "mod-all must say 跳過: $out"

# 5. --no-cache goes to every pdx test; --mod adds mod-all
run --no-cache "$c1"
[ "$(grep -c -- "--no-cache" "$tmp/log")" = 12 ] || fail "--no-cache on each of the 12 tasks: $(cat "$tmp/log")"
run --mod "$c1"
grep -q " test mod-all --json" "$tmp/log" || fail "--mod must send mod-all"

# 6. a commit the remote does not have: nothing is sent, exit 2
run 0123456789012345678901234567890123456789
[ "$rc" = 2 ] || fail "unknown commit: exit $rc: $out"
[ ! -s "$tmp/log" ] || fail "nothing may be sent for an unknown commit: $(cat "$tmp/log")"
run
[ "$rc" = 2 ] || fail "no commit given: exit $rc"

# 7. a new cut: fetched, checked out, and leftovers of the earlier run are gone (a snapshot would carry them)
echo two > "$remote/a.txt"; g "$remote" add -A; g "$remote" commit -q -m two
c2=$(git -C "$remote" rev-parse HEAD)
echo junk > "$clone/leftover.txt"
mkdir -p "$clone/node_modules" && echo junk > "$clone/node_modules/x"
run "$c2"
[ "$rc" = 0 ] || fail "second cut: exit $rc: $out"
[ "$(git -C "$clone" rev-parse HEAD)" = "$c2" ] || fail "the clone did not move to the new cut"
[ ! -e "$clone/leftover.txt" ] && [ ! -e "$clone/node_modules" ] || fail "untracked leftovers must be removed before the snapshot"

# 8. a report is not a pass because pdx exited 0 (#3708 R2): the task, the state, the exit, the job id and the host must all be there
for bad in FAKE_EMPTY FAKE_BADSTATE FAKE_WRONGTASK FAKE_NOHOST; do
	export "$bad=wwd-linux"
	run "$c2"
	unset "$bad"
	[ "$rc" = 1 ] || fail "$bad: exit $rc, want 1: $out"
	echo "$out" | grep "wwd-linux " | grep -q "FAIL" || fail "$bad: wwd-linux must be FAIL: $out"
done

# 9. one gate run per clone, by a lock of the system (lockf, or flock): a second run is refused while one holds it, and the lock goes with the
# process that holds it, however it ends (no stale lock to take over)
hpid=""
if command -v lockf >/dev/null 2>&1; then lockf -k "$clone.lock" sleep 30 & hpid=$!
elif command -v flock >/dev/null 2>&1; then flock "$clone.lock" sleep 30 & hpid=$!
fi
if [ -n "$hpid" ]; then
	sleep 1
	run "$c2"
	[ "$rc" = 2 ] || fail "while the lock is held: exit $rc, want 2: $out"
	has "$out" "another release gate" "the refusal says why"
	[ ! -s "$tmp/log" ] || fail "nothing may be sent while the clone is in use"
	pkill -P "$hpid" 2>/dev/null || true # the holder's sleep: the lock goes with it, nothing else is cleaned up
	wait "$hpid" 2>/dev/null || true
	run "$c2"
	[ "$rc" = 0 ] || fail "after the holder is gone the lock is free: exit $rc: $out"
fi

# 9b. a TERM while the tasks are waiting ends them too (no pdx test is left waiting for a gate that is gone) and the gate exits 130
cat > "$tmp/pdx-slow" <<'EOF'
#!/bin/sh
echo $$ >> "$FAKE_PIDS"
exec sleep 60
EOF
chmod +x "$tmp/pdx-slow"
: > "$tmp/pids"
FAKE_PIDS="$tmp/pids" sh "$gate" --pdx "$tmp/pdx-slow" --clone "$clone" --remote "$remote" "$c2" > "$tmp/slow.out" 2>&1 &
gpid=$!
n=0; while [ "$(wc -l < "$tmp/pids")" -lt 12 ] && [ $n -lt 100 ]; do sleep 0.2; n=$((n + 1)); done
[ "$(wc -l < "$tmp/pids")" -ge 12 ] || fail "the 12 tasks were not all started: $(cat "$tmp/slow.out")"
kill -TERM "$gpid"
set +e; wait "$gpid"; grc=$?; set -e
[ "$grc" = 130 ] || fail "a TERM: exit $grc, want 130: $(cat "$tmp/slow.out")"
sleep 1
for p in $(cat "$tmp/pids"); do
	kill -0 "$p" 2>/dev/null && fail "pdx test $p is still running after the gate was terminated"
done

# 10. the commit must be a hash that the remote has on a branch: a ref name, or a commit that only the clone has, is refused
run main
[ "$rc" = 2 ] || fail "a ref name: exit $rc, want 2: $out"
g "$clone" commit -q --allow-empty -m "only here"
local_only=$(git -C "$clone" rev-parse HEAD)
run "$local_only"
[ "$rc" = 2 ] || fail "a commit not on the remote: exit $rc, want 2: $out"
has "$out" "not on any branch" "the refusal says why"
[ ! -s "$tmp/log" ] || fail "nothing may be sent for a commit the remote does not have"

# 11. the clone must still be at the commit when the tasks are done (a snapshot is taken as each is sent)
FAKE_MOVE_TO=$c1 run "$c2"
[ "$rc" = 1 ] || fail "a clone that moved: exit $rc, want 1: $out"
has "$out" "moved" "the table says the clone moved"
unset FAKE_MOVE_TO

# 12. every run has a directory of its own
runs="$(dirname "$clone")/runs"
before=$(ls "$runs" | wc -l)
run "$c2"; run "$c2"
after=$(ls "$runs" | wc -l)
[ $((after - before)) = 2 ] || fail "two runs must leave two run directories: $before → $after"

echo ok
