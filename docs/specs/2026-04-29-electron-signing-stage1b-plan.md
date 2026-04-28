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
exist. Two of the four new tests pass immediately (the preload +
daemon gate guards) — they are added in T1 so they live alongside
the rest of the static surface.

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
    block. Uses **ordered array equality** so a re-ordered or
    duplicated step also fails (P1-2):
    ```ts
    it('updater applyUpdate emits exactly downloading → extracting → applying in order', () => {
      const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
      const literals = Array.from(
        updater.matchAll(/progress\(\s*['"]([^'"]+)['"]\s*\)/g),
        (m) => m[1],
      )
      expect(literals).toEqual(['downloading', 'extracting', 'applying'])
    })
    ```
  - **Add** the spec §6.1 preload-gate guard (passes immediately —
    no behaviour change required, just locks the contract):
    ```ts
    it('preload still gates dev update API behind PDX_DEV_MODE', () => {
      const preload = readFileSync(resolve(root, 'electron/preload.ts'), 'utf8')
      expect(preload).toMatch(/PDX_DEV_MODE/)
      expect(preload).toMatch(/applyUpdate:/)
      expect(preload).toMatch(/checkUpdate:/)
      expect(preload).toMatch(/onUpdateProgress:/)
      // gate predates the conditional spread so applyUpdate must appear
      // INSIDE a process.env.PDX_DEV_MODE-guarded block
      const gateIdx = preload.indexOf('PDX_DEV_MODE')
      const applyIdx = preload.indexOf('applyUpdate:')
      expect(gateIdx).toBeGreaterThan(-1)
      expect(applyIdx).toBeGreaterThan(gateIdx)
    })
    ```
  - **Add** the spec §6.1 daemon-gate guard (also passes
    immediately):
    ```ts
    it('daemon still gates /api/dev/update routes behind PDX_DEV_MODE=1', () => {
      const mod = readFileSync(resolve(root, 'internal/module/dev/module.go'), 'utf8')
      expect(mod).toMatch(/os\.Getenv\("PDX_DEV_MODE"\)\s*!=\s*"1"/)
      expect(mod).toMatch(/\/api\/dev\/update\/check/)
      expect(mod).toMatch(/\/api\/dev\/update\/download/)
    })
    ```
  - Leave the entire `describe('updater signing preflight (runtime)', ...)`
    block untouched in T1. It still passes (helpers still exist).

**Verification**:
- `pnpm --prefix electron test` → expected **2 failures, 2 passes**
  out of the 4 newly-added tests:
  - `'updater no longer ships runtime signing helpers'` ❌ fails
    (helpers still present).
  - `'updater applyUpdate emits exactly … in order'` ❌ fails
    (`progress('signing')` still emitted; literals array contains
    a 4th element).
  - `'preload still gates dev update API behind PDX_DEV_MODE'` ✅ passes.
  - `'daemon still gates /api/dev/update routes …'` ✅ passes.
- Other 38 existing tests still pass.
- `pnpm --prefix electron test` final exit code: non-zero (red).

**Commit**: `test(electron): add Stage 1b absence + progress-sequence
+ gate guards (red)`

Why commit while red: makes the TDD intent explicit in git history;
T2's commit cleanly shows "deletion makes guards pass".

---

### T2 — Delete runtime helpers + call site (guards green, suite red)

**TDD step 2**: delete the symbols and the call site so the new
guards turn green. The 17 orphan runtime tests will fail with
TypeError / undefined access because they import `__testing` which
no longer exists. This intermediate red is intentional — T3 cleans
it up. **Do not run tsc in T2** (the dangling `__testing` import in
runtime tests will fail compilation; tsc gate moves to T3).

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
  - **Trim `import { dirname, join } from 'path'` to
    `import { join } from 'path'`** — `dirname` was only consumed by
    `getAppBundlePath()` (P2-4).

**Verification**:
- `pnpm --prefix electron test` → mixed:
  - The 2 previously-failing T1 guards (absence + progress-sequence)
    turn **green**.
  - The 2 always-green T1 gate guards (preload + daemon) remain
    green.
  - The 17 runtime tests in `describe('updater signing preflight
    (runtime)', ...)` fail because `await import('./updater')).__testing`
    is undefined.
  - 2 existing static tests + keybindings tests still green.
  - `pnpm --prefix electron test` final exit code: non-zero (suite
    red, guards green).
