#!/bin/sh
# The C2 real-host matrix of wwd (docs/wwd-c2-matrix.md; plan WWD-4 "Acceptance (C2)").
#
#   scripts/wwd-c2-matrix.sh [--dry-run] [--list] [--manual] [--force] (--all | --case N[,N...])
#
# Run from mlab (the origin), once wwd is deployed on the four hosts. Each case checks what it needs, makes one fault with the
# service manager (never pfctl, iptables, sudoers or a firewall), waits, ends with read-only checks and prints PASS or FAIL
# with the evidence; whatever it made (jobs, a scratch repo, a frozen wwd, a worker switch) is put back at the end, also on
# ^C. --dry-run prints every step and runs none of them. This script has not been run on the hosts yet. Its names, codes and
# counted attempts were checked against wwd main (806591dcb) by reading the code; what the code cannot say (a host's PATH, its mysql) is
# checked at the start (precheck) or listed in docs/wwd-c2-matrix.md, section 8.
set -eu

dry=0; manual=0; force=0; list=0; all=0; keep=0; cases=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) dry=1 ;;
    --manual) manual=1 ;;
    --force) force=1 ;;
    --keep) keep=1 ;;
    --list) list=1 ;;
    --all) all=1 ;;
    --case) shift; cases="$cases ${1:-}" ;;
    *) echo "unknown argument $1" >&2; exit 2 ;;
  esac
  shift
done
cases=$(echo "$cases" | tr ',' ' ')

# ---- the hosts ------------------------------------------------------------------------------------------------
PORT=7871
addr() { case "$1" in mlab) echo 100.64.0.2 ;; air26) echo 100.64.0.4 ;; mserv) echo 100.64.0.6 ;; air19) echo 100.64.0.1 ;; esac; }
is_mac() { [ "$1" != mserv ]; }
SOCK='$HOME/.local/share/wwd/wwd.sock'
DB='$HOME/.local/share/wwd/wwd.db'
# the probe tasks append one line per run here, outside the workspace. C2_RAN_LOG moves it (the script's stub tests do, so that they never
# touch the file of a matrix that is running); the probe task gets the same path, and it is expanded by the shell of the host it runs on.
RAN="${C2_RAN_LOG:-/tmp/wwd-c2-ran.log}"
# it goes into shell text of four hosts and into a sed replacement: plain path characters (and a literal $HOME) only
case "$RAN" in *[!A-Za-z0-9_./\$-]*|'') echo "C2_RAN_LOG must be a path of letters, digits and _ . / - (or \$HOME/...): '$RAN'" >&2; exit 2 ;; esac
WORK="${TMPDIR:-/tmp}/wwd-c2.$$"

# ---- the cases (number, title, hosts, minutes, needs) --------------------------------------------------------
catalog() {
  cat <<'EOF'
1|two claims on one job|mlab air26|3||
2|worker unreachable mid-job (wwd frozen): lapse, second attempt, failed/lost|mlab air26|8||
3|delayed heartbeat: the old claim is refused|mlab air26|6||
4|origin restarted mid-job|mlab air26|4||
5|origin unreachable: snapshot fetch, mid-run, result upload|mlab air26|10|fault.snapshot_delay_s (5a only)|
6|result posted, log upload fails|mlab air26|3|fault.log_upload_fail|
7|air26 owner returns: reclaim|mlab air26|4|fault.owner_active_file, or --manual|
8|origin at pressure 2: its jobs are taken first|mlab air26 mserv|3|fault.zone|
9|running job superseded (interruptible and not)|mlab air26|6||
10|queue timeout|mlab|2||
11|wrong pool token|mlab air26 mserv air19|1||
12|undeclared task and bad arguments refused|mlab air26|3||
13|mserv: PHP job with its own database, sandbox|mlab mserv|4||
14|air19 claims nothing|mlab air19|1||
EOF
}
if [ "$list" -eq 1 ]; then catalog | awk -F'|' '{printf "%2s  %-58s hosts: %-26s ~%s min  %s\n", $1, $2, $3, $4, ($5=="" ? "" : "needs " $5)}'; exit 0; fi
[ "$all" -eq 1 ] && cases=$(catalog | cut -d'|' -f1 | tr '\n' ' ')
[ -n "$cases" ] || { echo "usage: $0 [--dry-run] [--list] [--manual] [--force] (--all | --case N[,N...])" >&2; exit 2; }
for c in $cases; do catalog | cut -d'|' -f1 | grep -qx "$c" || { echo "no case $c (see --list)" >&2; exit 2; }; done

