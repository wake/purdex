# Electron Signing — Stage 0 Hotfix TDD Plan

- **Date**: 2026-04-28
- **Status**: Draft plan (pending codex plan review)
- **Worktree**: `.claude/worktrees/electron-signing-stage0-hotfix` (branch `worktree-electron-signing-stage0-hotfix`)
- **Baseline**: `origin/main @ 166bac19` (`1.0.0-alpha.247`)
- **Spec**: `docs/specs/2026-04-28-electron-signing-stage0-spec.md` (v1.1)
- **Tracking**: #709 (epic) — Stage 0 deliverable
- **Target ship**: alpha.248 (separate bump PR)

---

## 0. Scope confirmation

### 0.1 Files this PR may modify

- `electron/updater.ts` — add `detectSignedState`, refactor `resignAppBundle` for three-state dispatch, add `__testing` namespace export, switch import to `node:child_process`.
- `electron/signing.test.ts` — replace third grep test with smoke; add 5 + 6 = 11 runtime tests under `vi.mock('node:child_process')` + `vi.mock('electron', ...)`.
- `docs/specs/2026-04-28-electron-signing-stage0-plan.md` — this plan (committed in T0).

### 0.2 Files this PR must NOT touch

- `scripts/build-electron.mjs` — explicitly out of scope per spec §5. Build-time signing is unchanged.
- `electron/main.ts`, `electron/preload.ts`, `electron/window-manager.ts` etc. — unrelated.
- `internal/module/dev/*` — daemon side dev update endpoints unchanged.
- `spa/**` — no SPA changes; the renderer-side progress UI already labels `signing` (`spa/src/components/settings/DevEnvironmentSection.tsx:256`).
- `package.json` — Stage 1 territory.

### 0.3 Concurrent-session safety

Memory `feedback_concurrent_session_safety.md` warns that the main repo may have parallel sessions. Worktree is isolated; the only files that could collide are the spec/plan we own. No risk.

---

## 1. Pre-flight checks (T0 — already complete)

- [x] Worktree created at `.claude/worktrees/electron-signing-stage0-hotfix`.
- [x] Issue #709 filed.
- [x] Spec v1.1 committed (`7eef4929`).
- [x] Plan committed (this file).

No code changes in T0 — the prior commits are spec-only.

---

## 2. Task sequence (TDD)

Each task ends with a single commit. Verification commands listed
under each task must pass before committing.

### T1 — Switch updater to `node:` prefix imports

**Why first.** The runtime tests in T2 rely on `vi.mock('node:child_process')`. Switching the production import first lets us write tests against the canonical specifier. T1 is a no-op refactor — green should stay green.

**Change**:

`electron/updater.ts:2`

```diff
- import { execFileSync } from 'child_process'
+ import { execFileSync, spawnSync } from 'node:child_process'
```

`spawnSync` is added in the same import to avoid a churn commit later. T1 does not yet use `spawnSync`; that lands in T3.

**Verification**:

```bash
pnpm --prefix electron exec tsc -p tsconfig.json --noEmit
pnpm --prefix electron test
```

Both must pass (existing tests are static grep — they still pass because `'codesign'`, `'--identifier'`, `'dev.wake.purdex'`, `'resignAppBundle'` strings remain).

**Commit**: `refactor(electron/updater): unify imports to node: prefix`

---

### T2 — Add 11 runtime tests (RED)

**Why before implementation.** Per spec §4.2 / §4.3 + project TDD policy.

**Change**: rewrite `electron/signing.test.ts` to:

1. Keep the two existing static tests for `package.json mac.identity` and `build-electron.mjs` codesign references (`signing.test.ts:8-18`).
2. Replace the third static grep test with the spec §4.1 smoke version that asserts presence of `detectSignedState`, `resignAppBundle`, and `'node:child_process'` strings.
3. Add a new `describe('updater signing preflight (runtime)', ...)` block with:
   - `vi.mock('electron', () => ({ app: { getPath: vi.fn(() => '/Applications/Purdex.app/Contents/MacOS/Purdex') } }))`
   - `vi.mock('node:child_process', () => ({ spawnSync: vi.fn(), execFileSync: vi.fn() }))`
   - `beforeEach`: `vi.resetModules()`, reset env vars, reset mocks
   - `afterEach`: restore env vars
   - 5 `detectSignedState` cases per spec §4.2 table
   - 6 `resignAppBundle` cases per spec §4.3 table

Test access pattern (per case):

```ts
const cp = await import('node:child_process')
;(cp.spawnSync as Mock).mockReturnValue({ status: 1, stderr: '...' })
const { __testing } = await import('./updater')
expect(__testing.detectSignedState('/x/Purdex.app')).toBe('unsigned')
```

For non-darwin path (one of the §4.3 cases): mock `process.platform`. Use `vi.stubGlobal('process', { ...process, platform: 'linux' })` or `Object.defineProperty(process, 'platform', { value: 'linux' })` with restore in `afterEach`. Pick the latter — simpler and doesn't require touching every property.

**Expected outcome**: tests fail because `__testing` does not exist on `./updater` yet (`undefined.detectSignedState` throws). This is the canonical RED state.

