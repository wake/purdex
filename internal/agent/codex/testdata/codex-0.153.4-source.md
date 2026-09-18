# codex-cli 0.153.4 Fixture Provenance

Frozen hook-payload fixtures for the codex 0.153.4 catalog (issue #1159, spec
`docs/specs/2026-09-18-codex-hooks-catalog-spec.md`). Layout mirrors
`internal/agent/opencode/testdata/opencode-1.14.23-*`.

- **CLI**: `codex --version` → `codex-cli 0.153.4` (npm `@openai/codex`; no source commit pinned,
  see `commitShaNote` in the manifest)
- **Docs**: <https://developers.openai.com/codex/hooks>, fetched 2026-09-18 — 12 hook events
  (`SessionStart, SessionEnd, SubagentStart, SubagentStop, PreToolUse, PermissionRequest,
  PostToolUse, PreCompact, PostCompact, UserPromptSubmit, Stop, Interrupt`). The same 12 rows
  appear in the interactive `/hooks` table of 0.153.4.
- **Catalog**: `codex-0.153.4-events.json` — per-event kind (installable / ignored / retired) and
  the status `DeriveStatus` must produce for the payload fixture
- **Manifest**: `codex-0.153.4-manifest.json` — points at `codex-0.153.4-payloads/` via
  `payloadFixtureDir`
- **Captured**: 2026-09-18 on mlab (macOS, tmux), by the Purdex daemon's own `pdx hook --agent codex`
  command and by project-level hooks (see below)

## Provenance classification

| Tag | Meaning |
|-----|---------|
| `runtime-trace` | Captured from a real codex-cli 0.153.4 run — the exact JSON codex piped into the hook's stdin, then scrubbed per the table below |

All ten fixtures are `runtime-trace`. There are no source-derived fixtures.

## Sources

### 1. Daemon trace store (7 fixtures)

The daemon records every accepted hook event; the raw stdin payload is kept as
`agent_trace_chains.root_payload_json` → `.raw_event` (the spec's original pointer to
`agent_trace_steps.payload_json` is empty for codex triggers — measured 2026-09-18). Read-only query,
newest row per event:

```sql
-- sqlite3 "file:$HOME/.config/pdx/agent_events.db?mode=ro"
select json_extract(root_payload_json, '$.raw_event')
from agent_trace_chains
where root_agent_type = 'codex' and root_event_name = '<PurdexName>'
order by started_at desc limit 1;
```

Events: `PdxSessionStart`, `PdxUserPromptSubmit`, `PdxPreToolUse`, `PdxStop`, `PdxSessionEnd`,
`PdxSubagentStart`, `PdxSubagentStop`. These sessions ran with `permission_mode = bypassPermissions`,
which is why `PdxPermissionRequest` had **zero** rows in the store and had to be captured live.

### 2. Live capture (3 fixtures)

`~/.codex/hooks.json` and `~/.codex/config.toml` were **not** edited. A throw-away git repository
under the session scratch directory carried a project-level `.codex/hooks.json` whose commands were
`cat >> <scratch>/<Event>.log` for `PermissionRequest`, `PostToolUse`, `Interrupt` (codex trusts the
directory and the three hooks on first start; that adds a `[projects."<scratch>"]` trust entry to
`config.toml`, written by codex itself). Then, in a tmux window:

1. `codex -a on-request -s read-only` (0.153.4 accepts only `on-request` / `never`; there is no
   `untrusted`), prompt: *Run exactly this shell command and nothing else: `echo hi >
   /tmp/codex-capture-probe.txt`* → the read-only sandbox forces an approval prompt →
   `PermissionRequest.log`; approve → command runs → `PostToolUse.log`.
2. Prompt: *Count from 1 to 800, one number per line* and press **Ctrl-C** while it streams →
   `Interrupt.log`.
3. `/quit`. Each log holds exactly one JSON object.

Startup of this run also printed `⚠ clamping Interrupt hook timeout to 3s` for the project hooks
(timeout 5 in the throw-away file) — the measurement behind the installer's `Interrupt → 3 s` entry.

## Scrub rules

Applied with `jq -S` (`with_entries`) to every fixture; keys are kept, values replaced, key order sorted.

| Key | Replacement |
|-----|-------------|
| `session_id` | `01a00000-0000-7000-8000-000000000001` |
| `turn_id` | `01a00000-0000-7000-8000-000000000002` |
| `agent_id` | `01a00000-0000-7000-8000-000000000003` |
| `tool_use_id` | `call_example0001` |
| `transcript_path` | `/Users/example/.codex/sessions/2026/09/18/rollout-example.jsonl` |
| `agent_transcript_path` | `/Users/example/.codex/sessions/2026/09/18/rollout-example-agent.jsonl` |
| `cwd` | `/Users/example/project` |
| `tool_input` | `{"command":"echo hi"}` |
| `tool_response` (string in 0.153.4) | `"hi\n"` |
| `last_assistant_message` | `ok` |
| `prompt` | `say hi` |
| `model`, `permission_mode`, `source`, `reason`, `stop_hook_active`, `agent_type`, `hook_event_name` | kept as captured |

`TestCodexPayloadFixtures_DeriveStatusContract` refuses any fixture containing `/Users/wake`.

## Fixture-by-fixture

| Fixture | Class | Source | Keys `DeriveStatus` reads |
|---------|-------|--------|---------------------------|
| `PdxSessionStart.json` | runtime-trace | trace store | `session_id`, `cwd` (detail) |
| `PdxUserPromptSubmit.json` | runtime-trace | trace store | — (status only) |
| `PdxPreToolUse.json` | runtime-trace | trace store | — (detail-only, Valid=true); `turn_id` read by frame_ops |
| `PdxPermissionRequest.json` | runtime-trace | live | `tool_name` (detail) |
| `PdxPostToolUse.json` | runtime-trace | live | `tool_name` (detail); `turn_id` read by frame_ops |
| `PdxStop.json` | runtime-trace | trace store | — (status only); `turn_id` read by frame_ops |
| `PdxInterrupt.json` | runtime-trace | live | `turn_id` (detail); `turn_id` read by frame_ops |
| `PdxSessionEnd.json` | runtime-trace | trace store | — (status only) |
| `PdxSubagentStart.json` | runtime-trace | trace store | `agent_id` (detail) |
| `PdxSubagentStop.json` | runtime-trace | trace store | `agent_id` (detail) |

Retired (`Notification`, `StopFailure`) and ignored (`PreCompact`, `PostCompact`) entries have no
payload fixture: codex never fires the first two, and the daemon never installs the last two.
