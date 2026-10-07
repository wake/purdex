#!/bin/sh
# Copied from Nexen v0.19.0, adapter/testdata/fake-claude-permission.sh, with
# three changes for Purdex's tests (permission_race_test.go):
#   - the frames of stream_permission_request.jsonl are inlined (frames()
#     below) instead of read from a sibling file through dirname "$0";
#   - FAKE_PERM_LOG, when set, names a file this script appends to: "asked
#     <request_id>" once the request is on stdout, "tool_ran <tool_use_id>"
#     whenever it plays the tool's successful run (tool_result_ok);
#   - the interrupt mode also answers a decision the way ask does (allow ->
#     the tool runs, deny -> a rejected tool_result, then result), so a
#     decision that reached stdin shows in the log.
#
# Models a claude turn on a permission-asking profile (P4): the CLI writes a
# can_use_tool control_request on stdout and waits for the host's
# control_response on stdin. Frames come from the real probe (E2); the
# behaviour is picked by FAKE_PERM_MODE:
#
#   ask (default)  request, then read ONE stdin line: "behavior":"allow" gives a
#                  successful tool_result, "behavior":"deny" a permission-rule
#                  tool_result carrying the deny message verbatim, EOF a
#                  "stream closed" tool_result; then result. Finally blocks on
#                  stdin to EOF so a test can observe WHEN stdin is closed.
#   cancel         request, then control_cancel_request and the user-rejected
#                  tool_result and an aborted_tools result, without reading.
#   multi          (PR-B) two control_requests back to back (different request_id
#                  and tool_use_id; the second carries agent_id), then read two
#                  stdin lines in ANY order, answering each by its request_id with
#                  its own tool_result, then result.
#   malformed      (PR-B) a can_use_tool with no input at all, then read ONE
#                  stdin line: a deny gets a rejected tool_result and a result;
#                  EOF gets "stream closed". Models a frame nexen cannot show.
#   nostdin        (PR-B R2) a request whose input is FAKE_PERM_BIG_BYTES (default
#                  1.5 MB, over the pipe's capacity) of "x", then NEVER read stdin
#                  again: a decision written to it blocks once the pipe is full.
#   cancelwatch    (PR-B R2) request; once the file named by FAKE_PERM_TRIGGER
#                  exists, a control_cancel_request; reads ONE stdin line like ask
#                  (allow -> tool_result, then result). Lets a test order the CLI's
#                  withdrawal against a host write that is parked.
#   postresult     (PR-C) request, read ONE stdin line, then result; afterwards
#                  every further stdin line is reported as a system/post_result_line
#                  event and EOF as system/eof_seen, then exit. Shows whether stdin
#                  is still open after the result, and when it closes.
#   bg             (PR-C) a background task: init, assistant tool_use, task_started,
#                  a non-empty background_tasks_changed, the FIRST result, and only
#                  then a control_request (carrying agent_id). Reads ONE stdin line
#                  (EOF => a "stream closed" tool_result, which is what a host that
#                  closed stdin at the first result gets). Then task_updated
#                  {completed}, task_notification, an empty background_tasks_changed,
#                  tool_result, the SECOND result, and blocks on stdin to EOF.
#   bg_noend       (PR-C) like bg, but the task's end frames never come: only the
#                  empty background_tasks_changed snapshot and the second result.
#   interrupt      request, then read stdin; an interrupt control_request
#                  produces the cancel, the interrupt ack and the aborted result;
#                  a decision is answered as in ask (see above).
MODE=${FAKE_PERM_MODE:-ask}
SID=995fb62d-4a59-4f35-af79-3b978d39ff8c
RID=e89aa38c-d064-4ea8-a9a0-fdb3f73f0143
TUID=toolu_01DGFR79wzyGL63u1dHNiKUc

