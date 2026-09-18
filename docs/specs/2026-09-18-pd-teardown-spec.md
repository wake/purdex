# Spec — P-D: tear down Stream mode, relay, bridge and M0

- Status: v1.2 (2026-09-18) — P-D.1 shipped (alpha.395); P-D.2 implementation notes folded in (orphaned `internal/history` deleted, `agent.SessionState` deleted with `StreamCapable`, `peer-proxy` is a hidden subcommand)
- v1.1: codex plan+spec review `task-mu6xgcjb-7b7n51` applied (11 findings: commit ordering, persist v3, snapshot/notification legacy paths, usage string, composed-mux 404 tests)
- Predecessors: P-C (`2026-09-18-pc-launch-ui-spec.md` v1.6) — its §6 passed on
  mlab (alpha.390); its "Successor" clause is the gate for this spec. Nexen
  integration decisions #3/#4 (kickoff `kickoff_nexen_into_purdex`): Stream
  mode / relay / bridge / M0 execution+dispatch are all removed; "-p ↔ tmux
  resume" goes through `nex-handoff` / `nex-takeback` + `claude --resume`,
  never through relay.
- Successor: none planned. Follow-ups that fall out of this spec are filed as
  issues, not folded in (§8).

## 1. Problem

Two generations of "run Claude Code headless from Purdex" coexist in the
tree:

- **Legacy** (2026-06/07): Stream mode (`internal/module/stream`, SPA
  `ConversationView` + `useStreamStore`), the relay process (`pdx relay`,
  `internal/relay`, `internal/bridge`), and M0 Ploom dispatch
  (`internal/module/execution`, `internal/module/dispatch`, alpha.326).
- **Current** (P-A → P-C): the nex module (`internal/module/nex`) driving
  Nexen, exec panes (`ConversationMessages`, `useExecutionStore`) and the
  `nex-handoff` / `nex-takeback` round trip.

The legacy generation has no users: M0 dispatch is dead code (Ploom's
integration moved to Nexen), the relay handoff was superseded by P-C.3 and
the SPA still ships a "Stream" view-mode dropdown that starts a code path
nobody maintains. Measured on `origin/main` at alpha.392:

| Go package | lines (`*.go`, incl. tests) | imported by |
|---|---|---|
| `internal/module/execution/` | 6059 | `dispatch`, `main.go` |
| `internal/module/dispatch/` | 4133 | `main.go` (imports `execution`, `stream`, `bridge`, `relay`) |
| `internal/module/stream/` | 2434 | `dispatch`, `main.go` |
| `internal/relay/` | 482 | `stream`, `dispatch`, `main.go` |
| `internal/bridge/` | 227 | `stream`, `dispatch` |

Total ≈ 13.3K Go lines, plus the SPA Stream family (14 non-test files
mention `'stream'`; 8 files import `lib/stream-ws.ts`).

## 2. Goals / non-goals

### Goals

- G1. Delete the five Go packages, the `pdx relay` subcommand, the
  `[stream]` and `[dispatch]` config sections and every daemon route that
  only they served.
- G2. Delete the SPA Stream family (store, relay WS manager, renderer,
  handoff button, view-mode switch) without touching exec-pane behaviour.
- G3. Keep the exec pane, `nex-handoff` / `nex-takeback`, and every
  non-stream session route byte-for-byte in behaviour. Pure moves are
  proved by per-declaration byte comparison, not by diffstat.
- G4. Ship in three PRs, each independently deployable, each followed by its
  own bump. Old persisted SPA tabs with `mode: 'stream'` must still load.

### Non-goals

- Removing the `mode` field from the daemon `Session` JSON / `session_meta`
  table, or from the SPA `PaneContent` union. P-D narrows it to the single
  value `terminal`; full removal is a follow-up (§8).
- Purging `cc_session_id` / `cc_model` / `has_relay` from the ~60 SPA test
  fixtures that spell them out. P-D.3 removes them from the `Session` type
  only if the mechanical fixture edit fits the PR budget; otherwise §8.
- Any change to Nexen, `internal/module/nex`, or the exec-pane UI.
- Migrating existing `session_meta` rows (alpha rule: no persist migration;
  extra columns with defaults are harmless).
- Deploying to air26 (default: mlab only; the user decides at the end).

## 3. Phases (one PR each)

The repo rule "≤ 800 lines / ≤ 20 files per PR" cannot hold for a 13K-line
deletion. **Pure-deletion PRs are split by file count of *edited* (not
deleted) files and by blast radius**, not by diff size: deleted packages
count as one unit each; the budget applies to the surviving files that are
edited to unwire them.

### P-D.1 — daemon M0 (execution + dispatch)

Safest first: nothing outside `main.go` imports these packages, and the
SPA has no M0 client.

