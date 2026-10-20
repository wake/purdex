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

# the clone: its origin is the remote, it holds the commit, it is detached at it and has nothing the commit does not have
export GIT_TERMINAL_PROMPT=0
if [ -d "$clone/.git" ]; then
	git -C "$clone" remote set-url origin "$remote" || die "cannot point $clone at $remote"
else
	mkdir -p "$(dirname "$clone")" || die "cannot make $(dirname "$clone")"
	git clone -q --no-checkout "$remote" "$clone" || die "cannot clone $remote"
fi
git -C "$clone" fetch -q --prune origin || die "cannot fetch $remote"
git -C "$clone" cat-file -e "$commit^{commit}" 2>/dev/null || die "$commit is not in $remote (is it pushed?)"
git -C "$clone" checkout -q --detach --force "$commit" || die "cannot check out $commit"
git -C "$clone" clean -qffdx || die "cannot clean $clone"
full=$(git -C "$clone" rev-parse HEAD)

work="$(dirname "$clone")/runs/$(date +%Y%m%d-%H%M%S)-$(echo "$full" | cut -c1-9)"
mkdir -p "$work" || die "cannot make $work"
echo "release gate at $full (clone $clone, logs $work)"

for t in $tasks; do
	case " $skips " in *" $t "*) continue ;; esac
	(
		cd "$clone" || exit 125
		"$pdx" test "$t" --json $nocache > "$work/$t.json" 2> "$work/$t.err"
		echo $? > "$work/$t.rc"
	) &
done
wait

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
	if ! jq -e . "$work/$t.json" >/dev/null 2>&1; then
		why=$(head -n 1 "$work/$t.err" 2>/dev/null | cut -c1-100)
		printf '%-20s %-8s %-4s %s\n' "$t" FAIL "$rc" "no report${why:+: $why}"
		failed=$((failed + 1))
		continue
	fi
	row=$(jq -r '[(.exit // "-" | tostring), (.host // "-"), (.id // "-"), (if .cache_hit then "cache" else "ran" end), ((.duration_s // 0) | floor | tostring)] | join(" ")' "$work/$t.json")
	# shellcheck disable=SC2086
	set -- $row
	if [ "$rc" = 0 ]; then
		printf '%-20s %-8s %-4s %-8s %-28s %-6s %s\n' "$t" pass "$1" "$2" "$3" "$4" "$5"
		pass=$((pass + 1))
		[ "$4" = cache ] && cached=$((cached + 1))
	else
		printf '%-20s %-8s %-4s %-8s %-28s %-6s %s\n' "$t" FAIL "$1" "$2" "$3" "$4" "$5"
		failed=$((failed + 1))
	fi
done

echo "release gate: $pass passed ($cached from cache), $failed failed, $skipped skipped; logs $work"
if [ "$failed" -gt 0 ]; then exit 1; fi
if [ "$skipped" -gt 0 ]; then
	echo "INCOMPLETE: skipped tasks did not run, this is not a full pass:$skips"
	exit 3
fi
exit 0