# frames is stream_permission_request.jsonl: init, the assistant's tool_use,
# and the can_use_tool control_request.
frames() {
  cat <<'JSONL'
{"type":"system","subtype":"init","cwd":"/tmp/scratch","session_id":"995fb62d-4a59-4f35-af79-3b978d39ff8c","tools":["Bash","Read","Write"],"mcp_servers":[],"model":"claude-haiku-4-5-20251001","permissionMode":"acceptEdits","slash_commands":["deep-research","design","design-sync","dataviz","update-config","verify","debug","code-review","simplify","batch","fewer-permission-prompts","doctor","loop","schedule","claude-api","workflow-authoring","run","run-skill-generator","plugin-authoring","advisor","agents","auto-mode-setup","autocompact","clear","color","compact","config","output-style","context","effort","fast","focus","heapdump","init","mcp","import","model","__remote-workflow","workflow-launch-exec","reload-plugins","reload-skills","rename","ultrareview","security-review","usage-credits","extra-usage","usage","insights","recap","skill-doctor","goal","design-consent","design-revoke","list-agents","team-onboarding"],"terminal_slash_commands":["doctor","color","focus","reload-plugins"],"apiKeySource":"none","claude_code_version":"2.1.291","output_style":"default","agents":["claude","Explore","general-purpose","Plan","statusline-setup"],"skills":["deep-research","design","design-sync","dataviz","update-config","verify","debug","code-review","simplify","batch","fewer-permission-prompts","doctor","loop","schedule","claude-api","workflow-authoring","run","run-skill-generator","plugin-authoring"],"plugins":[{"name":"cc-plugin-agents-md","path":"builtin","source":"cc-plugin-agents-md@builtin"},{"name":"cc-plugin-telemetry","path":"builtin","source":"cc-plugin-telemetry@builtin"},{"name":"cc-plugin-plugin-authoring","path":"builtin","source":"cc-plugin-plugin-authoring@builtin"}],"capabilities":["interrupt_receipt_v1","interrupt_cancel_queued_v1","interrupt_send_now_v1","msg_lifecycle_v1","sdk_mcp_tools_list_changed","sdk_mcp_manifests","mcp_read_resource_v1","mcp_tool_ui_meta_v1","ui_surface_v1"],"analytics_disabled":false,"product_feedback_disabled":false,"uuid":"b2db149c-a29a-4906-bb5d-4a9e408238db","memory_paths":{"auto":"/tmp/scratch/.claude/projects/scratch/memory/"},"messaging_socket_path":"/tmp/cc-socks/0.sock","fast_mode_state":"off","fast_mode_disabled_reason":"sdk_opt_in_required","per_turn_effort_active":false,"view_mode":"default"}
{"type":"assistant","message":{"model":"claude-haiku-4-5-20251001","id":"msg_011CfmJ5s6QM6mbLfP3UpeGW","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01DGFR79wzyGL63u1dHNiKUc","name":"Bash","input":{"command":"python3 -c \"open('probe-E2.txt','w').write('ok')\"","description":"Run Python command to create probe-E2.txt file"},"caller":{"type":"direct"}}],"container":null,"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":10,"cache_creation_input_tokens":11609,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":11609},"output_tokens":4,"service_tier":"standard","inference_geo":"not_available"},"input_transformations":[],"diagnostics":null,"context_management":null},"parent_tool_use_id":null,"session_id":"995fb62d-4a59-4f35-af79-3b978d39ff8c","uuid":"31681aa4-23de-4946-a45f-1820721eabcb","timestamp":"2026-10-06T16:25:37.747Z","request_id":"req_011CfmJ5rtkpxit61yYfRhGk","wire_tool_inputs":{"toolu_01DGFR79wzyGL63u1dHNiKUc":{"command":"python3 -c \"open('probe-E2.txt','w').write('ok')\"","description":"Run Python command to create probe-E2.txt file"}}}
{"type":"control_request","request_id":"e89aa38c-d064-4ea8-a9a0-fdb3f73f0143","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"python3 -c \"open('probe-E2.txt','w').write('ok')\"","description":"Run Python command to create probe-E2.txt file"},"description":"Run Python command to create probe-E2.txt file","permission_suggestions":[{"type":"addRules","rules":[{"toolName":"Bash","ruleContent":"python3 -c \"open('probe-E2.txt','w').write('ok')\""}],"behavior":"allow","destination":"localSettings"}],"decision_reason":"This command requires approval","decision_reason_type":"other","tool_use_id":"toolu_01DGFR79wzyGL63u1dHNiKUc"}}
JSONL
}