Remove:
- `internal/module/execution/` (whole directory).
- `internal/module/dispatch/` (whole directory).
- `cmd/pdx/main.go`: the two `c.AddModule(execution.New())` /
  `c.AddModule(dispatch.New())` lines and their imports.
- `internal/config/config.go`: `DispatchConfig` type, the `Dispatch` field on
  `Config`, its `Clone` line and any default; associated cases in
  `config_test.go`. TOML files that still carry a `[dispatch]` table keep
  loading (`toml.Unmarshal` ignores unknown tables — verify with a test).

Mark, do not delete:
- `docs/specs/m0-ploom-handoff.md`: prepend a "Deprecated — superseded by
  Nexen; code removed in P-D.1" banner. `docs/specs/2026-07-19-m0-*.md` and
  `m0-contract.md` stay as history untouched.

### P-D.2 — daemon Stream / relay / bridge

Remove:
- `internal/module/stream/`, `internal/relay/`, `internal/bridge/` (whole
  directories). This removes `POST /api/sessions/{code}/handoff`,
  `/ws/cli-bridge/{code}`, `/ws/cli-bridge-sub/{code}` and the WS `handoff`
  event (the nex module's `handoffProfile = "handoff"` constant is an
  unrelated Nexen profile name and stays).
- `cmd/pdx/main.go`: `case "relay"` and `runRelay` (flag set + `relay.Relay`
  construction), `c.AddModule(stream.New())`, imports, **and the hand-written
  usage string at `main.go:44`** that lists `relay`.
- `internal/config/config.go`: `StreamConfig`, `Preset`, the `Stream` field,
  its default preset and `Clone` line; `config_test.go` / `peers_test.go`
  cases that build one. `[stream]` tables in existing TOML keep loading.
- `internal/module/session`:
  - `POST /api/sessions/{code}/mode` route + `handleSwitchMode` +
    `switchModeRequest`.
  - `handleCreate`: `mode` accepts `""`, `"terminal"` **and the legacy
    `"stream"`**, and always stores `terminal`; any other value is
    `400 invalid mode: must be terminal`. Coercion (not rejection) keeps
    old workspace snapshots and device-state backups restorable — their
    `SessionMeta.mode` may still say `stream` and `snapshot/restore.ts:80`
    forwards it verbatim until P-D.3 normalises it.
  - `SessionInfo`: drop `CCSessionID`, `CCModel`, `HasRelay` (`has_relay` is
    never set to true anywhere today — bridge is its only producer and no
    caller wires it). `MetaUpdate`: drop `CCSessionID`, `CCModel`.
    `Mode` stays (always `terminal`).
  - `locks.go`: doc comment no longer mentions the stream module.
- `internal/store/meta.go`: drop `CCSessionID` / `CCModel` from
  `SessionMeta` and `MetaUpdate`, and the `cc_session_id` / `cc_model`
  columns from `CREATE TABLE` and every statement. Existing databases keep
  the columns (`DEFAULT ''`), so inserts that omit them still succeed —
  covered by a test that opens a DB created with the old schema.
- `internal/module/agent`: `GET /api/sessions/{code}/history` route +
  `handleHistory`; `agent.HistoryProvider` interface; `cc/history.go`,
  `CCHistoryProvider`, `HistoryKey` and its `registry.Register`;
  `agent.StreamCapable` and its only referent `agent.SessionState`;
  `internal/history/` (`CCProjectPath`, `ParseJSONL` — `cc/history.go` was
  its only reader). Rationale:
  the endpoint's only SPA caller is the `handoff` WS branch that P-D.2
  silences, and its session-id source (`cc_session_id`) has no writer once
  stream is gone. Rebuilding it on provenance is out of scope (no consumer).
- `CLAUDE.md` overview: "支援 Terminal、Stream、JSONL 三種模式" →
  "Terminal（tmux）與 exec（Nexen headless execution）兩種".

SPA compatibility during the P-D.2 → P-D.3 window (same day, mlab only):
`config.stream?.presets` is read with optional chaining;
`session.cc_session_id` is only read by `handoff-gate.ts` as the third
fallback (`!!undefined` → false, and the first two sources are the live
ones); `has_relay` has no reader; `mode` keeps arriving as `terminal`;
snapshot / device-state restore keeps working because `handleCreate`
coerces `stream`. What *is* degraded in the window — all of it fixed by
P-D.3, none of it crashing: (a) choosing "Stream" in the view-mode dropdown
(`POST …/mode` → 404); (b) a notification click with `reopenTabOnClick`
creates a `mode: 'stream'` pane (`useNotificationDispatcher.ts:355`
hard-codes it); (c) any persisted `mode: 'stream'` pane renders
`ConversationView` (`SessionPaneContent.tsx:103`) whose `/ws/cli-bridge`
connect fails. P-D.2 acceptance opens one such pane and checks the
failure is a visible error state, not a white screen.

