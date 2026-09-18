# Plan — P-C.2: per-host executions list store + sidebar Executions view

- Spec: `2026-09-18-pc-launch-ui-spec.md` v1.1 §4.3, §5 (P-C.2), §6.2.
- Worktree `.claude/worktrees/pc-launch-ui`, branch `worktree-pc-launch-ui`
  (now at alpha.380, P-C.1 merged). One PR.
- Every task: subagent, TDD, one commit with `git commit --only <files>`
  (new files `git add <exact path>` first). Every Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pc-launch-ui/spa && `.
  Verify per task: targeted `npx vitest run`, `npx eslint <touched>`,
  `npx tsc --noEmit -p tsconfig.app.json`; before the PR the full gates.
- **Tasks 1→2→3→4 sequential.** No daemon or Nexen change; `nex-sse.ts`,
  `useExecutionStore`, `useExecutionSubscription` untouched except where
  task 1 names them.

## Measured baseline (2026-09-18, alpha.380)

- `spa/src/lib/nex/subscription-slots.ts` 64 lines, 5 tests:
  `MAX_LIVE_SUBSCRIPTIONS_PER_HOST = 4` is a module constant used by
  `touch` (evicts while `size > cap`) and `claimIfFree` (`size >= cap`).
  Consumers: `hooks/useExecutionSubscription.ts` (32 tests; calls
  `touch`/`claimIfFree`/`release`/`isLive`/`onEvict`).
- `spa/src/components/hosts/nex/NexExecutionsTable.tsx` 307 lines, 15 tests:
  local `items` state; `refetch(forHostId, includeArchived)` with a request
  token + host guard (92–110); site-wide SSE effect `openNexSse({hostId, url:
  '/api/nex/v1/events', getLastEventId, onFrame → scheduleRefetch
  (LIST_REFRESH_DEBOUNCE_MS = 500), onStatus: reconnecting→open →
  refetch})` (116–161); the `includeArchived` toggle re-queries with
  `includeArchived: true` and later SSE/action refetches honour the current
  toggle; gated by `enabled` prop (from
  `isNexReady(info)` in `NexHostSection`); actions terminate/archive with
  their own post-action refetch.
- `spa/src/components/hosts/nex/NexExecutionRow.tsx` 139 lines:
  file-local `STATE_DOT_CLASSES` (19–26), `shortId`, `firstLine(text, max=80)`,
  `cwdBasename`.
- `spa/src/lib/module-registry.ts:30-44` `ViewProps {hostId?, workspaceId?,
  tabId?, isActive, region?}`, `ViewDefinition {id, label, icon, scope,
  component}`; `registerModule({views})`. `register-modules/index.tsx:227-231`
  registers module `execution` with one pane and no views;
  `:284-299` shows the two file-tree views.
- `spa/src/components/SidebarRegion.tsx` 192 lines, 11 tests: renders the
  active view with `hostId = activeHostId ?? hostOrder[0]`; `RegionManager`
  lists `useLayoutStore.regions[region].views` and lets the user add any
  registered view.
- `useTabStore.openSingletonTab(content)` (`stores/useTabStore.ts:504-522`).
- `lib/deeplink/deeplinkResolver.ts:18-21 openExecutionDetailTab(id, hostId)`.
- `stores/useNexHostStore.ts` (P-C.1): `ensure`, `selectReady(hostId)`,
  entries `byHost[hostId].{info, capabilities, phase}`.
- `ExecutionSummary` has `origin?`, `labels` (always an object), `state`,
  `brief`, `updated_at` (unix ms), `archived`.
- Locales `en.json`/`zh-TW.json`; `locale-completeness.test.ts`.

## Task 1 — reserved lane in `subscription-slots`

Files: `lib/nex/subscription-slots.ts` + test; `hooks/useExecutionSubscription.test.ts`
(+1 integration test; the hook itself needs no change — it only calls the
registry).

- `reserve(hostId, tag)` / `unreserve(hostId, tag)`; **only the tag
  `'site-wide'` is accepted** (any other tag throws — the budget has exactly
  one reserved lane per host; a floor of 1 is not needed because the
  effective cap is `MAX_LIVE_SUBSCRIPTIONS_PER_HOST - (reserved ? 1 : 0)` =
  3 or 4). `capFor(hostId)` exported. A shared private `evictToCap(hostId)`
  runs the LRU eviction loop (fires `onEvict`) and is used by both `touch`
  and `reserve`; `claimIfFree` compares against `capFor`. `resetForTests`
  clears reservations.
- Tests (named): reserve drops cap 4→3 and evicts the LRU live key with its
  `onEvict` fired; `claimIfFree` refuses the 4th while reserved; `unreserve`
  restores 4 and a new `claimIfFree` succeeds; double reserve counts once;
  reserving with 2 live keys evicts nothing; unknown tag throws.
  `useExecutionSubscription.test.ts`: `a site-wide reservation evicts the
  pane LRU and a later inactive claim cannot exceed three` (four active
  panes → reserve → one paused; a fifth inactive-at-mount pane stays paused).
- Commit: `feat(nex): subscription slots reserve a per-host site-wide lane (P-C.2 task 1)`.

## Task 2 — `useExecutionListStore` + `useHostExecutions(hostId)`

Files: new `stores/useExecutionListStore.ts` + test, new
`hooks/useHostExecutions.ts` + test, new `lib/nex/state-dot.ts` (moved,
exported `STATE_DOT_CLASSES`) + `NexExecutionRow.tsx` import,
`lib/host-lifecycle.ts` + test (cascade line), `stores/useNexHostStore.ts`
(the identity watcher also notifies this store — see below).

Design (codex §review 1–4): **runtime ownership and cache are separate.**

```ts
interface HostListCache { items: ExecutionSummary[]; phase: 'idle'|'loading'|'ready'|'error'; error: string|null; lastSeq: number|null }
interface HostListRuntime { subscribers: Set<string>; generation: number; sse: NexSseHandle|null; reserved: boolean; debounce: ReturnType<typeof setTimeout>|null; fetchToken: number }
```
`byHost: Record<hostId, HostListCache>` is the store state (rendered);
runtime lives in a module-level `Map<hostId, HostListRuntime>` (never
rendered).

- `subscribe(hostId): () => void` returns an **idempotent** unsubscribe
  bound to a unique token; adding the first token calls `open(hostId)`;
  removing the last calls `close(hostId)` but **keeps the cache** (instant
  re-display on the next subscribe).
- `open(hostId)`: only when `isNexReady(useNexHostStore.byHost[hostId]?.info)`
  (the **info** readiness — not the capabilities-complete `phase`, so the
  list gates exactly like today's table `enabled`); `reserve(hostId,
  'site-wide')` **before** `openNexSse`; then `fetch(hostId)`. When not
  ready: cache `phase: 'idle'`, nothing opened; the store's own
  `useNexHostStore.subscribe` watcher opens when readiness flips true while
  `subscribers.size > 0`, and calls `close` when it flips false.
- `close(hostId, {dropCache})`: `generation += 1`, clear debounce, close the
  SSE handle, `unreserve` (only if `reserved`), optionally reset the cache
  to `{items: [], phase: 'idle', error: null, lastSeq: null}`.
- **Terminal SSE close** (`onStatus('closed', err)` with an error, e.g.
  401/403/`nex_unavailable`): treat as `close(hostId, {dropCache: false})`
  with the cache `phase: 'error'`; the lane is released immediately;
  reopening happens only via readiness transition, explicit `refetch`, or
  a new subscribe (each path goes through `open`, which reserves first).
  `unreserve` is guarded by `reserved` so cleanup never double-releases.
- `fetch(hostId)`: captures `generation`, `fetchToken`, the host
  fingerprint from `useNexHostStore` (`ip:port:token`) and commits only if
  all still match **and** the host is still info-ready **and**
  `subscribers.size > 0`. Body: `listExecutions(hostId, {includeArchived:
  false, limit: 100})`.
- SSE (moved from `NexExecutionsTable.tsx:116-161`): `url:
  '/api/nex/v1/events'`, `getLastEventId: () => cache.lastSeq`, durable
  frame ids advance `lastSeq`, every frame → 500 ms debounced `fetch`;
  reconnecting→open → `fetch`. All timers/handles are per host in the
  runtime map (two hosts never share).
- **Host identity change / removal**: `useNexHostStore`'s identity watcher
  gains a hook (`onHostIdentityChanged(hostId)` callback list, or this
  store subscribes to the same `useHostStore` transition) → `close(hostId,
  {dropCache: true})`; readiness re-establishment reopens for the surviving
  subscribers. `clearHost(hostId)` (host-lifecycle cascade) = `close(…,
  {dropCache: true})` + delete the cache entry; runtime tokens are kept so a
  keep-tabs undo that re-adds the host reopens exactly once when readiness
  returns.
- `refetch(hostId)` public: if runtime has an SSE → `fetch`; if it was
  terminally closed → `open`.
- `useHostExecutions(hostId)`: `useEffect` subscribe/unsubscribe (calls
  `useNexHostStore.ensure(hostId)` first); returns `{items, phase, error,
  refetch, refreshRevision}` — `refreshRevision` is a per-host counter the
  store bumps every time a refresh cycle runs (SSE debounce, reconnect,
  explicit refetch), consumed by the table's archived query (task 3).

Tests (named, in `useExecutionListStore.test.ts` unless noted):
- `two subscribers share one SSE and one reservation; the last unsubscribe closes and unreserves but keeps the rows`
- `unsubscribe is idempotent per token`
- `frames are coalesced into one debounced refetch`
- `a durable frame id advances lastSeq and is replayed as Last-Event-ID on reconnect`
- `reconnecting then open refetches`
- `not info-ready: subscribe opens nothing; readiness flipping true opens exactly once`
- `readiness turning false invalidates an in-flight fetch and pending debounce` (fetch resolves after the flip → no commit; timer fires → no fetch)
- `host fingerprint change drops cached rows and reconnects only after the new host is ready`
- `clearHost while subscribed drops data, releases the lane, and later readiness reopens exactly once`
- `terminal SSE close releases the reserved lane and does not double-unreserve on cleanup`
- `stale fetch after generation bump is ignored`
- `two hosts maintain independent SSEs, reservations, cursors, debounces, and teardown`
- `refreshRevision increments per refresh cycle`
- `useHostExecutions.test.ts`: mount subscribes + ensures; unmount unsubscribes; re-mount shows cached rows immediately.
- `host-lifecycle.test.ts`: `deleteHostCascade clears the execution-list host while preserving the other host`.
- Commit: `feat(nex): per-host execution list store with one shared site-wide SSE (P-C.2 task 2)`.

## Task 3 — `NexExecutionsTable` on the shared store

Files: `NexExecutionsTable.tsx` + test (256 lines / 15 tests).

- Decision (codex §review 7): **table-local archived query.** The shared
  store holds non-archived rows only. When `includeArchived` is on, the
  table runs its own guarded `listExecutions(hostId, {includeArchived:
  true, limit: 100})` keyed on `[hostId, includeArchived,
  refreshRevision]` (so SSE frames, reconnects and post-action refreshes —
  which all bump the revision — refresh the archived view too); when off,
  it renders the shared `items` and issues no second query. Actions call
  the shared `refetch` (which bumps the revision).
- The `enabled` prop keeps gating on `isNexReady(info)` from
  `NexHostSection` as today (the store's own gate is the same predicate, so
  the table appears no later than before).
- Tests: the 15 existing tests stay green (those that mocked `openNexSse`
  directly now assert through the store's mocked SSE — report the diff
  stat); **DOM preservation**: one test snapshots the table's rows/toggle/
  actions DOM with a fixed dataset before and after the change (capture
  the snapshot in a first commit, keep it green after); new named tests:
  `archived toggle on: frame, reconnect, archive and terminate refresh the
  archived query`; `archived toggle off: no second query`; `table and
  sidebar mounted together open one site-wide SSE per host` (integration,
  can live in `ExecutionsView.test.tsx` after task 4 — note it there).
- Commit: `refactor(nex): NexExecutionsTable reads the shared execution list store (P-C.2 task 3)`.

## Task 4 — `ExecutionsView` + registration

Files: new `components/executions/ExecutionsView.tsx` (+ `ExecutionsGroup.tsx`
if > 150 lines) + tests, new `lib/nex/format.ts` (`firstLine`, `shortId`
moved from `NexExecutionRow.tsx`, exported) , new `lib/nex/relative-age.ts`
(+ test), `register-modules/index.tsx`, locales, `SidebarRegion.test.tsx`.

- `ViewDefinition {id: 'executions', label: 'Executions', icon: Lightning,
  scope: 'system', component: ExecutionsView}` on the `execution` module.
  Registration **does not** touch `useLayoutStore.regions[*].views`.
- `ExecutionsView({hostId, isActive})`: `useNexHostStore.ensure(hostId)` +
  `useHostExecutions(hostId)`; header = host name + nex phase dot;
  `disabled`/`unavailable` → reuse `newtab.headless.disabled/unavailable`;
  list newest-first by `updated_at`, grouped by `labels.source ?? 'local'`
  (`executions.group.local`, `executions.group.purdex`, other values raw);
  row = state dot (`STATE_DOT_CLASSES`), `firstLine(brief)`, relative age
  (`executions.age.just_now|minutes|hours|days`, boundaries 60 s / 60 min /
  24 h), `↩` marker (with `title` = the session code) only when `origin`
  starts with `purdex://host/${hostId}/session/` for **this** host; click →
  `openSingletonTab({kind: 'execution', executionId, host: hostId})`;
  `executions.empty`; loading skeleton; error line with a `refetch` button.
  The view never reads `HostInfo.nex` itself.