# log appends one line to FAKE_PERM_LOG, when set.
log() {
  if [ -n "$FAKE_PERM_LOG" ]; then printf '%s\n' "$*" >>"$FAKE_PERM_LOG"; fi
}

result() { # $1 = stop_reason
  printf '{"type":"result","subtype":"success","is_error":false,"stop_reason":"%s","session_id":"%s","total_cost_usd":0.01,"usage":{"input_tokens":1,"output_tokens":1}}\n' "$1" "$SID"
}
tool_result_ok() {
  log "tool_ran $TUID"
  printf '{"type":"user","message":{"role":"user","content":[{"tool_use_id":"%s","type":"tool_result","content":"(Bash completed with no output)","is_error":false}]},"parent_tool_use_id":null,"session_id":"%s"}\n' "$TUID" "$SID"
}
tool_result_rejected() { # $1 = content
  printf '{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"%s","is_error":true,"tool_use_id":"%s"}]},"parent_tool_use_id":null,"session_id":"%s","tool_use_result":"User rejected tool use","tool_result_meta":[{"id":"%s","non_execution_kind":"%s"}]}\n' "$1" "$TUID" "$SID" "$TUID" "$2"
}
aborted() {
  printf '{"type":"result","subtype":"error_during_execution","is_error":false,"stop_reason":"tool_use","terminal_reason":"aborted_tools","session_id":"%s","total_cost_usd":0.005,"usage":{"input_tokens":1,"output_tokens":1}}\n' "$SID"
}

# read, not head: head may swallow a second line that is already in the pipe.
IFS= read -r _first
if [ "$MODE" = malformed ]; then
  frames | sed -n 1p
  printf '{"type":"control_request","request_id":"%s","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"%s"}}\n' "$RID" "$TUID"
  if IFS= read -r line; then
    case "$line" in
      *'"behavior":"deny"'*) tool_result_rejected "denied" permission-rule ;;
      *) tool_result_rejected "unexpected control_response" permission-rule ;;
    esac
  else
    tool_result_rejected "stream closed" stream-closed
  fi
  result end_turn
  cat >/dev/null
  exit 0
fi
if [ "$MODE" = nostdin ]; then
  frames | sed -n 1,2p
  printf '{"type":"control_request","request_id":"%s","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"' "$RID"
  head -c "${FAKE_PERM_BIG_BYTES:-1500000}" /dev/zero | tr '\0' x
  printf '"},"tool_use_id":"%s"}}\n' "$TUID"
  exec sleep 60
fi
# FAKE_PERM_RID (PR-C) makes this turn raise a different request id, so a test
# can run a second turn against the same execution (the table's primary key is
# (execution, request id)).
if [ -n "$FAKE_PERM_RID" ]; then
  RID_FIXTURE=$RID
  RID=$FAKE_PERM_RID
  frames | sed "s/$RID_FIXTURE/$RID/g"
else
  if [ "$MODE" = bg ] || [ "$MODE" = bg_noend ]; then
  BGTASK=bgtask01
  frames | sed -n 1,2p
  printf '{"type":"system","subtype":"task_started","task_id":"%s","tool_use_id":"%s","description":"background work","is_backgrounded":true,"task_type":"local_bash","session_id":"%s"}\n' "$BGTASK" "$TUID" "$SID"
  printf '{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"%s","task_type":"local_bash","description":"background work"}],"session_id":"%s"}\n' "$BGTASK" "$SID"
  result end_turn
  printf '{"type":"control_request","request_id":"%s","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"echo from-background"},"tool_use_id":"%s","agent_id":"a8fbe6ba3e2e8d1c6"}}\n' "$RID" "$TUID"
  if IFS= read -r line; then
    case "$line" in
      *'"behavior":"allow"'*) tool_result_ok ;;
      *) tool_result_rejected "denied" permission-rule ;;
    esac
  else
    tool_result_rejected "stream closed" stream-closed
  fi
  if [ "$MODE" = bg ]; then
    printf '{"type":"system","subtype":"task_updated","task_id":"%s","patch":{"status":"completed","end_time":1790500509042},"session_id":"%s"}\n' "$BGTASK" "$SID"
    printf '{"type":"system","subtype":"task_notification","task_id":"%s","tool_use_id":"%s","status":"completed","summary":"background work done","session_id":"%s"}\n' "$BGTASK" "$TUID" "$SID"
  fi
  printf '{"type":"system","subtype":"background_tasks_changed","tasks":[],"session_id":"%s"}\n' "$SID"
  result end_turn
  cat >/dev/null
  exit 0
