# Electron Signing — Stage 1b TDD Plan (Option β)

- **Date**: 2026-04-29
- **Status**: Draft v1.0 (pending codex plan review)
- **Worktree**: `.claude/worktrees/electron-signing-stage1b` (branch `worktree-electron-signing-stage1b`)
- **Baseline**: `origin/main @ 96bae3ce` (`1.0.0-alpha.248`)
- **Spec**: `docs/specs/2026-04-29-electron-signing-stage1b-spec.md` (v1.1)
- **Tracking**: #709 (epic) — Stage 1b deliverable
- **Target ship**: alpha.250+ (separate post-merge bump PR; spec §0.1)

---

## 0. Scope confirmation

### 0.1 Files this PR may modify

| Path | Reason | Spec ref |
| ---- | ------ | -------- |
| `electron/updater.ts` | Delete runtime codesign helpers, import, call site | §3.1, §3.2 |
| `electron/signing.test.ts` | Replace 3rd static test with absence smoke; add progress-sequence guard; delete 17 runtime tests | §4.1, §4.1b, §4.3 |
| `spa/src/components/settings/DevEnvironmentSection.tsx` | Remove dead `signing: 'Signing app…'` entry from `stepLabels` | §3.1 SPA row |
| `docs/specs/2026-04-29-electron-signing-stage1b-plan.md` | This plan (added in T0) | — |

### 0.2 Files this PR must NOT touch

| Path | Reason |
| ---- | ------ |
| `scripts/build-electron.mjs` | Build-time signing pipeline; spec §5 non-goal |
| `package.json` (mac.identity, mac.hardenedRuntime, etc.) | Stage 1a territory |
| `internal/module/dev/*` | Daemon-side; no diff per spec §5 / §3.1 |
| `electron/main.ts` | `dev:*` IPC handlers stay registered (spec §7 R7) |
| `electron/preload.ts` | `PDX_DEV_MODE` gate for dev API stays as-is |
| `VERSION`, `CHANGELOG.md` | Separate post-merge bump PR (spec §0.1) |

Concurrent-session safety: worktree is isolated; only files we own
under `electron/` and `spa/src/components/settings/` plus docs.
Per `feedback_concurrent_session_safety.md`, no collision risk.

### 0.3 Verification toolchain — what is and isn't available

After `pnpm install --frozen-lockfile` from worktree root (already
done at session start):

| Tool | Path | Verdict |
| ---- | ---- | ------- |
| `vitest` | `pnpm --prefix electron test` | ✅ available; baseline 39 tests pass |
| `electron-vite build` | `pnpm exec electron-vite build` | ✅ available; produces `out/` (will clean up before commit) |
| `tsc` (electron pkg standalone) | `pnpm --prefix electron exec tsc` | ❌ NOT available (electron pkg has only vitest as devDep) |
| `tsc` (cross-prefix from spa) | `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit` | ✅ available — typescript 5.9.3 in spa devDeps |
| `eslint` (spa) | `pnpm --prefix spa run lint` | ✅ available |
| `vite build` (spa) | `pnpm --prefix spa run build` | ✅ available |

Standalone `pnpm --prefix electron exec tsc` is **not** a gate;
type checking happens via vitest's ts-loader at test time and via
`electron-vite build` at the bundler layer.

### 0.4 Baseline verification (pre-T0)

Confirmed at session start: `pnpm --prefix electron test` reports
**39 tests pass** (`signing.test.ts` 20 + `keybindings.test.ts` 19).
This is the contract the plan starts against.

---

## 1. Task list (TDD-ordered)

### T0 — Commit plan

**Goal**: land this plan document so subsequent commits can reference
it deterministically.

**Files**:
- `docs/specs/2026-04-29-electron-signing-stage1b-plan.md` (this file).

**Verification**:
- `git diff --stat` shows only the plan doc.
- `pnpm --prefix electron test` still 39/39 (no behaviour change).

**Commit**: `docs(electron): Stage 1b plan v1.0`

---

### T1 — Add new static guards (red)

**TDD step 1**: write the static tests that should pass *after*
deletion. They will fail now because the runtime helpers, the
`child_process` import, and the `progress('signing')` call still
exist.

**Files modified**:
- `electron/signing.test.ts`:
  - **Replace** the 3rd existing test (`'updater exposes signing
    preflight + re-sign helpers'`) with the absence smoke from spec
    §4.1:
    ```ts
    it('updater no longer ships runtime signing helpers', () => {
      const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
      expect(updater).not.toContain('detectSignedState')
      expect(updater).not.toContain('resignAppBundle')
      expect(updater).not.toMatch(/\bcodesign\b/)
      expect(updater).not.toContain('child_process')
    })
    ```
  - **Add** the §4.1b progress-sequence guard inside the same
    `describe('Electron macOS signing configuration (static)', ...)`
    block:
    ```ts
    it('updater applyUpdate emits exactly downloading → extracting → applying', () => {
      const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
      expect(updater).toMatch(/progress\(\s*['"]downloading['"]\s*\)/)
      expect(updater).toMatch(/progress\(\s*['"]extracting['"]\s*\)/)
      expect(updater).toMatch(/progress\(\s*['"]applying['"]\s*\)/)
      const literals = Array.from(
        updater.matchAll(/progress\(\s*['"]([^'"]+)['"]\s*\)/g),
        (m) => m[1],
      )
      expect(new Set(literals)).toEqual(new Set(['downloading', 'extracting', 'applying']))
    })
    ```
  - Leave the entire `describe('updater signing preflight (runtime)', ...)`
    block untouched in T1. It still passes (helpers still exist).

