# HSR PR-4 Follow-up — #586 + #588 Implementation Plan

- **Date**: 2026-04-22
- **Status**: Draft v1 (pending codex plan review)
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
| 1 (already) | docs | HSR PR-4 follow-up spec v1 + v2 | — (already committed `07de9e9a` + `64860323`) |
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
- `spa/src/components/HostPage.test.tsx` — existing tests adjust any ctx constructions to include `runtime`; add tests 7–12 per spec §6.2 (commit 3)
- `spa/src/components/hosts/HostSidebar.test.tsx` — existing ctx constructions gain `runtime`; add test 13 (commit 3)

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

**TDD order**:

1. **Red — write new/modified tests first**
   - `spa/src/lib/settings-contribution-types.test.ts`: add a type-level test (or TypeScript compile-error check via `// @ts-expect-error` patterns) asserting `SettingsContextFor<'host'>` requires `runtime: HostRuntime | undefined`. Keep light — the real enforcement comes from downstream consumers.
   - `spa/src/components/HostPage.test.tsx`:
     - Adjust every existing test's ctx constructions to include `runtime: undefined` (or a minimal stub) — these are the callsites TypeScript will flag after commit 3 lands the type change.
     - Add tests 7–12 per spec §6.2. Tests 10 (background host tick) and 11 (rapid flicker) require careful store-state seeding via merge-mode `useHostStore.setState` (feedback `zustand_harness_setstate`). Test 12 captures console spies before `cleanup()` then ticks runtime; assert no stale warning. Tests 7+8 use contributions registered directly into the registry via `registerSettingsContribution` (bypass module registration) to inject a test contribution with runtime-driven `disabled`.
   - `spa/src/components/hosts/HostSidebar.test.tsx`: add test 13; existing tests update ctx to include runtime.
   - Run: `cd spa && pnpm exec vitest run src/components/HostPage.test.tsx src/components/hosts/HostSidebar.test.tsx src/lib/settings-contribution-types.test.ts` — expect TypeScript errors / test failures.

2. **Green — extend types + HostPage + HostSidebar**

   **Types** (`settings-contribution-types.ts`):
   ```ts
   import type { HostRuntime } from '../stores/useHostStore'

   export type SettingsContext =
     | { scope: 'purdex' }
     | { scope: 'host'; hostId: string; runtime: HostRuntime | undefined }
     | { scope: 'workspace'; workspaceId: string }
   ```

   **HostPage.tsx** — two-pass selective subscription:
   ```tsx
   function preResolveHostId(
     location: string,
     hostOrder: string[],
     activeHostId: string | null,
     lastSel: Selection | null,
   ): string | null {
     if (hostOrder.length === 0) return null
     const parsed = parseRoute(location)
     if ((parsed?.kind === 'hosts' || parsed?.kind === 'hosts-invalid')
         && parsed.hostId && hostOrder.includes(parsed.hostId)) {
       return parsed.hostId
     }
     if (lastSel?.hostId && hostOrder.includes(lastSel.hostId)) return lastSel.hostId
     return getFallbackHostId(hostOrder, activeHostId)
   }

   export function HostPage(_props: PaneRendererProps) {
     const [location, setLocation] = useLocation()
     const hostOrder    = useHostStore((s) => s.hostOrder)
     const activeHostId = useHostStore((s) => s.activeHostId)
     const tentativeHostId = preResolveHostId(location, hostOrder, activeHostId, lastSelection)
     const tentativeRuntime = useHostStore((s) =>
       tentativeHostId ? s.runtime[tentativeHostId] : undefined,
     )
     // resolveSelection / pickSelectableSubPage take tentativeHostId + tentativeRuntime
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

---

## 5. Zustand harness notes (feedback-driven)

- **Merge-mode setState only** for tests touching `useHostStore` — do NOT use `setState({...}, true)` replace mode; it wipes zustand actions and causes "not a function" errors. Feedback: `feedback_zustand_harness_setstate.md`.
- When seeding for tests 10+11, use `useHostStore.setState((state) => ({ runtime: { ...state.runtime, [hostId]: nextRuntime } }))` — functional updater preserves zustand's merge semantics.
- For tests that need to wipe host state entirely, use explicit merge: `useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {}, activeHostId: null })` and list every mutable field (ref `feedback_zustand_harness_setstate.md`).

---

## 6. Risk & mitigation

| Risk | Mitigation |
|---|---|
| `HostRuntime` import from `../stores/useHostStore` into `settings-contribution-types.ts` creates a coupling back from lib→stores | Acceptable: `HostRuntime` is already exported at `useHostStore.ts:17`. Spec §5 documents the dependency. |
| `setHostBuiltinSections` call in `register-modules.tsx` runs once per `registerBuiltinModules()` invocation; HMR dispose already clears sources. Edge: test suites that re-import `register-modules` without a clear | Tests already call `clearContributions()` + `clearHostBuiltinPending` (now `clearHostBuiltinSources`) in `clearAll()`. The batch-replace API itself is idempotent (second call with same defs → same state). Mitigated by design. |
| `preResolveHostId` duplicates logic from `resolveSelection` / `getFallbackHostId` | Intentional — `preResolveHostId` is a *pure, runtime-independent* extraction. `resolveSelection` still owns the full redirect logic and uses `getFallbackHostId` as before. Small overlap (URL parse + hostOrder check); documented in the code comment. |
| Test 12 (unmount warning) relies on RTL's cleanup + console.error spy; vitest may not print warning under `jsdom` env | Assert on `console.error` / `console.warn` spy counts, not on regex match. Skip the test with explicit reason if jsdom strips warnings — leave a TODO linking to #588. |
| Commit 2 changes break a callsite outside grep | Run full suite + lint + build — TS catches removed imports; grep audit performed in plan §2. |

---

## 7. Validation checklist

Before PR:
- [ ] `git log --oneline df34d1d8..HEAD` shows exactly 3 commits (spec v1 + spec v2 + commit 2 + commit 3 = 4; note: v1 and v2 are separate commits per §1 layout; re-count is 4)
  - Actually: commits on branch are `07de9e9a` (spec v1), `64860323` (spec v2), commit 2, commit 3 → **4 commits total**
- [ ] `cd spa && pnpm exec vitest run` — full suite green
- [ ] `cd spa && pnpm run lint` — green
- [ ] `cd spa && pnpm run build` — green
- [ ] Manual smoke: `pnpm run dev` → host page renders six sub-pages → disconnect a host → sidebar row updates + body unaffected (no regression)
- [ ] Grep confirms no lingering `registerBuiltinHostSection` / `peekHostBuiltinQueue` / `drainHostBuiltinQueue` / `clearHostBuiltinPending` references
- [ ] Grep confirms no lingering `{ scope: 'host', hostId: X }` ctx construction missing the `runtime` field (TS would already catch this; grep is a belt-and-braces check)

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
