#!/bin/sh
# The release gate: every release task of .wwd/tasks.toml, for one commit, through `pdx test`, and one table of what happened to each.
#
#   scripts/release-gate.sh [--no-cache] [--mod] [--skip <task>]... <commit>
#
# `pdx test` sends the working tree of the repository it is run in (it has no option for a commit), so the gate keeps a clean clone of its own,
# moves it to <commit> (detached) and sends the tasks from there: the tree is the commit's and nothing else. Every task is sent at once, each by
# its own `pdx test <task> --json` that waits for its job; the queue decides who runs where and when. Results come back as they come.
#
# A result that was filed for the very same tree (the key is the whole snapshot tree, the task, its definition and the host's toolchain) is
# answered from the cache at once and marked "cache" in the table; --no-cache runs everything again.
#
#   --mod            also send mod-all (the mod suite; the lead sends it only when cmd/pdx/plugin changed)
#   --skip <task>    do not send this task (repeatable). It is written as 跳過 in the table, never counted as a pass, and the gate is then
#                    INCOMPLETE (exit 3) however the others went. Without --skip a task that cannot run (exit 5: no host holds its labels) fails.
#   --clone <dir>    the gate's clone (default ~/.local/share/purdex-gate/clone)
#   --remote <url>   its origin (default git@github.com:wake/purdex.git; never a local checkout, whose main is often behind the cut)
#   --pdx <path>     the pdx to run (default pdx)
#
# Exit: 0 every task passed · 1 a task failed or gave no report · 2 bad usage or the clone could not be made · 3 nothing failed but some task was skipped.
set -u

GATE_TASKS="go-all spa-full tsc-force e2e lint wwd-linux wwd-linux-vet wwd-scripts-mysql wwd-scripts-c2 wwd-scripts-c2-stub wwd-scripts-deploy wwd-scripts-gate"
MOD_TASK="mod-all"

usage() { sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; }
die() { echo "release-gate: $1" >&2; exit 2; }

nocache="" with_mod=0 skips="" commit=""
clone="${HOME:-/tmp}/.local/share/purdex-gate/clone"
remote="git@github.com:wake/purdex.git"
pdx="pdx"
while [ $# -gt 0 ]; do
	case $1 in
	--no-cache) nocache="--no-cache" ;;
	--mod) with_mod=1 ;;
	--skip) [ $# -ge 2 ] || die "--skip needs a task"; skips="$skips $2"; shift ;;
	--clone) [ $# -ge 2 ] || die "--clone needs a directory"; clone=$2; shift ;;
	--remote) [ $# -ge 2 ] || die "--remote needs a url"; remote=$2; shift ;;
	--pdx) [ $# -ge 2 ] || die "--pdx needs a path"; pdx=$2; shift ;;
	-h | --help) usage; exit 0 ;;
	-*) die "unknown option $1" ;;
	*) [ -z "$commit" ] || die "one commit only"; commit=$1 ;;
	esac
	shift
done
[ -n "$commit" ] || { usage >&2; exit 2; }
command -v jq >/dev/null 2>&1 || die "jq is needed to read the reports"

tasks=$GATE_TASKS
[ "$with_mod" = 1 ] && tasks="$tasks $MOD_TASK"
for s in $skips; do
	# only a task that is part of this run can be skipped: one that is not would leave no row, and the gate would read as whole
	case " $tasks " in
	*" $s "*) ;;
	*) if [ "$s" = "$MOD_TASK" ]; then die "--skip $MOD_TASK needs --mod (it is not part of this run)"; fi
		die "--skip $s: not a task of the gate ($tasks)" ;;
	esac
done