# ---- running things ----------------------------------------------------------------------------------------------
say() { printf '%s\n' "$*"; }
# x: a step that changes something (dry run: printed only)
x() { if [ "$dry" -eq 1 ]; then say "+ $*"; else "$@"; fi; }
# on HOST CMD: run CMD (a string for sh) on HOST; mlab is this machine
on() {
  on_h=$1; shift
  if [ "$dry" -eq 1 ]; then say "+ on $on_h: $*"; return 0; fi
  if [ "$on_h" = mlab ]; then sh -c "$*"; return; fi
  # ssh exits 255 when it has no connection. Only a connection that was never made (the connect itself, or the banner of a loaded host:
  # nothing has run on the host) is tried once more after a pause; a connection that broke later may have run the command, and a command
  # must not run twice (a job submitted, a fault file made). Any 255 is written down in $WORK/connfail (a $(...) cannot set a variable):
  # the go loop FAILs the case for it, a lost connection is not a result of the case, and the clean-up still runs.
  on_n=0
  while :; do
    on_rc=0; ssh -o BatchMode=yes -o ConnectTimeout=20 "$on_h" "$*" 2> "$WORK.err" || on_rc=$?
    cat "$WORK.err" >&2 2>/dev/null || true
    [ "$on_rc" -eq 255 ] || return "$on_rc"
    if [ "$on_n" -lt 1 ] && grep -q -E 'ssh: connect to host |banner exchange|Could not resolve hostname' "$WORK.err" 2>/dev/null; then
      on_n=$((on_n + 1)); sleep $((2 * ${C2_TIMEOUT_SCALE:-1})); continue
    fi
    { echo "$on_h" >> "$WORK/connfail"; } 2>/dev/null || true; return 255
  done
}
# q HOST CMD: a read; its output is the answer (dry run: a placeholder)
q() {
  q_h=$1; shift
  if [ "$dry" -eq 1 ]; then say "? on $q_h: $*" >&2; echo "{}"; return 0; fi
  on "$q_h" "$@"
}
body_arg() { if [ $# -gt 0 ]; then printf -- "-H 'Content-Type: application/json' -d '%s'" "$1"; fi; }
sock() { sk_h=$1; sk_m=$2; sk_p=$3; shift 3; q "$sk_h" "curl -sS --unix-socket $SOCK -X $sk_m $(body_arg "$@") http://wwd$sk_p"; }   # PUT /v1/worker takes the shape GET /v1/worker gives (strict decode, worker_routes.go)
# tcp HOST METHOD PATH [BODY]: the pool route of HOST over the tailnet with the pool token (read from mlab's file, never printed)
AUTH='-H "Authorization: Bearer $(cat $HOME/.config/wwd/pool.token)"'
tcp() { tc_h=$1; tc_m=$2; tc_p=$3; shift 3; q mlab "curl -sS -o /dev/null -w '%{http_code}' -X $tc_m $AUTH $(body_arg "$@") --max-time 10 http://$(addr "$tc_h"):$PORT$tc_p"; }
tcp_body() { tb_h=$1; tb_m=$2; tb_p=$3; shift 3; q mlab "curl -sS -w '\n%{http_code}' -X $tb_m $AUTH $(body_arg "$@") --max-time 10 http://$(addr "$tb_h"):$PORT$tb_p"; }
# tcp_bad HOST none|wrong : the route in $PATH_BAD with no token or with a wrong one
tcp_bad() { if [ "$2" = wrong ]; then tx_a="-H 'Authorization: Bearer c2-wrong-token'"; else tx_a=""; fi; q mlab "curl -sS -o /dev/null -w '%{http_code}' $tx_a --max-time 10 http://$(addr "$1"):$PORT$PATH_BAD"; }

# ---- the service manager (the only way a wwd is stopped) -----------------------------------------------------------
svc_pid() { if is_mac "$1"; then q "$1" "launchctl list gs.wake.wwd | awk -F' = ' '/\"PID\"/ {gsub(/;/,\"\",\$2); print \$2}'"; else q "$1" "systemctl show wwd.service -p MainPID --value"; fi; }
svc_restart() { if is_mac "$1"; then on "$1" 'launchctl kickstart -k gui/$(id -u)/gs.wake.wwd'; else on "$1" 'sudo systemctl restart wwd.service'; fi; }
# A wwd is never stopped here: launchctl bootout and systemctl stop send SIGTERM, and wwd answers that by handing its jobs back with a
# reclaim release, which counts no attempt (report.go KindShutdown, queue/claim.go). A lease only lapses when the wwd cannot talk, so a
# wwd that must look dead is frozen (svc_freeze) and thawed (svc_thaw).
# svc_pid_ok HOST PID: PID is a decimal number above 1 (0 and -1 would signal a whole group or every process) and is the wwd binary.
# Returns 0 for yes, 1 for "confirmed not wwd", 2 for "could not ask" (ssh or ps failed): a caller that must undo something keeps it then
svc_pid_ok() {
  case "$2" in ''|*[!0-9]*) return 1 ;; esac
  [ "$2" -gt 1 ] || return 1
  [ "$dry" -eq 1 ] && return 0
  # ps exits 1 when there is no such pid (confirmed gone); any other failure of ps is "could not ask", not "not wwd"
  po_r=$(on "$1" "po_o=\$(ps -p $2 -o command= 2>/dev/null); po_s=\$?; if [ \$po_s -gt 1 ]; then echo error; elif echo \"\$po_o\" | grep -q '/wwd/bin/wwd'; then echo yes; else echo no; fi") || return 2
  case "$po_r" in yes) return 0 ;; no) return 1 ;; *) return 2 ;; esac
}
STOPPED=""   # host:pid of each wwd this script froze
svc_freeze() {
  fz_p=$(svc_pid "$1"); [ "$dry" -eq 1 ] && fz_p=4242        # a dry run has no service manager to ask
  svc_pid_ok "$1" "$fz_p" || { say "FAIL: '$fz_p' is not the pid of wwd on $1: nothing is frozen"; exit 1; }
  STOPPED="$STOPPED $1:$fz_p"
  on "$1" "kill -STOP $fz_p"                                     # an exact pid, from the service manager
}
svc_thaw() {
  fz_p=$(echo "$STOPPED" | tr ' ' '\n' | grep "^$1:" | head -1 | cut -d: -f2)
  svc_pid_ok "$1" "$fz_p" && po_rc=0 || po_rc=$?
  case "$po_rc" in
    0) on "$1" "kill -CONT $fz_p" || return 1 ;;
    1) say "WARN: $1's wwd is no longer pid '$fz_p': no signal sent" ;;
    *) say "WARN: could not ask $1 about pid '$fz_p': it may still be frozen"; return 1 ;;
  esac
  STOPPED=$(echo "$STOPPED" | tr ' ' '\n' | grep -v "^$1:" | tr '\n' ' ')
}