**Verification**:
- `pnpm --prefix electron test` → expected **2 failures**:
  - `'updater no longer ships runtime signing helpers'` fails
    because `detectSignedState` / `resignAppBundle` / `codesign` /
    `child_process` are still present.
  - `'updater applyUpdate emits exactly …'` fails because
    `progress('signing')` is still emitted.
- Other 38 tests still pass.

**Commit**: `test(electron): add Stage 1b absence + progress-sequence
guards (red)`

Why commit while red: makes the TDD intent explicit in git history;
T2's commit cleanly shows "deletion makes guards pass".

---

### T2 — Delete runtime helpers + call site (green)

**TDD step 2**: delete the symbols and the call site so the new
guards turn green.

**Files modified**:
- `electron/updater.ts`:
  - Delete line 2: `import { execFileSync, spawnSync } from 'node:child_process'`
  - Delete line 12: `const APP_ID = 'dev.wake.purdex'`
  - Delete lines 45-49: `function getAppBundlePath(): string | null { … }`
  - Delete lines 51-107: `type SignedState`, `PREFLIGHT_TIMEOUT_MS`,
    `NOT_SIGNED_PATTERN`, `stripAnsi`, `detectSignedState`,
    `resignAppBundle` (entire signing block)
  - Delete lines 228-229: `progress('signing')` and the
    `resignAppBundle()` call inside the `try` block in `applyUpdate`
  - Delete line 260: `export const __testing = { detectSignedState, resignAppBundle }`

**Verification**:
- `pnpm --prefix electron test` → mixed:
  - The 2 new T1 guards turn **green**.
  - The 17 runtime tests in `describe('updater signing preflight (runtime)', ...)`
    fail with TypeErrors / undefined access because they still
    `await import('./updater').__testing` which no longer exists.
  - 2 existing static tests + keybindings tests still green.
- `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit`
  passes (no dangling references in `electron/main.ts` because the
  call site is what got deleted; `applyUpdate` signature is
  unchanged).

**Commit**: `refactor(electron): retire runtime codesign per Stage 1b §3.1`

---

### T3 — Delete orphaned runtime tests

**Files modified**:
- `electron/signing.test.ts`:
  - Delete the entire second `describe('updater signing preflight
    (runtime)', ...)` block (17 tests).
  - Delete the helper functions inside that block:
    `loadTesting`, `mockCodesign`, `getCp`, the `SpawnSyncResult`
    type alias, and the `vi.mock('node:child_process', ...)` and
    `vi.mock('electron', ...)` setup if they are only used by the
    runtime block. Re-check: the top-level `vi.mock('electron', ...)`
    is currently used by both the static tests (no — the static
    tests just `readFileSync`) and the runtime block. After T3
    confirm both `vi.mock` calls can be deleted because no test
    reaches Electron / child_process at runtime.

**Verification**:
- `pnpm --prefix electron test` → **23/23 green**:
  - `signing.test.ts`: 4 static (`'does not explicitly disable …'`,
    `'signs and verifies the final moved app bundles'`, the new
    absence smoke, the new progress-sequence guard).
  - `keybindings.test.ts`: 19.
- File `electron/signing.test.ts` shrinks from ~252 lines to roughly
  50 lines.

**Commit**: `test(electron): drop runtime signing helpers' orphaned tests`

---

### T4 — SPA `stepLabels` cleanup

**Files modified**:
- `spa/src/components/settings/DevEnvironmentSection.tsx`:
  - Delete the line `signing: 'Signing app…',` from the `stepLabels`
    map (currently line 256).

**Verification**:
- `pnpm --prefix spa run lint` passes (no unused-vars regression
  because `stepLabels` itself is still consumed via `stepLabels[updateStep] ?? updateStep`).
- `pnpm --prefix spa run build` passes (no TS regression).
- Optional: `pnpm --prefix spa exec vitest run` if any spa test
  references the `signing` label key. Confirmed at plan time that no
  spa test does (`grep -rn 'signing' spa/src/**/*.test.*` returns
  nothing).

**Commit**: `chore(spa): remove dead 'signing' stepLabel entry`

---

### T5 — Full gate sweep (no commit)

**Goal**: run every gate end-to-end against the final tree before PR.

**Commands**:
```
pnpm --prefix electron test                                                   # 23/23 green
pnpm exec electron-vite build                                                 # no regressions
pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit              # green
pnpm --prefix spa run lint                                                    # green
pnpm --prefix spa run build                                                   # green
```

After `electron-vite build` produces `out/`, clean up so it does not
appear in `git status`:
```
rm -rf out
```