- **No tsc gate in T2** — moved to T3.

**Commit**: `refactor(electron): retire runtime codesign per Stage 1b §3.1`

Body should make explicit that "guards are green; runtime suite
intentionally red until T3 deletes the orphan tests."

---

### T3 — Delete orphaned runtime tests + cross-prefix tsc gate

**Files modified**:
- `electron/signing.test.ts`:
  - Delete the entire second `describe('updater signing preflight
    (runtime)', ...)` block (17 tests).
  - Delete the helper functions inside that block:
    `loadTesting`, `mockCodesign`, `getCp`, the `SpawnSyncResult`
    type alias.
  - Delete top-level `vi.mock('node:child_process', ...)` and
    `vi.mock('electron', ...)` setup — only the runtime block used
    them; static tests only `readFileSync`.
  - Tighten the import line to the minimum surface left in use
    (P2-5):
    ```ts
    import { readFileSync } from 'node:fs'
    import { resolve } from 'node:path'
    import { describe, expect, it } from 'vitest'
    ```
    (Drop `afterEach`, `beforeEach`, `vi`, the `Mock` type — all
    only used by the deleted runtime block.)

**Verification**:
- `pnpm --prefix electron test` → **25/25 green**:
  - `signing.test.ts`: 6 static (2 existing preserved + absence
    smoke + progress-sequence guard + preload-gate guard +
    daemon-gate guard).
  - `keybindings.test.ts`: 19.
- `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit`
  passes (no dangling references; `__testing` is gone, runtime
  imports are gone).
- File `electron/signing.test.ts` shrinks from ~252 lines to ~55
  lines.

**Commit**: `test(electron): drop runtime signing helpers' orphaned tests`

---

### T4 — SPA `stepLabels` cleanup

**Files modified**:
- `spa/src/components/settings/DevEnvironmentSection.tsx`:
  - Delete the line `signing: 'Signing app…',` from the `stepLabels`
    map (currently line 256).
- `electron/signing.test.ts`:
  - Add the §6.1 SPA-absence guard (P2-7) — passes only after the
    edit above:
    ```ts
    it('SPA stepLabels no longer carries the signing entry', () => {
      const tsx = readFileSync(
        resolve(root, 'spa/src/components/settings/DevEnvironmentSection.tsx'),
        'utf8',
      )
      // The stepLabels map literal must not contain a `signing` key
      // mapped to a label string. Allow free-form text mentioning
      // "signing" elsewhere in the file (eg. comments, error msgs).
      expect(tsx).not.toMatch(/signing\s*:\s*['"]Signing app/)
    })
    ```

**Verification**:
- `pnpm --prefix electron test` → **26/26 green** (signing.test.ts:
  7; keybindings.test.ts: 19). The new SPA-absence guard turns
  green only after the SPA edit lands in the same commit.
- `pnpm --prefix spa run lint` passes (no unused-vars regression —
  `stepLabels` is still consumed via
  `stepLabels[updateStep] ?? updateStep`).
- `pnpm --prefix spa run build` passes (no TS regression).
- No spa test references the `signing` label key (verified at plan
  time: `grep -rn "signing" spa/src/**/*.test.*` returns nothing).

**Commit**: `chore(spa): remove dead 'signing' stepLabel entry`

---

### T5 — Full gate sweep (no commit)

**Goal**: run every gate end-to-end against the final tree before PR.

**Commands**:
```
pnpm --prefix electron test                                                   # 26/26 green
pnpm exec electron-vite build                                                 # no regressions
pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit              # green
pnpm --prefix spa run lint                                                    # green
pnpm --prefix spa run build                                                   # green
```

`out/` is in `.gitignore`, so `electron-vite build` should not show
up in `git status`. The cleanliness check is on **tracked** files
(P3-12):

```
git status --short    # must be empty
```

If `git status --short` shows anything (e.g. icon metadata
regenerated by `spa/scripts/generate-icon-data.mjs` running as part
of `pnpm --prefix spa run build`), inspect the diff before
proceeding — those regenerations are unrelated to Stage 1b and
should not ship in this PR.