# ---- the pool, workers, jobs -----------------------------------------------------------------------------------------
# worker settings: the first GET of each host is kept in $WORK/saved/HOST, and a PUT sends its body on stdin, so no character of an
# owner_rule (spaces, quotes) can split or break a command line
put_worker() {   # put_worker HOST FILE
  if [ "$dry" -eq 1 ]; then say "+ on $1: PUT /v1/worker (the body from a file, on stdin)"; return 0; fi
  on "$1" "curl -sS --unix-socket $SOCK -X PUT -H 'Content-Type: application/json' -d @- http://wwd/v1/worker" < "$2"
}
worker_set() {   # worker_set HOST true|false
  ws_cur=$(sock "$1" GET /v1/worker)
  if [ "$dry" -eq 1 ]; then say "+ keep $1's worker settings, then enabled=$2"; put_worker "$1" /dev/null; return 0; fi
  [ -e "$WORK/saved/$1" ] || printf '%s' "$ws_cur" > "$WORK/saved/$1"
  printf '%s' "$ws_cur" | jq -c ".enabled=$2" > "$WORK/saved/$1.new"
  put_worker "$1" "$WORK/saved/$1.new" >/dev/null
}
# a fault artifact on a host (a file the product reads): recorded before it is made, removed by finish
FAULTS=""; REMOTE_DIRS=""
SB_TAG=$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')   # names the host /tmp files of case 13, so they are this run's alone (nothing pre-existing to spare or to delete)
# a file that was there before this run is not ours: it is made in one step that only creates (noclobber), the path is registered before
# that step (an interruption in between still cleans up) and un-registered when the answer is "present" or "error"; if the answer never
# came (ssh died) it is not removed either, since this run cannot say it made it
fault_on() {   # fault_on HOST PATH-ON-HOST
  if [ "$dry" -eq 1 ]; then FAULTS="$FAULTS $1:$2"; on "$1" "mkdir -p \$(dirname $2) && touch $2"; return 0; fi
  FAULTS="$FAULTS $1:$2"
  fo_r=$(on "$1" "mkdir -p \$(dirname $2) && if ( set -C; : > $2 ) 2>/dev/null; then echo created; elif [ -e $2 ]; then echo present; else echo error; fi") || fo_r=lost
  case "$fo_r" in
    created) ;;
    present) fault_forget "$1" "$2"; say "WARN: $2 was already on $1: left as it is at the end" ;;
    *) fault_forget "$1" "$2"; say "FAIL: could not make $2 on $1 ($fo_r): look at it by hand"; exit 1 ;;
  esac
}
fault_forget() { FAULTS=$(echo "$FAULTS" | tr ' ' '\n' | grep -v "^$1:$2\$" | tr '\n' ' '); }
fault_off() {
  case " $FAULTS " in *" $1:$2 "*) ;; *) return 0 ;; esac
  on "$1" "rm -f $2" || return 1
  fault_forget "$1" "$2"
}
only_workers() {   # only_workers HOST... : exactly these workers are on (air19 stays off in every case)
  for ow_h in mlab air26 mserv; do
    case " $* " in *" $ow_h "*) worker_set "$ow_h" true ;; *) worker_set "$ow_h" false ;; esac
  done
}
jclaim() { printf '{"claim_id":"%s"}' "$1"; }
jresult() { printf '{"claim_id":"%s","state":"succeeded","exit":0}' "$1"; }
jrelease() { printf '{"claim_id":"%s","reason":"reclaim"}' "$1"; }
jworker() { printf '{"worker":"%s","labels":["os:darwin","arch:arm64"],"fingerprint":{}}' "$1"; }   # a real worker's labels: the job's must be a subset
# db_q SQL: a read-only query of the origin's wwd.db (SQL with single quotes only)
db_q() { if [ "$dry" -eq 1 ]; then say "? on mlab: sqlite3 -readonly: $1" >&2; echo 0; else q mlab "sqlite3 -readonly $DB \"$1\""; fi; }
# air19_new: how many jobs air19 claimed since the run began. Two independent sources, added: (1) the ids of jobs with claim_worker='air19' now
# that were not in the list taken at the start (a clean-up of old rows cannot hide a new one; a job made before the run counts too);
# (2) the lines in air19's own probe log, which was emptied at the start and which a job leaves even if it is taken back from air19 later
AIR19_Q="select id from jobs where claim_worker='air19'"
air19_new() {
  [ "$dry" -eq 1 ] && { echo 0; return 0; }
  a_cur=$(db_q "$AIR19_Q") || { echo "?"; return 0; }
  a_new=$(printf '%s\n' "$a_cur" | sort -u | comm -13 "$WORK/air19.base" - | grep -c . || true)
  a_ran=$(q air19 "n=\$(grep -c 'job=' $RAN 2>/dev/null); echo \${n:-0}") || { echo "?"; return 0; }
  case "$a_ran" in ''|*[!0-9]*) echo "?"; return 0 ;; esac
  echo $((a_new + a_ran))
}
# job_db_name ID: the name of the database of job ID, as jobdb makes it: wwd_ and the first 32 hex of the SHA-256 of the id
# (internal/wwd/jobdb/jobdb.go Name). Worked out here, not read from the job's log: the log of a job on mserv reaches mlab only while
# fault.log_upload_fail is off there, and the matrix runs with it on.
job_db_name() { if command -v shasum >/dev/null 2>&1; then jd_h=$(printf '%s' "$1" | shasum -a 256); else jd_h=$(printf '%s' "$1" | sha256sum); fi; echo "wwd_$(echo "$jd_h" | cut -c1-32)"; }
# mserv_db NAME: 1 if the job database NAME (wwd_ and 32 hex) is on mserv's test MySQL, 0 if not. The databases are on the dev instance and
# only the wwd_admin account of jobdb's client file may list them (CALL wwdctl.wwd_list_dbs(), docs/wwd-deploy.md); the default ~/.my.cnf is
# another instance and would answer 0 whatever is there. When it cannot ask it prints ?, never 0. It asks about the one database of the case:
# another job's database on the instance (a run with --force, a leftover) is not this case's to judge.
MSERV_ADMIN='$HOME/.config/wwd/mysql-admin.cnf'
mserv_db() { q mserv "o=\$(mysql --defaults-extra-file=$MSERV_ADMIN -N -B -e 'CALL wwdctl.wwd_list_dbs()' 2>/dev/null) || { echo '?'; exit 0; }; printf '%s\n' \"\$o\" | grep -c -x '$1' || true"; }
# the jobs this run made are listed in a file: submit runs in a $( ) subshell, which could not hand a variable back
track() { if [ "$dry" -eq 0 ]; then echo "${2:-mlab} $1" >> "$WORK/jobs"; fi; }     # track ID [ORIGIN]
submit() {   # submit TASK [ARGS...] -> the job id; runs in the scratch repo, the caller never waits  (pdx test --detach --json prints an indented {"id":...})
  sb_t=$1; shift
  if [ "$dry" -eq 1 ]; then say "+ submit $sb_t $* (in the scratch repo)" >&2; echo "<$sb_t>"; return 0; fi
  sb_id=$(q mlab "cd $WORK/repo && pdx test --detach --json $sb_t $* | jq -r .id")
  track "$sb_id"; echo "$sb_id"
}
job() { sock mlab GET "/v1/jobs/$1"; }   # the job JSON of spec 4.1: .state, .result.{reason,host,started_at,superseded_by,summary}
jf_on() { sock "$1" GET "/v1/jobs/$2" | jq -r "$3" 2>/dev/null || echo "?"; }     # jf_on ORIGIN ID '.state'
jf() { jf_on "${WF_HOST:-mlab}" "$1" "$2"; }                                      # jf ID '.state' : on mlab, or on WF_HOST while wait_for_on runs
# precond ID JQEXPR WANT SECONDS : a state the case needs before its checks can mean anything ("running", "claimed"). If it is not reached
# the case FAILs here, says what it saw (the job and air26's owner), and the caller returns: no later check may pass on a job that never
# got there (2026-10-19: air26's owner was using the machine, c2-sleep was never claimed, and "queued again" passed as a lapse)
precond() {
  wait_for "$@" && return 0
  pr_job=$(sock mlab GET "/v1/jobs/$1" 2>/dev/null | jq -c '{state, claim_worker: .claim.worker}' 2>/dev/null || true)
  say "  FAIL precondition not reached: $1 $2 == $3 within $4 s -- the job: ${pr_job:-?}; air26 facts.owner: $(sock air26 GET /v1/facts 2>/dev/null | jq -c .owner 2>/dev/null || echo '?')"
  CASE_FAILS=$((CASE_FAILS + 1))
  return 1
}
# owner_idle : air26's worker yields to its owner (facts.owner.active: HID idle under 5 minutes, or a build of theirs), and an empty
# owner_rule does not switch that off (it is the default rule), so a case that needs air26's worker waits here, up to 10 minutes.
owner_idle() {
  if [ "$dry" -eq 1 ]; then say "~ wait up to 600 s until air26's facts.owner.active is not true"; return 0; fi
  oi_n=0; oi_max=$((600 * ${C2_TIMEOUT_SCALE:-1})); oi_said=0
  while [ "$(sock air26 GET /v1/facts | jq -r '.owner.active' 2>/dev/null)" = true ]; do
    [ "$oi_said" -eq 1 ] || { say "   waiting for air26's owner to be idle (up to 10 min; $(sock air26 GET /v1/facts | jq -c .owner 2>/dev/null))"; oi_said=1; }
    [ "$oi_n" -lt "$oi_max" ] || return 1
    sleep 5; oi_n=$((oi_n + 5))
  done
}
wait_for_on() { WF_HOST=$1; shift; wf_r=0; wait_for "$@" || wf_r=$?; WF_HOST=mlab; return "$wf_r"; }
# attempts ID: the counted failures of the job in wwd.db (a lapse or an infra release counts, a reclaim does not): 0 for a job that
# succeeded at once; the claim in hand is number attempts+1
attempts() { db_q "select attempts from jobs where id='$1'"; }
wait_for() {   # wait_for ID JQEXPR WANT SECONDS
  if [ "$dry" -eq 1 ]; then say "~ wait up to $4 s until $1 $2 == $3"; return 0; fi
  wf_n=0; wf_max=$(($4 * ${C2_TIMEOUT_SCALE:-1}))     # C2_TIMEOUT_SCALE=0: look once (the stub test of this script)
  while :; do
    [ ! -s "$WORK/connfail" ] || return 1          # a host could not be reached: waiting longer shows nothing
    [ "$(jf "$1" "$2")" = "$3" ] && return 0
    [ "$wf_n" -ge "$wf_max" ] && return 1
    sleep 2; wf_n=$((wf_n + 2))
  done
}

nap() { if [ "$dry" -eq 1 ]; then say "~ wait $1 s"; else sleep $(($1 * ${C2_TIMEOUT_SCALE:-1})); fi; }

