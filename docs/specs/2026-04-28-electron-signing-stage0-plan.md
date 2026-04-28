# Electron Signing — Stage 0 Hotfix TDD Plan

- **Date**: 2026-04-28
- **Status**: Draft plan v1.1 — incorporates codex plan review (job `task-moiq52ft-3yvukg`, 8 findings: 4 P1 + 4 P2, all addressed).
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

- `scripts/build-electron.mjs` — explicitly out of scope per spec §5.
- `electron/main.ts`, `electron/preload.ts`, `electron/window-manager.ts` etc. — `resignAppBundle` is only referenced in `electron/updater.ts:190`; nothing else needs to change.
- `internal/module/dev/*` — daemon-side dev update endpoints unchanged.
- `spa/**` — no SPA changes; the renderer-side progress UI already labels `signing` (`spa/src/components/settings/DevEnvironmentSection.tsx:256`).
- `package.json` — Stage 1 territory.

### 0.3 Concurrent-session safety

Worktree is isolated. Per `feedback_concurrent_session_safety.md`, the only collision risk is the spec/plan/test files we own. No risk.

### 0.4 Verification toolchain — what is and isn't available

After `pnpm install --frozen-lockfile` from the worktree root:

| Tool | Path | Verdict |
| ---- | ---- | ------- |
| `vitest` | `pnpm --prefix electron test` | ✅ available, 22 existing tests pass |
| `electron-vite` | `pnpm exec electron-vite ...` | ✅ available (root devDependency) |
| `tsc` (electron pkg) | `pnpm --prefix electron exec tsc` | ❌ NOT available (electron pkg has only vitest as devDep) |
| `tsc` (spa pkg) | `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit` | ✅ available — typescript 5.9.3 in spa devDeps; works cross-prefix because `tsc` is a CLI not a workspace tool |
| `electron-vite build` | `pnpm exec electron-vite build` | ✅ available; produces `out/` (we will clean up before commit) |

Implication: standalone `pnpm --prefix electron exec tsc` is removed from gates throughout. Type checking happens via:
- vitest's ts-loader during `pnpm --prefix electron test` (catches type errors in `electron/**/*.ts` reachable from tests)
- `pnpm exec electron-vite build` for the full bundler-side type check
- optionally `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit` for explicit no-emit pass

---

## 1. Pre-flight checks (T0)

- [x] Worktree created at `.claude/worktrees/electron-signing-stage0-hotfix`.
- [x] Issue #709 filed.
- [x] Spec v1.1 committed (`7eef4929`).
- [x] Plan v1.1 committed (this file).
- [x] **`pnpm install --frozen-lockfile` run from worktree root** — required before any test/build command works in the worktree.

If a fresh subagent picks up this plan, **first action** is to verify `node_modules` exists; if not, run `pnpm install --frozen-lockfile` before T1.

---

## 2. Task sequence (TDD)

Each task ends with a single commit. RED commits in TDD are explicitly allowed for T2 (commit policy §2.0).

### 2.0 Commit policy clarification

The CLAUDE.md "每個 task 獨立 commit" rule does not require every commit to be green. TDD's RED→GREEN cycle requires the failing-test commit to land first as evidence the test actually fails for the right reason. T2 produces a **behaviour-RED** commit (assertions fail, not import errors); T3 turns it green.

The PR description must explicitly note this policy so reviewers don't flag the temporary failing tests.

### T1 — Switch updater to `node:` prefix imports

**Why first.** Tests in T2 mock `node:child_process`. Production import must be on the canonical specifier first.

**Change**: `electron/updater.ts:2`

```diff
- import { execFileSync } from 'child_process'
+ import { execFileSync, spawnSync } from 'node:child_process'
```

`spawnSync` is added now to avoid a churn diff in T3. `electron/tsconfig.json` does not enable `noUnusedLocals` and there is no lint script for the electron package, so the unused import in T1 is not a gate violation.

**Verification**:

```bash
pnpm --prefix electron test
```