fi
frames
fi
log "asked $RID"

case "$MODE" in
  ask)
    if IFS= read -r line; then
      case "$line" in
        *'"behavior":"allow"'*|*'"behavior": "allow"'*) tool_result_ok ;;
        *'"behavior":"deny"'*|*'"behavior": "deny"'*)
          msg=$(printf '%s' "$line" | sed -n 's/.*"message": *"\([^"]*\)".*/\1/p')
          tool_result_rejected "$msg" permission-rule ;;
        *) tool_result_rejected "unexpected control_response" permission-rule ;;
      esac
    else
      tool_result_rejected "stream closed" stream-closed
    fi
    result end_turn
    cat >/dev/null
    ;;
  multi)
    RID2=cdc18807-3f3e-4ed5-85ab-4f92c417bcf2
    TUID2=toolu_01DW69nH34THqoRE7UmRwpqR
    printf '{"type":"control_request","request_id":"%s","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"echo second"},"tool_use_id":"%s","agent_id":"a8fbe6ba3e2e8d1c6"}}\n' "$RID2" "$TUID2"
    n=0
    while [ "$n" -lt 2 ] && IFS= read -r line; do
      n=$((n+1))
      rid=$(printf '%s' "$line" | sed -n 's/.*"request_id": *"\([^"]*\)".*/\1/p')
      if [ "$rid" = "$RID2" ]; then TUID=$TUID2; else TUID=toolu_01DGFR79wzyGL63u1dHNiKUc; fi
      case "$line" in
        *'"behavior":"allow"'*) tool_result_ok ;;
        *) tool_result_rejected "denied" permission-rule ;;
      esac
    done
    result end_turn
    cat >/dev/null
    ;;
  postresult)
    IFS= read -r _decision
    tool_result_ok
    result end_turn
    while IFS= read -r _line; do
      printf '{"type":"system","subtype":"post_result_line","session_id":"%s"}\n' "$SID"
    done
    printf '{"type":"system","subtype":"eof_seen","session_id":"%s"}\n' "$SID"
    ;;
  cancelwatch)
    ( while [ ! -e "$FAKE_PERM_TRIGGER" ]; do sleep 0.02; done
      printf '{"type":"control_cancel_request","request_id":"%s"}\n' "$RID" ) &
    if IFS= read -r line; then
      case "$line" in
        *'"behavior":"allow"'*) tool_result_ok ;;
        *) tool_result_rejected "denied" permission-rule ;;
      esac
    fi
    result end_turn
    cat >/dev/null
    ;;
  cancel)
    printf '{"type":"control_cancel_request","request_id":"%s"}\n' "$RID"
    tool_result_rejected "The user doesn't want to proceed with this tool use." user-rejected
    aborted
    cat >/dev/null
    ;;
  interrupt)
    while IFS= read -r line; do
      case "$line" in
        *'"subtype":"interrupt"'*|*'"subtype": "interrupt"'*)
          printf '{"type":"control_cancel_request","request_id":"%s"}\n' "$RID"
          printf '{"type":"control_response","response":{"subtype":"success","request_id":"int-1","response":{"still_queued":[]}}}\n'
          tool_result_rejected "The user doesn't want to proceed with this tool use." user-rejected
          aborted
          ;;
        *'"behavior":"allow"'*|*'"behavior": "allow"'*)
          tool_result_ok
          result end_turn
          ;;
        *'"behavior":"deny"'*|*'"behavior": "deny"'*)
          tool_result_rejected "denied" permission-rule
          result end_turn
          ;;
      esac
    done
    ;;
esac