# a hash, not a ref name: the commit is looked up in what the remote has, never in a local branch of the clone
case $commit in
*[!0-9a-fA-F]*) die "give a commit hash (7 to 40 hex digits), not $commit" ;;
esac
[ ${#commit} -ge 7 ] && [ ${#commit} -le 40 ] || die "give a commit hash (7 to 40 hex digits), not $commit"

# one gate run per clone: it is checked out, cleaned and snapshotted by every task, so two runs at once would send each other's tree. The lock is
# one the system keeps (lockf on macOS, flock where there is one) and a small process holds it for as long as this script lives: it looks
# for this pid every 0.2 s and ends when it is gone, so the lock goes with the script however that ends (a kill -9 too): there is no stale lock to
# take over and no pid to mistake for another. The holder says it has the lock by writing its own pid to a file; one that never does found it taken.
export GIT_TERMINAL_PROMPT=0
mkdir -p "$(dirname "$clone")" || die "cannot make $(dirname "$clone")"
marker=$(mktemp "${TMPDIR:-/tmp}/release-gate-lock.XXXXXX") || die "cannot make a temporary file"
rm -f "$marker"
holder_sh='echo $$ > "$1.tmp" && mv "$1.tmp" "$1"; while kill -0 "$2" 2>/dev/null; do sleep 0.2; done'
if command -v lockf >/dev/null 2>&1; then
	lockf -t 0 -k "$clone.lock" sh -c "$holder_sh" sh "$marker" $$ 2>/dev/null &
elif command -v flock >/dev/null 2>&1; then
	flock -n -E 75 "$clone.lock" sh -c "$holder_sh" sh "$marker" $$ 2>/dev/null &
else
	die "neither lockf nor flock is here: cannot keep two gate runs off one clone"
fi
lockproc=$!
n=0
while [ ! -s "$marker" ] && kill -0 $lockproc 2>/dev/null && [ $n -lt 100 ]; do sleep 0.1; n=$((n + 1)); done
[ -s "$marker" ] || die "another release gate is using $clone: wait for it"
holder=$(cat "$marker")
trap 'kill "$holder" 2>/dev/null; rm -f "$marker"' EXIT

# the clone: its origin is the remote, it holds the commit, it is detached at it and has nothing the commit does not have
if [ -d "$clone/.git" ]; then
	git -C "$clone" remote set-url origin "$remote" || die "cannot point $clone at $remote"
else
	git clone -q --no-checkout "$remote" "$clone" || die "cannot clone $remote"
fi
git -C "$clone" fetch -q --prune origin || die "cannot fetch $remote"
full=$(git -C "$clone" rev-parse --verify -q "$commit^{commit}") || die "$commit is not in $remote (is it pushed?)"
# the clone also holds objects of earlier remotes and commits made in it: the commit has to be on a branch of this fetch
[ -n "$(git -C "$clone" for-each-ref --count=1 --contains "$full" refs/remotes/origin)" ] || die "$commit is not on any branch of $remote (is it pushed?)"
git -C "$clone" checkout -q --detach --force "$full" || die "cannot check out $commit"
git -C "$clone" clean -qffdx || die "cannot clean $clone"

runs="$(dirname "$clone")/runs"
mkdir -p "$runs" || die "cannot make $runs"
work=$(mktemp -d "$runs/$(date +%Y%m%d-%H%M%S)-$(echo "$full" | cut -c1-9).XXXXXX") || die "cannot make a run directory in $runs"
echo "release gate at $full (clone $clone, logs $work)"

# a signal ends the `pdx test` calls too: none is left waiting for a gate that is gone (the jobs they sent stay in the queue)
pids=""
trap 'trap - INT TERM HUP; kill $pids 2>/dev/null; wait $pids 2>/dev/null; echo "release gate: interrupted"; exit 130' INT TERM HUP
for t in $tasks; do
	case " $skips " in *" $t "*) continue ;; esac
	(
		cd "$clone" || exit 125
		"$pdx" test "$t" --json $nocache > "$work/$t.json" 2> "$work/$t.err" &
		child=$!
		trap 'kill $child 2>/dev/null' INT TERM HUP
		wait $child
		echo $? > "$work/$t.rc"
	) &
	pids="$pids $!"
done
wait $pids # only the tasks: the holder of the lock is also a child, and ends when this script does

pass=0 cached=0 failed=0 skipped=0
printf '%-20s %-8s %-4s %-8s %-28s %-6s %s\n' task result exit host job source secs
for t in $tasks; do
	case " $skips " in
	*" $t "*)
		printf '%-20s %-8s\n' "$t" "跳過"
		skipped=$((skipped + 1))
		continue ;;
	esac
	rc=$(cat "$work/$t.rc" 2>/dev/null || echo "?")
	if ! jq -s -e 'length == 1 and (.[0] | type == "object")' "$work/$t.json" >/dev/null 2>&1; then
		why=$(head -n 1 "$work/$t.err" 2>/dev/null | cut -c1-100)
		printf '%-20s %-8s %-4s %s\n' "$t" FAIL "$rc" "no report${why:+: $why}"
		failed=$((failed + 1))
		continue
	fi
	row=$(jq -r -s '.[0] | [(.exit // "-" | tostring), (.host // "-"), (.id // "-"), (if .cache_hit then "cache" else "ran" end), ((.duration_s // 0) | floor | tostring)] | join(" ")' "$work/$t.json")
	# shellcheck disable=SC2086
	set -- $row
	# a pass is pdx's exit 0 AND a report that says so: this task, succeeded, exit 0, with a job id and a host (a report of another task, a
	# failed one, an empty one or a half one is not what a green gate may rest on)
	if [ "$rc" = 0 ] && ! jq -s -e --arg t "$t" '.[0] | .task == $t and .state == "succeeded" and .exit == 0 and (.id | type == "string" and length > 0) and (.host | type == "string" and length > 0)' "$work/$t.json" >/dev/null 2>&1; then
		printf '%-20s %-8s %-4s %-8s %-28s %-6s %s\n' "$t" FAIL "$1" "$2" "$3" "$4" "report is not a success of $t"
		failed=$((failed + 1))
	elif [ "$rc" = 0 ]; then
		printf '%-20s %-8s %-4s %-8s %-28s %-6s %s\n' "$t" pass "$1" "$2" "$3" "$4" "$5"
		pass=$((pass + 1))
		[ "$4" = cache ] && cached=$((cached + 1))
	else
		printf '%-20s %-8s %-4s %-8s %-28s %-6s %s\n' "$t" FAIL "$1" "$2" "$3" "$4" "$5"
		failed=$((failed + 1))
	fi
done

# every task took its snapshot of the clone as it was sent: if the clone is not at the commit now, some of the results may be for another tree
now=$(git -C "$clone" rev-parse HEAD 2>/dev/null || echo "?")
if [ "$now" != "$full" ]; then
	echo "release gate: the clone moved from $full to $now while the tasks were sent: the results are not for the commit"
	failed=$((failed + 1))
fi

echo "release gate: $pass passed ($cached from cache), $failed failed, $skipped skipped; logs $work"
if [ "$failed" -gt 0 ]; then exit 1; fi
if [ "$skipped" -gt 0 ]; then
	echo "INCOMPLETE: skipped tasks did not run, this is not a full pass:$skips"
	exit 3
fi
exit 0
