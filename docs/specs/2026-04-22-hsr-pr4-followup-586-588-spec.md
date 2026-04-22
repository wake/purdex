# HSR PR-4 Follow-up — #586 + #588 (Architecture Polish)

- **Date**: 2026-04-22
- **Status**: Draft v1 (pending codex review)
- **Target**: `main` — post-alpha.206
- **Scope**: Two architectural polish items closed together in one PR, as preparation for PR-5 (Editor module dynamic register).
- **Related**:
  - GH #586 (bug/spa): `dispatchSettingsContributions()` 第二次 standalone 呼叫會清空 host built-ins
  - GH #588 (bug/spa): `disabled(ctx)` 動態變化時 body 不 reactive 卸載
  - GH #587 (refactor/spa): `parseRoute` / `isHostSubPage` 依賴 mutable registry — **out of scope**, deferred to post-PR-5
  - Master spec: `docs/specs/2026-04-21-settings-contribution-registry-design.md`
  - PR-4 merged at `a0452e02` (alpha.206, `df34d1d8`)

---

## 1. Why

Both findings were raised by Round 2 / Round 3 codex review on PR-4 (#582) and explicitly deferred — they are **not currently triggered in production** but become real risks the moment PR-5 introduces the first module with `disabled(ctx)` and/or a dynamic module-register callsite. PR-5 is the validation proof of the whole HSR architecture; we want to enter PR-5 with these two contracts already in place so that PR-5's review noise is about the Editor feature, not about leftover architectural debt.

Consolidating the two into a single small PR keeps review concentrated (same PR-4 cluster, same reviewers' mental model) and avoids two serialized bump cycles.

---

## 2. Scope

Single squash-merged PR with two commits (plus incidental test scaffolding commits as needed; see plan). No feature changes. No visible UX change. All existing tests must stay green; new tests capture the new contracts.

**In scope**
- #586 — replace the host-builtin pending-buffer/one-shot-drain design with a stable source map that is re-materialized every dispatch (Option A in the issue body).
- #588 — make `HostPage` reactive to `HostRuntime` changes and plumb `runtime` into `SettingsContextFor<'host'>`, so `disabled(ctx)` predicates reflect live host runtime (Option A in the issue body).

**Out of scope (tracked separately)**
- Legacy adapter (`settings-section-registry`) still uses the pending buffer + drain pattern. It is purdex-scoped and currently only populated during `registerBuiltinModules()`; the same symmetric fix is strictly better but is **not** part of this PR. Tracked as issue #586 comment or new follow-up if desired; spec explicitly punts to keep blast radius small.
- #587 `parseRoute` / `isHostSubPage` pure-parser discussion — needs a real dynamic module-declared sub-page (PR-5 or later) to judge which option lands best. Status: remain OPEN, untouched.
- Any change to `globalConfig` / `workspaceConfig` deprecation path (PR-5).

---

## 3. Current behaviour (grounded in code)

### 3.1 Host built-in registration (today)

`spa/src/lib/host-builtin-sections.ts`:

```ts
const pendingHostBuiltinContributions = new Map<string, HostBuiltinDeclaration>()

export function registerBuiltinHostSection(def): void {
  const Wrapped = /* scope-guard wrapper around def.component */
  pendingHostBuiltinContributions.set(def.localId, { ...decl, component: Wrapped })
}
export function peekHostBuiltinQueue(): readonly ...[] { /* Array.from(values) */ }
export function drainHostBuiltinQueue(): ...[] {
  const out = Array.from(values); pendingHostBuiltinContributions.clear(); return out
}
export function clearHostBuiltinPending(): void { pendingHostBuiltinContributions.clear() }
```

`dispatch-settings-contributions.ts` Phase 2 calls `drainHostBuiltinQueue()` *after* `clearContributions()`. Problem (from #586): after the first successful dispatch the buffer is empty, so a second standalone `dispatchSettingsContributions()` call (no re-`registerBuiltinHostSection(...)` in between) would:

1. Phase 1 validate — batch contains module-declared only, host-builtin peek returns `[]`, no collisions, passes.
2. Phase 2 `clearContributions()` — wipes live host-builtin contributions previously written.
3. Phase 2 `drainHostBuiltinQueue()` — no-op.
4. Result: `listContributions('host')` returns `[]`; `HostPage` UI collapses (sidebar empty, body self-heals via canonicalPath redirect loop guarded by `hostOrder === 0` check, but still broken).

Today the only callsite is inside `registerBuiltinModules()` which *does* re-push. PR-5 will add `registerModule({..., settings: [...]})` at least once dynamically; even if PR-5 is careful to co-locate a fresh `registerBuiltinModules()`-style flush, any future module-dynamic-register API is a landmine.

### 3.2 HostPage reactivity (today)

`HostPage.tsx` subscribes to:

```tsx
const hostOrder = useHostStore((s) => s.hostOrder)
const activeHostId = useHostStore((s) => s.activeHostId)
// does NOT subscribe to `runtime`
```

`HostSidebar.tsx` already subscribes to `runtime` (for `StatusIcon`) and builds `hostCtx = { scope: 'host', hostId }` without a `runtime` field — `disabled(ctx)` predicates passed from modules have no access to runtime.

Consequence: a module authoring `disabled: (ctx) => useHostStore.getState().runtime[ctx.hostId]?.status !== 'connected'` today:
- `HostSidebar` ticks on runtime changes → predicate re-runs via re-render → row greys/ungreys reactively ✓
- `HostPage.resolveSelection` runs only when `location` / `hostOrder` / `activeHostId` change — runtime changes **do not** trigger redirect away from a body that just became disabled ✗

This violates PR-4's new "disabled contribution must not mount body" contract the moment a real consumer exists.

### 3.3 `SettingsContextFor<'host'>` shape (today)

`spa/src/lib/settings-contribution-types.ts`:

```ts
type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string }
  | { scope: 'workspace'; workspaceId: string }
```

No runtime field for host scope. To make `disabled(ctx)` reflect host-runtime reactively without `getState()` side reads, we must plumb runtime through ctx.

---

## 4. Design

### 4.1 #586 — stable source map (Option A)

Concept: host built-ins become a **stable registration source**, not a one-shot buffer. Every `dispatchSettingsContributions()` call re-materializes host-builtin declarations from the source map; the source map is only mutated by `registerBuiltinHostSection()` (population) and `clearHostBuiltinSources()` (HMR dispose / test reset).

#### 4.1.1 New `host-builtin-sections.ts` shape

```ts
interface HostBuiltinSource {
  def: HostBuiltinSectionDef         // original inputs
  wrapped: React.ComponentType<{ ctx: SettingsContextFor<'host'> }>  // cached wrapper
}

// Stable source map. Populated by registerBuiltinHostSection(),
// read non-destructively by the dispatcher, cleared only on HMR dispose.
const hostBuiltinSources = new Map<string, HostBuiltinSource>()

export function registerBuiltinHostSection(def: HostBuiltinSectionDef): void {
  const existing = hostBuiltinSources.get(def.localId)
  // Idempotent: if all user-visible fields unchanged, reuse the cached wrapper
  // so React.ComponentType identity is stable across re-dispatches.
  if (existing &&
      existing.def.component === def.component &&
      existing.def.labelKey === def.labelKey &&
      existing.def.order === def.order) {
    return
  }
  const wrapped = createHostBuiltinWrapper(def)  // same scope-guard logic as today
  hostBuiltinSources.set(def.localId, { def, wrapped })
}

export function getHostBuiltinDeclarations(): readonly AnySettingsContributionDeclaration[] {
  return Array.from(hostBuiltinSources.values(), (src) => ({
    localId: src.def.localId,
    scope: 'host' as const,
    order: src.def.order,
    labelKey: src.def.labelKey,
    component: src.wrapped,  // STABLE reference across dispatches
  }))
}

export function clearHostBuiltinSources(): void {
  hostBuiltinSources.clear()
}
```

Removed: `pendingHostBuiltinContributions`, `peekHostBuiltinQueue`, `drainHostBuiltinQueue`, `clearHostBuiltinPending`. The WeakMap `hostBuiltinComponentMap` stays (identity test helper).

#### 4.1.2 Dispatcher changes

`buildSettingsContributionBatch`:

```ts
// Host built-ins: re-materialize every dispatch from the stable source map.
// NOT a queue drain — sources are long-lived; dispatch is idempotent.
const hostBuiltinDecls = getHostBuiltinDeclarations()
for (const decl of hostBuiltinDecls) {
  const full = { ...decl, moduleId: HOST_BUILTIN_MODULE_ID, id: `${HOST_BUILTIN_MODULE_ID}.${decl.localId}` }
  checkAndRecord(full, 'host-builtin')
  batch.push(full)
}
```

`dispatchSettingsContributions` Phase 2:

```ts
clearContributions()
drainLegacyContributionQueue()  // legacy keeps the old drain contract (out of scope)
// NO drainHostBuiltinQueue() — sources are stable, batch already contains host-builtins.
for (const contribution of batch) registerSettingsContribution(contribution)
```

`resetSettingsContributionsForHmr`:

```ts
clearContributions()
clearLegacyPending()
clearHostBuiltinSources()  // renamed from clearHostBuiltinPending
```

#### 4.1.3 Invariants restored

- **Idempotent dispatch**: N consecutive `dispatchSettingsContributions()` calls (same module list, same built-in source map) yield identical live registry state.
- **Order-independence**: `registerBuiltinHostSection()` may be called before or after any `registerModule()` without changing the end state; same for the ordering between the two categories.
- **HMR safety**: `clearHostBuiltinSources()` runs in HMR dispose; the next `registerBuiltinModules()` repopulates without duplication.
- **Component identity stability**: When `registerBuiltinHostSection()` is called twice with the same `def.component` / `def.labelKey` / `def.order`, the cached `wrapped` reference is preserved, so React does not remount built-in host sub-page bodies across re-dispatches.

### 4.2 #588 — reactive host runtime (Option A)

#### 4.2.1 Extend `SettingsContextFor<'host'>`

```ts
type SettingsContext =
  | { scope: 'purdex' }
  | { scope: 'host'; hostId: string; runtime: HostRuntime | undefined }  // NEW runtime field
  | { scope: 'workspace'; workspaceId: string }
```

- `HostRuntime` imported from `../stores/useHostStore`.
- `runtime` is explicitly `HostRuntime | undefined` (not optional `?`) to force callers to think about the absent case (host exists but no runtime yet — common on first render before any status tick).
- All existing `disabled(ctx)` / component consumers that ignore `runtime` continue to compile (property unused).

#### 4.2.2 `HostPage` subscribes runtime

```tsx
const hostOrder  = useHostStore((s) => s.hostOrder)
const activeHostId = useHostStore((s) => s.activeHostId)
const runtime    = useHostStore((s) => s.runtime)   // NEW — whole map
```

Rationale for subscribing the whole map, not a selective `runtime[hostId]`:
- `hostId` is derived from `resolveSelection(location, hostOrder, activeHostId, runtime)` — chicken-and-egg otherwise.
- `runtime` is a `Record<string, HostRuntime>` whose object identity only changes on runtime ticks for *any* host; impact is bounded to host-area lifecycle. For the typical user session (single-digit hosts, ticks every few seconds), re-render cost is negligible and strictly better than the alternative of refactoring selection to a two-pass design.
- `HostSidebar` already subscribes to the same map; cost is already paid at the HostPage level via child render.

#### 4.2.3 `resolveSelection` / `pickSelectableSubPage` / `renderContent` take runtime

- `resolveSelection(location, hostOrder, activeHostId, runtime)` — additional `runtime` parameter.
- `pickSelectableSubPage(hostId, requestedSubPage, runtime)` — builds ctx as `{ scope: 'host', hostId, runtime: runtime[hostId] }`.
- `renderContent` builds ctx the same way; `isSelectable(contribution, ctx)` receives runtime-aware ctx.

#### 4.2.4 `HostSidebar` propagates runtime in ctx

```tsx
const hostCtx = { scope: 'host' as const, hostId, runtime: runtime[hostId] }
// page.disabled(hostCtx) now sees runtime
```

Already subscribes to `runtime`; only change is plumbing through ctx.

#### 4.2.5 `host-builtin-sections` wrapper

Wrapper already type-guards `props.ctx.scope === 'host'`. The extended ctx shape gains `runtime` but the wrapper forwards `hostId` only — no change to built-in section components (which receive `{ hostId }` as before).

#### 4.2.6 Invariants restored

- **Reactivity**: when `runtime[hostId]` changes and a disabled(ctx) predicate flips true, `HostPage.renderContent` re-evaluates, `isSelectable` returns false, body returns `null`, `canonicalPath` effect redirects to the next selectable sub-page. PR-4's "disabled body must not mount" contract is now upheld at **evaluation time AND reactivity time**.
- **No chicken-and-egg**: single pass. `hostId` → `runtime[hostId]` resolved inline at `pickSelectableSubPage` callsite; `resolveSelection` never needs the runtime to pick a hostId (host selection is driven by order/active/URL, not runtime).

### 4.3 Interaction with PR-4's three unified fallback sites

The three sites fixed by `ca000a81` (getFallbackSelection / sidebarSubPage / resolveSelection) all flow through `pickSelectableSubPage`; they automatically benefit from runtime-aware ctx after the signature change. No new fallback sites.

---

## 5. Contract changes (public / semi-public)

| Surface | Before | After | Breakage |
|---|---|---|---|
| `registerBuiltinHostSection(def)` | Push to pending buffer | Upsert into stable source map (idempotent) | None externally — call semantics unchanged from caller's viewpoint |
| `peekHostBuiltinQueue()` / `drainHostBuiltinQueue()` / `clearHostBuiltinPending()` | Exported | Removed | Internal to `host-builtin-sections` + `dispatch-settings-contributions` + tests — grep confirms no other importers |
| New: `getHostBuiltinDeclarations()` | — | Exported | Used by dispatcher only |
| New: `clearHostBuiltinSources()` | — | Exported | Replaces `clearHostBuiltinPending` in `resetSettingsContributionsForHmr` |
| `SettingsContextFor<'host'>` | `{ scope: 'host'; hostId }` | `{ scope: 'host'; hostId; runtime: HostRuntime \| undefined }` | All consumer sites must now set `runtime` when building ctx. Consumers that **read** ctx stay source-compatible (extra field). |
| `HostRuntime` type | — | **exported** from `useHostStore` (already exported at `spa/src/stores/useHostStore.ts:17` — no change needed) | None |

All call-site churn for `runtime` is inside `HostPage.tsx` + `HostSidebar.tsx` + a handful of tests. No module-declared consumer exists today (host-builtin sections don't use disabled).

---

## 6. Test plan

### 6.1 New tests — #586

`spa/src/lib/host-builtin-sections.test.tsx`:

1. **Idempotent re-register**: call `registerBuiltinHostSection(def)` twice with identical def → source map size = 1 AND `getHostBuiltinDeclarations()[0].component === getHostBuiltinDeclarations()[0].component` across calls (stable reference).
2. **Def change invalidates cache**: register with `order: 0`, then re-register with `order: 1` → declarations reflect new order, wrapper is rebuilt (reference may differ; new def fully replaces).
3. **`clearHostBuiltinSources()` empties the map**: populate → clear → `getHostBuiltinDeclarations()` returns `[]`.

`spa/src/lib/dispatch-settings-contributions.test.ts`:

4. **Standalone re-dispatch preserves host built-ins** (THE #586 bug): `registerBuiltinHostSection(...)` × 6 → `dispatchSettingsContributions()` → assert 6 host contributions → `dispatchSettingsContributions()` again (no re-register) → assert still 6 host contributions AND same component identities.
5. **Interleaved module + built-in dispatch**: `registerModule({ settings: [...] })` + `registerBuiltinHostSection(...)` → dispatch → dispatch again → state stable.
6. **HMR reset**: populate → `resetSettingsContributionsForHmr()` → dispatch → 0 host contributions (sources cleared).

### 6.2 New tests — #588

`spa/src/components/HostPage.test.tsx` (if absent, new; otherwise extend):

7. **Runtime tick drops disabled body**: register a host contribution with `disabled: (ctx) => ctx.runtime?.status !== 'connected'` → URL `/hosts/hA/featureX` → seed `runtime.hA = { status: 'connected' }` → body mounts → mutate to `{ status: 'disconnected' }` → `HostPage` re-renders → body unmounts, URL navigates to the next selectable sub-page.
8. **Runtime tick re-enables body**: inverse of 7 — disabled initially → runtime flips → `canonicalPath` is null (already at correct subPage) OR redirects into the now-enabled body cleanly.
9. **`pickSelectableSubPage` uses runtime**: unit test on the helper with synthetic contributions + runtime maps; disabled predicate returns true iff runtime is absent.

`spa/src/components/hosts/HostSidebar.test.tsx` (if absent, new; otherwise extend):

10. **Sidebar builds runtime-aware ctx**: register contribution with `disabled: (ctx) => ctx.runtime === undefined` → host in store but no runtime yet → sidebar row rendered with `data-disabled-ctx="true"` → runtime tick arrives → row becomes enabled.

### 6.3 Existing tests

All existing tests must pass. Specifically:
- `spa/src/lib/host-builtin-sections.test.tsx` — adjust assertions referring to `peek/drain` API if any (expected: delete / rewrite those).
- `spa/src/lib/dispatch-settings-contributions.test.ts` — host-builtin atomicity tests rewrite: validation failure still leaves source map intact (source map is long-lived by design; no "queue" to preserve), but the behavioural contract that commit only happens on validation success still holds.
- `spa/src/lib/register-modules.test.ts` — no expected change (public registerBuiltinModules surface unchanged).
- `spa/src/components/HostPage.test.tsx` / existing route-utils / HostSidebar tests — adjust any ctx construction to include `runtime` field (many tests will just add `runtime: undefined`).

Target: all scope tests + existing suite pass (`pnpm exec vitest run`). Lint + build green.

### 6.4 Manual smoke (pre-PR)

1. `pnpm run dev` → launch SPA → Host page renders six built-in sub-pages as before.
2. Disconnect/reconnect a host via daemon off/on — sidebar row status icon updates (unchanged), body unaffected (no contribution uses `disabled` yet; regression check).
3. Local ad-hoc module with `disabled: (ctx) => ctx.runtime?.status === 'disconnected'` (dev-only manual harness) — verify redirect on status flip. Not committed; used to validate during self-review.

---

## 7. Rollout

Single PR, squash merge, alpha bump afterwards.

Commit sequence (detail in plan):
1. **feat(spa): host built-in stable source map** — replace pending buffer with source map + idempotent wrapper cache + dispatcher re-materialization + HMR rename. Rewrites tests for #586. Ships alone and green.
2. **feat(spa): reactive host runtime in SettingsContextFor<'host'>** — add `runtime` to host ctx, plumb through HostPage + HostSidebar + pickSelectableSubPage. Rewrites / adds tests for #588. Ships alone and green.

Optional commit 0 (docs): add spec file. Kept alongside merge.

Post-merge: bump PR to alpha.207 from separate worktree (CLAUDE.md dev process §9). Update `project_progress.md`, `kickoff_host_module_settings.md`, MEMORY index.

---

## 8. Risks & mitigation

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| `SettingsContextFor<'host'>` breaking change surfaces in unseen callsite | Low | Medium | TypeScript compilation catches all sites; grep for `scope: 'host'` or `SettingsContextFor<'host'>` in plan §checklist |
| HostPage re-render on every runtime tick degrades perf | Low | Low | Single-digit hosts; ticks are heartbeat-speed; matches HostSidebar subscription cost already paid today |
| Component identity regression forces body remount | Med | Low | Unit test 1+4 explicitly assert wrapper identity across re-registers and re-dispatches |
| Legacy adapter kept on old buffer pattern looks asymmetric | High | Low (it actually is asymmetric; intentional) | Note in PR body + issue #586 (or follow-up) captures "legacy adapter same fix, deferred"; adds strictly more risk to expand scope here |
| `runtime: HostRuntime \| undefined` confuses callers expecting optional | Low | Low | Use `?` optional makes callers forget runtime — keeping required-undefined is a deliberate contract to force explicit handling. Documented in spec §5 |
| Test rewrites miss an invariant the old pending-buffer captured | Med | Medium | Each rewritten test's behavioural property is explicitly listed in §6.1; pass/fail table in PR body cross-refs old vs new |

---

## 9. Out-of-scope / follow-ups

- **#587** (parseRoute / isHostSubPage purity) — unchanged OPEN; revisit after PR-5 lands a real dynamic module-declared subPage.
- **Legacy adapter same treatment** — the `_builtin.legacy-section` adapter still uses pending buffer + drain. Strictly symmetric fix applies, but no live callsite forces the issue today. Deferred; note in issue #586 after merge.
- **`ModuleDefinition.settings` dynamic register API** — PR-5 will land the first real module-dynamic register path. If it chooses to call `dispatchSettingsContributions()` externally, the #586 fix in this PR directly protects it.

---

## 10. Acceptance checklist

- [ ] #586: `dispatchSettingsContributions()` is idempotent (test 4 green)
- [ ] #586: HMR reset + dispatch → host built-ins gone (test 6 green)
- [ ] #586: Component identity stable across re-dispatches (test 1+4 green)
- [ ] #588: `SettingsContextFor<'host'>` has `runtime: HostRuntime | undefined`
- [ ] #588: `HostPage` subscribes `runtime` and re-resolves on ticks (test 7+8 green)
- [ ] #588: `HostSidebar` passes runtime in ctx (test 10 green)
- [ ] All existing tests green (`pnpm exec vitest run`)
- [ ] Lint green (`pnpm run lint`)
- [ ] Build green (`pnpm run build`)
- [ ] No changes to `host-routes.ts` (#587 deferred)
- [ ] No changes to legacy-section adapter
- [ ] PR body includes the two-issue cluster summary + codex review round digest
