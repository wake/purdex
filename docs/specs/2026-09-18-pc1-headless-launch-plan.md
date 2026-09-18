# Plan — P-C.1: nex host store + Headless section on NewTab

- Spec: `2026-09-18-pc-launch-ui-spec.md` v1.1 §4.1, §4.2, §5 (P-C.1).
- Worktree `.claude/worktrees/pc-launch-ui`, branch `worktree-pc-launch-ui`
  (base alpha.379). One PR for P-C.1; P-C.2 / P-C.3 get their own plans.
- Every task: subagent, TDD (failing test first), one commit with
  `git commit --only <files>` (new files: `git add <exact path>` first).
  Every Bash call prefixed with
  `cd /Users/wake/Workspace/wake/purdex/.claude/worktrees/pc-launch-ui/spa && `.
  Verify per task: `npx vitest run <touched test files>`, `npx eslint
  <touched files>`, `npx tsc --noEmit -p tsconfig.app.json`; before the PR:
  `npx vitest run && pnpm run lint && pnpm run build`.
- **Tasks 1→2→3→4→5 are sequential** (each builds on the previous module).
- No daemon or Nexen change. `nex-sse.ts`, `useExecutionStore`, the reducer
  are not edited.

## Measured baseline (2026-09-18, alpha.379)

- `spa/src/lib/nex/types.ts` 183 lines; `NexCapabilities` at 64–84 (typed
  subset + `[key: string]: unknown`); `ExecutionSummary` has `session_id?`
  but no `resume_session_id`.
- `spa/src/lib/nex/nex-api.ts` 143 lines: `nexFetch(hostId, path, init?)`
  at 24 (rejects `host_removed` for unknown hosts; network → `NexApiError(0,
  'network')`); `fetchNexCapabilities` 67; `fetchNexHost` 71;
  `listExecutions` 82; no `delegate`. Errors: `NexApiError(status, code)`
  from `types.ts`.
- `spa/src/lib/host-api.ts:59-68` `NexInfo {configured, mounted, ready,
  init_error, effective, restart_required?}`; **`fetchInfo(hostId)` at 179
  returns a raw `Response`** (there is no `fetchHostInfo`); callers do
  `.then(readJson)` — `useNexHostData.ts:94` shows the idiom. `hostFetch`
  for an unknown host id falls back to the active/first host
  (`useHostStore.ts:242`) — a store must guard identity itself.
- `spa/src/lib/new-tab-registry.ts:88-98`: `registerNewTabProviderSource`
  keeps sources in a separate map; `unregisterNewTabProvidersByModule` only
  removes static providers — **dynamic sources have no teardown today**
  (the sessions source is never unregistered).
- `SessionLauncher` keeps its form state component-local; **there is no
  persisted launcher-memory store to reuse** — task 4 creates one.
- `spa/src/components/hosts/nex/nex-ready.ts` (11 lines) `isNexReady(info)`.
- `spa/src/components/hosts/nex/useNexHostData.ts` 158 lines: returns
  `{phase: 'offline'|'failed'|'loading'|'ready', info, config, refreshError,
  retry, refresh, onConfigSaved}`; fetches `/api/info` (94, 129) and
  `/api/config`; `useNexHostData.test.ts` 101 lines / 4 tests.
- `spa/src/components/hosts/nex/NexEngineStatus.tsx` 178 lines: fetches
  `Promise.all([fetchNexHost, fetchNexCapabilities])` at 67–70 keyed on
  `${hostId}:${ready}:${tick}`; `NexEngineStatus.test.tsx` 141 lines / 12
  tests. `NexHostSection.tsx:17` page phases `offline|failed|loading|ready`
  come from `useNexHostData` — the page must keep those semantics (a
  capabilities 503 is a card-level degradation, never a page-level
  `failed`).
- `spa/src/lib/session-new-tab-providers.tsx` 70 lines — the per-host
  provider-source pattern (component cache per host, `subscribe` on host
  store + hydration, `isReady = hasHydrated()`, `ownsId` by prefix,
  `migrations`). Registered at `register-modules/index.tsx:350`.
