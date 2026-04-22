# HSR PR-4 Follow-up — #586 + #588 Implementation Plan

- **Date**: 2026-04-22
- **Status**: Draft v2 (post codex plan R1 — absorbed P1-1/1-2, P2-1/2/3, P3-1/2/3)
- **Base spec**: `docs/specs/2026-04-22-hsr-pr4-followup-586-588-spec.md` (v2)
- **Branch**: `worktree-hsr-pr4-followup-586-588`
- **Base commit**: `df34d1d8` (alpha.206)
- **Target commits on branch**: 3 (1 docs already committed, 2 feat)

---

## 1. Scope recap

Two-commit PR closing #586 (dispatch idempotence / batch-replace source map) and #588 (host-ctx runtime reactivity). Each commit independently green. No cross-commit ordering requirement beyond "commit 1 before commit 2" purely for review linearity; the spec supports the reverse order too.

### Commit layout

| # | Kind | Subject | Gate |
|---|---|---|---|
| docs (already) | docs | HSR PR-4 follow-up spec v1 + v2 + plan v1 + v2 | — (already committed `07de9e9a` + `64860323` + `89891ae0` + this plan v2) |
| 2 | feat | host built-in batch-replace + stable wrapper | #586 tests + existing tests + lint + build |
| 3 | feat | SettingsContextFor<'host'>.runtime + HostPage two-pass selective subscription | #588 tests + existing tests + lint + build |

Reviewer reads spec v2 → commit 2 → commit 3.

---

## 2. Files impacted

### Added
- None (all work lands in existing files + existing test files extended).

### Modified
- `spa/src/lib/host-builtin-sections.ts` — full rewrite (commit 2, see §3.1)
- `spa/src/lib/dispatch-settings-contributions.ts` — drop `peek/drainHostBuiltinQueue` + `clearHostBuiltinPending` imports; replace with `getHostBuiltinDeclarations` + `clearHostBuiltinSources` (commit 2, §3.1)
- `spa/src/lib/register-modules.tsx` — collapse six `registerBuiltinHostSection(...)` calls into one `setHostBuiltinSections([...6 defs...])` call; update HMR dispose-helper import if renamed (commit 2, §3.1)
- `spa/src/lib/settings-contribution-types.ts` — `SettingsContextFor<'host'>` adds `runtime: HostRuntime | undefined` (commit 3, §3.2)
- `spa/src/components/HostPage.tsx` — two-pass selective subscription + runtime-aware `pickSelectableSubPage` + runtime in ctx (commit 3, §3.2)
- `spa/src/components/hosts/HostSidebar.tsx` — add `runtime: runtime[hostId]` to the ctx it passes to `disabled(ctx)` (commit 3, §3.2)

### Tests — rewritten / extended
- `spa/src/lib/host-builtin-sections.test.tsx` — remove `registerBuiltinHostSection` + `clearHostBuiltinPending` imports; use `setHostBuiltinSections` + `clearHostBuiltinSources`; add tests 1–5 per spec §6.1 (commit 2)
- `spa/src/lib/dispatch-settings-contributions.test.ts` — add tests 6–8 per spec §6.1; the host-builtin atomicity test case may simplify since sources are long-lived (commit 2)
- `spa/src/lib/register-modules.test.ts` — no expected change (public surface `registerBuiltinModules()` unchanged); smoke-verify six host contributions still present (commit 2)
- `spa/src/lib/settings-contribution-types.test.ts` — **existing ctx constructions must be updated to include `runtime: undefined`** (any direct `{ scope: 'host', hostId }` objects in fixtures/asserts); add 1–2 type-level assertions proving new shape (commit 3). **Flagged by codex R1 P1-2**
- `spa/src/components/HostPage.test.tsx` — existing tests adjust any ctx constructions to include `runtime`; add tests 7–12 per spec §6.2 (commit 3)
- `spa/src/components/hosts/HostSidebar.test.tsx` — existing ctx constructions gain `runtime`; add test 13 (commit 3)
- **Full-repo grep audit for ctx construction sites missing `runtime`** — any other test file constructing `{ scope: 'host', hostId, ... }` objects. Plan §7 checklist enforces this (codex R1 P1-2 + P3-1)

