# K-1 — daemon submit by typing (shared by Mac and iOS) — plan

Spec: `docs/specs/2026-10-11-keystroke-submit-spec.md`. Two PRs: **K-1a** submit, **K-1b** recall. Daemon + mod (the mod change
ships with the daemon: deploy runs `pdx setup`, which installs the plugin).

## K-1a submit
1. **promptq job kind `fill`** (internal/promptq): `Job{ID, Kind:"fill", SessionID, Text}`; outcomes `accepted` (filled) or
   `dropped` with reason `input_not_empty | dialog_open | no_composer | fill_mismatch | session_changed | refused`. Unlike
   `submit`, a fill is allowed mid-turn (that is the point). Ledger / owner-stream rules as for submit (single owner stream,
   at-most-once per client_msg_id, 10 s lease).
2. **mod `pqRun` for `fill`** (cmd/pdx/plugin/purdex/hooks/events.js): session check as today; `$.prompt.read()` → non-empty text →
   `input_not_empty`; `$.prompt.fill({text, mode:'replace'})` → `refusal:'dialog'` → `dialog_open`, `'no_composer'` → `no_composer`;
   `!isFilled` or returned text ≠ ours → try `fill({text:'', mode:'replace'})` to clean up, then `fill_mismatch`; else `accepted`.
   Tests in events.test.ts with a fake `$` (each branch).
3. **Daemon route `POST /api/sessions/{code}/submit`** (internal/module/session, next to send-keys; device-allowed like send-keys —
   add to cmd/pdx/device_scope.go): body `{text, client_msg_id, expected_tmux_instance}`.
   Order: per-session-code mutex → ledger (seen id → recorded outcome) → sanitise (move the iOS rules into the daemon: tabs→4 spaces,
   control chars except \n stripped, trim, ≤4000 bytes, leading `/` or `!` → `409 needs_terminal`, trailing `@token` padded) →
   session exists, generation guard → agent is Claude Code and not `waiting` (agent status; `409 waiting`) → resolve the pane's
   Claude Code session id (provenance / frame) and its mod owner (promptq Owners):
   - **owner present (mode `fill`)**: enqueue `fill`, wait its outcome (≤ 8 s); `accepted` → send Enter with
     `tmux.SendKeysIfInstance(code, instance, "Enter")` (same generation guard) → `{outcome:"handed", mode:"fill"}`; a dropped fill →
     `409 {reason}`; a timeout → `{outcome:"unknown"}` (nothing typed by us after that point; never retried).
   - **no owner (mode `keys`)**: today's iOS behaviour moved server-side: literal text (LF for newlines) then Enter, each under the
     generation guard → `{outcome:"handed", mode:"keys"}`.
   Ledger records the outcome per client_msg_id (bounded, e.g. 1000 entries / 10 min).
4. **Tests**: sanitiser table (iOS cases); ledger replay; mutex serialises two submits; waiting → 409; generation mismatch → 409;
   owner path with a fake promptq: accepted → Enter sent once, each dropped reason → 409 and no Enter, timeout → unknown and no
   Enter; no-owner keys path (tmux test server `-L <label>`, `unset TMUX`; never a bare kill-server); device scope allows the route.

## K-1b recall
`POST /api/sessions/{code}/recall {expected_tmux_instance}` under the same mutex: requires `header.queued` (convfeed entry of the
pane's sid) non-empty; mod job `read` must be empty; tmux `Up`; job `read` → text; it must equal the queued user texts joined the
way Claude Code joins popped items (measure the separator first on a throwaway Haiku session) else `409 recall_mismatch` and the
input is left as is; job `fill('')` clears it; returns `{text}`. A turn ending in between (queue already empty when read) →
`409 already_sent`. Mod jobs `read` / `clear` added like `fill`. Tests as above.

## Out of scope
SPA (K-2) and iOS (K-3) switching to the route; the iOS 3-second buffer question (user).

## Acceptance (real machine, before K-2 / K-3 merge)
Busy session: a submit queues in Claude Code (header.queued shows it) and is read mid-turn; idle: runs at once; a terminal draft →
`input_not_empty`, nothing changed; an open permission dialog → `dialog_open`, nothing typed; recall returns the queued text into the
deck and empties the TUI input.