- `spa/src/lib/new-tab-registry.ts` 176 lines: `NewTabProvider {id, label,
  labelParams?, icon, order, component, disabled?, disabledReason?,
  moduleId?}`, `NewTabProviderProps = {onSelect}`,
  `registerNewTabProviderSource`, `unregisterNewTabProvidersByModule`.
- `spa/src/components/session-launcher/SessionLauncher.tsx` 219 lines —
  the form pattern (liveness `isHostLive(hostId)` from `lib/host-live.ts`,
  `busy`/`error` state, `data-testid="launcher-error"`, i18n `launcher.*`).
- `spa/src/lib/host-lifecycle.ts:175-176` clear cascade calls
  `useExecutionStore.getState().clearHost(hostId)` then
  `useHostSettingsStore…clearHost`.
- `spa/src/stores/useNewTabLayoutStore.ts:223-240` auto-places unknown
  provider ids.
- Locales: `spa/src/locales/{en,zh-TW}.json`, key-set equality test
  `src/locales/locale-completeness.test.ts`.
- Nexen wire (F1/F2 in the spec): delegate body `{provider, brief,
  sandbox_profile, mounts: [{path, role: 'cwd', writable}], origin?, labels?,
  resume_session_id?}` → 200 `{id, state, reject_reason?,
  effective_profile?}`; capabilities extra fields `brief.max_bytes`,
  `origin.max_bytes`, `labels{…}`, `delegate.resume_session_id`.

## Task 1 — typed capabilities + `delegateExecution`

Files: `lib/nex/types.ts`, `lib/nex/nex-api.ts`, `lib/nex/nex-api.test.ts`,
`lib/nex/types.test.ts`.

- `NexCapabilities` gains optional typed fields: `brief?: {max_bytes:
  number}`, `origin?: {max_bytes: number}`, `labels?: {max_count,
  max_key_bytes, max_value_bytes, max_total_bytes, reserved_prefix}`,
  `delegate?: {resume_session_id?: boolean}`. `ExecutionSummary` gains
  `resume_session_id?: string`, `requested_profile?`, `effective_profile?`.
- `export interface DelegateRequest { brief: string; cwd: string; profile?:
  string; labels?: Record<string, string>; origin?: string; resume_session_id?:
  string }`, `export interface DelegateResult { id: string; state: string;
  reject_reason?: string; effective_profile?: string }`.