**Verification**:
- All gates green.
- `git status` clean.
- `git log --oneline` shows: T0 plan → T1 red guards → T2 deletion
  → T3 orphan-test cleanup → T4 SPA cleanup. Five commits, linear.

**No commit** in T5 — verification only.

---

## 2. Task dependency graph

```
T0 (plan) ─→ T1 (red) ─→ T2 (green) ─→ T3 (orphan cleanup) ─→ T4 (SPA) ─→ T5 (gate sweep)
```

Strictly linear. No parallel branches.

---

## 3. Rollback plan

- If T2 reveals a hidden consumer of `getAppBundlePath` /
  `__testing` / etc. outside the search done at plan time:
  `git reset --hard HEAD~1` (back to T1's red state) and add the
  consumer to scope before retrying T2.
- If T5 fails on `electron-vite build` due to type errors not
  caught by vitest: roll back to T2 commit, fix in a focused
  amend, re-run T5.
- If catastrophic (multiple gates fail unrelated to our change):
  `git reset --hard origin/main` and re-enter the worktree.

---

## 4. Verification gate summary

| Gate | Command | Phases | Notes |
| ---- | ------- | ------ | ----- |
| dependency install | `pnpm install --frozen-lockfile` | once at session start | already done |
| electron unit tests | `pnpm --prefix electron test` | T1 (red expected), T2 (mixed expected), T3 (green), T5 (final) | strict count: 23/23 by T5 |
| electron build | `pnpm exec electron-vite build` | T5 | also catches type regressions |
| cross-prefix tsc | `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit` | T2 (sanity), T5 | no dangling refs |
| spa lint | `pnpm --prefix spa run lint` | T4, T5 | DevEnvironmentSection edit |
| spa build | `pnpm --prefix spa run build` | T5 | full SPA bundles |

---

## 5. Manual verification (post-PR-merge or pre-merge on Air)

Spec §8.2 (unsigned bundle) and §8.3 (ad-hoc signed bundle) are the
manual gate. The ad-hoc signed path (§8.3) is the **Stage 1b gate**
exercising the §3.4 same-machine same-path safety claim. Order:

1. On Mini, `pnpm run electron:build` (no env var needed).
2. Copy `dist/mac-arm64/Purdex.app` to Air `/Applications/`.
3. Spec §8.2 unsigned path: launch, dev update, confirm
   `downloading → extracting → applying` sequence, relaunch
   succeeds, post-update `codesign -dv` reports `not signed at all`.
4. Spec §8.3 ad-hoc signed path: `codesign --force --deep --sign -
   /Applications/Purdex.app`, confirm `codesign --verify --deep
   --strict --verbose=4` passes pre-update. Trigger dev update.
   Confirm relaunch succeeds. Confirm `codesign --verify --deep
   --strict` post-update **fails** with resource hash mismatch
   (this is the documented expected outcome — see spec §6.2 and
   §3.4).

If §8.3 step 4 (post-update verify failure) does not match the
documented outcome — e.g. relaunch breaks, or verify still passes —
investigate before merging the PR.

---

## 6. Out-of-scope (explicit non-tasks)

- Daemon-side changes (`internal/module/dev/*`) — Stage 1a/2/3.
- `package.json mac.*` changes — Stage 1a.
- `scripts/build-electron.mjs` — out of scope; build-time signing
  pipeline unchanged.
- Any new runtime test for `applyUpdate` happy path — spec §4.4
  rejects this; integration coverage lives at §8.2 / §8.3 manual
  level.
- Closing #712 / #713 — done in PR body, not as code change.
- VERSION / CHANGELOG bump — separate post-merge bump PR per spec §0.1.

---

## 7. PR description checklist (for §6 of workflow)

When opening the PR:

- Title: `refactor(electron): retire runtime codesign — Stage 1b
  (#709)`
- Body sections:
  - **Summary**: 2-3 sentence why (Stage 1b retires
    detect/resign vestige; Option β scope).
  - **Spec / plan links**: relative paths to both docs.
  - **Behaviour delta**: paste spec §3.3 table.
  - **Test changes**: 39 → 23 with the 4-line breakdown.
  - **Manual verification**: link to spec §8.2 + §8.3, confirm
    Air-side §8.3 was run with expected outcome.
  - **Closes**: `Closes #712, #713`.
  - **Followups**: none expected; if codex review surfaces any,
    open issues and link here.
- Reviewers: codex via `/codex:review` and `/codex:adversarial-review`
  (two rounds per CLAUDE.md project workflow).

---

## 8. References

- Spec: `docs/specs/2026-04-29-electron-signing-stage1b-spec.md` v1.1
- Stage 0 spec: `docs/specs/2026-04-28-electron-signing-stage0-spec.md` v1.1
- Stage 0 plan: `docs/specs/2026-04-28-electron-signing-stage0-plan.md` v1.1
- Codex spec review: job `task-moivfw7n-a8x1ph` (8 findings, all addressed in spec v1.1)
- Issue #709 (epic), #712 (becomes obsolete), #713 (becomes obsolete)
