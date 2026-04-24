# Agent Dynamic Title Metadata Spec

- **Date**: 2026-04-24
- **Status**: Draft
- **Context**: Purdex currently mixes terminal OSC titles and CC statusline `session_name` in `useAgentStore.oscTitles`. Validation showed tmux `pane_title` is closer to mux-internal state, while forwarding `#{pane_title}` through `set-titles-string` has real terminal-parser risk. v1 uses tmux metadata as the source of truth.

## Scope

- Use tmux `pane_title` metadata as the SOT for agent dynamic titles.
- Add `SessionInfo.pane_title` and `SessionInfo.window_name`; populate `current_command` from active pane metadata.
- Hide the Host > Agents CC statusline installer/test UI without deleting backend statusline routes/code.
- Stop mirroring CC statusline `session_name` into title state.
- Split the old `Show agent dynamic title` setting into `Dynamic tab name` and `Show in status bar`.
- Add Host > Agents title integration status/install/remove for `allow-set-title` only.
- Add read-only per-agent dynamic-title capability rows under installed Claude/Codex/OpenCode cards.
- Open a GitHub issue after implementation asking whether the retained CC statusline backend should be removed or repurposed.

## Non-goals

- Do not remove `pdx statusline-proxy`, `/api/agent/cc/statusline/*`, or `ccStatus` store state.
- Do not rely on `set-titles` or `set-titles-string '#{pane_title}'`.
- Do not write Claude/Codex/OpenCode config from the per-agent capability rows.
- Do not redesign the terminal/status bar layout.

## Invariants

- Title resolution uses `session.pane_title` only; no terminal OSC fallback and no CC statusline fallback.
- Only agent-identified sessions use dynamic titles.
- Tab label and status bar title are independently gated.
- `setCcStatus()` stores `ccStatus` only and does not write `oscTitles`.
- `clearHostAgentStatus()` clears `ccStatus` only and does not delete `oscTitles`.
- Host/tmux integration install/remove manages only `allow-set-title`.

## Active Pane Metadata

For each live tmux session, Purdex resolves the session's current window and active pane using target `=<session_name>:`. The title shown in Purdex always comes from that active pane.

Required metadata:

- `session_id`
- `session_name`
- `window_id`
- `pane_id`
- `pane_title`
- `window_name`
- `pane_current_command`

Use `tmux display-message -p -t '=<session_name>:' '<format>'` per field rather than a tab-delimited `list-panes -a` parser. This avoids corrupting parsing when title/window text contains tabs, newlines, or control characters. Sanitize metadata strings before exposing them to SPA rendering.

## Tmux Title Integration

Endpoints:

- `GET /api/agent/title/status`
- `POST /api/agent/title/setup`

`GET` response shape:

```json
{
  "allow_set_title": true,
  "installed": true,
  "runtime_applied": true,
  "managed_config_path": "/Users/wake/.tmux.conf",
  "error": ""
}
```

`POST` body:

```json
{ "action": "install" }
```

or:

```json
{ "action": "remove" }
```

Managed config contract:

- Only edit the Purdex marker block in `~/.tmux.conf`.
- Marker block:
  - `# >>> purdex agent-title >>>`
  - `set -gw allow-set-title on`
  - `# <<< purdex agent-title <<<`
- Install creates/updates the marker block and runtime-applies `tmux set-window-option -g allow-set-title on`.
- Remove deletes only the marker block and does not force runtime rollback to off.
- Existing unmanaged `allow-set-title` lines are not rewritten.

## Per-agent Capability Rows

Rows are best-effort and read-only.

- Claude Code:
  - Check daemon environment `CLAUDE_CODE_DISABLE_TERMINAL_TITLE`.
  - `1` means disabled for daemon-launched sessions.
  - Otherwise show likely enabled, with a note that session-local env overrides are not guaranteed.
- Codex:
  - Best-effort parse `~/.codex/config.toml` for `terminal_title`.
  - Missing config means default title behavior.
  - `terminal_title = []` means disabled.
  - Non-empty value means configured.
  - Parse failure means unknown.
- OpenCode:
  - Show unknown / no documented persistent title toggle.

## Settings

Replace persisted `showOscTitle` with:

- `dynamicTabName`
- `showAgentTitleInStatusBar`

Migration:

- old `showOscTitle=true` -> both true
- old `showOscTitle=false` or missing -> both false
- bump UI settings persist version
- preserve the older agent-store import path from pre-v2 migration
- update sync preferences to the two new keys

## Lights Phase 2b Coordination

`lights-phase-2b` may touch `useTabDisplay.ts`, `useTabDisplay.test.ts`, `useAgentStore.ts`, and `internal/module/agent/handler.go`.

- Preserve `subagentCount` and future `subagentRefs` in `TabDisplayData`.
- Do not reintroduce `string[]` subagent fixtures.
- Only touch `setCcStatus()` and title-related tests in `useAgentStore.ts`.
- Keep title capability handlers near detect/config code and do not touch frame/proxy/sweep logic.