- `export async function delegateExecution(hostId, req, caps:
  Pick<NexCapabilities, 'delegate'> | null): Promise<DelegateResult>`:
  builds F2's body (`provider: 'claude'`, `mounts: [{path: req.cwd, role:
  'cwd', writable: true}]`, `sandbox_profile` only when `profile` given);
  `resume_session_id` is put on the wire only when `req.resume_session_id`
  is set **and** `caps?.delegate?.resume_session_id === true` — if set but
  unsupported → reject with `NexApiError(0, 'resume_unsupported')` before
  any fetch. Non-2xx → `NexApiError(status, code-from-body)`.
- Tests (named): body mapping for the minimal request; profile omitted when
  absent; labels/origin passthrough; `resume_session_id` sent when supported;
  dropped-with-error when unsupported (no fetch call); `caps === null` +
  resume → `resume_unsupported`; 200 `rejected` returned as data (not
  thrown); 400 `invalid_brief` and 400 `invalid_origin` → thrown
  `NexApiError(400, <code>)` (generic passthrough, no special-casing);
  503 `nex_unavailable`; unknown host → `host_removed`. Type test:
  capabilities JSON with the new fields parses into the typed shape.
- Commit: `feat(nex): delegateExecution + typed delegate/limit capabilities (P-C.1 task 1)`.

## Task 2 — `useNexHostStore`

Files: new `stores/useNexHostStore.ts` + `.test.ts`; `lib/host-lifecycle.ts`
(one line in the clear cascade) + its test.

- State per host: `{info: NexInfo | null, capabilities: NexCapabilities |
  null, phase: 'unknown' | 'loading' | 'ready' | 'disabled' | 'unavailable',
  error: string | null, fetchedAt: number, generation: number}`.
- **Invariant**: `phase === 'ready'` ⇔ `isNexReady(info) && capabilities !==
  null`. A refresh that fails the capabilities call (or returns
  `info.ready === false`) leaves `ready` — the reducer recomputes phase from
  the new data every commit; there is no "keep old ready" path.
- `ensure(hostId): Promise<void>` — no-op when `fetchedAt` is within
  `NEX_HOST_TTL_MS = 60_000` and phase ≠ `unavailable`; dedups an in-flight
  fetch per host (module-level `Map<string, Promise>`); sets `loading` only
  when there is no prior data; fetches `fetchInfo(hostId).then(readJson)` →
  `.nex` (guard: `useHostStore.getState().hosts[hostId]` must exist before
  the call and when committing — `hostFetch` silently falls back to another
  host for unknown ids, so the store never relies on it); when
  `isNexReady(nex)` also `fetchNexCapabilities(hostId)`; phase per spec
  §4.1. `NexApiError(503, 'nex_unavailable')`, network, `init_error` →
  `unavailable` with the message. Missing host → entry deleted, nothing
  fetched.
- **Generation token**: each entry carries `generation`; `ensure` captures
  it (and the host's `ip:port` fingerprint) before fetching and commits the
  result only if both still match; `invalidate`, `clearHost` and a host
  identity change bump it. A stale resolve is dropped, never resurrects an
  entry.
- `invalidate(hostId)` bumps `generation`, sets `fetchedAt = 0`, returns
  `ensure(hostId)`.
- **Reconnect invalidation**: `startNexHostInvalidation()` (module-level,
  called once from `register-modules` like other store watchers) subscribes
  to `useHostStore` and invalidates a host when `runtime[hostId].status`
  transitions into `connected`.
- Selectors: `selectReady(hostId)` (= phase ready, by the invariant),
  `selectHandoffReady(hostId)` (ready ∧ `delegate.resume_session_id === true`
  ∧ `sandbox_profiles.includes('handoff')`) — pure functions over state.
- `clearHost(hostId)`; wired into `host-lifecycle.ts` next to
  `useExecutionStore.getState().clearHost(hostId)`.
- Tests (named): phase matrix (configured=false → disabled; mounted=false →
  disabled; `info.ready` true but capabilities 503 → unavailable; both ok →
  ready; init_error → unavailable); invariant (first load with info ready
  and caps pending is **not** ready; a refresh whose caps call fails flips
  ready → unavailable); TTL (second `ensure` within 60 s makes no fetch;
  after → refetch); in-flight dedup (two concurrent `ensure` = one fetch);
  `invalidate` forces a fetch; **stale resolve after `clearHost` is
  ignored**; **stale resolve after the same host id is re-added is
  ignored**; **`invalidate` during an in-flight fetch drops that fetch's
  result and the fresh one wins**; unknown host → no fetch, no entry;
  reconnect transition → next `ensure` refetches; `selectHandoffReady`
  truth table; host-lifecycle cascade calls `clearHost` (extend the existing
  lifecycle test that asserts `useExecutionStore.clearHost`).
- Commit: `feat(nex): useNexHostStore — per-host readiness + capabilities cache (P-C.1 task 2)`.

## Task 3 — Host page reads the store

Files: `components/hosts/nex/NexEngineStatus.tsx` + test,
`components/hosts/nex/useNexHostData.ts` + test.

- `NexEngineStatus`: replace the local `Promise.all` for capabilities with
  `useNexHostStore` (capabilities via selector; `ensure(hostId)` in an
  effect keyed on `hostId`/`ready`/`tick`, where the Refresh tick calls
  `invalidate`). `fetchNexHost` (account/quota card) stays local — it is
  not part of readiness. Tests: the ones asserting `fetchNexCapabilities`
  calls move to asserting store state; Refresh → `invalidate`.
- `useNexHostData` (spec §4.1: one truth for `info`): it **stops fetching
  `/api/info`**; `info` comes from `useNexHostStore` (selector), and the
  hook calls `ensure(hostId)` on mount / reconnect. It keeps owning
  `/api/config`. Page phase mapping preserved for `NexHostSection`:
  `offline` = host runtime not connected (as today); `failed` = **config**
  load failed (as today) — a store `unavailable` (capabilities 503) is
  **not** page-level failed: `info` may be present with `ready: true` while
  the engine card degrades, exactly like today's `NexEngineStatus` catch
  path; `loading` = store has no entry yet or config pending; `ready` =
  store info present and config loaded. `onConfigSaved`, `retry`, `refresh`
  call `invalidate(hostId)`.
- Tests: `useNexHostData.test.ts` — no direct `/api/info` fetch any more
  (spy on `fetchInfo` asserts zero calls from the hook); a store `info`
  change re-renders the page phase; config load failure still yields
  `failed`; capabilities 503 with `info.ready` true keeps the page `ready`.
  `NexHostSection.test.tsx` (451 lines) is the DOM guard: it must stay
  green **unmodified except** for seeding the store instead of mocking
  `/api/info` — config form and executions table render under the same
  conditions as before.
- Commit: `refactor(nex): NexEngineStatus and useNexHostData read readiness from useNexHostStore (P-C.1 task 3)`.

## Task 4 — `HeadlessLauncher` form

Files: new `components/headless/HeadlessLauncher.tsx` + `.test.tsx`, new
`lib/nex/cwd-input.ts` + `.test.ts` (pure validation), new
`stores/useHeadlessLauncherMemoryStore.ts` (new, zustand `persist`, key
`purdex-headless-launcher`, `{byHost: Record<hostId, {root, profile}>}`,
`remember(hostId, v)` / `forgetHost(hostId)`; no existing store to reuse),
locales.

- `lib/nex/cwd-input.ts`: `validateSubPath(sub: string): {ok: true, value:
  string} | {ok: false, reason: 'absolute' | 'tilde' | 'dotdot' |
  'dot_segment' | 'empty_segment' | 'backslash' | 'whitespace'}` — policy:
  leading `/` → absolute; leading `~` → tilde; any `..` segment → dotdot;
  any `.` segment → dot_segment; `a//b` → empty_segment; any `\` →
  backslash; leading/trailing whitespace → whitespace (not trimmed — the
  user sees the error); `$HOME`/`$VAR` stay **literal** (never expanded,
  accepted as characters); trailing `/` trimmed; empty → ok `''`; no client
  length cap (a very long path is sent and the server's `rejected`/400 is
  shown — test that a 10 000-char sub-path neither throws nor blocks).
  `joinCwd(root, sub)`; `utf8ByteLength(s)` via `TextEncoder`. Tests: one
  named `it` per reason, `$HOME` literal, unicode byte count, `a/b/` →
  `a/b`, empty ok, 10 000-char ok.
