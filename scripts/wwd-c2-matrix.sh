#!/bin/sh
# The C2 real-host matrix of wwd (docs/wwd-c2-matrix.md; plan WWD-4 "Acceptance (C2)").
#
#   scripts/wwd-c2-matrix.sh [--dry-run] [--list] [--manual] [--force] (--all | --case N[,N...])
#
# Run from mlab (the origin), once wwd is deployed on the four hosts. Each case checks what it needs, makes one fault with the
# service manager (never pfctl, iptables, sudoers or a firewall), waits, ends with read-only checks and prints PASS or FAIL
# with the evidence; whatever it made (jobs, a scratch repo, a stopped wwd, a worker switch) is put back at the end, also on
# ^C. --dry-run prints every step and runs none of them. This script has not been run on the hosts yet: the lines marked
# TODO(first-run) are assumptions about names that wwd's routes and CLI fix when they land.
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
RAN=/tmp/wwd-c2-ran.log          # the probe tasks append one line per run here, outside the workspace (darwin hosts)
WORK="${TMPDIR:-/tmp}/wwd-c2.$$"

# ---- the cases (number, title, hosts, minutes, needs) --------------------------------------------------------
catalog() {
  cat <<'EOF'
1|two claims on one job|mlab air26|3||
2|worker killed mid-job: lapse, second attempt, failed/lost|mlab air26|8||
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
  if [ "$on_h" = mlab ]; then sh -c "$*"; else ssh -o BatchMode=yes -o ConnectTimeout=5 "$on_h" "$*"; fi
}
# q HOST CMD: a read; its output is the answer (dry run: a placeholder)
q() {
  q_h=$1; shift
  if [ "$dry" -eq 1 ]; then say "? on $q_h: $*" >&2; echo "{}"; return 0; fi
  on "$q_h" "$@"
}
body_arg() { if [ $# -gt 0 ]; then printf -- "-H 'Content-Type: application/json' -d '%s'" "$1"; fi; }
sock() { sk_h=$1; sk_m=$2; sk_p=$3; shift 3; q "$sk_h" "curl -sS --unix-socket $SOCK -X $sk_m $(body_arg "$@") http://wwd$sk_p"; }   # TODO(first-run): PUT /v1/worker takes the shape GET /v1/worker gives
# tcp HOST METHOD PATH [BODY]: the pool route of HOST over the tailnet with the pool token (read from mlab's file, never printed)
AUTH='-H "Authorization: Bearer $(cat $HOME/.config/wwd/pool.token)"'
tcp() { tc_h=$1; tc_m=$2; tc_p=$3; shift 3; q mlab "curl -sS -o /dev/null -w '%{http_code}' -X $tc_m $AUTH $(body_arg "$@") --max-time 10 http://$(addr "$tc_h"):$PORT$tc_p"; }
tcp_body() { tb_h=$1; tb_m=$2; tb_p=$3; shift 3; q mlab "curl -sS -w '\n%{http_code}' -X $tb_m $AUTH $(body_arg "$@") --max-time 10 http://$(addr "$tb_h"):$PORT$tb_p"; }
# tcp_bad HOST none|wrong : the route in $PATH_BAD with no token or with a wrong one
tcp_bad() { if [ "$2" = wrong ]; then tx_a="-H 'Authorization: Bearer c2-wrong-token'"; else tx_a=""; fi; q mlab "curl -sS -o /dev/null -w '%{http_code}' $tx_a --max-time 10 http://$(addr "$1"):$PORT$PATH_BAD"; }

# ---- the service manager (the only way a wwd is stopped) -----------------------------------------------------------
svc_pid() { if is_mac "$1"; then q "$1" "launchctl list gs.wake.wwd | awk -F' = ' '/\"PID\"/ {gsub(/;/,\"\",\$2); print \$2}'"; else q "$1" "systemctl show wwd.service -p MainPID --value"; fi; }
svc_restart() { if is_mac "$1"; then on "$1" 'launchctl kickstart -k gui/$(id -u)/gs.wake.wwd'; else on "$1" 'sudo systemctl restart wwd.service'; fi; }
DOWNED=""   # hosts whose wwd this script took down and has not brought up: finish brings them up
svc_down() {
  DOWNED="$DOWNED $1"
  if is_mac "$1"; then on "$1" 'launchctl bootout gui/$(id -u)/gs.wake.wwd'; else on "$1" 'sudo systemctl stop wwd.service'; fi
}
svc_up() {
  if is_mac "$1"; then su_c='launchctl bootstrap gui/$(id -u) $HOME/Library/LaunchAgents/gs.wake.wwd.plist'; else su_c='sudo systemctl start wwd.service'; fi
  on "$1" "$su_c" || return 1                                                  # a failed start stays on the list
  DOWNED=$(echo "$DOWNED" | tr ' ' '\n' | grep -v "^$1\$" | tr '\n' ' ')     # only once it is up
}
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
mserv_dbs() { q mserv "mysql -N -e 'show databases' 2>/dev/null | grep -c '^wwd_' || true"; }     # TODO(first-run): the database naming and the credentials of mserv's test MySQL
# the jobs this run made are listed in a file: submit runs in a $( ) subshell, which could not hand a variable back
track() { if [ "$dry" -eq 0 ]; then echo "${2:-mlab} $1" >> "$WORK/jobs"; fi; }     # track ID [ORIGIN]
submit() {   # submit TASK [ARGS...] -> the job id; runs in the scratch repo, the caller never waits  TODO(first-run): pdx test --detach --json prints {"id":...}
  sb_t=$1; shift
  if [ "$dry" -eq 1 ]; then say "+ submit $sb_t $* (in the scratch repo)" >&2; echo "<$sb_t>"; return 0; fi
  sb_id=$(q mlab "cd $WORK/repo && pdx test --detach --json $sb_t $* | jq -r .id")
  track "$sb_id"; echo "$sb_id"
}
job() { sock mlab GET "/v1/jobs/$1"; }   # TODO(first-run): the job JSON of spec 4.1
jf_on() { sock "$1" GET "/v1/jobs/$2" | jq -r "$3" 2>/dev/null || echo "?"; }     # jf_on ORIGIN ID '.state'
jf() { jf_on "${WF_HOST:-mlab}" "$1" "$2"; }                                      # jf ID '.state' : on mlab, or on WF_HOST while wait_for_on runs
wait_for_on() { WF_HOST=$1; shift; wf_r=0; wait_for "$@" || wf_r=$?; WF_HOST=mlab; return "$wf_r"; }
# attempts ID: the counted failures of the job in wwd.db (a lapse or an infra release counts, a reclaim does not): 0 for a job that
# succeeded at once; the claim in hand is number attempts+1
attempts() { db_q "select attempts from jobs where id='$1'"; }
wait_for() {   # wait_for ID JQEXPR WANT SECONDS
  if [ "$dry" -eq 1 ]; then say "~ wait up to $4 s until $1 $2 == $3"; return 0; fi
  wf_n=0; wf_max=$(($4 * ${C2_TIMEOUT_SCALE:-1}))     # C2_TIMEOUT_SCALE=0: look once (the stub test of this script)
  while :; do
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
    pc_q=$(sock mlab GET '/v1/jobs?state=queued' | jq 'length' 2>/dev/null || echo 0)
    [ "$dry" -eq 1 ] || [ "$pc_q" = 0 ] || { say "FAIL: $pc_q jobs are queued already (the matrix wants a quiet pool; --force to go on)"; exit 1; }
  fi
  [ "$(sock air19 GET /v1/worker | jq -r .enabled 2>/dev/null || echo false)" = false ] || [ "$dry" -eq 1 ] || { say "FAIL: air19's worker is on"; exit 1; }
  x mkdir -p "$WORK/repo" "$WORK/saved"
  if [ "$dry" -eq 0 ]; then
    pc_b=$(db_q "$AIR19_Q") || { say "FAIL: cannot read the claims of air19 from wwd.db"; exit 1; }
    printf '%s\n' "$pc_b" | sort -u > "$WORK/air19.base"
  fi
  on air19 "rm -f $RAN"
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
echo "job=${WWD_JOB:-?} host=$(hostname -s) start=$(date +%s)" >> /tmp/wwd-c2-ran.log 2>/dev/null || true
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
// Uses the database the job was given: connect, make a table, write, read back. TODO(first-run): the variable names of the per-job
// database are fixed by 4e; these are the usual ones. Exit 0 only when the round trip worked.
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
# exits 0 only when the sandbox of spec section 5 holds: no docker socket, nothing writable outside the workspace, sudo blocked
fail=0
[ -S /run/docker.sock ] && { echo "docker.sock reachable"; fail=1; }
touch "/home/wake/wwd-c2-outside.$$" 2>/dev/null && { echo "wrote outside the workspace"; rm -f "/home/wake/wwd-c2-outside.$$"; fail=1; }
sudo -n true 2>/dev/null && { echo "sudo works"; fail=1; }
touch /tmp/wwd-c2-private 2>/dev/null || { echo "no private /tmp"; fail=1; }
echo "sandbox probe done fail=$fail"
exit "$fail"
EOS
  ( cd "$WORK/repo" && git init -q . && git add -A && git -c user.name=c2 -c user.email=c2@localhost commit -q -m c2 )
}

finish() {
  fn_rc=$?
  trap - EXIT INT TERM
  say "-- cleaning up"
  fn_bad=""     # what the clean-up could not do: said at the end, and the exit code is not 0
  for fn_pair in $STOPPED; do svc_thaw "${fn_pair%%:*}" || fn_bad="$fn_bad thaw:${fn_pair%%:*}"; done
  for fn_h in $DOWNED; do svc_up "$fn_h" || fn_bad="$fn_bad start:$fn_h"; done
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
    claim "$pid" fakeA | tail -1 > "$WORK/out/a" &
    claim "$pid" fakeB | tail -1 > "$WORK/out/b" &
    wait
    codes=$(cat "$WORK/out/a" "$WORK/out/b" | sort | tr '\n' ' ')
  fi
  check "one claim wins and one gets 409" "$codes" "200 409 "
  check "the job is claimed once" "$(jf "$pid" .state)" claimed
}

case_2() {
  only_workers air26
  id=$(submit c2-sleep)
  wait_for "$id" .state running 90 || true
  say "-- attempt 1: air26's wwd goes down with the job in it"
  svc_down air26
  wait_for "$id" .state queued 150 || true
  check "lease lapsed, queued again" "$(jf "$id" .state)" queued
  check "attempts after the lapse" "$(attempts "$id")" 1
  svc_up air26
  wait_for "$id" .state running 120 || true
  say "-- attempt 2: down again"
  svc_down air26
  wait_for "$id" .state failed 150 || true
  check "state after two lapses" "$(jf "$id" .state)" failed
  check "reason" "$(jf "$id" .result.reason)" lost
  check "attempts" "$(attempts "$id")" 2
  svc_up air26
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
  wait_for "$id" .state running 90 || true
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
  wait_for "$id" .state running 90 || true
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
    wait_for "$id" .state claimed 60 || true
    svc_freeze mlab; nap 20; svc_thaw mlab
    wait_for "$id" .state succeeded 600 || true
    check "5a state" "$(jf "$id" .state)" succeeded
    check "5a an infra release or a lapse was counted" "$(attempts "$id")" 1
  else say "SKIP 5a: $CASE_SKIP"; CASE_SKIP=""; fi
  say "-- 5b: the origin stops answering during the run, shorter than the lease, then longer"
  id=$(submit c2-sleep); wait_for "$id" .state running 90 || true
  svc_freeze mlab; nap 30; svc_thaw mlab
  wait_for "$id" .state succeeded 200 || true
  check "5b short: state" "$(jf "$id" .state)" succeeded
  check "5b short: attempts" "$(attempts "$id")" 0
  id=$(submit c2-sleep); wait_for "$id" .state running 90 || true
  svc_freeze mlab; nap 90; svc_thaw mlab
  wait_for "$id" .state succeeded 400 || true
  check "5b long: state" "$(jf "$id" .state)" succeeded
  check "5b long: the lease lapsed once" "$(attempts "$id")" 1
  say "-- 5c: the origin stops answering when the command is ending (result upload)"
  id=$(submit c2-sleep); wait_for "$id" .state running 90 || true
  nap 84
  svc_freeze mlab; nap 40; svc_thaw mlab
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
  check "the log is not there" "$(tcp mlab GET "/v1/jobs/$id/log")" 404   # TODO(first-run): the code for a missing log
}

case_7() {
  if ! need_fault air26 owner_active_file && [ "$manual" -eq 0 ]; then return 0; fi
  CASE_SKIP=""
  only_workers air26
  id=$(submit c2-sleep)
  wait_for "$id" .state running 90 || true
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
  rdir="${TMPDIR:-/tmp}/wwd-c2.r.$$"; REMOTE_DIRS="$REMOTE_DIRS air26:$rdir"
  x sh -c "tar -C $WORK -cf - repo | ssh air26 'mkdir -p $rdir && tar -C $rdir -xf -'"        # the scratch repo on air26: a second origin
  ida=$(q air26 "cd $rdir/repo && pdx test --detach --json c2-any | grep -o '\"id\" *: *\"[^\"]*\"' | cut -d'\"' -f4")   # TODO(first-run): pdx on air26's non-interactive PATH
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
  say "-- interruptible: the older one is cancelled and recorded superseded"
  a=$(submit c2-sleep-int); wait_for "$a" .state running 90 || true
  b=$(submit c2-sleep-int)
  wait_for "$a" .state superseded 60 || true
  check "A state" "$(jf "$a" .state)" superseded
  check "A reason" "$(jf "$a" .result.reason)" "superseded_by:$b"
  sock mlab POST "/v1/jobs/$b/cancel" >/dev/null
  say "-- not interruptible: the older one runs to its end, its result keeps superseded_by"
  a=$(submit c2-sleep-noint); wait_for "$a" .state running 90 || true
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
  for spec in 'undeclared|c2-undeclared|[]' 'bad option|c2-files|["--bad-option"]' 'path escape|c2-files|["../../etc/hosts"]' 'shell metacharacters|c2-files|["a;touch /tmp/wwd-c2-pwned"]'; do
    name=${spec%%|*}; rest=${spec#*|}; task=${rest%%|*}; args=${rest#*|}
    body="$base,\"task\":\"$task\",\"args\":$args}"
    id=$(sock mlab POST /v1/jobs "$body" | jq -r .id 2>/dev/null || echo "?")
    track "$id"
    wait_for "$id" .state failed 90 || true
    check "$name: refused (state)" "$(jf "$id" .state)" failed        # TODO(first-run): the state and reason words of a refusal are 4e's; the matrix follows them
    check "$name: nothing ran" "$(ran_count air26 "$id")$(ran_count mlab "$id")" 00
  done
  check "no marker file was made on mlab" "$(q mlab 'test -e /tmp/wwd-c2-pwned && echo made || echo none')" none
  check "no marker file was made on air26" "$(q air26 'test -e /tmp/wwd-c2-pwned && echo made || echo none')" none
  check "air26 made no workspace" "$(q air26 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l')" "$before_a"
  check "mlab made no workspace" "$(q mlab 'ls $HOME/.local/share/wwd/work 2>/dev/null | wc -l')" "$before_m"
}

case_13() {
  only_workers mserv
  id=$(submit c2-php-db)
  wait_for "$id" .state succeeded 200 || true
  check "php job state" "$(jf "$id" .state)" succeeded
  check "its database is gone" "$(mserv_dbs)" 0
  sb=$(submit c2-sandbox)
  wait_for "$sb" .state succeeded 120 || true
  check "sandbox probe (no docker socket, nothing writable outside, no sudo)" "$(jf "$sb" .state)" succeeded
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
  CASE_FAILS=0; CASE_SKIP=""
  "case_$run_case"
  # the air19 rule holds in every case: it claimed nothing since the matrix began
  if [ "$run_case" != 14 ]; then
    check "air19 claimed nothing during case $run_case" "$(air19_new)" 0
  fi
  if [ -n "$CASE_SKIP" ]; then say "SKIP case $run_case: $CASE_SKIP"; SKIPPED="$SKIPPED $run_case"
  elif [ "$CASE_FAILS" -gt 0 ]; then say "FAIL case $run_case"; FAILED="$FAILED $run_case"
  else say "PASS case $run_case"; PASSED="$PASSED $run_case"; fi
done
