# Interface language U1-3 — lights v2 (SPA) — plan

Spec: `docs/specs/2026-10-08-interface-u1-spec.md` §2 (**N5**, **N6**), **§7 (lights v2 contract, "SPA (U1-3)" and "U1-3 notes")**, §9 (current-state map). Daemon side: `docs/plans/2026-10-08-interface-u1-2-plan.md` (b-2 emit slot, b-3 snapshot), shipped in alpha.612. Cross-line precedent: `docs/specs/2026-10-08-worker-status-deltas-design.md` §3.5 (the `nex.*` `(epoch, bseq)` cursor, SPA side merged in #1964 / #1965).

Format as the U1-2 plan: contracts, rules, named tests and mutation gates; implementers write the code (TDD: each named test is written red first). Line numbers are as of `84f1d1e5`; re-check before editing.

Three PRs, each ≤ 800 lines diff / ≤ 20 files (estimates include tests; a PR that grows past the limit splits again before review):

| PR | Content | Depends on | Est. lines |
|---|---|---|---|
| **U1-3a** | store: wire types, one unread rule for the hook channel (transition only), `applyHookEvent` / `applyAgentSnapshot`; the WS `hook` branch switches to `applyHookEvent` | — | ~420 |
| **U1-3b** | wire: `?agent=v2`, the per-connection hook cursor, the `agent.snapshot` branch, resync on a gap | a | ~530 |
| **U1-3c** | render: tab light over **all** panes, the N6 corner symbol, workspace indicators over all panes | a | ~600 |

a → b → c in order (one member). Each PR is safe to deploy on its own: a alone already stops the same-status unread noise; b alone orders frames and replaces state on reconnect; c alone renders what the store holds.

Common rules:
- SPA: `cd spa && npx vitest run <affected files>` during development; **the full vitest only once before merge, `--maxWorkers=3`, after asking the coordinator (purdex-1f) for the slot**; `pnpm run lint`; `npx tsc -p tsconfig.app.json --noEmit`; `pnpm run build` before the PR. No daemon change in U1-3.
- Each task one commit; parallel subagents in one worktree commit with `git commit --only <files>`.
- No new tab-hosted component (CLAUDE.md checklist does not apply); no persisted state (the cursor is runtime-only).

---

## Lead rulings (2026-10-09, purdex-88)

1. **One unread rule for the hook channel; the worker projection keeps its own.** `handleNormalizedEvent` stays as it is for `useWorkerAgentProjection` (it deliberately dispatches same-status events: a new pending request while already `waiting`, a new turn — `useWorkerAgentProjection.ts` `signatureOf` ~:303). Daemon `hook` frames (live and snapshot) go through new actions with the **transition rule**: unread is set only when the code's previous status is **known and different** and the new status is actionable and not visible (rule table below). This covers the six U1-3 notes and replaces the replay stopgap (`useAgentStore.ts` ~:298).
2. **Snapshot unread = the same transition rule** (refines §7 "replayed state never sets unread"): a snapshot entry never marks a code the SPA had no status for, or whose status is unchanged — the reconnect flood (§12.1 #11) stays fixed — but a status that changed while the SPA was disconnected (running → idle) is a real transition the person has not seen and marks unread, as today's stopgap already does. The plan PR rewrites the §7 sentence.
3. **Legacy frames.** A `hook` frame whose `value` has neither `epoch` nor `seq` comes from a daemon before alpha.611 (e.g. air26): it is applied immediately with the transition rule (no cursor, no snapshot). A daemon at exactly alpha.611 (stamps `seq`, sends no `agent.snapshot`) is not supported — its lights would freeze until an upgrade; 611 ran only on mlab and is superseded (documented, no fallback timer).
4. **What a snapshot replaces.** The host's codes listed in the snapshot are applied; a code of that host that is **absent** from the snapshot is cleared exactly like a `clear` frame (`clearSession`) **only if** the store holds a status or a `lastEvents` entry for it **and** its code does not start with `exec-` (execution panes are owned by the worker projection, not by the daemon's hook frames; daemon hook codes are a 6-char tmux code or `cc-<session_id>`, `internal/module/agent/nontmux.go:18`). A plain shell session (OSC title only, no agent) is untouched.
5. **Background symbol** is read from `lastEvents[key].background` — no new store map (host-lifecycle's snapshot/restore of `lastEvents` already carries it). Absent or `""` = none.
6. **Tab aggregation (N5)**: a tab's light is the highest priority over every agent pane of its layout (error > waiting > running > idle); unread and "awaiting approval" are OR over panes; agent icon, subagent dots come from the **representative pane** (highest priority; tie → the primary pane, then layout leaf order); the corner symbol is the highest background across panes (workflow > monitor > schedule), mirroring the daemon's per-tmux-session rule (U1-2b-1). Workspace indicators count over the same pane set.
7. **Corner symbol tooltip** names the one kind shown. The design doc's "hover lists all three" needs the daemon to send every active kind (today `background` is one value) → follow-up issue (daemon `background_kinds` + SPA tooltip), not U1-3.
8. **A gap or a foreign epoch on a live frame → resync** through the same path a health-check recovery takes (rule below); the hook frames until the new connection's snapshot are dropped. The `nex.*` cursor is untouched; the two families never share state.

---

## U1-3a — store: wire types and the transition rule

Files: `spa/src/stores/useAgentStore.ts`, `spa/src/hooks/useMultiHostEventWs.ts` (one call), tests.

### Types

`NormalizedEvent` (`useAgentStore.ts` ~:57) gains, all optional (legacy daemons and the worker projection omit them):

```ts
background?: '' | 'workflow' | 'monitor' | 'schedule'
source?: 'mod' | 'hook'
epoch?: string
seq?: number
snapshot?: boolean
```

`export type BackgroundKind = 'workflow' | 'monitor' | 'schedule'` and `export function backgroundOf(e?: NormalizedEvent): BackgroundKind | undefined` (unknown strings → undefined, forward-compatible).

### Actions

Refactor the body of `handleNormalizedEvent` into an internal `applyEvent(state, hostId, code, event, unreadPolicy)` that returns the partial state; three entry points share it so every side effect stays identical (exit record, `clear` → `clearSession`, `lastEvents`, `agentTypes`, provenance record / `flagUnverifiedAgent` on `replay`, model kept when empty, `subagents` presence rule, status):

| entry point | caller | unread policy |
|---|---|---|
| `handleNormalizedEvent(hostId, code, event)` (unchanged signature) | worker projection, tests | **today's** rule, unchanged, including the replay stopgap |
| `applyHookEvent(hostId, code, event)` (new) | WS `hook` frames (a: every one; b: those the cursor accepts, and legacy ones) | **transition** |
| `applyAgentSnapshot(hostId, entries: {session: string, event: NormalizedEvent}[])` (new) | WS `agent.snapshot` (b) | **transition** per entry, then ruling 4's clear, **in one `set`** (one render) |

**Transition rule** (`prev` = `statuses[key]` before the event):

| new status | unread |
|---|---|
| `running` | delete (as today) |
| `waiting`, `error` | set iff `prev !== undefined && prev !== status` and not `isAgentVisibleInActiveTab` |
| `idle` | as `waiting`, and additionally not a `Notification` / `PdxNotification` event and not `detail.notification_silent === true` (today's exclusions) |
| `clear` | `clearSession` (as today) |

### Tests (`useAgentStore.test.ts`, new describe blocks; existing ones stay green)

- `applyHookEvent — same status never marks unread`: idle → idle (representative / model change), idle → idle with only `background` changed, `running (hook)` → `running (mod)`, idle (hook) → idle (mod), 25 repeated subagent idles, a `PdxSubagentStop` idle on an idle code (the six U1-3 notes, one `it` each).
- `applyHookEvent — a real transition marks unread`: running → idle, running → waiting, waiting → error, idle → waiting; each not visible → unread; visible → not.
- `applyHookEvent — unknown previous status never marks unread` (first sight of a code, status idle / waiting / error).
- `applyHookEvent — Notification idle and silent Stop keep their exclusions`.
- `applyHookEvent — clear wipes the code` (same as `handleNormalizedEvent`).
- `applyHookEvent — model kept when empty`, `background stored in lastEvents`, `subagents presence rule unchanged`.
- `handleNormalizedEvent — worker same-status still marks unread` (pins ruling 1: waiting → waiting with a new request marks unread).
- `applyAgentSnapshot — replaces the host`: two listed codes applied, an absent code with a status cleared, an absent code with only an OSC title untouched, an absent `exec-…` code untouched, another host untouched; one `set` (subscribe counter = 1).
- `applyAgentSnapshot — unread`: unchanged status → no unread; first-seen code → no unread; known running → snapshot idle → unread (not visible); existing unread on an unchanged idle code is kept.
- `applyAgentSnapshot — replay side effects`: an entry without a provenance record calls `flagUnverifiedAgent` exactly as a `replay` hook frame does.
- `backgroundOf`: the three kinds, `""`, absent, an unknown string.

WS: the `hook` branch (`useMultiHostEventWs.ts` ~:166) calls `applyHookEvent` instead of `handleNormalizedEvent`; the provenance probes after it are unchanged. Test in a new `hooks/useMultiHostEventWs.agent-v2.test.ts` (pattern: `useMultiHostEventWs.nex-delta.test.ts`): `hook frame goes through applyHookEvent`.

Mutation gates: drop the `prev !== status` check → every "same status" test red; drop `prev !== undefined` → "unknown previous status" red; route the worker projection through the transition rule → "worker same-status still marks unread" red; clear absent `exec-` codes → snapshot "replaces the host" red.

---

## U1-3b — wire: `agent=v2`, the hook cursor, the snapshot

Files: new `spa/src/lib/agent-lights/hook-cursor.ts` (+ test), `spa/src/hooks/useMultiHostEventWs.ts`, `spa/src/lib/host-events.ts` (type union only), tests.

### Contract

- The host-events URL adds `agent=v2` next to `nex=v1` (`useMultiHostEventWs.ts` ~:123). The daemon makes the subscriber strict (it already is through `nex=v1`): a frame that does not fit ends the connection.
- `HostEvent['type']` gains `'agent.snapshot'`.
- Cursor per host, runtime only: `type HookCursor = { epoch: string; last: number } | null`. **Reset to `null`** on the connection's `onOpen`, on `onClose`, on the resync below, and deleted in the teardown next to `forgetHost` (~:115) and on host removal.
- Pure `decideHookFrame(cursor, value): 'legacy' | 'drop' | 'apply' | 'resync'`:

| condition (in order) | decision |
|---|---|
| `epoch` and `seq` both absent | `cursor === null` → `legacy`; else `drop` + `console.warn` (a v2 daemon always stamps both) |
| either present but malformed (`epoch` not a non-empty string, `seq` not a non-negative safe integer) | `drop` + `console.warn` |
| `cursor === null` | `drop` (before this connection's snapshot) |
| `epoch !== cursor.epoch` | `resync` (only a snapshot changes the epoch; a rotation at 2^53 lands here too) |
| `seq <= cursor.last` | `drop` |
| `seq !== cursor.last + 1` | `resync` |
| otherwise | `apply`, and the caller sets `cursor.last = seq` |

- `parseAgentSnapshot(value)`: `{epoch: non-empty string, seq: non-negative safe integer, sessions: array of {session: non-empty string, event: object}}`; anything else → `null` + `console.warn`, the frame is dropped and the cursor stays `null` (a daemon bug; lights for that host hold until the next connection — no reconnect loop). A valid snapshot → `applyAgentSnapshot(hostId, sessions)`, then `cursor = {epoch, last: seq}`, then the provenance probes for each entry's session (the loop that follows a `hook` frame today). A second snapshot on one connection is applied the same way.
- `hook` branch: `legacy` and `apply` → `applyHookEvent` (+ probes); `drop` → nothing; `resync` → **once per connection**: cursor `null`, then the recovery path — `connectionClosed(hostId)`, runtime `reconnecting`, `sm.trigger()` (the health check's success callback supersedes the socket with `reconnectWithTicket`, ~:146); a flag blocks a second resync until the next `onOpen`. Frames that keep arriving on the old socket are dropped (`cursor === null`), and the superseded socket's queued frames are already ignored (`host-events.ts` `socketEpoch`).
- The `agent.snapshot` frame has `session: ""`; it is dispatched by `type` before any session-based branch.

### Tests

`lib/agent-lights/hook-cursor.test.ts`: one `it` per table row (legacy with and without a cursor, malformed epoch / seq / negative / float / > 2^53, before snapshot, foreign epoch, duplicate, old, gap, next); `parseAgentSnapshot` valid / empty `sessions: []` with `seq: 0` / each malformed field.

`hooks/useMultiHostEventWs.agent-v2.test.ts`:
- `URL carries agent=v2 and nex=v1`.
- `hook frames before the snapshot are dropped` (a frame with `seq` arrives first, then the snapshot; the store reflects only the snapshot).
- `snapshot then contiguous frames apply in order`; `a duplicate seq is dropped`.
- `a gap triggers exactly one resync and nothing until the next snapshot` (two gaps in a row → one `sm.trigger`; frames after the gap not applied; after a new `onOpen` + snapshot, frames apply again).
- `a foreign epoch on a live frame resyncs`.
- `a legacy daemon (no epoch / seq, no snapshot) keeps working` (frames apply, transition rule).
- `onClose resets the cursor` (frames after a reconnect wait for the new snapshot).
- `malformed snapshot is dropped without a reconnect`.
- `empty snapshot clears the host's agent codes` (`sessions: []`, `seq: 0`).
- `nex cursor untouched` (an `nex.execution` delta still applies across a hook resync).

Mutation gates: apply frames while `cursor === null` → "before the snapshot" red; ignore the gap → "gap triggers exactly one resync" red; resync on every gap frame → same test red (count); reset nothing on `onClose` → "onClose resets" red; share the cursor with `nex` → "nex cursor untouched" red.

---

## U1-3c — render: all panes and the corner symbol

Files: new `spa/src/lib/agent-lights/tab-aggregate.ts` (+ test), `spa/src/hooks/useTabDisplay.ts`, `spa/src/hooks/useSessionAgentIndicator.ts`, new `spa/src/components/BackgroundSymbol.tsx` (+ test), `spa/src/components/TabIcon.tsx`, `spa/src/features/workspace/workspace-indicators.ts`, tests.

### Aggregation

- `tabAgentKeys(layout): string[]` — every leaf of the layout (`pane-tree.ts` `collectLeaves` ~:218) mapped through `paneAgentKey` (~:122) to a composite key, **primary pane first**, then leaf order, de-duplicated (two panes on one session code count once).
- Pure `aggregateTabAgents(keys, {statuses, unread, subagents, agentTypes, lastEvents}, awaitingByKey)` → `{status, isUnread, isAwaitingApproval, repKey, background}` per ruling 6. Rank: error 4 > waiting 3 > running 2 > idle 1 > none 0 — **one shared rank** used by this and by `workspace-indicators.ts` `STATUS_PRIORITY` (~:19, same order; move it to `lib/agent-lights/status-rank.ts`, keep `aggregateStatus` behaviour).
- `useTabDisplay` (~:52, primary pane only today) and `useSessionAgentIndicator` take the key list; selectors return primitives / a shallow-compared tuple (`useShallow`) so a frame for an unrelated session does not re-render every tab. The existing awaiting-approval source (worker pending request) is evaluated per pane key and OR-ed.
- `getWorkspaceCompositeKeys` (`workspace-indicators.ts`, primary pane today) uses `tabAgentKeys` for every tab; `unreadCount` stays "tabs with unread" (a tab with two unread panes counts once).

### Corner symbol (N6)

- `BackgroundSymbol({kind})`: Phosphor `TreeStructure` (workflow) / `Eye` (monitor) / `Clock` (schedule); static (no animation); colour = the icon's `currentColor`; size small relative to the 16 px icon box (start at 8 px, tune by screenshot); `title` / `aria-label` names the kind: 「Workflow 執行中」／「Monitor 監看中」／「排程喚醒」(follow the SPA's existing string mechanism for tab tooltips).
- `TabIcon` gets `background?: BackgroundKind`. Placement: **top-left of the agent icon** in the badge (default) style and in `iconDot` (on the icon, not the dot); **top-left of the dot** in the `dot` style; **not rendered** when lights are off (`icon` style, including the awaiting-approval exception). The main light, unread pip (top-right) and error diamond are unchanged; subagent dots stay where they are — if the symbol collides with the dots' arc (badge mode parks it at `left: -4`), the PR shows both options in screenshots and the lead picks.
- Inline tabs (`renderInlineTabIcon`) and `SortableTab` pass the same field.

### Tests

`tab-aggregate.test.ts`: `older waiting pane beats newer running pane`, `error beats waiting`, `tie → primary pane`, `unread is OR`, `awaiting is OR and forces waiting`, `background highest across panes`, `rep pane gives icon and dots`, `a split pane with the only agent represents the tab` (primary pane is a shell), `duplicate session code counted once`.
`useTabDisplay.test.ts`: the aggregate wired through (split tab with two agent panes); `an event for another session does not re-render the tab` (render counter).
`BackgroundSymbol.test.tsx` / `TabIcon.test.tsx`: the three icons; none for `undefined`; hidden in `icon` style (also with `awaitingApproval`); top-left in badge / `iconDot` (on the icon) / `dot`; no animation class.
`workspace-indicators` tests: a split tab's second pane counts; unread counted per tab.

Mutation gates: primary pane only → "older waiting pane beats" and "split pane with the only agent" red; background from the rep pane only → "background highest across panes" red; symbol rendered with lights off → hidden test red.

**Screenshot gate (before review):** `playwright cli` against the worktree's dev server, light and dark theme, active and inactive tab: 3 styles × {no dots, 3 dots} × {workflow, monitor, schedule}, plus a split tab. Saved in the member's scratchpad; the lead reviews them and shows the person before c merges (user-visible design).

---

## Acceptance (after c merges; the coordinator moves main / the SPA)

Script `accept-u1-3` (member scratchpad), following the U1-2 acceptance rules: a real tmux server session `acc-u13-<n>` created with `new-session -d`, killed only with `kill-session -t` of that name (**never `kill-server`, never a `TMUX_TMPDIR` trick**, `feedback_tmux_test_isolation`); a **Sonnet** `claude` in default permission mode (Haiku does not call `Monitor`); the SPA under `playwright cli -s=<worktree>` with its own session; tokens read into variables, never printed.

1. Load: the host connects with `agent=v2` (requests list), lights match `pdx peers` / the screen, **no unread anywhere** on first load.
2. With the test tab in the background: a Bash call needing permission → yellow + unread; approve → green (unread cleared); finish → grey + unread. A peer message to the session (`pdx msg send`) → it runs and ends → **one** unread at the end, none mid-turn.
3. `Monitor` → Eye top-left; `CronCreate` → Clock; both → Eye; `/exit` → gone. Records the **Monitor** check U1-2's gate left open.
4. Split tab: pane A idle, pane B waiting → the tab is yellow with B's icon.
5. Daemon restart (coordinator's window): after the reconnect no unread flood; a session that finished during the restart (running → idle) is unread; lights match.
6. Styles: `dot` → symbol at the dot's top-left; `icon` → no symbol.
Results and screenshots go into the PR / kickoff memory.

## Out of scope / follow-ups

- Hover listing every active background kind (needs daemon `background_kinds`) — issue (ruling 7).
- Multi-pane dots merge (spec §7 known limit) — unchanged.
- iOS (U2) consumes the same `agent=v2` contract; nothing here is App-specific beyond rendering.