**Verification**:

```bash
pnpm --prefix electron test 2>&1 | tee /tmp/red.log
# Expected: new tests fail with "Cannot read properties of undefined (reading 'detectSignedState')"
# Existing static tests (smoke + package.json + build-electron) still pass
```

Confirm RED tests are exactly the 11 new ones, no other regressions.

**Commit**: `test(electron/updater): add detectSignedState + resignAppBundle preflight tests (RED)`

---

### T3 — Implement three-state detection + refactor resign (GREEN)

**Change**: `electron/updater.ts`

1. Add `SignedState` type and `detectSignedState` helper per spec §3.2.
2. Refactor `resignAppBundle` to:
   - Early-return on `!appBundle` or `PDX_SKIP_MAC_SIGN === '1'` (existing).
   - Call `detectSignedState(appBundle)`.
   - If `'unsigned'`, return.
   - If `'unknown'`, throw `Error('codesign preflight detection failed; aborting re-sign')`.
   - If `'signed'`, run existing `execFileSync` codesign + verify chain.
3. Add the `__testing` namespace export at file end:

```ts
export const __testing = { detectSignedState, resignAppBundle }
```

Function ordering: `getAppBundlePath` → `detectSignedState` → `resignAppBundle` → existing functions, keeping diffs local.

**Verification**:

```bash
pnpm --prefix electron exec tsc -p tsconfig.json --noEmit
pnpm --prefix electron test
```

All 11 new tests must turn GREEN. Existing tests must remain GREEN.

**Commit**: `feat(electron/updater): three-state preflight skips unsigned re-sign`

---

### T4 — Final verification

No code change. Confirm acceptance criteria:

```bash
# Test suite
pnpm --prefix electron test

# Type check
pnpm --prefix electron exec tsc -p tsconfig.json --noEmit

# Build
pnpm exec electron-vite build

# Smoke read of updater.ts to confirm the contract is wired
grep -n "detectSignedState\|resignAppBundle\|node:child_process" electron/updater.ts
```

If all green, T4 produces no commit; the branch is ready for PR.

If anything red, **do not paper over** — go back to T2/T3 and adjust. Memory `feedback_codex_review_termination.md` says known/tracked issues can ship, but `pnpm test` red is not a known issue, it's a regression.

---

## 3. Out-of-scope guards

This plan deliberately defers to Stage 1+:

- ❌ `scripts/build-electron.mjs` — leave alone. Spec §5.
- ❌ `package.json mac.entitlements` — Stage 1 (1a).
- ❌ Removing the runtime resign — Stage 1 (1b architectural fix).
- ❌ daemon-side bundle packaging — Stage 1.
- ❌ Self-signed cert workflow — Stage 2.
- ❌ Notarization — Stage 3.

If during T2/T3 a bug surfaces that requires touching one of these, **stop and surface** (per `feedback_phase_skip_threshold.md`). Do not silently expand scope.

---

## 4. Manual verification protocol (post-merge, pre-bump)

Per spec §8.2, before tagging alpha.248:

1. Mini: `PDX_SKIP_MAC_SIGN=1 pnpm run electron:build`
2. Copy `dist/mac-arm64/Purdex.app` to Air's `/Applications/`
3. `codesign -dv /Applications/Purdex.app 2>&1` → confirm `code object is not signed at all`
4. Launch this Purdex
5. Trigger dev update from Settings → Development
6. SPA progress reaches `signing` then app exits and relaunches
7. New version visible after relaunch
8. Post-state: `codesign -dv /Applications/Purdex.app 2>&1` still reports unsigned

If any step fails — do not bump. File issue. The spec covers `unknown` state via throw + rollback, so a thrown detection should leave the system in a recoverable state, but a SIGKILL would mean the contract didn't hold and Stage 1 needs to come earlier.

---

## 5. PR + bump protocol

Per CLAUDE.md:

- PR (Stage 0 fix) → 2-round codex review → resolve findings → merge to `main`.
- Bump PR (alpha.248) — separate worktree, base `origin/main`, update `VERSION` + `package.json` + `spa/package.json` + `CHANGELOG.md`.
- Manual verification protocol §4 lives **before** bump; if bump bypasses it, document why in the bump PR description.

---

## 6. Risk register (delta from spec)

Plan-specific risks (spec §7 covers product-level risks):

| # | Risk | Mitigation |
| - | ---- | ---------- |
| P1 | RED→GREEN flip lies — tests pass for the wrong reason (e.g. mock leaks across tests) | `vi.resetModules()` + explicit `vi.clearAllMocks()` in `beforeEach`; each test re-imports updater fresh. |
| P2 | `Object.defineProperty(process, 'platform', ...)` mutates real process | Restore in `afterEach`; alternative is `vi.stubGlobal` or hoisted setup-file mock. Acceptable risk for a single test. |
| P3 | Test for "throw on unknown" doesn't actually go through `applyUpdate` rollback (only tests `resignAppBundle` in isolation) | Acceptable — `applyUpdate`'s try/catch is existing tested-by-staging surface; Stage 0 doesn't change it. Spec §4.4 marks this as deferred to Stage 1+ verification. |