### P-D.3 — SPA Stream family

Order inside the PR matters; each bullet is its own commit and **every
commit must pass `tsc --noEmit -p tsconfig.app.json`** (or `tsc -b`; the
bare `tsc --noEmit` is a no-op because `spa/tsconfig.json` is
solution-style with `files: []`) — so consumers are unwired *before* the
files they import are deleted.

1. **Type move** (pure move, no codex): `StreamMessage`, `ContentBlock`,
   `AssistantMessage` and whatever else `lib/nex/event-reducer.ts`,
   `ConversationMessages.tsx`, `ToolUseBlock.tsx` and `useExecutionStore.ts`
   import from `lib/stream-ws.ts` move to `lib/nex/message-types.ts`. Proof:
   per-declaration byte comparison between the old and new file; imports
   are the only other edits.
2. Unwire: `App.tsx` (`useRelayWsManager()`, `handleViewModeChange`),
   `SessionPaneContent.tsx` (`handleHandoff`, `handleHandoffToTerm`, stream
   branch, `config.stream` read), `StatusBar.tsx` view-mode dropdown,
   `TabContextMenu.tsx` / `features/workspace/hooks.ts` `viewMode-*` items,
   `useTabStore.setViewMode`, `useMultiHostEventWs.ts` `handoff` branch,
   `host-lifecycle.ts` `streamStore.clearHost`, `host-api.ts`
   (`switchMode`, `fetchHistory`, `stream.presets` on the config type),
   `hosts/OverviewSection.tsx` presets field, `useNotificationDispatcher.ts`
   stream cases, `SessionPicker.tsx` / `SessionPanel.tsx` /
   `hosts/SessionsSection.tsx` stream icons, `snapshot/{types,capture}.ts`
   (`SessionMeta.mode` narrows to `'terminal'`; `restore.ts` and the
   device-state restore path pass `'terminal'` regardless of what an old
   snapshot says), `lib/route-utils.ts` (`stream` leaves the route union;
   `validateMode('/stream')` — an old deep link — resolves to terminal, with
   a test), `useNotificationDispatcher.ts:355` (`mode: 'terminal'`).
3. Delete `lib/stream-ws.ts` (remaining WS client code),
   `stores/useStreamStore.ts`, `hooks/useRelayWsManager.ts`,
   `components/ConversationView.tsx` (+ `ConversationView.snapshot.test.tsx`),
   `components/HandoffButton.tsx`, and their tests. Keep
   `MessageBubble` / `ThinkingBlock` / `ToolCallBlock` and their default-prop
   snapshots — they are exec-pane components.
4. `types/tab.ts`: `PaneContent` tmux-session `mode` narrows to the literal
   `'terminal'`. Persisted state carrying `mode: 'stream'` is normalised to
   `'terminal'` by **bumping the tab-store persist `version` from 2 to 3**
   and extending `migrateTabStore` (`useTabStore.ts:32`, today only
   `version < 2`) with a `< 3` step that walks every tab layout and rewrites
   tmux-session panes — a plain `merge`/rehydrate hook would not run for
   existing v2 blobs. Test: a v2 blob with one stream pane through
   `migrateTabStore(blob, 2)` → no throw, `mode === 'terminal'`, other
   panes untouched; plus the real-localStorage reload in §6. `PaneLayoutRenderer` /
   `SessionPaneContent` `key={pane.id}-${mode}` stays syntactically valid
   with the single value; remount behaviour is checked, not assumed.
5. `Session` type in `host-api.ts`: drop `cc_session_id`, `cc_model`,
   `has_relay` **only if** the fixture sweep stays mechanical (one sed, no
   hand edits); otherwise leave the fields optional and file the sweep
   under §8.

## 4. Interface contract after P-D

