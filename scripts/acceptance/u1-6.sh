#!/bin/zsh
# U1-6 acceptance: the conversation API (HTTP snapshot / increments / subagents, the WebSocket and its approvals).
# Plan docs/plans/2026-10-09-interface-u1-6-plan.md "Acceptance". Run by hand against a daemon that has U1-6 deployed
# (mlab): `scripts/acceptance/u1-6.sh`. Exit status = number of failed checks. Results are printed (PASS / FAIL / WARN)
# and the raw material is kept under $ACC_OUT (default: a fresh directory under $TMPDIR).
#
# What it drives: real Claude Code sessions (Sonnet, default permission mode, cwd a throw-away git repo), one with the
# purdex mod and one without it (a `--settings` overlay that empties CLAUDE_CODE_PLUGIN_DIRS, the no-mod path), through tmux; their transcripts are
# read through the API and the WebSocket (`rec-conv.mjs`) and compared with what the sessions did.
#
# TMUX RULES (feedback_tmux_test_isolation): the daemon only sees the DEFAULT tmux server, so the sessions live on it,
# but ONLY as `new-session -d -s acc-u16-<n>`; cleanup is `kill-session -t` of exactly those names (also in the trap).
# NEVER kill-server, never touch another session; send-keys only to the sessions this script created. TMUX is left as
# it is for the user's own server; no private tmux server is started. Tokens: read from ~/.config/pdx/config.toml into a
# 0600 header file used with `curl -H @file` (and by rec-conv.mjs); nothing secret is on a command line or printed.
#
# Needs: curl, jq, node >= 22 (global WebSocket), tmux, claude, pdx on PATH (or PDX_BIN). The daemon's host-events
# subscribers other than this script (an App) cannot be excluded; check 4d says so when that matters.
set -u
HERE=${0:A:h}
BASE=${PDX_BASE:-http://100.64.0.2:7860}
PDX=${PDX_BIN:-pdx}
OUT=${ACC_OUT:-$(mktemp -d "${TMPDIR:-/tmp}/acc-u16.XXXXXX")}
mkdir -p "$OUT"
HDR=$OUT/hdr
( umask 077; printf 'Authorization: Bearer %s\n' "$(awk -F'"' '/^token *=/ {print $2; exit}' ~/.config/pdx/config.toml)" > "$HDR" )
FAILS=0
ALLSES=""
RECPIDS=""
log()  { print -r -- "$(date +%T) $*"; }
pass() { log "PASS  $*"; }
fail() { log "FAIL  $*"; FAILS=$((FAILS+1)); }
warn() { log "WARN  $*"; }
api()  { curl -s -H @"$HDR" "$BASE$1"; }
code() { curl -s -o /dev/null -w '%{http_code}' -H @"$HDR" "$BASE$1"; }

cleanup() {
  for p in ${=RECPIDS}; do kill "$p" 2>/dev/null; done
  for n in ${=ALLSES}; do tmux kill-session -t "$n" 2>/dev/null; done
  log "cleanup: killed tmux sessions:${ALLSES:- (none)}; raw material in $OUT"
  rm -f "$HDR"
  rm -rf "$OUT"/work.*
}
trap cleanup EXIT INT TERM

# ---- helpers ---------------------------------------------------------------------------------------------------
slug_of() { print -rn -- "$1" | sed 's/[^A-Za-z0-9]/-/g'; }
send()   { tmux send-keys -t "$SES" -l "$1"; sleep 0.3; tmux send-keys -t "$SES" Enter; }
# A permission box is on screen when the selector line "❯ 1. Yes" is in the LAST lines of the pane (older output that
# mentions "1. Yes" scrolls up and must not count: a stray "1" + Enter would be a prompt of its own).
asks()   { tmux capture-pane -p -t "$SES" | tail -14 | grep -qE '^[[:space:]]*❯ 1\. Yes'; }
approve() { tmux send-keys -t "$SES" 1; sleep 0.3; tmux send-keys -t "$SES" Enter; }
# approve every permission box that shows up until the conversation is idle again (idle for 3 s in a row) or time is up
settle() { # seconds
  local i quiet=0
  for i in $(seq 1 $1); do
    if asks; then approve; quiet=0; sleep 1; continue; fi
    if [[ "$(snap | jq -r .header.status 2>/dev/null)" == "idle" ]]; then quiet=$((quiet+1)); [[ $quiet -ge 3 ]] && return 0; else quiet=0; fi
    sleep 1
  done
  return 1
}
snap()  { api "/api/conversations/claude/$SID${1:-}"; }
start_claude() { # mod|nomod  -> sets SES WORK SID(empty until the first prompt wrote a transcript)
  local mode=$1 cmd="claude --model sonnet --permission-mode default" j trusted=0 ready=0
  # The mod is loaded through env.CLAUDE_CODE_PLUGIN_DIRS of ~/.claude/settings.json, so unsetting it in the shell does
  # nothing; a --settings overlay that empties it gives this one session no mod (no mod stream, no hello).
  [[ $mode == nomod ]] && cmd="$cmd --settings '{\"env\":{\"CLAUDE_CODE_PLUGIN_DIRS\":\"\"}}'"
  SES=acc-u16-$RANDOM; ALLSES="$ALLSES $SES"
  WORK=$OUT/work.$SES; mkdir -p "$WORK"; git -C "$WORK" init -q && git -C "$WORK" commit -q --allow-empty -m init
  WORK=$(cd "$WORK" && pwd -P)
  tmux new-session -d -s "$SES" -x 180 -y 50 -c "$WORK"; sleep 4
  tmux send-keys -t "$SES" -l "$cmd"; sleep 0.3; tmux send-keys -t "$SES" Enter
  for j in {1..90}; do
    sleep 1
    if [[ $trusted == 0 ]] && tmux capture-pane -p -t "$SES" | grep -q "I trust this folder"; then
      tmux send-keys -t "$SES" Down; sleep 0.3; tmux send-keys -t "$SES" Enter; trusted=1; continue
    fi
    ready=$($PDX peers 2>/dev/null | awk -v n="$SES" '{for(i=1;i<=NF;i++) if($i==n){print; exit}}' | grep -c ' cc ')
    [[ $ready == 1 ]] && break
  done
  SID=""
  [[ $ready == 1 ]]
}
find_sid() { # the newest transcript of this session's cwd
  local d=$HOME/.claude/projects/$(slug_of "$WORK")
  local -a fs
  fs=("$d"/*.jsonl(N.om))   # newest first; empty when there is no transcript yet
  (( ${#fs} > 0 )) && SID=${${fs[1]:t}%.jsonl}
}
wait_sid() { local i; for i in {1..60}; do find_sid; [[ -n $SID ]] && return 0; sleep 1; done; return 1; }
rec() { # name [after] -> starts a recorder on $SID; file $OUT/<name>.ndjson; pid in REC_PID
  node "$HERE/rec-conv.mjs" "$OUT/$1.ndjson" "$SID" 0 ${2:-} & REC_PID=$!; RECPIDS="$RECPIDS $REC_PID"
}
unrec() { kill "$1" 2>/dev/null; sleep 0.5; }
frames() { jq -c "select(.type == \"$2\")" "$OUT/$1.ndjson" 2>/dev/null; }

# ---- 1 capability -------------------------------------------------------------------------------------------------
INFO=$(api /api/info)
print -r -- "$INFO" | jq -e '.capabilities | index("conversations.v1")' >/dev/null && pass "1 /api/info lists conversations.v1" || { fail "1 conversations.v1 is not advertised ($(print -r -- "$INFO" | jq -c .capabilities))"; exit 1; }

# ---- the session with the mod ----------------------------------------------------------------------------------------
start_claude mod || { fail "claude did not come up in $SES"; exit 1; }
log "session $SES (mod) cwd $WORK"
send "Use the Bash tool to run exactly: echo hello-u16 and then reply with the single word: done"
wait_sid || { fail "no transcript for $SES"; exit 1; }
log "conversation $SID"
settle 90 || warn "turn 1 did not settle to idle in 90 s"
CUR1=$(snap | jq -r .cursor)

# ---- 2 two turns: snapshot, paging, increments -----------------------------------------------------------------------
send "Use the Task tool with the general-purpose agent to do this: reply with the single word hi. Then reply with the single word: finished"
settle 150 || warn "turn 2 did not settle to idle in 150 s"
S=$(snap)
print -r -- "$S" > "$OUT/snapshot.json"
N=$(print -r -- "$S" | jq '.conversation.turns | length')
[[ $N -ge 2 ]] && pass "2a the snapshot shows $N turns" || fail "2a expected >= 2 turns, got $N"
print -r -- "$S" | jq -e '[.conversation.turns[].items[] | select(.type=="step") | .status] | length > 0 and all(. != "running")' >/dev/null && pass "2b every step has a final status" || fail "2b step statuses: $(print -r -- "$S" | jq -c '[.conversation.turns[].items[] | select(.type=="step") | .status]')"
[[ "$(print -r -- "$S" | jq -r .header.live)" == true ]] && pass "2c header.live is true while the pane runs it" || fail "2c header.live = $(print -r -- "$S" | jq -r .header.live)"
[[ "$(print -r -- "$S" | jq -r .header.status)" == idle ]] && pass "2d header.status is idle at the prompt (the pane's light)" || fail "2d header.status = $(print -r -- "$S" | jq -r .header.status)"
print -r -- "$S" | jq -e '[.conversation.turns[] | .items | to_entries[] | .key == .value.index] | all' >/dev/null && pass "2e every item's index is its position" || fail "2e item indexes are not positions"
P=$(snap "?turns=1")
FIRST=$(print -r -- "$P" | jq -r .window.first_index)
[[ "$(print -r -- "$P" | jq -r .window.has_more_before)" == true ]] && pass "2f turns=1 has more before (first_index $FIRST)" || fail "2f turns=1: has_more_before false"
P2=$(snap "?turns=1&before=$FIRST")
[[ "$(print -r -- "$P2" | jq -r .window.last_index)" == "$((FIRST-1))" ]] && pass "2g before=$FIRST pages back to turn $((FIRST-1))" || fail "2g before: last_index $(print -r -- "$P2" | jq -r .window.last_index)"
INC=$(snap "?after=$CUR1")
print -r -- "$INC" | jq -e '(.reset // false) == false and ([.changes[] | select(.items | length > 0) | .turn.index] | all(. >= 1))' >/dev/null && pass "2h ?after=<cursor between the turns> reports only the second turn's rows" || fail "2h increment: $(print -r -- "$INC" | jq -c '{reset, turns: [.changes[] | {i: .turn.index, n: (.items|length)}]}')"
[[ "$(snap "?after=ffffffffffffffff:1" | jq -r .reset)" == true ]] && pass "2i a foreign cursor is a reset with a snapshot" || fail "2i foreign cursor was not a reset"

# ---- 3 subagent endpoint ---------------------------------------------------------------------------------------------
AGENT=$(print -r -- "$S" | jq -r '[.. | objects | select(.agent_id? != null) | .agent_id][0] // empty')
if [[ -z $AGENT ]]; then
  warn "3 no step with a subagent in the snapshot (the model did not start one): $(print -r -- "$S" | jq -c '[.conversation.turns[].items[] | select(.type=="step") | .tool]')"
else
  SUB=$(api "/api/conversations/claude/$SID/subagents/$AGENT")
  print -r -- "$SUB" > "$OUT/subagent.json"
  print -r -- "$SUB" | jq -e '(.items | length) > 0 and .partial == false' >/dev/null && pass "3 subagent $AGENT: $(print -r -- "$SUB" | jq '.items|length') items (<sid>/subagents/agent-<id>.jsonl is where this CC version puts it)" || fail "3 subagent answer: $(print -r -- "$SUB" | head -c 300)"
fi

# NOTE: commands such as `echo` need no permission in default mode; `perl -e` always asks, which the checks below need.
# ---- 4 the WebSocket ---------------------------------------------------------------------------------------------------
rec ws1; R1=$REC_PID; sleep 2
[[ "$(frames ws1 conversation.snapshot | wc -l | tr -d ' ')" == 1 && "$(frames ws1 approvals.snapshot | wc -l | tr -d ' ')" == 1 ]] && pass "4a the stream starts with conversation.snapshot and approvals.snapshot" || fail "4a first frames: $(jq -r .type "$OUT/ws1.ndjson" | head -3 | tr '\n' ' ')"
jq -s 'map(select(.seq)) | [.[].seq] | . == [range(1; length+1)]' "$OUT/ws1.ndjson" | grep -q true && pass "4b seq is contiguous from 1" || fail "4b seq: $(jq -c .seq "$OUT/ws1.ndjson" | tr '\n' ' ')"
# With the mod present, the mod raises the remote row for AskUserQuestion (hook_ask, lead-team spec §6.6 / U19); a
# permission ask in such a session opens no terminal-only row (the mod owns the session's rows) - that case is 4g/4h's
# no-mod session. So: ask a question, expect approval opened, answer it in the terminal, expect approval closed.
send "Use the AskUserQuestion tool to ask me one question with header Color and options Red and Blue, then reply with my answer."
OPENED=0
for i in $(seq 1 90); do
  [[ -n "$(jq -c 'select(.type=="approval" and .value.op=="opened" and .value.approval.kind=="hook_ask")' "$OUT/ws1.ndjson" 2>/dev/null)" ]] && { OPENED=1; break; }
  sleep 1
done
[[ $OPENED == 1 ]] && pass "4c the question arrived as approval opened (hook_ask)" || fail "4c no approval opened frame for the question"
APPROVED_AT=$(python3 -c 'import time;print(int(time.time()*1000))')
# answer in the terminal (the first option): Enter, again every 3 s until the row is closed (the dialog may still be
# drawing when the first one arrives; a stray Enter at an idle prompt does nothing)
for i in 1 2 3 4 5; do
  sleep 2; tmux send-keys -t "$SES" Enter; sleep 1
  [[ -n "$(jq -c 'select(.type=="approval" and .value.op=="closed")' "$OUT/ws1.ndjson" 2>/dev/null)" ]] && break
done
for i in $(seq 1 10); do asks && approve; sleep 1; done
settle 90 || warn "the question turn did not settle in 90 s"
[[ -n "$(jq -c 'select(.type=="approval" and .value.op=="closed")' "$OUT/ws1.ndjson" 2>/dev/null)" ]] && pass "4c' answering in the terminal closed it (approval closed frame)" || fail "4c' no approval closed frame"
NCH=$(frames ws1 conversation.changes | wc -l | tr -d ' ')
[[ $NCH -ge 1 ]] && pass "4d the turn produced $NCH conversation.changes frames" || fail "4d no conversation.changes frame for the turn"
LAT=$(jq -s --argjson a "${APPROVED_AT:-0}" '[.[] | select(.type=="conversation.changes" and .t > $a)] | (.[0].t - $a)' "$OUT/ws1.ndjson" 2>/dev/null)
log "     first changes frame $LAT ms after the approval keystroke"
# disconnect mid-turn, reconnect with the last cursor -> exactly the missed changes
LASTCUR=$(jq -r 'select(.value.cursor?) | .value.cursor' "$OUT/ws1.ndjson" | tail -1)
send "Use the Bash tool to run exactly: perl -e 'sleep 5; print 1' and then reply with the single word: slept"
for i in $(seq 1 40); do asks && break; sleep 1; done
unrec "$R1"
approve
settle 90 || warn "the disconnect turn did not settle"
rec ws2 "$LASTCUR"; R2=$REC_PID; sleep 3
FIRSTF=$(jq -r .type "$OUT/ws2.ndjson" | head -1)
[[ "$FIRSTF" == conversation.changes ]] && pass "4e reconnect with the last cursor starts with the catch-up (conversation.changes)" || fail "4e first frame after reconnect: $FIRSTF"
HTTPINC=$(snap "?after=$LASTCUR" | jq -S '[.changes[] | {t: .turn.id, i: [.items[].id]}]')
WSINC=$(jq -s -S '[.[] | select(.type=="conversation.changes")][0].value | [.changes[] | {t: .turn.id, i: [.items[].id]}]' "$OUT/ws2.ndjson")
[[ "$HTTPINC" == "$WSINC" && "$HTTPINC" != "[]" ]] && pass "4f the catch-up is exactly the missed changes (same as ?after=: $(print -r -- "$HTTPINC" | jq -c '[.[] | .t[0:8]]'))" || fail "4f catch-up differs. http=$HTTPINC ws=$WSINC"
unrec "$R2"

# ---- 4g a no-mod session's permission ask creates a terminal-only row, with ONLY the conversation stream connected ----
MODSES=$SES; MODSID=$SID; MODWORK=$WORK
if start_claude nomod; then
  send "reply with the single word: ready"
  wait_sid || fail "4g no transcript for the no-mod session"
  NMSTREAM=$(api /api/mod/streams | jq -r --arg w "$WORK" '[.streams[]? | select(.cwd==$w)] | length')
  [[ "$NMSTREAM" == 0 ]] && pass "4g0 the no-mod session has no mod stream" || fail "4g0 the no-mod session has $NMSTREAM mod stream(s): the mod was loaded"
  settle 60 >/dev/null
  rec ws3; R3=$REC_PID; sleep 3     # the stream is up (and holds the responder) BEFORE the ask
  send "Use the Bash tool to run exactly: perl -e 'sleep 2; print qq(nomod-ask-u16)' and then reply with the single word: ok"
  ROW=0
  for i in $(seq 1 60); do
    [[ -n "$(jq -c 'select(.type=="approval" and .value.op=="opened" and .value.approval.kind=="hook_permission")' "$OUT/ws3.ndjson" 2>/dev/null)" ]] && { ROW=1; break; }
    sleep 1
  done
  [[ $ROW == 1 ]] && pass "4g a no-mod session's permission ask is an approval opened on its conversation stream (terminal-only row; no App needed)" || fail "4g no hook_permission approval reached the stream ($(jq -r .type "$OUT/ws3.ndjson" | tr '\n' ' '))"
  approve; settle 60 || warn "no-mod turn did not settle"
  [[ -n "$(jq -c 'select(.type=="approval" and .value.op=="closed")' "$OUT/ws3.ndjson" 2>/dev/null)" ]] && pass "4g' approving in the terminal closed it" || fail "4g' no approval closed frame for the no-mod ask"
  unrec "$R3"
  # without the stream: a new ask creates no row (unless an App is connected to /ws/host-events)
  send "Use the Bash tool to run exactly: perl -e 'sleep 2; print qq(nomod-ask2-u16)' and then reply with the single word: ok"
  sleep 12
  ROWS=$(api /api/team/approvals | jq --arg s "$SID" '[.. | objects | select(.kind? == "hook_permission" and .origin?.session_id? == $s)] | length')
  if [[ "$ROWS" == 0 ]]; then pass "4h with the stream closed and no App, the ask creates no row (the stream was the responder)"
  else warn "4h a row exists with the stream closed: another /ws/host-events client (an App) is connected, so check 4g does not prove the stream's role by itself"; fi
  approve; settle 60 >/dev/null
else
  fail "4g the no-mod claude did not come up"
fi
tmux kill-session -t "$SES" 2>/dev/null
SES=$MODSES; SID=$MODSID; WORK=$MODWORK

# ---- 5 /clear: the old conversation goes live:false / ended; the new session id is its own conversation ----------------
OLDSID=$SID
rec ws4; R4=$REC_PID; sleep 2
send "/clear"; sleep 6
send "reply with the single word: cleared"
sleep 3
find_sid
for i in $(seq 1 30); do [[ "$(snap | jq -r .header.status)" == ended ]] && break; sleep 1; done
H=$(api "/api/conversations/claude/$OLDSID" | jq -c '.header | {live, status}')
[[ "$H" == '{"live":false,"status":"ended"}' ]] && pass "5a after /clear the old conversation is $H" || fail "5a old conversation header: $H"
jq -e 'select(.type=="conversation.header" or .type=="conversation.changes") | (.value.header.live == false and .value.header.status == "ended")' "$OUT/ws4.ndjson" >/dev/null 2>&1 && pass "5b the old conversation's stream reported it" || fail "5b the old stream never reported live:false/ended"
unrec "$R4"
if [[ -n $SID && $SID != $OLDSID ]]; then
  NEW=$(api "/api/conversations/claude/$SID" | jq -c '{turns: (.conversation.turns|length), live: .header.live}')
  pass "5c /clear started a new conversation $SID ($NEW), its own session id"
else fail "5c no new session id after /clear"; fi

# ---- 6 exit -> ended ---------------------------------------------------------------------------------------------------
settle 60 >/dev/null
send "/exit"; sleep 8
for i in $(seq 1 30); do [[ "$(snap | jq -r .header.status)" == ended ]] && break; sleep 1; done
H=$(snap | jq -c '.header | {live, status}')
[[ "$H" == '{"live":false,"status":"ended"}' ]] && pass "6 after /exit the conversation is $H" || fail "6 after /exit: $H"

log "done: $FAILS failed check(s)"
exit $FAILS