**Verification**:
- All gates green.
- `git status --short` clean.
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
  caught by vitest: roll back to T3 commit (last known
  guards-and-suite-green state), fix in a focused new commit,
  re-run T5.
- If catastrophic (multiple gates fail in ways unrelated to our
  change, suggesting environment / merge contamination):
  `git reset --hard origin/main` inside the worktree and re-enter
  the affected tasks.
- **Worktree-level fallback (P3-11)**: if the worktree itself is
  inconsistent — uncommitted state from a previous interrupted
  session, divergent local branch ahead of the planned commits, or
  the cwd-collision behaviour described in
  `feedback_concurrent_session_safety.md` — preserve the spec/plan
  docs (`docs/specs/2026-04-29-electron-signing-stage1b-{spec,plan}.md`
  are already in `worktree-electron-signing-stage1b`'s commit
  history), exit the worktree without removing it, then enter a
  fresh worktree off latest `origin/main` and cherry-pick the
  spec/plan commits before resuming at T0.

---

## 4. Verification gate summary

| Gate | Command | Phases | Expected outcome |
| ---- | ------- | ------ | ---------------- |
| dependency install | `pnpm install --frozen-lockfile` | once at session start | already done |
| electron unit tests | `pnpm --prefix electron test` | T1 (2 fail / 2 new pass), T2 (guards green; 17 orphan tests fail), T3 (25/25 green), T4 (26/26 green), T5 (26/26 final) | strict count: 26/26 by T4-T5 |
| electron build | `pnpm exec electron-vite build` | T5 | also catches type regressions; `out/` is gitignored |
| cross-prefix tsc | `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit` | T3 (after orphan tests deleted), T5 | no dangling `__testing` references |
| spa lint | `pnpm --prefix spa run lint` | T4, T5 | DevEnvironmentSection edit |
| spa build | `pnpm --prefix spa run build` | T5 | full SPA bundles; may regenerate icon metadata — inspect tracked diff |
| git status clean | `git status --short` | T5 | empty output (tracked files only) |

---

## 5. Manual verification (pre-merge on Air)

Spec §8.2 (unsigned bundle) and §8.3 (ad-hoc signed bundle) are the
manual gate. The ad-hoc signed path (§8.3) is the **Stage 1b gate**
exercising the §3.4 same-machine same-path safety claim.

**Run §8.2 and §8.3 as two independent installs.** Each manual run
must start from a stale-enough bundle that the daemon-side hash
delta makes a dev update available. Doing §8.2 first then §8.3 on
the same install would leave Air at the daemon's current hash, and
§8.3 would have no update to trigger (P2-6).

### 5.1 Pre-build (do once)

1. On Mini, ensure the daemon source has at least one commit ahead
   of whatever Air's installed `.app` was built from. If both are
   identical, dev update will report "up to date" — manufacture a
   trivial source delta first (or rebuild Mini after merging the
   Stage 1b PR; this is what "pre-merge on Air" means in
   practice).
2. On Mini, `pnpm run electron:build`.
3. Keep `dist/mac-arm64/Purdex.app` available; both runs copy from
   it.

### 5.2 Run A — unsigned bundle (spec §8.2)

1. On Air, `rm -rf /Applications/Purdex.app` (clean install).
2. Copy `dist/mac-arm64/Purdex.app` from Mini to
   `/Applications/Purdex.app`.
3. Confirm pre-state:
   ```
   codesign -dv /Applications/Purdex.app 2>&1
   # Expected: "code object is not signed at all"
   ```
4. Launch Purdex.app. Trigger dev update from
   Settings → Development.
5. Confirm SPA progress sequence: `downloading → extracting →
   applying`. **No** `signing` step.
6. Confirm Electron relaunches automatically with the new version.
7. Confirm post-state: `codesign -dv /Applications/Purdex.app 2>&1`
   still reports `code object is not signed at all`.

### 5.3 Run B — ad-hoc signed bundle (spec §8.3)

1. On Air, `rm -rf /Applications/Purdex.app` (fresh install — must
   not reuse the post-Run-A copy).