Daemon routes removed: `POST /api/sessions/{code}/handoff`,
`POST /api/sessions/{code}/mode`, `GET /api/sessions/{code}/history`,
`/ws/cli-bridge/{code}`, `/ws/cli-bridge-sub/{code}`, and every route the
execution / dispatch modules registered. Each removed route has a
composed-mux regression test in `cmd/pdx` (404 through the real
`registerServeModules` chain, not just the module's own mux). `pdx`
subcommands after P-D.2 (measured at `main.go:47-75`):
`serve hook setup token start stop status statusline-proxy peers msg nex
path version` plus the hidden `peer-proxy` (`relay` gone; the usage string
at `main.go:44` lists the non-hidden set).

`Session` JSON after P-D.2:

```json
{ "code": "...", "name": "...", "cwd": "...", "mode": "terminal",
  "tmux_instance": "...", "pane_title": "..." }
```

(`cc_session_id`, `cc_model`, `has_relay` gone; every other field unchanged.)

Config after P-D.2: no `[stream]`, no `[dispatch]`; unknown tables in an
existing `config.toml` are ignored, not rejected.

WS event `handoff` is never emitted. `agent.*` events unchanged.

## 5. Invariants

- I1. `internal/module/nex` `Dependencies()` stays `{"session","agent"}`;
  `nex/imports_test.go` boundary test keeps passing.
- I2. `session.HandoffLocksKey` stays in the session module (nex is now its
  only user; moving it is not P-D's job).
- I3. Exec-pane rendering is unchanged: `event-reducer.test.ts`,
  `ConversationMessages` tests and the P-B2 default-prop snapshots pass
  without snapshot updates.
- I4. Grep guard: no Go file imports `internal/module/stream`,
  `internal/relay`, `internal/bridge`, `internal/module/execution` or
  `internal/module/dispatch`; no SPA file imports `stream-ws`,
  `useStreamStore`, `useRelayWsManager`, `ConversationView` or
  `HandoffButton`. (The words themselves survive legitimately —
  `internal/terminal` relay, agent `probe_intent_dispatcher`, nex
  `handoffProfile`, Nexen "execution" — so the guard is on import paths,
  not vocabulary.)

## 6. Acceptance (per phase, run before the bump PR)

### Daemon phases (P-D.1, P-D.2)

1. `go build ./... && go vet ./... && go test ./...` green.
2. `make build` → `./bin/pdx stop` → `env PDX_DEV_MODE=1 ./bin/pdx start`;
   `GET /api/health` reports the new build hash.
3. `./bin/pdx` (no args / `help`) lists no `relay` (P-D.2).
4. The removed routes return 404 with a Bearer token; `GET /api/sessions`
   still lists live sessions with the shape in §4 (P-D.2 checks the three
   fields are absent).
5. Every commit on the branch builds (per-commit `go build ./...` check
   before opening the PR).
6. Existing `~/.config/pdx/config.toml` loads without error (daemon log
   shows no config warning; `GET /api/config` — or whatever the config
   route is — no longer returns `stream` / `dispatch`).
7. P-D.2 only: a scratch daemon opened against a copy of the live
   `meta.db` (old schema, extra columns) lists sessions and accepts
   `POST /api/sessions` — the test in §3 covers the store layer, this
   covers the wiring.
8. P-D.2 only: with the P-D.2 daemon and the pre-P-D.3 SPA, open one
   persisted `mode: 'stream'` pane — visible error state, no crash.
9. Real-machine: exec pane `Hand to nex` → typewriter → `Take back` once
   (P-C spec §6 steps 3–4, shortened), proving the nex path never depended
   on stream.

### SPA phase (P-D.3)

1. `cd spa && npx vitest run && pnpm run lint && pnpm run build` green;
   `npx tsc --noEmit -p tsconfig.app.json` green on every commit of the
   branch.
2. Grep guard (I4) on `spa/src`.
3. Main checkout `git pull --ff-only` + `pnpm install`; :5174 HMR picks it
   up.
4. Real-machine on :5174: (a) an existing terminal tab still renders;
   (b) a tab persisted before the change with `mode: 'stream'` (seed one in
   localStorage under the persist key before reloading) opens as a terminal;
   (c) right-click on a session pane shows `Hand to nex` and no
   view-mode / Stream items; (c') an old `/…/stream` deep link opens a
   terminal pane; (c'') a workspace snapshot captured before the change
   with a `mode: 'stream'` session restores as a terminal; (d) `Hand to nex` → typewriter → `Take back`
   once; (e) Host → Overview no longer shows "stream presets".

## 7. Review protocol

Per `feedback_codex_quota_saving_flow` (2026-09-18): plan reviewed together
with this spec in one `gpt-5.6-sol` round; each PR gets R1 → attacker →
critic serially; fixes re-reviewed as `--base <last reviewed sha>` only;
the P-D.3 type-move commit and every bump PR are not sent to codex (byte
comparison / `tsc` + vitest are the proof). Findings with confidence < 0.6
go to a "to verify" list and are reproduced before entering the table.

## 8. Follow-ups (file as issues when each phase merges)

- `mode` field removal end-to-end (daemon `Session.mode`, `session_meta.mode`,
  SPA `PaneContent.mode`, tab-store `c.mode === 'terminal'` guards).
- SPA test-fixture sweep for `cc_session_id` / `cc_model` / `has_relay` if
  P-D.3 step 5 leaves them optional.
- `session_meta` column drop (`cc_session_id`, `cc_model`) via a real
  migration once alpha rules are lifted.
- Archive memory files `project_stream_handoff_status`, `project_stream_gaps`.
- Existing follow-ups untouched by P-D: #1133, #1134, #1139, #1163, #1171.
