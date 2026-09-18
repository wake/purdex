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
  token + host guard (94–113); site-wide SSE `openNexSse({hostId, url:
  '/api/nex/v1/events', getLastEventId, onFrame → scheduleRefetch
  (LIST_REFRESH_DEBOUNCE_MS = 500), onStatus: reconnecting→open →
  refetch})` (120–160); gated by `enabled` prop (from
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

## Task 1 — reserved slot in `subscription-slots`

Files: `lib/nex/subscription-slots.ts` + test; `hooks/useExecutionSubscription.ts`
only if it reads the constant directly (measure; expected: it calls the
registry, no change).

- `reserve(hostId, tag: string): void` / `unreserve(hostId, tag)`; a
  host's effective cap = `MAX_LIVE_SUBSCRIPTIONS_PER_HOST - reservedCount`
  (never below 1). `touch` evicts down to the effective cap immediately on
  `reserve` (call the eviction loop inside `reserve` so listeners fire);
  `claimIfFree` uses the effective cap; `capFor(hostId)` exported for tests.
  `resetForTests` clears reservations.
- Tests (named): reserve drops cap 4→3 and evicts the LRU live key with
  its `onEvict` fired; `claimIfFree` refuses the 4th while reserved;
  `unreserve` restores 4 and a new `claimIfFree` succeeds; double reserve
  with the same tag counts once; reserving on a host with 2 live keys
  evicts nothing.
- Commit: `feat(nex): subscription slots can reserve a per-host connection lane (P-C.2 task 1)`.

## Task 2 — `useExecutionListStore` + `useHostExecutions(hostId)`

Files: new `stores/useExecutionListStore.ts` + test, new
`hooks/useHostExecutions.ts` + test, `lib/nex/state-dot.ts` (moved
`STATE_DOT_CLASSES`, exported) + `NexExecutionRow.tsx` import.

- Store per host: `{items: ExecutionSummary[], phase: 'idle' | 'loading' |
  'ready' | 'error', error: string | null, lastSeq: number | null,
  subscribers: number}` + actions `subscribe(hostId): () => void`
  (refcount; first subscriber opens the site-wide SSE, reserves the slot,
  and fetches; last unsubscribe closes the SSE, unreserves, and keeps the
  items for instant re-display), `refetch(hostId)`, `clearHost(hostId)`
  (wired into `host-lifecycle.ts` cascade). Fetch = `listExecutions(hostId,
  {includeArchived: false, limit: 100})` with the same request-token +
  host guard as the table. SSE logic moved verbatim from
  `NexExecutionsTable.tsx:120-160` (500 ms debounce, reconnect refetch,
  `lastSeq` from durable frame ids). Gate: subscribing while
  `useNexHostStore.selectReady(hostId)` is false sets `phase: 'idle'` and
  opens nothing; a store watcher (inside the existing
  `startNexHostInvalidation`-style pattern, or a `useNexHostStore.subscribe`
  in this store's module) opens the stream when readiness flips true while
  subscribers > 0, and closes it when readiness flips false.
- `useHostExecutions(hostId)`: `useEffect` subscribe/unsubscribe; returns
  `{items, phase, error, refetch}` via selectors.
- Tests: refcount (two subscribers → one `openNexSse`, one `reserve`; last
  unsubscribe closes + unreserves); frame → debounced single refetch; durable
  frame id advances `lastSeq` and is passed as `Last-Event-ID` on reconnect
  (`getLastEventId`); reconnecting→open refetch; readiness false → no SSE,
  flips true → SSE opens; `clearHost` closes + drops; stale fetch (host
  switched / token superseded) ignored; hook unmount unsubscribes.
- Commit: `feat(nex): per-host execution list store with one shared site-wide SSE (P-C.2 task 2)`.

## Task 3 — `NexExecutionsTable` on the shared store (pure behaviour move)

Files: `NexExecutionsTable.tsx` + test.

- Remove its local list/SSE code; consume `useHostExecutions(hostId)` when
  `enabled`; the `includeArchived` toggle needs archived rows → keep a
  table-local `listExecutions(…, {includeArchived: true})` fetch **only**
  when the toggle is on (the store holds non-archived), or extend the store
  with an `includeArchived` per-host flag — pick the smaller change that
  keeps the 15 tests green; report which. Post-action refetch → `refetch`.
- Tests: the 15 existing tests stay green; ones that mocked `openNexSse`
  directly may need to seed/spy the store instead — report the diff stat.
- Commit: `refactor(nex): NexExecutionsTable reads the shared execution list store (P-C.2 task 3)`.

## Task 4 — `ExecutionsView` + registration

Files: new `components/executions/ExecutionsView.tsx` (+ `ExecutionsGroup.tsx`
if > 150 lines) + tests, `register-modules/index.tsx`, locales.

- `ViewDefinition {id: 'executions', label: 'Executions', icon: Lightning,
  scope: 'system', component: ExecutionsView}` on the `execution` module.
- `ExecutionsView({hostId, isActive})`: `useNexHostStore.ensure(hostId)` +
  `useHostExecutions(hostId)`; header = host name + phase dot;
  `disabled`/`unavailable` → the `newtab.headless.disabled/unavailable`
  strings (reuse keys); list newest-first by `updated_at`, grouped by
  `labels.source ?? 'local'` (headers `executions.group.local`,
  `executions.group.purdex`, other values raw); row = state dot
  (`STATE_DOT_CLASSES`), `firstLine(brief)` (move `firstLine`/`shortId`
  to `lib/nex/format.ts`, exported, used by both), relative age
  (`executions.age.*` keys: `just_now`, `minutes`, `hours`, `days`), `↩`
  marker with `title` when `origin` starts with
  `purdex://host/${hostId}/session/`; click → `openSingletonTab({kind:
  'execution', executionId, host: hostId})`; `executions.empty` when no
  items; loading skeleton; error line with `refetch` button.
- Tests: grouping and order; marker; click opens singleton tab (spy
  `useTabStore.getState().openSingletonTab`); disabled/unavailable/empty/
  error; `SidebarRegion.test.tsx` +1: the view is registered and renders
  with the active host id.
- Commit: `feat(exec): Executions sidebar view per host (P-C.2 task 4)`.

## Task 5 — PR

Full gates; PR "feat(exec): Executions sidebar view + shared execution list
store (P-C.2)"; codex R1 (`--model gpt-5.6-sol`) then R2 attack → critic
serial (templates in `~/.claude/skills/codex-dispatch/SKILL.md`); acceptance
spec §6.2 on mlab (connection budget check: 4 execution panes + sidebar,
REST still prompt; one `/v1/events` without `execution_id` per host per tab).