2. Copy `dist/mac-arm64/Purdex.app` from Mini to
   `/Applications/Purdex.app`.
3. Ad-hoc sign the freshly-installed copy:
   ```
   codesign --force --deep --sign - /Applications/Purdex.app
   ```
4. Confirm pre-state passes verify:
   ```
   codesign --verify --deep --strict --verbose=4 /Applications/Purdex.app
   # Expected: exit 0, output containing "valid on disk" and
   # "satisfies its Designated Requirement"
   ```
5. (Optional but recommended) Manufacture a fresh hash delta on
   Mini before launching Air's Purdex.app — touch a source file +
   re-build, so Air's build hash differs from the daemon's current
   source hash. Otherwise dev update reports "up to date" and
   step 6 has nothing to test.
6. Launch Air's Purdex.app. Trigger dev update.
7. Confirm SPA progress sequence: `downloading → extracting →
   applying`. **No** `signing` step.
8. Confirm Electron relaunches automatically with the new version.
   This success is the §3.4 same-path-relaunch claim under test —
   the **acceptance criterion**.
9. Confirm post-state verify **fails**:
   ```
   codesign --verify --deep --strict --verbose=4 /Applications/Purdex.app
   # Expected: non-zero exit code; stderr message indicating a
   # sealed-resource mismatch — typically of the form
   # "a sealed resource is missing or invalid" or
   # "resource modified" with a path inside Resources/app/out/...
   ```
   This failure is the documented Stage 1b limitation (§6.2,
   §3.4 redistribution scope) and is **expected**.

### 5.4 Failure modes that block the PR

If any of the following is observed, do not merge — investigate:

- Run A or Run B step 5/7 shows the `signing` step.
- Run A or Run B step 6/8 fails (Electron does not relaunch, or
  relaunches into an old version).
- Run B step 9 returns exit 0 (post-update verify still passes) —
  this would mean either the update did not actually mutate the
  bundle, or macOS's behaviour does not match §3.4 evidence.
- Run B step 9 fails with a different error class (e.g. a missing
  binary, a broken Mach-O) — this would indicate the dev update
  damaged the bundle in a way Stage 1b's safety claim does not
  cover.

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

- **Title**: `refactor(electron): retire runtime codesign — Stage 1b
  (#709)`
- **Header metadata** (top of body):
  - `Base: main`
  - `Depends on: none`
  - `Related: #709 (epic). Release ordering: PR #716 also targeted
    alpha.249, so Stage 1b's bump PR will land at alpha.250 or
    later — read VERSION on origin/main right before opening the
    bump PR.`
- **Body sections**:
  - **Summary**: 2-3 sentence why (Stage 1b retires
    detect/resign vestige; Option β scope).
  - **Spec / plan links**: relative paths to both docs.
  - **Behaviour delta**: paste spec §3.3 table.
  - **Test changes**: 39 → 26 with breakdown
    (signing.test.ts: 20 → 7; keybindings.test.ts: 19, unchanged).
  - **Manual verification**: link to spec §8.2 + §8.3 and plan §5,
    confirm Run A and Run B were executed on Air with expected
    outcomes (specifically, Run B step 9 returned non-zero — the
    documented expected outcome).
  - **Closes**: use full closing syntax for each issue (P2-8) so
    GitHub auto-closes both:
    ```
    Closes #712
    Closes #713
    ```
    (Comma-joined `Closes #712, #713` is documented to only
    auto-close the first.)
  - **Followups**: none expected; if codex review surfaces any,
    open issues and link here.
- **Reviewers**: codex via `/codex:review` and
  `/codex:adversarial-review` (two rounds per CLAUDE.md project
  workflow).

---

## 8. References

- Spec: `docs/specs/2026-04-29-electron-signing-stage1b-spec.md` v1.1
- Stage 0 spec: `docs/specs/2026-04-28-electron-signing-stage0-spec.md` v1.1
- Stage 0 plan: `docs/specs/2026-04-28-electron-signing-stage0-plan.md` v1.1
- Codex spec review: job `task-moivfw7n-a8x1ph` (8 findings, all addressed in spec v1.1)
- Issue #709 (epic), #712 (becomes obsolete), #713 (becomes obsolete)