# ---- checks -----------------------------------------------------------------------------------------------------
FAILS=0; CASE_FAILS=0; CASE_SKIP=""; PASSED=""; FAILED=""; SKIPPED=""
check() {   # check DESC ACTUAL EXPECTED
  if [ "$dry" -eq 1 ]; then say "= check: $1 (expect: $3)"; return 0; fi
  if [ "$2" = "$3" ]; then say "  ok   $1"; else say "  FAIL $1: got '$2', expected '$3'"; CASE_FAILS=$((CASE_FAILS + 1)); fi
}
need_fault() {   # need_fault HOST KEY : the hook is in this wwd (H0: /v1/info lists the fault keys that are on), else the case is skipped
  [ "$dry" -eq 1 ] && { say "= needs hook fault.$2 on $1"; return 0; }
  [ "$(sock "$1" GET /v1/info | jq -r ".fault.$2 // empty")" ] || { CASE_SKIP="the hook fault.$2 is not on $1 (docs/wwd-c2-matrix.md, hooks)"; return 1; }
}
# ran_count HOST JOB: how many times the probe wrote its line for the job (0 when the file is not there: grep -c prints nothing then)
ran_count() { if [ "$dry" -eq 1 ]; then echo 0; else q "$1" "n=\$(grep -c 'job=$2 ' $RAN 2>/dev/null); echo \${n:-0}"; fi; }

# ---- before and after -----------------------------------------------------------------------------------------------
# AIR26_FIND_PDX: the command that prints where pdx is on air26 (the first that exists); the $HOME is air26's own
AIR26_FIND_PDX='command -v pdx || { [ -x "$HOME/.local/bin/pdx" ] && echo "$HOME/.local/bin/pdx"; } || { [ -x "$HOME/.config/pdx/bin/pdx" ] && echo "$HOME/.config/pdx/bin/pdx"; }'
AIR26_PDX=pdx
precheck() {
  say "-- preconditions"
  pc_vers=""
  for pc_h in mlab air26 mserv air19; do
    if [ "$pc_h" != mlab ]; then on mlab "ssh -o BatchMode=yes -o ConnectTimeout=5 $pc_h true" >/dev/null 2>&1 || [ "$dry" -eq 1 ] || { say "FAIL: $pc_h is not reachable"; exit 1; }; fi
    pc_v=$(sock "$pc_h" GET /v1/info | jq -r '"\(.version) proto \(.proto)"' 2>/dev/null || echo "?")
    pc_vers="$pc_vers|$pc_v"
  done
  [ "$dry" -eq 1 ] || [ "$(echo "$pc_vers" | tr '|' '\n' | sort -u | grep -c .)" -eq 1 ] || { say "FAIL: the hosts run different wwd versions:$pc_vers"; exit 1; }
  if [ "$force" -eq 0 ]; then
    pc_q=$(sock mlab GET '/v1/jobs?state=queued' | jq '.jobs | length' 2>/dev/null || echo 0)   # {"jobs":[...],"cursor":..,"epoch":..}
    [ "$dry" -eq 1 ] || [ "$pc_q" = 0 ] || { say "FAIL: $pc_q jobs are queued already (the matrix wants a quiet pool; --force to go on)"; exit 1; }
  fi
  [ "$(sock air19 GET /v1/worker | jq -r .enabled 2>/dev/null || echo false)" = false ] || [ "$dry" -eq 1 ] || { say "FAIL: air19's worker is on"; exit 1; }
  # H1 (fault.log_upload_fail on mlab) makes every log upload to mlab fail, and a worker holds its slot for the 2 minutes it retries
  # (#3404): the next case then cannot be claimed within its wait. It is for case 6 alone.
  if [ "$dry" -eq 0 ] && [ "$(sock mlab GET /v1/info | jq -r '.fault.log_upload_fail // false' 2>/dev/null)" = true ]; then
    for pc_c in $cases; do
      [ "$pc_c" = 6 ] || { say "FAIL: fault.log_upload_fail is on in mlab's config, and case $pc_c would wait behind the 2-minute log retries of the case before it; turn it off for every case but 6 (docs/wwd-c2-matrix.md, section 7.1; #3404)"; exit 1; }
    done
  fi
  # case 7 (H2) needs air26 to have an owner rule: the effective rule is the only basis for the owner being active (#3457), and the H2 file
  # only replaces the reading (#3561), so with an empty rule the file shows owner.active but nothing yields and the case waits for a reclaim
  # that never comes. An empty rule is also what a PUT of this run would store back, so for the other cases it is a warning.
  pc_rule=""; [ "$dry" -eq 1 ] || pc_rule=$(sock air26 GET /v1/worker | jq -r '.owner_rule // empty' 2>/dev/null)
  if [ "$dry" -eq 1 ]; then
    case " $cases " in *" 7 "*) say "+ check air26's owner_rule is not empty (case 7 FAILs here when it is)" ;; esac
  elif [ -z "$pc_rule" ]; then
    case " $cases " in
      *" 7 "*) say "FAIL: air26's owner_rule is empty, and case 7 (H2) needs a rule: with none the H2 file shows owner.active but headroom stays and the worker does not yield (#3457). Fix: [owner] enabled = true in air26's config, then PUT /v1/worker with owner_rule \"hid<5m\" (docs/wwd-c2-matrix.md, sections 7.1 and the note after it)"; exit 1 ;;
    esac
    say "WARN: air26's owner_rule is empty: this run stores it empty; put it back to hid<5m by hand afterwards (docs/wwd-c2-matrix.md, section 7.1)"
  fi
  # case 8 submits from air26 over a non-interactive ssh, whose PATH has no pdx on a Mac (the shell reads .zshenv only): find it on the PATH,
  # then in the two places pdx is installed, and say which one is used
  case " $cases " in *" 8 "*)
    if [ "$dry" -eq 1 ]; then on air26 "$AIR26_FIND_PDX"; AIR26_PDX=pdx
    else
      AIR26_PDX=$(on air26 "$AIR26_FIND_PDX" 2>/dev/null | head -1) || AIR26_PDX=""
      case "$AIR26_PDX" in *[!A-Za-z0-9_./-]*) say "FAIL: the path of pdx on air26 has characters the script will not put into a command: '$AIR26_PDX'"; exit 1 ;; esac
      [ -n "$AIR26_PDX" ] || { say "FAIL: pdx is not on air26's non-interactive PATH, nor in ~/.local/bin/pdx, nor in ~/.config/pdx/bin/pdx (case 8 submits from there)"; exit 1; }
      say "   pdx on air26: $AIR26_PDX"
    fi ;;
  esac
  x mkdir -p "$WORK/repo" "$WORK/saved"
  on air19 "rm -f $RAN"                       # first the log, then the list: the list is the moment the watching starts
  if [ "$dry" -eq 0 ]; then
    pc_b=$(db_q "$AIR19_Q") || { say "FAIL: cannot read the claims of air19 from wwd.db"; exit 1; }
    printf '%s\n' "$pc_b" | sort -u > "$WORK/air19.base"
  fi
  scratch_repo
}
# the scratch repo: probe tasks, one commit
scratch_repo() {
  say "-- scratch repo $WORK/repo"
  [ "$dry" -eq 1 ] && { say "+ write .wwd/tasks.toml (c2-quick c2-any c2-sleep c2-sleep-int c2-sleep-noint c2-expire c2-files c2-php-db c2-sandbox) and scripts/*.sh, one commit"; return 0; }
  mkdir -p "$WORK/repo/.wwd" "$WORK/repo/scripts"
  cat > "$WORK/repo/scripts/probe.sh" <<'EOS'
#!/bin/sh
# probe.sh SECONDS EXIT : record the run outside the workspace, sleep, exit
echo "job=${WWD_JOB:-?} host=$(hostname -s) start=$(date +%s)" >> @RAN@ 2>/dev/null || true
echo "host=$(hostname -s) job=${WWD_JOB:-?}"
sleep "${1:-0}"
exit "${2:-0}"
EOS
  cat > "$WORK/repo/.wwd/tasks.toml" <<'EOS'
[entry]
mode = "advise"
[tasks.c2-quick]
run = "sh scripts/probe.sh 3 0"
args = "none"
class = "dev"
labels = ["os:darwin"]
summary = "plain"
[tasks.c2-sleep]
run = "sh scripts/probe.sh 90 0"
args = "none"
class = "dev"
labels = ["os:darwin"]
timeout = "10m"
summary = "plain"
[tasks.c2-any]
run = "sh scripts/probe.sh 5 0"
args = "none"
class = "dev"
labels = []
summary = "plain"
[tasks.c2-sleep-int]
run = "sh scripts/probe.sh 120 0"
args = "none"
class = "dev"
labels = ["os:darwin"]
interruptible = true
summary = "plain"
[tasks.c2-sleep-noint]
run = "sh scripts/probe.sh 60 0"
args = "none"
class = "dev"
labels = ["os:darwin"]
interruptible = false
summary = "plain"
[tasks.c2-expire]
run = "sh scripts/probe.sh 1 0"
args = "none"
class = "dev"
labels = ["os:darwin"]
queue_timeout = "20s"
summary = "plain"
[tasks.c2-files]
run = "sh scripts/probe.sh 1 0 {files}"
args = "files"
class = "dev"
labels = ["os:darwin"]
summary = "plain"
[tasks.c2-php-db]
run = "php scripts/db_probe.php"
args = "none"
class = "dev"
labels = ["os:linux"]
db = "per-job"
summary = "plain"
[tasks.c2-sandbox]
run = "sh scripts/sandbox_probe.sh"
args = "none"
class = "dev"
labels = ["os:linux"]
summary = "plain"
EOS
  cat > "$WORK/repo/scripts/db_probe.php" <<'EOS'
<?php
// Uses the database the job was given: connect, make a table, write, read back. The variable names are the DB_* set that jobdb gives a
// task with db = "per-job" (jobdb.Info.Env). Exit 0 only when the round trip worked.
foreach (getenv() as $k => $v) { if (preg_match('/^(DB_|WWD_DB)/', $k) && stripos($k, 'PASS') === false) echo "$k=$v\n"; }
$db = getenv('DB_DATABASE') ?: getenv('WWD_DB_NAME');
if (!$db) { fwrite(STDERR, "no database name in the environment\n"); exit(2); }
if (!extension_loaded('pdo_mysql')) { fwrite(STDERR, "no pdo_mysql\n"); exit(3); }
try {
  $pdo = new PDO(sprintf('mysql:host=%s;port=%s;dbname=%s', getenv('DB_HOST') ?: '127.0.0.1', getenv('DB_PORT') ?: '3306', $db),
                 getenv('DB_USERNAME') ?: 'root', getenv('DB_PASSWORD') ?: '', [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
  $pdo->exec('CREATE TABLE c2_probe (id INT PRIMARY KEY, v VARCHAR(20))');
  $pdo->exec("INSERT INTO c2_probe VALUES (1, 'round-trip')");
  $v = $pdo->query('SELECT v FROM c2_probe WHERE id = 1')->fetchColumn();
  echo "db=$db read=$v\n";
  exit($v === 'round-trip' ? 0 : 1);
} catch (Throwable $e) { fwrite(STDERR, $e->getMessage() . "\n"); exit(4); }
EOS
  cat > "$WORK/repo/scripts/sandbox_probe.sh" <<'EOS'
#!/bin/sh
# exits 0 only when the sandbox of spec section 5 holds: no docker socket, nothing writable outside the workspace (ProtectSystem=strict),
# a /tmp of its own, sudo blocked. Every refusal is paired with a control that must succeed, so a probe that cannot write at all fails.
fail=0
note() { echo "$1"; fail=1; }
# the workspace is the working directory: the one place that must be writable
touch "./wwd-c2-inside.$$" 2>/dev/null && rm -f "./wwd-c2-inside.$$" || note "cannot write the workspace"
# the docker socket: InaccessiblePaths leaves a stand-in at the path that is still a socket but has mode 000 (measured on mserv),
# so the test is "can this user connect" (write permission), not "does it exist" or "is it a socket" (either path)
for s in /run/docker.sock /var/run/docker.sock; do
  [ -w "$s" ] && note "$s can be connected to"
  if command -v curl >/dev/null 2>&1; then   # the real thing: a request over the socket must not get an answer
    curl -s --max-time 3 --unix-socket "$s" http://localhost/_ping >/dev/null 2>&1 && note "$s answers a request"
  fi
done
# nothing outside the workspace is writable: the home, the system, and the usual places a job might drop a file
for d in /home/wake /etc /usr/local /usr/bin /opt /srv /root /var/lib /var/log /run; do
  [ -d "$d" ] || continue
  touch "$d/wwd-c2-outside.$$" 2>/dev/null && { note "wrote $d"; rm -f "$d/wwd-c2-outside.$$"; }
done
sudo -n true 2>/dev/null && note "sudo works"
# /tmp is the job's own: writable, and the host's sentinel (put there by the matrix) is not in it
touch /tmp/wwd-c2-private.@TAG@ 2>/dev/null || note "no writable /tmp"
[ -e /tmp/wwd-c2-sentinel.@TAG@ ] && note "the host's /tmp is visible"
echo "sandbox probe done fail=$fail"
exit "$fail"
EOS
  sed -i.bak "s/@TAG@/$SB_TAG/g" "$WORK/repo/scripts/sandbox_probe.sh" && rm -f "$WORK/repo/scripts/sandbox_probe.sh.bak"
  sed -i.bak "s|@RAN@|$RAN|g" "$WORK/repo/scripts/probe.sh" && rm -f "$WORK/repo/scripts/probe.sh.bak"
  ( cd "$WORK/repo" && git init -q . && git add -A && git -c user.name=c2 -c user.email=c2@localhost commit -q -m c2 )
}

finish() {
  fn_rc=$?
  trap - EXIT INT TERM
  say "-- cleaning up"
  fn_bad=""     # what the clean-up could not do: said at the end, and the exit code is not 0
  for fn_pair in $STOPPED; do svc_thaw "${fn_pair%%:*}" || fn_bad="$fn_bad thaw:${fn_pair%%:*}"; done
  if [ "$dry" -eq 1 ]; then say "+ PUT /v1/worker: put back the saved settings of every host it changed"
  else for fn_f in "$WORK"/saved/*; do
    case "$fn_f" in *.new) continue ;; esac
    [ -f "$fn_f" ] || continue
    put_worker "$(basename "$fn_f")" "$fn_f" >/dev/null || fn_bad="$fn_bad worker:$(basename "$fn_f")"
  done; fi
  for fn_pair in $FAULTS; do on "${fn_pair%%:*}" "rm -f ${fn_pair#*:}" || fn_bad="$fn_bad file:$fn_pair"; done
  if [ -f "$WORK/jobs" ]; then
    while read -r fn_h fn_id; do sock "$fn_h" POST "/v1/jobs/$fn_id/cancel" >/dev/null 2>&1 || true; done < "$WORK/jobs"
  fi
  for fn_d in $REMOTE_DIRS; do on "${fn_d%%:*}" "rm -rf ${fn_d#*:}" || true; done
  for fn_h in mlab air26 mserv air19; do on "$fn_h" "rm -f $RAN" || true; done
  # the three things above that change a host fail the exit code when they cannot be done; the rest is best-effort, but a lost connection
  # there is said, so that nobody believes the scratch directories and probe logs are gone
  [ ! -s "$WORK/connfail" ] || say "WARN: ssh to $(sort -u "$WORK/connfail" | tr '\n' ' ')was lost during the clean-up: a scratch directory (/tmp/wwd-c2.r.*) or the probe log ($RAN) may be left there"
  [ "$dry" -eq 1 ] || rm -f "$WORK.err"
  if [ "$keep" -eq 1 ]; then say "kept $WORK"; else x rm -rf "$WORK"; fi
  say ""
  say "passed:${PASSED:- -}  failed:${FAILED:- -}  skipped:${SKIPPED:- -}"
  [ -z "$FAILED" ] || fn_rc=1
  [ -z "$fn_bad" ] || { say "FAIL: the clean-up could not put back:$fn_bad -- look at these hosts by hand"; fn_rc=1; }
  exit "$fn_rc"
}

# ---- the cases ------------------------------------------------------------------------------------------------------
# Fake workers: with every real worker switched off, the script itself plays the workers over TCP, which makes a race exact.
claim() { tcp_body mlab POST "/v1/jobs/$1/claim" "$(jworker "$2")"; }   # prints body, newline, code

case_1() {
  only_workers mlab air26
  say "-- real: one job, two real workers"
  id=$(submit c2-quick)
  wait_for "$id" .state succeeded 120 || true
  check "state" "$(jf "$id" .state)" succeeded
  n=$(( $(ran_count mlab "$id") + $(ran_count air26 "$id") ))
  check "the command ran once, on one host" "$n" 1
  check "attempts" "$(attempts "$id")" 0
  say "-- exact: no real worker is on; two fake workers claim at the same moment"
  only_workers
  pid=$(submit c2-quick)
  if [ "$dry" -eq 1 ]; then
    say "+ two claims at once (fakeA, fakeB) on $pid over TCP with the pool token"; codes="200 409 "
  else
    mkdir -p "$WORK/out"
    # claim prints the body, a newline and the code, and curl's code has no newline after it: the echo ends the line, or the two
    # codes would run together in one file ("409200")
    { claim "$pid" fakeA | tail -1; echo; } > "$WORK/out/a" &
    { claim "$pid" fakeB | tail -1; echo; } > "$WORK/out/b" &
    wait
    codes=$(cat "$WORK/out/a" "$WORK/out/b" | sort | tr '\n' ' ')
  fi
  check "one claim wins and one gets 409" "$codes" "200 409 "
  check "the job is claimed once" "$(jf "$pid" .state)" claimed
  sock mlab POST "/v1/jobs/$pid/cancel" >/dev/null || true     # nobody renews that claim: do not leave it to lapse into the next case
}

case_2() {
  only_workers air26
  id=$(submit c2-sleep)
  precond "$id" .state running 90 || return 0
  # Not launchctl bootout / systemctl stop: that is SIGTERM, and wwd answers it by handing the job back with a reclaim release, which
  # counts no attempt and never ends in failed/lost. A lease lapses only when the wwd cannot talk: a frozen one.
  say "-- lapse 1: air26's wwd is frozen with the job in it, past the 60 s lease"
  svc_freeze air26
  wait_for "$id" .state queued 150 || true
  check "lease lapsed, queued again" "$(jf "$id" .state)" queued
  check "attempts after the lapse" "$(attempts "$id")" 1
  svc_thaw air26                                    # its worker finds the lease gone, ends the command, and takes the job again
  precond "$id" .state running 120 || return 0
  say "-- lapse 2: frozen again"
  svc_freeze air26
  wait_for "$id" .state failed 150 || true
  check "state after two lapses" "$(jf "$id" .state)" failed
  check "reason" "$(jf "$id" .result.reason)" lost
  check "attempts" "$(attempts "$id")" 2
  svc_thaw air26
  nap 20                                            # the thawed worker sees the lease is gone and ends the command
  check "no process of the job is left on air26" "$(q air26 "ps eww -A | grep -c '[W]WD_JOB=$id' || true")" 0
}

case_3() {
  say "-- exact: a stale claim is refused (fake workers, no real worker is on)"
  only_workers
  pid=$(submit c2-quick)
  a=$(claim "$pid" fakeA); ca=$(echo "$a" | head -1 | jq -r .claim_id 2>/dev/null || echo "?")
  nap 75                                                    # past the 60 s lease: the origin takes the job back
  b=$(claim "$pid" fakeB); cb=$(echo "$b" | head -1 | jq -r .claim_id 2>/dev/null || echo "?")
  check "the second claim is a new one" "$([ "$ca" != "$cb" ] && echo yes || echo no)" yes
  check "heartbeat of the old claim" "$(tcp mlab POST "/v1/jobs/$pid/heartbeat" "$(jclaim "$ca")")" 409
  check "result of the old claim" "$(tcp mlab POST "/v1/jobs/$pid/result" "$(jresult "$ca")")" 409
  check "the job is still claimed by the new one" "$(jf "$pid" .state)" claimed
  tcp mlab POST "/v1/jobs/$pid/release" "$(jrelease "$cb")" >/dev/null
  say "-- real: air26's wwd is frozen past its lease, then thawed"
  only_workers air26
  id=$(submit c2-sleep)
  precond "$id" .state running 90 || return 0
  svc_freeze air26
  worker_set mlab true
  nap 75
  svc_thaw air26
  wait_for "$id" .state succeeded 240 || true
  check "state" "$(jf "$id" .state)" succeeded
  check "it finished on mlab, not on the thawed air26" "$(jf "$id" .result.host)" mlab
  check "attempts" "$(attempts "$id")" 1
}

case_4() {
  only_workers air26
  id=$(submit c2-sleep)
  precond "$id" .state running 90 || return 0
  say "-- the origin (mlab) restarts while air26 runs the job"
  svc_restart mlab
  wait_for "$id" .state succeeded 200 || true
  check "state" "$(jf "$id" .state)" succeeded
  check "host" "$(jf "$id" .result.host)" air26
  check "attempts" "$(attempts "$id")" 0
}

case_5() {
  only_workers air26
  say "-- 5a: the origin stops answering when the worker has claimed (snapshot fetch)"
  if need_fault air26 snapshot_delay_s; then
    id=$(submit c2-quick)
    precond "$id" .state claimed 60 || return 0
    svc_freeze mlab; nap 20; svc_thaw mlab
    wait_for "$id" .state succeeded 600 || true
    check "5a state" "$(jf "$id" .state)" succeeded
    # the worker's fetch waits on the frozen origin and goes on after the thaw: 20 s is under the 60 s lease, nothing lapses and no infra
    # release is made, so nothing is counted. (A freeze over the lease, about 75 s, would count one.) Set fault.snapshot_delay_s to 5-10 s so the
    # request is made inside the freeze.
    check "5a the fetch waited and nothing lapsed (attempts)" "$(attempts "$id")" 0
  else say "SKIP 5a: $CASE_SKIP"; CASE_SKIP=""; fi
  say "-- 5b: the origin stops answering during the run, shorter than the lease, then longer"
  id=$(submit c2-sleep); precond "$id" .state running 90 || return 0
  svc_freeze mlab; nap 30; svc_thaw mlab
  wait_for "$id" .state succeeded 200 || true
  check "5b short: state" "$(jf "$id" .state)" succeeded
  check "5b short: attempts" "$(attempts "$id")" 0
  id=$(submit c2-sleep); precond "$id" .state running 90 || return 0
  svc_freeze mlab; nap 90; svc_thaw mlab
  wait_for "$id" .state succeeded 400 || true
  check "5b long: state" "$(jf "$id" .state)" succeeded
  check "5b long: the lease lapsed once" "$(attempts "$id")" 1
  say "-- 5c: the origin stops answering when the command is ending (result upload)"
  id=$(submit c2-sleep); precond "$id" .state running 90 || return 0
  nap 84
  svc_freeze mlab; nap 30; svc_thaw mlab                # the worker's deadline is 60 s after its last heartbeat (every 15 s): 30 s leaves room
  wait_for "$id" .state succeeded 200 || true
  check "5c state" "$(jf "$id" .state)" succeeded
  check "5c attempts" "$(attempts "$id")" 0
}

case_6() {
  need_fault mlab log_upload_fail || return 0
  only_workers air26
  id=$(submit c2-quick)
  wait_for "$id" .state succeeded 120 || true
  check "the result stands" "$(jf "$id" .state)" succeeded
  check "the log is not there" "$(tcp mlab GET "/v1/jobs/$id/log")" 404   # no log row: ErrNotFound -> 404 (queue/log.go, jobsapi/api.go)
}

case_7() {
  if ! need_fault air26 owner_active_file && [ "$manual" -eq 0 ]; then return 0; fi
  CASE_SKIP=""
  only_workers air26
  id=$(submit c2-sleep)
  precond "$id" .state running 90 || return 0
  worker_set mlab true
  if [ "$manual" -eq 1 ]; then say ">> move the mouse on air26 now (waiting up to 5 min for owner.active)"
  else fault_on air26 '$HOME/.local/share/wwd/fault/owner-active'; fi
  if [ "$dry" -eq 1 ]; then say "~ wait up to 300 s until air26's facts say owner.active"
  else n=0; while [ "$n" -lt $((300 * ${C2_TIMEOUT_SCALE:-1})) ] && [ "$(sock air26 GET /v1/facts | jq -r .owner.active)" != true ]; do sleep 2; n=$((n + 2)); done; fi
  wait_for "$id" .result.host mlab 300 || true
  check "it ended on mlab" "$(jf "$id" .state)/$(jf "$id" .result.host)" succeeded/mlab
  check "the reclaim did not count an attempt" "$(attempts "$id")" 0
  check "no process of the job is left on air26" "$(q air26 "ps eww -A | grep -c '[W]WD_JOB=$id' || true")" 0
  [ "$manual" -eq 1 ] || fault_off air26 '$HOME/.local/share/wwd/fault/owner-active'
}

case_8() {
  need_fault mlab zone || return 0
  say "-- one worker (mserv); two jobs queued at the same time from two origins; mlab's pressure is 2 (the hook)"
  only_workers
  rdir="/tmp/wwd-c2.r.$$"; REMOTE_DIRS="$REMOTE_DIRS air26:$rdir"
  # (a path of air26's own: mlab's TMPDIR, /var/folders/..., cannot be made there)
  x sh -c "tar -C $WORK -cf - repo | ssh air26 'mkdir -p $rdir && tar -C $rdir -xf -'"        # the scratch repo on air26: a second origin
  ida=$(q air26 "cd $rdir/repo && $AIR26_PDX test --detach --json c2-any | grep -o '\"id\" *: *\"[^\"]*\"' | cut -d'\"' -f4")   # pdx on air26's non-interactive PATH is checked in precheck
  track "$ida" air26
  idm=$(submit c2-any)                      # the younger job, but its origin is the one under pressure
  worker_set mserv true                     # only now can anything be claimed
  wait_for "$idm" .state succeeded 120 || true
  wait_for_on air26 "$ida" .state succeeded 120 || true
  sm=$(jf "$idm" .result.started_at); sa=$(jf_on air26 "$ida" .result.started_at)
  first=$(printf '%s\n%s\n' "$sm" "$sa" | sort | head -1)
  [ "$dry" -eq 1 ] && first=$sm
  check "the job of the origin under pressure started first, on the same worker" "$([ "$first" = "$sm" ] && echo mlab || echo air26)" mlab
}

case_9() {
  only_workers air26
  say "-- interruptible: the older one is told to stop; the worker reports it cancelled and the origin records who superseded it"
  a=$(submit c2-sleep-int); precond "$a" .state running 90 || return 0
  b=$(submit c2-sleep-int)
  wait_for "$a" .state cancelled 60 || true
  check "A state" "$(jf "$a" .state)" cancelled                  # superseded is the state only of a job that was still queued, or let go
  check "A reason" "$(jf "$a" .result.reason)" "superseded_by:$b"
  check "A is marked superseded_by B" "$(jf "$a" .result.superseded_by)" "$b"
  sock mlab POST "/v1/jobs/$b/cancel" >/dev/null
  say "-- not interruptible: the older one runs to its end, its result keeps superseded_by"
  a=$(submit c2-sleep-noint); precond "$a" .state running 90 || return 0
  b=$(submit c2-sleep-noint)
  wait_for "$a" .state succeeded 150 || true
  check "A ran to its end" "$(jf "$a" .state)" succeeded
  check "A is marked superseded_by B" "$(jf "$a" '.superseded_by // .result.superseded_by')" "$b"
  sock mlab POST "/v1/jobs/$b/cancel" >/dev/null
}

case_10() {
  only_workers
  say "-- no worker is on: the job waits for its queue timeout (20 s)"
  if [ "$dry" -eq 1 ]; then say "+ pdx test c2-expire (in the scratch repo, in the foreground)"; rc=6
  else rc=0; ( cd "$WORK/repo" && pdx test c2-expire ) || rc=$?; fi
  check "pdx test exit code" "$rc" 6
  id=$(db_q "select id from jobs where task='c2-expire' order by seq desc limit 1")
  check "state" "$(jf "$id" .state)" expired
  check "nothing ran" "$(ran_count mlab "$id")" 0
}

case_11() {
  # every TCP route refuses a missing or a wrong token, on every host; a job in flight is not touched
  only_workers mlab air26
  id=$(submit c2-sleep)
  for h in mlab air26 mserv air19; do
    for r in /v1/info /v1/facts /v1/pool /v1/events '/v1/jobs?state=queued' /v1/worker /v1/jobs/x/snapshot; do
      PATH_BAD=$r
      check "$h $r without a token" "$(tcp_bad "$h" none)" 401
      check "$h $r with a wrong token" "$(tcp_bad "$h" wrong)" 401
    done
    PATH_BAD=/v1/jobs
    check "$h POST /v1/jobs on TCP is not a route" "$(tcp "$h" POST /v1/jobs '{}' | grep -E -q '^(401|404|405)$' && echo refused)" refused
  done
  wait_for "$id" .state succeeded 200 || true
  check "the job in flight finished" "$(jf "$id" .state)" succeeded
}

case_12() {
  only_workers mlab air26
  cm=$(q mlab "git -C $WORK/repo rev-parse HEAD")
  tree=$(q mlab "git -C $WORK/repo rev-parse HEAD^{tree}")
  base='{"repo":{"name":"c2","root":"'$cm'","remote":""},"snapshot":{"commit":"'$cm'","tree":"'$tree'","base":"'$cm'","rel_cwd":"."},"class":"dev","labels":["os:darwin"],"locks":[],"cacheable":false,"timeout_s":60,"supersede_key":"c2-12","interruptible":false,"queue_timeout_s":120,"adhoc":false,"submitter":{"session":"c2","ref":"_c2c2c2","team":"","host":"mlab"},"git_dir":"'"$WORK"'/repo/.git"'
  before_a=$(q air26 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l'); before_m=$(q mlab 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l')
  # name|task|args|the code the worker refuses with (remotejob.go): the origin takes all four (it does not read the manifest), the worker's
  # Prepare refuses; the job ends failed with no reason field, and the code is the first word of the one line of summary.tail
  for spec in 'undeclared|c2-undeclared|[]|task_unknown' 'bad option|c2-files|["--bad-option"]|args_invalid' 'path escape|c2-files|["../../etc/hosts"]|args_invalid' 'shell metacharacters|c2-files|["a;touch /tmp/wwd-c2-pwned"]|args_invalid'; do
    name=${spec%%|*}; rest=${spec#*|}; task=${rest%%|*}; rest=${rest#*|}; args=${rest%%|*}; code=${rest#*|}
    body="$base,\"task\":\"$task\",\"args\":$args}"
    id=$(sock mlab POST /v1/jobs "$body" | jq -r '.id // empty' 2>/dev/null || true)
    [ "$dry" -eq 1 ] && id="<$name>"
    case "$id" in j_*|"<"*) ;; *) check "$name: the origin took the submission (it answered with an id)" "no id" "an id"; continue ;; esac
    track "$id"
    wait_for "$id" .state failed 90 || true
    check "$name: refused (state)" "$(jf "$id" .state)" failed
    check "$name: refusal code" "$(jf "$id" '(.result.summary.tail[0] // "") | split(":")[0]')" "$code"
    check "$name: nothing ran" "$(ran_count air26 "$id")$(ran_count mlab "$id")" 00
  done
  # a job's workspace is removed after its result is posted (runner: finish, then Cleanup): ask again until both hosts are back to the
  # count they had, at most 30 s
  now_a=$(q air26 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l'); now_m=$(q mlab 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l')
  wn=0
  if [ "$dry" -eq 0 ]; then
    while { [ "$now_a" != "$before_a" ] || [ "$now_m" != "$before_m" ]; } && [ "$wn" -lt $((30 * ${C2_TIMEOUT_SCALE:-1})) ]; do
      sleep 2; wn=$((wn + 2))
      now_a=$(q air26 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l'); now_m=$(q mlab 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l')
    done
  fi
  check "no marker file was made on mlab" "$(q mlab 'test -e /tmp/wwd-c2-pwned && echo made || echo none')" none
  check "no marker file was made on air26" "$(q air26 'test -e /tmp/wwd-c2-pwned && echo made || echo none')" none
  check "air26 left no workspace behind (within 30 s)" "$now_a" "$before_a"
  check "mlab left no workspace behind (within 30 s)" "$now_m" "$before_m"
}

case_13() {
  only_workers mserv
  id=$(submit c2-php-db)
  wait_for "$id" .state succeeded 200 || true
  check "php job state" "$(jf "$id" .state)" succeeded
  # the result is posted first and the workspace and the database are removed after it (runner: finish, then Cleanup; the drop is bounded at
  # 30 s), so the answer is asked for until it is 0, at most 45 s
  # the case's own database, by name: another job's database on the instance is not this case's to judge. That the job had one is what
  # its state says: db_probe.php exits 0 only after it connected to the database it was given, made a table, wrote and read it back.
  dbn=$(job_db_name "$id")
  left=$(mserv_db "$dbn"); dn=0
  if [ "$dry" -eq 0 ]; then
    while [ "$left" != 0 ] && [ "$dn" -lt $((45 * ${C2_TIMEOUT_SCALE:-1})) ]; do sleep 3; dn=$((dn + 3)); left=$(mserv_db "$dbn"); done
  fi
  check "its database is gone (within 45 s of the result)" "$left" 0
  # the host's /tmp holds a sentinel the job must not see; what the job writes to its /tmp must not reach the host's. The names carry
  # this run's tag, so both files are the run's alone: finish() removes them (also when the run is interrupted) and nothing that was
  # there before is touched.
  fault_on mserv "/tmp/wwd-c2-sentinel.$SB_TAG"
  FAULTS="$FAULTS mserv:/tmp/wwd-c2-private.$SB_TAG"
  check "the host has a docker socket (else the probe's refusal proves nothing)" "$(q mserv "test -S ${C2_DOCKER_SOCK:-/run/docker.sock} && echo yes || echo no")" yes
  # the control: outside the sandbox, the account the matrix logs in as (the job account on mserv, in the docker group) is answered
  check "the host's docker socket answers the job's account, outside the sandbox (control)" "$(q mserv "${C2_DOCKER_PING:-curl -s --max-time 3 --unix-socket /run/docker.sock http://localhost/_ping}" 2>/dev/null)" OK
  sb=$(submit c2-sandbox)
  wait_for "$sb" .state succeeded 120 || true
  check "sandbox probe (no docker socket, nothing writable outside the workspace, own /tmp, no sudo)" "$(jf "$sb" .state)" succeeded
  check "the job's /tmp write did not reach the host" "$(q mserv "test -e /tmp/wwd-c2-private.$SB_TAG && echo reached || echo private")" private
  fault_off mserv "/tmp/wwd-c2-sentinel.$SB_TAG" || true
  fault_off mserv "/tmp/wwd-c2-private.$SB_TAG" || true
  check "no failed wwd unit is left" "$(q mserv "systemctl --failed --no-legend | grep -c 'wwd-' || true")" 0
}

case_14() {
  check "air19 claimed nothing since this run began" "$(air19_new)" 0
  check "air19's worker is off" "$(sock air19 GET /v1/worker | jq -r .enabled 2>/dev/null || echo false)" false
}

# ---- go -----------------------------------------------------------------------------------------------------------------
trap finish EXIT INT TERM
precheck
for run_case in $cases; do
  say ""; say "== case $run_case: $(catalog | awk -F'|' -v c="$run_case" '$1==c {print $2}') =="
  CASE_FAILS=0; CASE_SKIP=""; [ "$dry" -eq 1 ] || rm -f "$WORK/connfail"
  # a case that runs a job on air26's worker needs its owner away
  # (not 8: air26 is only a second origin there and its worker is off; not 11: it only sends tokens)
  if [ "$run_case" != 8 ] && [ "$run_case" != 11 ] && catalog | awk -F'|' -v c="$run_case" '$1==c && $3 ~ /air26/ {f=1} END {exit !f}' && ! owner_idle; then
    CASE_SKIP="air26's owner stayed active for 10 minutes ($(sock air26 GET /v1/facts | jq -c .owner 2>/dev/null)): its worker would not take the job"
  else
    # a failing command does not end the run: its case ends with FAIL checks, and the clean-up is the trap's, whatever happens
    set +e
    "case_$run_case"
    set -e
  fi
  # the air19 rule holds in every case: it claimed nothing since the matrix began
  if [ "$run_case" != 14 ]; then
    check "air19 claimed nothing during case $run_case" "$(air19_new)" 0
  fi
  # a connection that was lost anywhere in the case, its checks included, FAILs it (also a case that would have been a SKIP)
  if [ -s "$WORK/connfail" ]; then
    CASE_FAILS=$((CASE_FAILS + 1)); CASE_SKIP=""
    say "  FAIL ssh to $(sort -u "$WORK/connfail" | tr '\n' ' ')was lost (the connection, not a result of this case)"
    rm -f "$WORK/connfail"
  fi
  if [ -n "$CASE_SKIP" ]; then say "SKIP case $run_case: $CASE_SKIP"; SKIPPED="$SKIPPED $run_case"
  elif [ "$CASE_FAILS" -gt 0 ]; then say "FAIL case $run_case"; FAILED="$FAILED $run_case"
  else say "PASS case $run_case"; PASSED="$PASSED $run_case"; fi
done
