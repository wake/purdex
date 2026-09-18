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
  init_error, effective, restart_required?}`; `fetchHostInfo(hostId)` at
  ~180 returns `HostInfo` with `nex?: NexInfo`.
- `spa/src/components/hosts/nex/nex-ready.ts` (11 lines) `isNexReady(info)`.
- `spa/src/components/hosts/nex/useNexHostData.ts` 158 lines: returns
  `{phase: 'offline'|'failed'|'loading'|'ready', info, config, refreshError,
  retry, refresh, onConfigSaved}`; fetches `/api/info` (94, 129) and
  `/api/config`; 101 tests in `useNexHostData.test.ts`.
- `spa/src/components/hosts/nex/NexEngineStatus.tsx` 178 lines: fetches
  `Promise.all([fetchNexHost, fetchNexCapabilities])` at 67–70 keyed on
  `${hostId}:${ready}:${tick}`; 141 tests in `NexEngineStatus.test.tsx`.
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
  thrown); 400 `invalid_brief` → thrown `NexApiError(400, 'invalid_brief')`;
  503 `nex_unavailable`; unknown host → `host_removed`. Type test:
  capabilities JSON with the new fields parses into the typed shape.
- Commit: `feat(nex): delegateExecution + typed delegate/limit capabilities (P-C.1 task 1)`.

## Task 2 — `useNexHostStore`

Files: new `stores/useNexHostStore.ts` + `.test.ts`; `lib/host-lifecycle.ts`
(one line in the clear cascade) + its test.

- State per host: `{info: NexInfo | null, capabilities: NexCapabilities |
  null, phase: 'unknown' | 'loading' | 'ready' | 'disabled' | 'unavailable',
  error: string | null, fetchedAt: number}`.
- `ensure(hostId): Promise<void>` — no-op when `fetchedAt` is within
  `NEX_HOST_TTL_MS = 60_000` and phase ≠ `unavailable`; dedups an in-flight
  fetch per host (module-level `Map<string, Promise>`); sets `loading` only
  when there is no prior data (a refresh keeps the old phase visible);
  fetches `fetchHostInfo(hostId)` → `.nex`; when `isNexReady(nex)` also
  `fetchNexCapabilities(hostId)`; phase per spec §4.1. `NexApiError(503,
  'nex_unavailable')`, network, `init_error` → `unavailable` with the message.
  Unknown host (`host_removed`) → entry deleted.
- `invalidate(hostId)` sets `fetchedAt = 0` (next `ensure` refetches) and
  returns `ensure(hostId)`.
- Selectors: `selectReady(hostId)`, `selectHandoffReady(hostId)` (ready ∧
  `delegate.resume_session_id === true` ∧ `sandbox_profiles.includes('handoff')`)
  — pure functions over state, tested.
- `clearHost(hostId)`; wired into `host-lifecycle.ts` next to
  `useExecutionStore.getState().clearHost(hostId)`.
- Tests: phase matrix (configured=false → disabled; mounted=false →
  disabled; ready but capabilities 503 → unavailable; both ok → ready;
  init_error → unavailable); TTL (second `ensure` within 60 s makes no
  fetch; after → refetch); in-flight dedup (two concurrent `ensure` = one
  fetch); `invalidate` forces a fetch; `selectHandoffReady` truth table;
  `clearHost` drops the entry; host-lifecycle cascade calls it (extend the
  existing lifecycle test that asserts `useExecutionStore.clearHost`).
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
- `useNexHostData`: `onConfigSaved` additionally calls
  `useNexHostStore.getState().invalidate(hostId)`; `retry`/`refresh` call it
  too. `info`/`phase` semantics unchanged this task (the page's `offline`/
  `failed` phases depend on `/api/config`, which the store does not own) —
  record this in the PR as the remaining seam.
- Verify `NexHostSection.test.tsx` (451 lines) stays green untouched.
- Commit: `refactor(nex): NexEngineStatus and useNexHostData read readiness from useNexHostStore (P-C.1 task 3)`.

## Task 4 — `HeadlessLauncher` form

Files: new `components/headless/HeadlessLauncher.tsx` + `.test.tsx`, new
`lib/nex/cwd-input.ts` + `.test.ts` (pure validation), new
`stores/useHeadlessLauncherMemoryStore.ts` (persisted last root/profile per
host; mirror the session launcher's memory store if one exists — measure
first, reuse if so), locales.

- `lib/nex/cwd-input.ts`: `validateSubPath(sub: string): {ok: true, value:
  string} | {ok: false, reason: 'absolute' | 'tilde' | 'dotdot' |
  'empty_segment'}` (trims trailing `/`; empty → ok ''), `joinCwd(root,
  sub)`; `utf8ByteLength(s)` via `TextEncoder`. Tests: each reason, unicode
  byte count, `a/b/` → `a/b`, empty ok.
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

Files: new `lib/headless-new-tab-providers.tsx` + `.test.ts`,
`lib/register-modules/index.tsx`, `lib/new-tab-registry.test.ts` only if a
registry change is needed (expected: none).

- `createHeadlessProviderSource()` mirroring `session-new-tab-providers.tsx`:
  id `headless`, per-host ids `headless:<hostId>`, label
  `newtab.headless.title` with `{host}`, icon `'Lightning'`, `order: 5`
  (after sessions at 0), cached component per host rendering
  `<HeadlessLauncher hostId onSelect />`, same `subscribe`/`isReady`/`ownsId`;
  `moduleId: 'execution'`. No migrations.
- Register right after the sessions source in `register-modules/index.tsx`;
  ensure `unregisterNewTabProvidersByModule('execution')` covers it when the
  module is disabled (check how the sessions source is torn down and mirror).
- Tests: one provider per host in host order; ids/labels; `ownsId`; not
  ready before hydration; NewTabPage integration: with two hosts the page
  shows two Headless sections (extend `NewTabPage.test.tsx` minimally with
  the store seeded to `disabled` so no fetch runs).
- Commit: `feat(exec): Headless section on New Tab, one per host (P-C.1 task 5)`.

## Task 6 — PR

`npx vitest run && pnpm run lint && pnpm run build`; PR "feat(exec):
Headless launch section on New Tab + shared nex host store (P-C.1)"; codex
R1 + R2 (attack: store phase/TTL races, form validation bypass, delegate
error mapping; defend: spec §4.1/§4.2 drift, one-truth migration complete
enough; file health: HeadlessLauncher size, store shape). Acceptance spec
§6.1 on mlab with the worktree dev server (`npx vite --host 100.64.0.2
--port 5175 --strictPort`, playwright cli session `pc-launch-ui`).