- `HeadlessLauncher({hostId, onSelect})`: reads `useNexHostStore` (calls
  `ensure` on mount); renders per spec §4.2 (disabled / unavailable /
  loading / ready form with brief textarea + byte counter, roots `<select>`
  + sub-path input, profile `<select>` defaulting to
  `sandbox_default_profile`, muted max-profile note, `no_roots` state);
  submit → `isHostLive` gate → `busy` → `delegateExecution(hostId, {brief,
  cwd, profile, labels: {source: 'purdex'}, origin:
  'purdex://host/<hostId>/newtab'}, caps)`; outcomes per §4.2 (rejected
  inline / 400 code / unavailable + `invalidate` / success `onSelect({kind:
  'execution', executionId, host: hostId})`); remembers last root + profile
  per host.
- i18n keys (en + zh-TW): `newtab.headless.title` ("Headless — {{host}}"),
  `.brief`, `.brief_bytes` ("{{used}} / {{max}} bytes"), `.directory`,
  `.subpath`, `.subpath_error.<reason>` ×4, `.profile`, `.max_profile`
  ("host allows up to {{profile}}"), `.submit`, `.disabled` ("Nexen is not
  enabled on this host — Hosts → Nex"), `.unavailable`, `.no_roots`,
  `.rejected` ("Rejected: {{reason}}"), `.bad_request` ("Request refused:
  {{code}}"), `.loading`.
- Tests (named `it`s): disabled/unavailable/loading render; ready renders
  roots + default profile preselected; byte counter and over-limit blocks
  submit; each sub-path reason blocks submit with its message; `no_roots`
  disables; submit body (spy on `delegateExecution`) has the joined cwd,
  labels, origin, chosen profile; rejected shows reason and does not call
  `onSelect`; 400 shows code; 503 shows unavailable and invalidates; success
  calls `onSelect` with the execution content; last-used root/profile
  restored on remount; host not live → submit refused with the liveness
  message.
- Commit: `feat(exec): HeadlessLauncher form driven by nex capabilities (P-C.1 task 4)`.

## Task 5 — NewTab provider source + registration

Files: `lib/new-tab-registry.ts` + `.test.ts` (source teardown), new
`lib/headless-new-tab-providers.tsx` + `.test.ts`,
`lib/register-modules/index.tsx`, `components/NewTabPage.test.tsx`.

- Registry first (TDD): `NewTabProviderSource` gains optional `moduleId`;
  `unregisterNewTabProvidersByModule(moduleId)` also removes sources with
  that `moduleId` and calls their active unsubscribe; new
  `unregisterNewTabProviderSource(id)`. Tests: module unregister removes the
  source's providers from `getProviders()` output and stops notifying
  (listener count after unregister is zero); the sessions source (no
  `moduleId`) is untouched.
- `createHeadlessProviderSource()` mirroring `session-new-tab-providers.tsx`:
  id `headless`, per-host ids `headless:<hostId>`, label
  `newtab.headless.title` with `{host}`, icon `'Lightning'`, `order: 5`
  (after sessions at 0), cached component per host rendering
  `<HeadlessLauncher hostId onSelect />`, same `subscribe`/`isReady`/`ownsId`;
  `moduleId: 'execution'`. No migrations.
- Register right after the sessions source in `register-modules/index.tsx`
  with `moduleId: 'execution'` so the execution module's disable path
  (`unregisterNewTabProvidersByModule('execution')`) removes it.
- Tests: one provider per host in host order; ids/labels; `ownsId`; not
  ready before hydration; NewTabPage integration (extend
  `NewTabPage.test.tsx`, store seeded to `disabled` so no fetch runs): with
  two hosts the page shows two Headless sections **and** the existing
  sessions/editor/browser entries are still present in their previous
  order — only `headless:<hostId>` ids were added.
- Commit: `feat(exec): Headless section on New Tab, one per host (P-C.1 task 5)`.

## Task 6 — PR

`npx vitest run && pnpm run lint && pnpm run build`; PR "feat(exec):
Headless launch section on New Tab + shared nex host store (P-C.1)"; codex
R1 + R2 (attack: store phase/TTL races, form validation bypass, delegate
error mapping; defend: spec §4.1/§4.2 drift, one-truth migration complete
enough; file health: HeadlessLauncher size, store shape). Acceptance spec
§6.1 on mlab with the worktree dev server (`npx vite --host 100.64.0.2
--port 5175 --strictPort`, playwright cli session `pc-launch-ui`).

## Review log

- codex `task-mu6j5emj-69tliy` (gpt-5.5), 9 findings, all applied: (1) P1
  task 3 now migrates `useNexHostData.info` onto the store (page phase
  mapping preserved, DOM guard); (2) P1 `fetchHostInfo` does not exist →
  `fetchInfo(hostId).then(readJson)` + host-existence guard; (3) P1 stale
  resolve race → generation token + fingerprint, three named tests; (4) P1
  registry has no source teardown → `moduleId` on sources +
  `unregisterNewTabProviderSource`, tests; (5) P2 reconnect invalidation →
  `startNexHostInvalidation` watcher + test; (6) P2 ready ⇔ caps present
  invariant + tests; (7) P2 cwd policy extended (backslash, `.` segment,
  whitespace, `$HOME` literal, long path); (8) P2 DOM guards for Host page
  and NewTab ordering; (9) P3 baseline counts fixed, memory store declared
  new; plus generic 400 passthrough test.