Existing 22 tests must remain green. (Type check is implicit via vitest's loader; standalone `tsc` not used per §0.4.)

**Commit**: `refactor(electron/updater): unify imports to node: prefix`

---

### T2 — Add 11 runtime tests + smoke update + minimal stub (behaviour RED)

**Why a stub.** Per codex F3, RED must come from assertion mismatch, not from `__testing` being undefined and crashing test setup. T2 commits a stub that makes all 11 tests **runnable but failing on assertions**.

**Changes**:

#### 2.T2.A `electron/updater.ts` — minimal stub

Add at end of file (will be replaced by real implementation in T3):

```ts
type SignedState = 'signed' | 'unsigned' | 'unknown'

function detectSignedState(_appBundle: string): SignedState {
  return 'unknown'   // T3 replaces with real codesign -dv invocation
}

export const __testing = { detectSignedState, resignAppBundle }
```

Also add `_appBundle` to keep the parameter named for the test contract; existing `resignAppBundle` body is unchanged — the test cases for signed/unsigned will fail because the stub returns `'unknown'`, which causes `resignAppBundle` to throw rather than skip-or-resign appropriately. This is the desired behaviour-RED.

#### 2.T2.B `electron/signing.test.ts` — restructure

Keep static tests for `package.json mac.identity` and `build-electron.mjs` codesign references (`signing.test.ts:8-18`). Replace the third static grep with the spec §4.1 smoke version:

```ts
it('updater exposes signing preflight + re-sign helpers', () => {
  const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
  expect(updater).toContain('detectSignedState')
  expect(updater).toContain('resignAppBundle')
  expect(updater).toContain("'node:child_process'")
})
```

(Smoke test PASSES in T2 because the stub already contains `detectSignedState` and the import is `'node:child_process'`. So the **expected RED count after T2 is 11**, all behavioural — codex F2 resolved.)

Add a new `describe('updater signing preflight (runtime)', ...)` block:

```ts
import { describe, it, expect, beforeEach, afterEach, vi, type Mock } from 'vitest'
// import readFileSync, resolve as before for static tests

vi.mock('electron', () => ({
  app: {
    getPath: vi.fn((name: string) => {
      if (name === 'exe') return '/Applications/Purdex.app/Contents/MacOS/Purdex'
      if (name === 'temp') return '/tmp'
      return '/'
    }),
  },
}))

vi.mock('node:child_process', () => ({
  spawnSync: vi.fn(),
  execFileSync: vi.fn(),
}))

describe('updater signing preflight (runtime)', () => {
  let originalPlatform: PropertyDescriptor
  let originalEnv: NodeJS.ProcessEnv

  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
    originalPlatform = Object.getOwnPropertyDescriptor(process, 'platform')!
    Object.defineProperty(process, 'platform', { ...originalPlatform, value: 'darwin' })
    originalEnv = { ...process.env }
    delete process.env.PDX_SKIP_MAC_SIGN
    delete process.env.PDX_MAC_SIGN_IDENTITY
  })

  afterEach(() => {
    Object.defineProperty(process, 'platform', originalPlatform)
    process.env = originalEnv
  })

  // 5 detectSignedState cases + 6 resignAppBundle cases per spec §4.2 / §4.3
})
```

Test cases use this access pattern:

```ts
it('detectSignedState returns "signed" when codesign exits 0', async () => {
  const cp = await import('node:child_process')
  ;(cp.spawnSync as Mock).mockReturnValue({ status: 0, stderr: 'Identifier=dev.wake.purdex\n' })
  const { __testing } = await import('./updater')
  expect(__testing.detectSignedState('/Applications/Purdex.app')).toBe('signed')
})
```

**Critical**: codex F4 — `Object.defineProperty(process, 'platform')` MUST preserve descriptor flags. Pattern above (spread `originalPlatform` into the override, restore full descriptor in `afterEach`) is the correct shape. Do NOT use `vi.stubGlobal('process', ...)` — that replaces the entire process object and breaks unrelated test infrastructure.

#### 2.T2.C Capture the RED log

Before committing T2:

```bash
pnpm --prefix electron test 2>&1 | tee /tmp/stage0-red.log
# Expected:
#   - 22 existing tests pass (2 static + 20 keybindings)
#   - smoke test passes (sees stub strings)
#   - 11 new runtime tests fail with "Expected 'unsigned'/... but received 'unknown'" or thrown errors
# Total: ~14 pass + 11 fail
```

Confirm:
- All 11 RED failures reference assertion mismatches or thrown messages, NOT `Cannot read properties of undefined`.
- No other test regressed.

**Commit**: `test(electron/updater): add three-state preflight tests + stub (RED)`

The commit message body should explicitly state "T2 of TDD plan; assertion-level RED, T3 turns green."

---

### T3 — Implement detectSignedState + refactor resign (GREEN)

**Changes**: `electron/updater.ts`

1. Replace the T2 stub `detectSignedState` with the real spec §3.2 implementation:

```ts
function detectSignedState(appBundle: string): SignedState {
  const result = spawnSync('codesign', ['-dv', appBundle], {
    stdio: ['ignore', 'pipe', 'pipe'],
    encoding: 'utf8',
  })
  if (result.status === 0) return 'signed'
  if (result.status !== null && result.stderr?.includes('code object is not signed at all')) {
    return 'unsigned'
  }
  return 'unknown'
}
```

2. Refactor `resignAppBundle` (`electron/updater.ts:51-68`) per spec §3.2:

```ts
function resignAppBundle(): void {
  const appBundle = getAppBundlePath()
  if (!appBundle || process.env.PDX_SKIP_MAC_SIGN === '1') return

  const state = detectSignedState(appBundle)
  if (state === 'unsigned') return
  if (state === 'unknown') {
    throw new Error('codesign preflight detection failed; aborting re-sign')
  }
  // state === 'signed': existing codesign + verify chain (unchanged)
  const identity = process.env.PDX_MAC_SIGN_IDENTITY || '-'
  const signArgs = [
    '--force',
    '--deep',
    '--options', 'runtime',
    '--identifier', APP_ID,
    '--sign', identity,
  ]
  if (identity === '-') signArgs.push('--timestamp=none')
  signArgs.push(appBundle)

  execFileSync('codesign', signArgs, { stdio: 'inherit' })
  execFileSync('codesign', ['--verify', '--deep', '--strict', '--verbose=4', appBundle], { stdio: 'inherit' })
}
```

3. `__testing` namespace export at the file end remains as added in T2.

**Verification (RED→GREEN proof)**:

```bash
# Targeted: confirm signing.test.ts is fully green
pnpm --prefix electron exec vitest run signing.test.ts 2>&1 | tee /tmp/stage0-green.log

# Expected:
#   - 3 static tests pass (2 existing + 1 smoke)
#   - 11 runtime tests pass
#   - Total: 14 pass, 0 fail

# RED→GREEN diff
diff <(grep -E "(✓|×|FAIL)" /tmp/stage0-red.log | sort) \
     <(grep -E "(✓|×|FAIL)" /tmp/stage0-green.log | sort) || true
# Confirm: T2 RED tests are exactly the ones that flipped to ✓ in T3.

# Full electron test suite
pnpm --prefix electron test

# Full build (catches type errors at bundler level; cleans out/ after)
pnpm exec electron-vite build
rm -rf out  # leave worktree clean — out/ is gitignored anyway
```

All three commands must report success. If `electron-vite build` fails on type errors, fix in T3 before commit.

**Commit**: `feat(electron/updater): three-state preflight skips unsigned re-sign`

The commit message body lists which RED test names flipped to GREEN.

---

### T4 — Final verification

No code change. Confirm acceptance criteria from spec §6:

```bash
# 1. Tests
pnpm --prefix electron test

# 2. Build
pnpm exec electron-vite build && rm -rf out

# 3. Smoke read of updater.ts contract
grep -n "detectSignedState\|resignAppBundle\|node:child_process" electron/updater.ts
# Expect: detectSignedState definition, resignAppBundle dispatching three states,
#         and exactly one 'node:child_process' import.

# 4. Static + runtime test count
pnpm --prefix electron exec vitest run signing.test.ts --reporter verbose
# Expect: 14 pass (3 static + 11 runtime)
```

If all green, T4 produces no commit; the branch is ready for PR.

If anything red, **do not paper over** — go back to T2/T3 and adjust.

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

1. Mini: from main repo (post-merge), `PDX_SKIP_MAC_SIGN=1 pnpm run electron:build`
2. Copy `dist/mac-arm64/Purdex.app` to Air's `/Applications/`
3. `codesign -dv /Applications/Purdex.app 2>&1` → confirm `code object is not signed at all`
4. Launch this Purdex
5. Trigger dev update from Settings → Development
6. SPA progress reaches `signing` then app exits and relaunches
7. New version visible after relaunch
8. Post-state: `codesign -dv /Applications/Purdex.app 2>&1` still reports unsigned

**If any step 5-8 fails: do NOT bump alpha.248.** Open an issue, refer back to this plan and the spec. Mock-level test green ≠ runtime contract held.

The bump PR description MUST include a copy-paste of the manual verification result (steps 3, 6, 7, 8 outputs). If not present, bump is incomplete.

---

## 5. PR + bump protocol

### 5.1 Stage 0 PR

Per CLAUDE.md:

1. Push branch, open PR against `main` with title `fix(electron): unsigned-aware preflight in dev update resign (#709 Stage 0)`.
2. PR description references #709, links spec v1.1 + plan v1.1, notes T2 RED commit policy from §2.0.
3. Round-1 codex standard review.
4. Round-2 codex 3-parallel: attack / defense / file-health.
5. Resolve findings into table; merge when no P0/P1 outstanding.
6. After merge, run §4 manual verification before bump.

### 5.2 Bump PR (per `feedback_bump_base_origin_not_local`)

Stage 0 PR squash-merges to `origin/main`. Bump PR is created from a fresh worktree against the latest `origin/main`:

```bash
# In main repo (NOT this Stage 0 worktree)
EnterWorktree --name bump-alpha-248
git fetch origin main
git reset --hard origin/main
git log -1                  # must show the just-merged Stage 0 SHA

# Bump
# - VERSION → 1.0.0-alpha.248
# - package.json version → 1.0.0-alpha.248
# - spa/package.json version → 1.0.0-alpha.248
# - CHANGELOG.md → new section + manual verification result paste

git add -A
git commit -m "chore: bump version to 1.0.0-alpha.248"
git push -u origin worktree-bump-alpha-248
gh pr create --base main --title "chore: bump version to 1.0.0-alpha.248" ...
```

Per `feedback_bump_base_origin_not_local`: the `git reset --hard origin/main` is mandatory — without it, the worktree may carry a local-main state that includes parallel-session commits not yet on origin.

---

## 6. Risk register

| # | Risk | Likelihood | Impact | Mitigation |
| - | ---- | ---------- | ------ | ---------- |
| P1 | RED→GREEN flip lies — tests pass for the wrong reason (e.g. mock leaks across tests) | Low | Medium | `vi.resetModules()` + `vi.clearAllMocks()` in `beforeEach`; each test re-imports updater fresh; T3 verification explicitly diffs RED vs GREEN logs (§T3). |
| P2 | `process.platform` mock leaks past test boundary | Low | High | Use `getOwnPropertyDescriptor` to capture full descriptor, restore in `afterEach`. Documented in §T2.B. Codex F4 source. |
| P3 | Test for "throw on unknown" doesn't go through `applyUpdate` rollback (only tests `resignAppBundle` in isolation) | High | Low | Acceptable — `applyUpdate`'s try/catch is existing pre-tested surface; Stage 0 doesn't change it. Spec §4.4 marks this as deferred to Stage 1+. |
| **P4** | **Mock tests pass but manual §4 protocol fails on real unsigned bundle** | **Low** | **High** | **Manual §4 is mandatory before bump. Bump PR description must paste verification output. If §4 fails, bump is blocked and Stage 1 must be accelerated. Codex F7 source.** |
| P5 | Subagent runs commands without first running `pnpm install --frozen-lockfile` and gets "command not found" | Medium | Low | §0.4 lists the install precondition; T0 list includes the install as a checkpoint; subagent prompt will reference §0.4. Codex F1 source. |
| P6 | T2 RED commit accidentally lands on main without T3 (interrupted PR) | Low | Medium | PR is unitary (T1+T2+T3+T4 in one PR, squash-merge). Even if pushed mid-way, the PR is not mergeable until T3 turns tests green. CI gate would block. |