- Tests (named): header shows host name + phase dot; only `includeArchived:
  false, limit: 100` is requested; grouping order and labels (local /
  purdex i18n, unknown raw); state dot class per state; brief single line
  with ellipsis; relative age boundaries; marker only for same-host
  `/session/` origins (other host's origin → no marker); click opens the
  singleton tab; disabled / unavailable / empty / error+retry; `table and
  sidebar mounted together open one site-wide SSE per host`;
  `registration does not mutate any region's configured views`.
  `SidebarRegion.test.tsx` +2: region without the view configured renders
  no Executions; configured → renders with the active host id.
- Commit: `feat(exec): Executions sidebar view per host (P-C.2 task 4)`.

## Task 5 — PR

Full gates; PR "feat(exec): Executions sidebar view + shared execution list
store (P-C.2)"; codex R1 (`--model gpt-5.6-sol`, no `--effort`) then R2
attack → critic serial (templates in
`~/.claude/skills/codex-dispatch/SKILL.md`); acceptance spec §6.2 on mlab
(connection budget: 4 execution panes + sidebar → one pane paused, REST
still prompt; one `/v1/events` without `execution_id` per host per tab
even with Host → Nex open).

## Review log

- codex `task-mu6lptei-rhye4d` (gpt-5.6-sol), 11 findings, all applied:
  (1) P1 repoint shows old rows → identity watcher drops cache + reconnects
  after readiness; (2) P1 `clearHost` vs live subscribers → runtime
  ownership separated from cache, idempotent token unsubscribe; (3) P1
  readiness flip vs in-flight fetch → generation + readiness + ownership
  checked at commit, debounce cleared; (4) P1 terminal SSE close leaks the
  lane → unreserve on terminal close, guarded; (5) P2 shared `evictToCap`,
  single `'site-wide'` tag, hook integration test; (6) P2 two-host
  isolation test; (7) P2 archived: table-local query keyed on
  `refreshRevision`; (8) P2 host-lifecycle files listed in task 2; (9) P2
  §4.3 UI rules each named; (10) P3 baseline line numbers (fetch 92–110,
  SSE effect 116–161); (11) P3 DOM-preservation tests for table and
  regions; the list gate uses `isNexReady(info)` so the table appears no
  later than before.