### Not touched
- `spa/src/lib/host-routes.ts` (#587 deferred; no `parseRoute` / `isHostSubPage` change)
- `spa/src/lib/settings-section-registry.ts` (legacy adapter out of scope)
- All other modules / store files

---

## 3. Commit-by-commit plan

### 3.1 Commit 2 — host built-in batch-replace + stable wrapper (#586)

**Subject**: `feat(spa): host built-in batch-replace source map (closes #586)`

**TDD order**:

1. **Red — write new/modified tests first, run them, expect failures**
   - `spa/src/lib/host-builtin-sections.test.tsx`: replace imports, delete `peek/drain/clearHostBuiltinPending`-touching tests; write tests 1–5 (spec §6.1). At this point source file is unchanged so tests fail at import time.
   - `spa/src/lib/dispatch-settings-contributions.test.ts`: add tests 6–8 (spec §6.1). These may pass trivially until the impl lands; aim at the behavioural assertion (idempotent re-dispatch returns same refs).
   - Run: `cd spa && pnpm exec vitest run src/lib/host-builtin-sections.test.tsx src/lib/dispatch-settings-contributions.test.ts` — expect red (missing `setHostBuiltinSections` export etc.).

2. **Green — rewrite source files**
   - `spa/src/lib/host-builtin-sections.ts`:
     - Remove `pendingHostBuiltinContributions` Map; add `hostBuiltinSources` Map keyed by localId (value: `{ component, labelKey, order, wrapped }`).
     - Remove `registerBuiltinHostSection`, `peekHostBuiltinQueue`, `drainHostBuiltinQueue`, `clearHostBuiltinPending`.
     - Add `setHostBuiltinSections(defs: readonly HostBuiltinSectionDef[]): void` — drop absent localIds, upsert present ones, reuse wrapper per localId.
     - Add `getHostBuiltinDeclarations(): readonly AnySettingsContributionDeclaration[]`.
     - Add `clearHostBuiltinSources(): void`.
     - `createHostBuiltinWrapper(localId)` — closure over `localId` only; on render read `hostBuiltinSources.get(localId)?.component` and delegate; scope guard `if (props.ctx.scope !== 'host') return null`. Defensive `if (!source) return null` for mid-render drop.
     - `hostBuiltinComponentMap` WeakMap stays; in `setHostBuiltinSections` update it to map `wrapped → def.component` (latest component) so identity tests continue to work.
   - `spa/src/lib/dispatch-settings-contributions.ts`:
     - Replace `peekHostBuiltinQueue` import with `getHostBuiltinDeclarations`.
     - Replace `clearHostBuiltinPending` import in `resetSettingsContributionsForHmr` with `clearHostBuiltinSources`.
     - In `buildSettingsContributionBatch`: replace the `peekHostBuiltinQueue()` loop with `getHostBuiltinDeclarations()` loop (structurally identical except no drain semantics).
     - In `dispatchSettingsContributions`: remove `drainHostBuiltinQueue()` call from Phase 2 (sources are long-lived).
   - `spa/src/lib/register-modules.tsx`:
     - Replace the six `registerBuiltinHostSection(...)` calls (lines 399-404) with one `setHostBuiltinSections([...six defs...])` block. Keep the same label/order per-line for reviewability:
       ```tsx
       setHostBuiltinSections([
         { localId: 'overview', labelKey: 'hosts.overview', order: 0, component: OverviewSection },
         { localId: 'sessions', labelKey: 'hosts.sessions', order: 1, component: SessionsSection },
         ...
       ])
       ```
     - Update import from `./host-builtin-sections` to use `setHostBuiltinSections` instead of `registerBuiltinHostSection`.
   - Run tests until green.

3. **Verify full scope + existing suite**
   - `cd spa && pnpm exec vitest run src/lib/host-builtin-sections.test.tsx src/lib/dispatch-settings-contributions.test.ts src/lib/register-modules.test.ts src/lib/settings-contribution-registry.test.ts src/lib/settings-contribution-types.test.ts src/lib/settings-contribution-smoke.test.tsx src/stores/useGlobalSettingsStore.test.ts src/stores/useHostSettingsStore.test.ts src/stores/useWorkspaceSettingsStore.test.ts src/lib/host-lifecycle.test.ts src/components/HostPage.test.tsx src/components/hosts/HostSidebar.test.tsx` — all must be green. The HostPage/HostSidebar tests may still use the old ctx shape (no runtime field) — commit 2 does NOT change the ctx contract; those stay passing.
   - `cd spa && pnpm exec vitest run` — full suite green (any pre-existing failures such as flaky EditorPane tests documented in PR-1 still allowed).
   - `cd spa && pnpm run lint` — green.
   - `cd spa && pnpm run build` — green (TypeScript `tsc -b` catches any dangling import).

4. **Commit**
   ```
   feat(spa): host built-in batch-replace source map (closes #586)

   Replaces the one-shot pending-buffer + drain pattern with a stable source map
   mutated only by setHostBuiltinSections(defs[]) (batch replace) and cleared
   only on HMR dispose. dispatchSettingsContributions() re-materializes host
   built-ins on every call — idempotent under repeated dispatch (the #586 bug
   where a standalone second dispatch wiped the live registry).

   Wrapper identity is stable per localId (cached on first materialization and
   reused on subsequent calls). HMR reloading a host section file replaces the
   inner component reference but preserves the wrapper — React does not remount
   the host sub-page body across HMR (spec §4.1.3, codex R1 P2 #2).

   Full-replace semantics: any localId absent from the new defs is dropped,
   ruling out stale-localId leak from partial re-registrations (codex R1 P2 #1).

   Public API change:
   - removed: registerBuiltinHostSection, peekHostBuiltinQueue,
     drainHostBuiltinQueue, clearHostBuiltinPending
   - added:   setHostBuiltinSections, getHostBuiltinDeclarations,
              clearHostBuiltinSources

   Only production callsite is register-modules.tsx; migrated.

   Tests: §6.1 tests 1–8 (spec v2) cover full-replace, wrapper identity,
   dispatcher idempotence, HMR reset.
   ```

**Exit criteria for commit 2**:
- All scope tests green.
- Lint + build green.
- `git status` clean.
- `git log --oneline` shows 1 new commit on top of `64860323`.

### 3.2 Commit 3 — reactive host runtime (#588)

**Subject**: `feat(spa): reactive host runtime in SettingsContextFor<'host'> (closes #588)`

**Ordering contract (codex R1 P1-1)**: This commit contains an ATOMIC type change. TDD is NOT expressed as "TypeScript compile-error = red" — that produces a broken build state mid-commit, not a meaningful red. Instead, each intermediate file state inside this commit keeps TypeScript green; the "red" phase lives in **behavioural test failures** (tests 7–13 fail at runtime before HostPage/HostSidebar logic lands) while TS always compiles.

The recommended sequence inside the single commit:

1. **Step A — atomic type + fixture migration (TS green throughout)**
   - Extend `SettingsContextFor<'host'>` in `settings-contribution-types.ts` with `runtime: HostRuntime | undefined`.
   - Migrate every host-ctx callsite in one pass: HostPage, HostSidebar, host-builtin-sections wrapper, all `*.test.ts[x]` fixtures. Use the grep audit from §7 to enumerate. Baseline for migration: "any construction of `{ scope: 'host', hostId: X, ... }` must add `runtime`; initial value `undefined` is always safe (matches pre-runtime behaviour)".
   - Run `pnpm run build` until green. No behavioural change yet — ctx now carries runtime but `disabled(ctx)` predicates in existing code all ignore it.
   - Run existing tests → all still pass (no behavioural change).
2. **Step B — write the new behavioural tests (red in runtime, TS still green)**
   - `HostPage.test.tsx` add tests 7–12 (spec §6.2). The contribution factory in tests 7/8/11 injects `disabled: (ctx) => ctx.runtime?.status !== 'connected'` and `registerSettingsContribution(...)` directly. Tests 10+12 use deterministic spies — see §5 test harness details.
   - `HostSidebar.test.tsx` add test 13.
   - `settings-contribution-types.test.ts` add 1–2 type-level assertions documenting the new shape (purely for docs + compile-time guard; no runtime effect).
   - Run these new tests → they fail at runtime: HostPage still ignores runtime ticks, so tests 7/8/11 never observe the expected unmount; test 10's render counter sees extra renders because runtime subscription isn't selective yet.
3. **Step C — land HostPage + HostSidebar behavioural changes (tests turn green)**
   - HostPage: introduce the shared `pickHostIdFallback` helper (codex R1 P2-3), extract `preResolveHostId` on top of it, add `useHostStore((s) => tentativeHostId ? s.runtime[tentativeHostId] : undefined)` selective subscription, thread `tentativeRuntime` through `resolveSelection` / `pickSelectableSubPage` / `renderContent`.
   - HostSidebar: `hostCtx` gains `runtime: runtime[hostId]`.
   - Run until green.
4. **Single commit**: step A + B + C together (no mid-state push). Message body notes the atomic-migration rationale and references codex R1 P1-1.

(Original three-step TDD description retained below but marked non-authoritative — it described the conceptually-correct flow but not the physically-correct ordering for a type-change commit.)

**Legacy TDD description (reference only, superseded by the sequence above)**:

1. ~~**Red — write new/modified tests first**~~ (superseded by Step A→B→C above to avoid a mid-commit broken-TS state)

2. **Concrete code sketches for Step A+B+C**

   **Types** (`settings-contribution-types.ts`):
   ```ts
   import type { HostRuntime } from '../stores/useHostStore'

   export type SettingsContext =
     | { scope: 'purdex' }
     | { scope: 'host'; hostId: string; runtime: HostRuntime | undefined }
     | { scope: 'workspace'; workspaceId: string }
   ```

   **Shared hostId fallback helper** (codex R1 P2-3 — extract the duplicated fallback logic):
   ```ts
   // spa/src/components/HostPage.tsx (or a new host-selection-utils.ts if extraction is cleaner)
   /** @internal */
   export function pickHostIdFallback(
     hostOrder: string[],
     activeHostId: string | null,
     lastSel: Selection | null,
   ): string | null {
     if (hostOrder.length === 0) return null
     if (lastSel?.hostId && hostOrder.includes(lastSel.hostId)) return lastSel.hostId
     if (activeHostId && hostOrder.includes(activeHostId)) return activeHostId
     return hostOrder[0] ?? null
   }
   ```
   Both `preResolveHostId` and the existing inline fallback logic inside `resolveSelection` / `getFallbackSelection` call this helper. Equivalence test (plan §4 new entry 14): same input → same output across both callsites.

   **preResolveHostId** (pure, runtime-independent — built on the shared helper):
   ```ts
   function preResolveHostId(
     location: string,
     hostOrder: string[],
     activeHostId: string | null,
     lastSel: Selection | null,
   ): string | null {
     const parsed = parseRoute(location)
     if ((parsed?.kind === 'hosts' || parsed?.kind === 'hosts-invalid')
         && parsed.hostId && hostOrder.includes(parsed.hostId)) {
       return parsed.hostId
     }
     return pickHostIdFallback(hostOrder, activeHostId, lastSel)
   }
   ```

   **HostPage.tsx** — two-pass selective subscription:
   ```tsx
   export function HostPage(_props: PaneRendererProps) {
     const [location, setLocation] = useLocation()
     const hostOrder    = useHostStore((s) => s.hostOrder)
     const activeHostId = useHostStore((s) => s.activeHostId)
     const tentativeHostId = preResolveHostId(location, hostOrder, activeHostId, lastSelection)
     const tentativeRuntime = useHostStore((s) =>
       tentativeHostId ? s.runtime[tentativeHostId] : undefined,
     )
     const { selection, canonicalPath, shouldPersistSelection } = resolveSelection(
       location, hostOrder, activeHostId, tentativeHostId, tentativeRuntime,
     )
     // ... effects unchanged ...
     const renderContent = () => {
       if (!selection?.hostId) return <p className="text-text-muted">{t('hosts.no_host_selected')}</p>
       const { hostId, subPage } = selection
       const contributions = listContributions('host') as SettingsContribution<'host'>[]
       const contribution = contributions.find((c) => c.localId === subPage)
       if (!contribution) return null
       const ctx: SettingsContextFor<'host'> = { scope: 'host', hostId, runtime: tentativeRuntime }
       if (!isSelectable(contribution, ctx)) return null
       const Component = contribution.component
       return <Component key={`${hostId}:${contribution.id}`} ctx={ctx} />
     }
     // sidebarSubPage: use pickSelectableSubPage with tentativeRuntime
   }
   ```
   - `pickSelectableSubPage(hostId, requested, runtime)` signature change.
   - `resolveSelection(..., tentativeHostId, tentativeRuntime)` threads runtime into every `pickSelectableSubPage` callsite.
   - `getFallbackSelection` inline fallback logic calls `pickHostIdFallback` (shared helper).
   - `lastSelection` logic unchanged (pure bookkeeping).

   **HostSidebar.tsx**:
   ```tsx
   const hostCtx = { scope: 'host' as const, hostId, runtime: runtime[hostId] }
   ```
   Already imports `runtime` at line 29; only the ctx construction at line 52 needs the `runtime: runtime[hostId]` field added.

   Run tests until green.

3. **Verify full scope + existing suite**
   - `cd spa && pnpm exec vitest run src/components/HostPage.test.tsx src/components/HostPage.route-sync.test.tsx src/components/hosts/HostSidebar.test.tsx src/lib/settings-contribution-types.test.ts src/lib/host-builtin-sections.test.tsx src/lib/dispatch-settings-contributions.test.ts` — scope green.
   - `cd spa && pnpm exec vitest run` — full suite green (same allowance as commit 2).
   - `cd spa && pnpm run lint` — green.
   - `cd spa && pnpm run build` — green. TypeScript will catch any ctx construction site still missing `runtime` field; fix those until clean.

4. **Commit**
   ```
   feat(spa): reactive host runtime in SettingsContextFor<'host'> (closes #588)

   Extends SettingsContextFor<'host'> with runtime: HostRuntime | undefined and
   plumbs it through HostPage + HostSidebar + pickSelectableSubPage. Host
   contributions whose disabled(ctx) predicate depends on runtime now
   reactively unmount the body when the predicate flips true: HostPage
   re-evaluates on runtime changes, isSelectable returns false, the
   canonicalPath effect redirects to the next selectable sub-page. Fixes the
   gap where the sidebar row greyed out but the body remained mounted.

   HostPage uses a two-pass selective subscription (pre-resolve hostId from
   URL/hostOrder/activeHostId/lastSelection, then subscribe only that host's
   runtime) so background-host heartbeat ticks do NOT re-render HostPage
   (codex R1 P3 #3). HostSidebar already subscribes to the full runtime map
   (StatusIcon / per-row disabled); no new cost.

   Tests: §6.2 tests 7–13 (spec v2) cover tick-driven unmount, rapid flicker
   stability, unmount-during-tick warning guard, background-host no-rerender
   assertion, and sidebar runtime-aware ctx.
   ```

**Exit criteria for commit 3**:
- All scope tests green.
- Lint + build green.
- `git status` clean.
- `git log --oneline` shows 2 new commits on top of `64860323`.

---

## 4. Test-plan crosswalk (spec → plan)

| Spec test # | Where | Assertion | Commit |
|---|---|---|---|
| §6.1 #1 | `host-builtin-sections.test.tsx` | Batch replace is full set; drop absent | 2 |
| §6.1 #2 | `host-builtin-sections.test.tsx` | Wrapper identity stable across re-calls | 2 |
| §6.1 #3 | `host-builtin-sections.test.tsx` | Wrapper delegates to latest component | 2 |
| §6.1 #4 | `host-builtin-sections.test.tsx` | Re-add after drop rebuilds wrapper | 2 |
| §6.1 #5 | `host-builtin-sections.test.tsx` | `clearHostBuiltinSources` empties map | 2 |
| §6.1 #6 | `dispatch-settings-contributions.test.ts` | Standalone re-dispatch preserves built-ins | 2 |
| §6.1 #7 | `dispatch-settings-contributions.test.ts` | Interleaved module + built-in stable | 2 |
| §6.1 #8 | `dispatch-settings-contributions.test.ts` | HMR reset → 0 contributions | 2 |
| §6.2 #7 | `HostPage.test.tsx` | Runtime flip drops disabled body + redirects | 3 |
| §6.2 #8 | `HostPage.test.tsx` | Runtime flip re-enables body | 3 |
| §6.2 #9 | `HostPage.test.tsx` | `pickSelectableSubPage` uses runtime | 3 |
| §6.2 #10 | `HostPage.test.tsx` | Background host tick does NOT re-render HostPage | 3 |
| §6.2 #11 | `HostPage.test.tsx` | Rapid flicker stable | 3 |
| §6.2 #12 | `HostPage.test.tsx` | Unmount during tick no warning | 3 |
| §6.2 #13 | `HostSidebar.test.tsx` | Sidebar ctx carries runtime | 3 |
| **NEW 14** | `HostPage.test.tsx` (or new `host-selection-utils.test.ts`) | `pickHostIdFallback` equivalence: extracted helper produces identical output to inline fallback for representative inputs (hostOrder ∈ {empty, single, multi}; activeHostId ∈ {null, present, absent}; lastSel ∈ {null, present, stale}). Codex R1 P2-3 | 3 |

---

## 5. Zustand harness notes (feedback-driven)

- **Merge-mode setState only** for tests touching `useHostStore` — do NOT use `setState({...}, true)` replace mode; it wipes zustand actions and causes "not a function" errors. Feedback: `feedback_zustand_harness_setstate.md`.
- When seeding for tests 10+11, use `useHostStore.setState((state) => ({ runtime: { ...state.runtime, [hostId]: nextRuntime } }))` — functional updater preserves zustand's merge semantics.
- For tests that need to wipe host state entirely, use explicit merge: `useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })` and list every mutable field (ref `feedback_zustand_harness_setstate.md`).

### 5.1 Deterministic test harness for tests 10 + 12 (codex R1 P2-1, P3-3)

Codex R1 P2-1 / P3-3 flagged that render-counter / `console.error` spy approaches for "no re-render" / "no warning after unmount" are brittle under React 19 + jsdom + strict mode. Use deterministic alternatives:

**Test 10 — background host tick does NOT re-render HostPage**:
- Wrap HostPage's render in a counter via a thin spy component:
  ```tsx
  const renderCount = { value: 0 }
  function HostPageProbe(props: PaneRendererProps) {
    renderCount.value++
    return <HostPage {...props} />
  }
  ```
- Mount HostPage on `/hosts/hA/overview`. Snapshot `renderCount.value` after initial render + flush.
- Mutate `runtime[hB]` (background host) via `useHostStore.setState((state) => ({ runtime: { ...state.runtime, hB: { status: 'disconnected' } } }))`.
- Assert `renderCount.value` unchanged (selective subscription returned same reference for `runtime[tentativeHostId]`).
- Repeat: mutate `runtime[hA]` (selected host) → assert counter incremented.

**Test 12 — unmount during tick has no side effects**:
- Use a deterministic subscription-leak probe instead of console-warning spy:
  ```ts
  // before mount
  const subBefore = useHostStore.subscribe(() => {})
  // After cleanup() unmount, push a runtime tick
  useHostStore.setState((state) => ({ runtime: { ...state.runtime, hA: { status: 'reconnecting' } } }))
  // Assert: no error thrown; the test-local subscriber recorded the tick (proving store healthy);
  //         no React-internal listener leak (count of internal listeners returned to baseline)
  ```
- Concretely assert via `useHostStore.getState()` accessibility post-unmount and that the store's internal listener Set size returns to the baseline before mount. Zustand exposes `useHostStore.subscribe` returning an unsubscribe fn; before HostPage mount, capture the set size via `(useHostStore as unknown as { getSubscribers?(): unknown }).getSubscribers?.()` if available, else snapshot via temporary subscribe/unsubscribe pair to count delta.
- Fallback if zustand internals are not introspectable: assert that `cleanup()` followed by a runtime tick does not raise + a deterministic effect counter on a sentinel component does not increment. The key contract is "no callback fires into the unmounted HostPage tree", measurable via the spy on a child component that records calls.

**Test 12 must NOT be skipped** (codex R1 P2-2). If neither approach is feasible during implementation, downgrade to the lightest possible deterministic check (assert `cleanup() → setState(...)` does not throw + sentinel's effect callback runs only N times during lifecycle) and document the limitation in the test's docstring.

---

## 6. Risk & mitigation

| Risk | Mitigation |
|---|---|
| `HostRuntime` import from `../stores/useHostStore` into `settings-contribution-types.ts` creates a coupling back from lib→stores | Acceptable: `HostRuntime` is already exported at `useHostStore.ts:17`. Spec §5 documents the dependency. |
| `setHostBuiltinSections` call in `register-modules.tsx` runs once per `registerBuiltinModules()` invocation; HMR dispose already clears sources. Edge: test suites that re-import `register-modules` without a clear | Tests already call `clearContributions()` + `clearHostBuiltinPending` (now `clearHostBuiltinSources`) in `clearAll()`. The batch-replace API itself is idempotent (second call with same defs → same state). Mitigated by design. |
| `preResolveHostId` duplicates logic from `resolveSelection` / `getFallbackHostId` | Intentional — `preResolveHostId` is a *pure, runtime-independent* extraction. `resolveSelection` still owns the full redirect logic and uses `getFallbackHostId` as before. Small overlap (URL parse + hostOrder check); documented in the code comment. |
| Test 12 (unmount warning) relies on RTL's cleanup + console.error spy; vitest may not print warning under `jsdom` env (codex R1 P2-1) | **Resolved**: §5.1 prescribes deterministic subscription-leak probe instead of warning spy. NOT skipped. |
| `pickHostIdFallback` extraction creates two callsites that drift independently (codex R1 P2-3) | Equivalence test 14 (plan §4) asserts shared output; helper is `@internal`-marked single source of truth. |
| `hostBuiltinComponentMap` retains stale wrapper keys after batch-replace drops a localId (codex R1 P3-2) | WeakMap's GC handles dropped wrappers automatically once `hostBuiltinSources` releases the strong ref. Add explicit test in §6.1 #4 (wrapper rebuilt on re-add). No code change needed beyond the natural GC; documented in commit 2's body. |
| Commit 2 changes break a callsite outside grep | Run full suite + lint + build — TS catches removed imports; grep audit performed in plan §2. |

---

## 7. Validation checklist

Before PR:
- [ ] `git log --oneline df34d1d8..HEAD` shows the expected commits on the branch (spec v1, spec v2, plan v1, plan v2, commit 2, commit 3 → **6 commits total** at PR open, with the four docs commits providing review context for the two feat commits)
- [ ] `cd spa && pnpm exec vitest run` — full suite green
- [ ] `cd spa && pnpm run lint` — green
- [ ] `cd spa && pnpm run build` — green
- [ ] Manual smoke: `pnpm run dev` → host page renders six sub-pages → disconnect a host → sidebar row updates + body unaffected (no regression)
- [ ] **Negative grep audits** (codex R1 P3-1):
  - [ ] `rg -n "registerBuiltinHostSection|peekHostBuiltinQueue|drainHostBuiltinQueue|clearHostBuiltinPending" spa/src` → empty (or only hits in historical docs / spec / plan files)
  - [ ] `rg -n "scope:\s*'host'" spa/src --type ts --type tsx` → every match is a ctx construction that includes the `runtime` field (manual eyeball or follow-up grep `rg -n "scope:\s*'host'.*hostId" spa/src` cross-checked for `runtime` presence)
  - [ ] `rg -n "SettingsContextFor<'host'>" spa/src` → every consumer either uses `ctx.runtime` deliberately or ignores it harmlessly (no `// @ts-expect-error` work-arounds)

Before merge:
- [ ] Codex R1 (standard) + R2 (3-way adversarial) both green / findings absorbed
- [ ] CI green
- [ ] PR body includes codex review round digest + references to #586 / #588

---

## 8. Out of scope

Same as spec §2 + §9: no #587, no legacy adapter rewrite, no PR-5 work, no `globalConfig`/`workspaceConfig` deprecation. Strictly #586 + #588.

---

## 9. Post-merge

1. Squash merge PR (CLAUDE.md: no direct push to main).
2. Delete remote branch.
3. ExitWorktree with remove + discard_changes.
4. Enter a fresh bump worktree → `git fetch origin && git reset --hard origin/main` → bump VERSION + CHANGELOG alpha.206 → alpha.207 → commit → push → PR → merge.
5. Update memory: `kickoff_host_module_settings.md`, `project_progress.md`, MEMORY index.
6. Add comment on issue #586 / #588 closing with PR link.
