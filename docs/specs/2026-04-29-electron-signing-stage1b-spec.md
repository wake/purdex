# Electron Signing — Stage 1b Spec (Option β)

- **Version**: 1.0.0-alpha.248 (worktree base; ship target alpha.250+, see §0.1)
- **Date**: 2026-04-29
- **Spec revision**: v1.1 (2026-04-29) — incorporates codex spec review (job `task-moivfw7n-a8x1ph`, 8 findings: 2 P1 + 4 P2 + 2 P3, all addressed).
- **Base**: `96bae3ce` (main @ alpha.248)
- **Author**: claude-code + wake
- **Status**: Draft (pending codex review)
- **Tracking**: #709 (epic) — Stage 1b deliverable

## 0. Preface

### 0.1 Ship target

The Stage 1b implementation PR must NOT modify `VERSION` or
`CHANGELOG.md`. A separate post-merge bump PR (a different worktree
per `feedback_bump_base_origin_not_local`) reads `VERSION` from latest
`origin/main` immediately before bumping. Given PR #716
(opencode-plugin-spawn-fix) also targets alpha.249, the resulting
shipped alpha is expected to be alpha.250 or later.

### 0.2 Why this stage exists

Stage 0 (alpha.248) shipped a three-state preflight that decides
whether to call codesign on the running bundle. It works — but the
underlying premise is wrong: this project never needed runtime codesign
at all. Stage 1b retires the entire codesign-at-runtime concept.

## 1. Context

Stage 0 (PR #711, alpha.248, 2026-04-28) added a `detectSignedState()`
preflight that classifies the running bundle into `signed`, `unsigned`,
or `unknown`. On the user's actual deployment (always-unsigned), the
preflight returns `unsigned` and skips the codesign call, preventing
the SIGKILL that PR #672 introduced.

The preflight is structurally sound for what it does, but it sits on
fragile foundations:

- It depends on stable `codesign -dv` exit-code semantics and stderr
  phrasing. Apple makes no stability guarantee.
- Confidence is mock-only — no signed Stage 0 setup exists in this
  project to runtime-test the `signed` path.
- The `unknown` classification throws, which propagates through the
  existing rollback in `applyUpdate` and shows the user a "broken
  update" modal. Every detection edge case (PATH issues, broken
  codesign install, codesign signal-killed) becomes a user-visible
  failure even when the underlying bundle is fine.
- Even when working as designed, the only thing the preflight does is
  decide whether to perform an action that, on inspection, has no
  legitimate purpose for this project.

The relevant facts about macOS behaviour (verified during Stage 0
exploration; recorded in #709 epic and `kickoff_signing_roadmap.md` —
"Pre-Stage-1b exploration findings"):

- macOS does NOT continuously re-verify `CodeResources` after first
  launch. Gatekeeper checks at first launch (and on quarantine xattr
  presence); AMFI gates Mach-O load, not arbitrary file writes.
- Dev update mutates `Resources/app/out/{main,preload,renderer}` —
  these are plain JS bundles loaded via Node's module system, not
  Mach-O. They are not individually signed; `CodeResources` merely
  lists their hashes.
- `app.relaunch()` on macOS uses execvp re-exec from the same path,
  not LaunchServices, so Gatekeeper doesn't re-check on relaunch.
- Dev update is gated by `PDX_DEV_MODE`; production users never hit
  this path.

In short: mutating `out/` on a previously-signed bundle does cause
`CodeResources` hash drift, but nothing in macOS's normal operation
notices. The runtime codesign that Stage 0 sometimes-skips was
re-stamping a hash record that the OS doesn't read.

## 2. Problem statement

The runtime codesign step (`resignAppBundle` + `detectSignedState`
preflight) is unnecessary, and its presence is a net liability:

- **Necessity**: zero. macOS does not enforce CodeResources integrity
  post-launch on normal dev/production code paths.
- **Surface area**: ~80 lines of `electron/updater.ts` + 17 mock-driven
  runtime tests in `electron/signing.test.ts`.
- **Failure modes added by keeping it**: every codesign-output drift
  on a future macOS release becomes a P0; every transient `codesign`
  spawn failure becomes a user-visible "update broken".
- **Stage 1a/2/3 do not depend on it**. Build-time signing
  (`scripts/build-electron.mjs`, `package.json mac.identity`,
  Stage 1a entitlements, Stage 3 notarization) is the entire signing
  story. The runtime path was always vestigial.

The fix is to delete the runtime codesign concept end-to-end:

- Remove the helpers and their tests.
- Remove the call site and the `signing` progress step.
- Add a static absence smoke test so a future PR cannot silently
  re-introduce the pattern.

## 3. Proposed change

### 3.1 Files modified

| File | Change |
| ---- | ------ |
| `electron/updater.ts` | Remove `node:child_process` import (line 2), `APP_ID` constant (line 12), `getAppBundlePath()` (lines 45-49), `SignedState` type + signing helpers (lines 51-107), `progress('signing'); resignAppBundle()` call site (lines 228-229), `__testing` export (line 260). |
| `electron/signing.test.ts` | Replace 3rd static test (presence assertion) with absence smoke. Delete the entire `describe('updater signing preflight (runtime)', ...)` block (17 tests). |
| `spa/src/components/settings/DevEnvironmentSection.tsx` | Remove `signing: 'Signing app…'` entry from `stepLabels` map (line 256) — dead entry once the updater no longer emits the `signing` step. This is not an i18n key removal; `stepLabels` is an inline `Record<string, string>` literal (no locale file involvement), and the render path falls back to the raw `updateStep` for unknown keys, so removing the entry cannot dangle. |

### 3.2 Symbols removed

From `electron/updater.ts`:

- `APP_ID` constant (was the codesign identifier)
- `getAppBundlePath()` (only consumer was `resignAppBundle`)
- `SignedState` type alias
- `PREFLIGHT_TIMEOUT_MS`, `NOT_SIGNED_PATTERN`, `stripAnsi()`
- `detectSignedState()`
- `resignAppBundle()`
- `__testing` namespace export

From `electron/signing.test.ts`:

- The 17 runtime tests under `describe('updater signing preflight (runtime)', ...)`:
  - 5 detectSignedState core cases
  - 5 detectSignedState resilience cases (case folding, ANSI, stdout
    fallback, irregular whitespace, timeout)
  - 1 timeout-options assertion
  - 6 resignAppBundle dispatch cases

Behaviour preserved (build-time path, NOT runtime):

- `package.json mac.identity` setting (Stage 1a will harden it)
- `scripts/build-electron.mjs` codesign+verify step (build pipeline)
- `PDX_SKIP_MAC_SIGN` and `PDX_MAC_SIGN_IDENTITY` env vars — still
  honoured by `build-electron.mjs`. They no longer have any
  runtime-time interpretation, but the build-time semantics are
  unchanged.

### 3.3 Behaviour delta

| Scenario | Before (alpha.248) | After (Stage 1b) |
| -------- | ------------------ | ---------------- |
| Dev update on unsigned bundle | preflight → unsigned → skip codesign → no observable codesign work | identical externally; ~10ms saved on `codesign -dv` spawn |
| Dev update on signed bundle (rare) | preflight → signed → re-sign + verify → fresh signature on disk | mutate `out/`, leave signature record stale on disk; macOS does not notice |
| `codesign` binary absent or broken | preflight → unknown → throw → rollback → user sees "update broken" | dev update succeeds; `codesign` is never invoked |
| `codesign -dv` killed by signal / hangs | preflight times out → unknown → throw → rollback | not invoked |
| User sets `PDX_MAC_SIGN_IDENTITY="Developer ID …"` | runtime path: ignored (preflight wins). Build path: applied. | runtime path: gone. Build path: applied (unchanged). |
| User sets `PDX_SKIP_MAC_SIGN=1` | runtime path: skip codesign. Build path: skip codesign at build. | runtime path: gone (no codesign to skip). Build path: skip at build (unchanged). |

### 3.4 Why mutating Resources/ post-launch is safe (scoped claim)

This is the core safety claim. **The scope is deliberately narrow.**
We claim safety only for the Stage 1b dev update path:

> The already-running local app mutates JS resources under
> `Contents/Resources/app/out/` and relaunches via
> `app.relaunch()` from the same installed path on the same
> machine. We do NOT rely on the modified signed bundle remaining
> valid for redistribution, re-quarantine, first launch on another
> machine, AirDrop/email/web download, or explicit
> `codesign --verify` / `spctl --assess` invocations.

Within that scope, evidence the claim holds:

1. **CodeResources is not re-checked during same-path relaunch on a
   first-launch-cleared bundle.** Apple's Gatekeeper / AMFI flow
   exercises signature checks at well-defined entry points:
   - First launch (Gatekeeper assessment + quarantine xattr
     translocation decision)
   - Re-quarantine entry (a re-downloaded / AirDropped / re-zipped
     copy gets a fresh quarantine xattr → Gatekeeper re-evaluates)
   - Mach-O load (AMFI integrity check on the executable + linked
     dylibs)
   - Explicit developer-driven `codesign --verify` / `spctl --assess`

   Same-path `app.relaunch()` on a bundle whose quarantine xattr was
   already cleared does not re-trigger Gatekeeper assessment. It does
   not periodically re-hash `Resources/` files between relaunches.

2. **`app.relaunch()` is execvp, not LaunchServices.** From Stage 0
   exploration: macOS's Electron `app.relaunch()` uses execvp to
   re-exec the same `Contents/MacOS/Purdex` binary. This bypasses
   Gatekeeper's first-launch checks (which were already passed when
   the user first opened the app).

3. **The Mach-O is not modified.** `Contents/MacOS/Purdex` (the binary
   AMFI cares about) is never touched by dev update. Only JS bundles
   under `Resources/app/out/` change. AMFI does not gate Node's
   `require()` resolution.

4. **Dev update is `PDX_DEV_MODE`-gated at the renderer-API and
   daemon-route layers.** Specifically:
   - `electron/preload.ts:122-148` only exposes `applyUpdate`,
     `checkUpdate`, `streamCheck`, `onUpdateProgress` on
     `window.electronAPI` when `process.env.PDX_DEV_MODE` is truthy.
   - `internal/module/dev/module.go:166-178` only registers the
     `/api/dev/update/*` HTTP routes when `PDX_DEV_MODE === "1"`.
   - The corresponding `dev:*` `ipcMain.handle(...)` handlers in
     `electron/main.ts:120-150` are registered unconditionally.
     They are reachable only via `ipcRenderer`, which the renderer
     cannot access (sandbox + contextIsolation), and the preload
     bridge omits the channels in production. Production renderers
     therefore have no path to invoke them, but the handlers
     themselves remain registered.

   Blast radius: developer machines with `PDX_DEV_MODE=1`. Production
   users cannot reach the path through the public API. See §7 R7 for
   the residual main-process handler surface.

5. **`codesign --verify --deep --strict` would notice.** A developer
   running this manually after dev update on a signed bundle would
   see hash mismatches. This is a known and documented limitation
   of dev update — Stage 1a's hardened-runtime + entitlements
   rebuild produces a fresh signed bundle from scratch and does not
   depend on dev update preserving signatures.

If a future macOS version starts continuously re-verifying
`CodeResources`, the response is bundle-swap (atomic-replace the whole
`.app`), not runtime codesign — that change would already be needed
for hardened-runtime to remain valid post-mutation.

## 4. Test contract changes

### 4.1 Replace 3rd static test with absence smoke

Currently (Stage 0):

```ts
it('updater exposes signing preflight + re-sign helpers', () => {
  const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
  expect(updater).toContain('detectSignedState')
  expect(updater).toContain('resignAppBundle')
  expect(updater).toContain("'node:child_process'")
})
```

After Stage 1b:

```ts
it('updater no longer ships runtime signing helpers', () => {
  const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
  expect(updater).not.toContain('detectSignedState')
  expect(updater).not.toContain('resignAppBundle')
  expect(updater).not.toMatch(/\bcodesign\b/)
  expect(updater).not.toContain('child_process')
})
```

Purpose: regression guard. Future PRs that re-introduce runtime
codesign will fail this test, surfacing intent for explicit review.

### 4.1b Add static progress-sequence guard (P2-2)

Adds one static test asserting `applyUpdate` emits exactly the three
expected progress literals, and no others:

```ts
it('updater applyUpdate emits exactly downloading → extracting → applying', () => {
  const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
  // Positive: each expected literal appears at least once
  expect(updater).toMatch(/progress\(\s*['"]downloading['"]\s*\)/)
  expect(updater).toMatch(/progress\(\s*['"]extracting['"]\s*\)/)
  expect(updater).toMatch(/progress\(\s*['"]applying['"]\s*\)/)
  // Negative: no other progress(...) literal sneaks in
  const literals = Array.from(
    updater.matchAll(/progress\(\s*['"]([^'"]+)['"]\s*\)/g),
    (m) => m[1],
  )
  expect(new Set(literals)).toEqual(new Set(['downloading', 'extracting', 'applying']))
})
```

Purpose: prevents `signing` (or any new step) from being silently
re-introduced and protects the SPA `stepLabels` map from drifting out
of sync with updater emits.

### 4.2 Existing static tests preserved unchanged

```ts
it('does not explicitly disable macOS signing', () => {
  // package.json mac.identity assertion — Stage 1a hardens this
})

it('signs and verifies the final moved app bundles', () => {
  // scripts/build-electron.mjs codesign+verify assertion — build path
})
```

These belong to the build-time signing surface, untouched by Stage 1b.

### 4.3 Runtime tests deleted

The 17 tests in `describe('updater signing preflight (runtime)', ...)`
test code being deleted. Deletion is implicit; the tests go with the
helpers.

### 4.4 No new runtime tests

There is no new runtime behaviour to test. The "happy path" of
`applyUpdate` minus the signing step is already covered by the
existing dev update flow at the integration level (Stage 0 §8.2-style
manual verification).

## 5. Non-goals

- **Daemon (`pdx`) changes** — Stage 1a/2/3 territory. Daemon never had
  runtime codesign, will never grow one. No diff to `internal/module/dev/*`.
- **Entitlements / Hardened Runtime** — Stage 1a (combined PR with
  Stage 2).
- **`scripts/build-electron.mjs` changes** — out of scope. Build-time
  signing pipeline is unchanged.
- **`package.json mac.identity` changes** — Stage 1a hardens; Stage 1b
  leaves alone.
- **Self-signed cert workflow / cross-machine trust docs** — Stage 2.
- **Apple Developer ID / notarization** — Stage 3.
- **DMG packaging** — locked NO per 2026-04-29 distribution decision
  (brew + curl).
- **Reworking rollback semantics in `applyUpdate`** — the existing
  rollback `catch` block remains; only one of its trigger conditions
  (codesign throw) goes away.

## 6. Acceptance criteria

### 6.1 Verifiable in CI

- `electron/updater.ts` does not import `node:child_process` or
  `child_process`.
- `electron/updater.ts` does not contain `codesign`,
  `detectSignedState`, `resignAppBundle`, `__testing`, `getAppBundlePath`,
  `SignedState`, `PREFLIGHT_TIMEOUT_MS`, `NOT_SIGNED_PATTERN`,
  `stripAnsi`, or `APP_ID`.
- `electron/updater.ts` does not emit `progress('signing')`.
- `electron/updater.ts` emits exactly the progress literals
  `'downloading'`, `'extracting'`, `'applying'` (no other steps;
  no order regression). Static assertion in `signing.test.ts` greps
  for these three string literals and asserts no other
  `progress('...')` call exists.
- `electron/preload.ts` continues to gate `applyUpdate`,
  `checkUpdate`, `streamCheck`, and `onUpdateProgress` behind
  `process.env.PDX_DEV_MODE` (static grep assertion).
- `internal/module/dev/module.go` continues to gate
  `/api/dev/update/*` route registration on `PDX_DEV_MODE === "1"`
  (static grep assertion).
- `electron/signing.test.ts` static absence smoke (§4.1) passes.
- `electron/signing.test.ts` 2 existing static tests (§4.2) still pass.
- `spa/src/components/settings/DevEnvironmentSection.tsx` `stepLabels`
  has no `signing` entry.
- `pnpm --prefix electron test` green. Expected count drops from 39
  to 23: `signing.test.ts` from 20 → 4 (2 existing static preserved
  + 1 new absence smoke replaces the old 3rd presence test + 1 new
  progress-sequence guard from §4.1b); `keybindings.test.ts` 19
  unchanged.
- `pnpm exec electron-vite build` green (no type/build regressions).
- `pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit`
  green (no dangling references).
- `pnpm --prefix spa run lint` green (DevEnvironmentSection edit must
  pass eslint).
- `pnpm --prefix spa run build` green (SPA bundles cleanly).

### 6.2 Verifiable manually

**Unsigned bundle path** (project's actual deployment shape):

- Dev update flow shows SPA progress `downloading → extracting →
  applying` (no `signing` step), Electron relaunches automatically,
  and the new instance loads with new `out/{main,preload,renderer}`.
- After update, `codesign -dv /Applications/Purdex.app` reports the
  same state as pre-update (`code object is not signed at all`).

**Ad-hoc signed bundle path** (Stage 1b gate; see §8.3):

- Pre-state: `codesign --verify --deep --strict --verbose=4` passes
  on the ad-hoc-signed bundle.
- Dev update flow shows the same `downloading → extracting →
  applying` sequence (no `signing` step), and Electron relaunches.
- Post-state: `codesign --verify --deep --strict --verbose=4` is
  **expected to fail** with a resource hash mismatch in `Resources/`.
  This is documented as expected behaviour and the §3.4 safety
  scope (we do not rely on the modified bundle remaining
  cryptographically intact).

### 6.3 Not verified by Stage 1b (deferred)

- Runtime behaviour on a Developer-ID-signed and notarized bundle.
  No such setup exists in Stage 1b; Stage 3 produces it from clean
  builds, never via dev update.
- Long-running multi-update soak (more than one back-to-back dev
  update on the same bundle).

## 7. Risk assessment

| # | Risk | Likelihood | Impact | Mitigation |
| - | ---- | ---------- | ------ | ---------- |
| R1 | Signed bundle's `CodeResources` becomes stale after dev update — bundle fails `codesign --verify --deep --strict` | Inevitable on signed installs | Low for same-machine dev relaunch; **High** if the modified bundle is redistributed, AirDropped, transferred, or re-quarantined | §3.4 explicitly limits the safety claim to same-path same-machine relaunch on a first-launch-cleared bundle. Stage 1a's hardened-runtime build produces fresh signatures from scratch; Stage 1b documents that dev update is not a redistribution mechanism. PR description and CHANGELOG (bump PR) both call this out. |
| R2a | **Current** Gatekeeper / quarantine entry points re-evaluate the modified bundle and refuse to launch — e.g. user re-zips the dev-updated `.app` for a colleague, or downloads a new copy that picks up quarantine xattr | Low in normal dev workflow | High when triggered | Documented; Stage 1b is dev-only, not for distribution. Users wanting to ship a dev build to another machine must rebuild from source. |
| R2b | **Future** macOS starts enforcing resource envelope during same-path same-machine relaunch on a first-launch-cleared bundle | Very low | Medium / High | Theoretical; no precedent in 15+ years. Response: bundle-swap (atomic replace whole `.app`), not runtime codesign — runtime codesign on a running bundle is exactly the SIGKILL pattern Stage 0 fixed. |
| R3 | Lose 17 runtime tests' coverage | N/A | None | Tests cover deleted code. Static absence smoke (§4.1) blocks accidental re-introduction. |
| R4 | Followup #712 (darwin integration test for Stage 0 preflight) becomes obsolete | High | Low | Stage 1b PR explicitly closes #712 — preflight is gone, nothing to integration-test. Same for #713 (`APP_ID` deleted). |
| R5 | A user is depending on `PDX_SKIP_MAC_SIGN=1` or `PDX_MAC_SIGN_IDENTITY` taking effect at dev-update time | Very low | Negligible | Both env vars retain build-time semantics in `scripts/build-electron.mjs`. CHANGELOG (bump PR) notes the runtime-time interpretation removal. |
| R6 | Stage 1a/2/3 surfaces a need to re-sign at runtime that we didn't anticipate | Low | Low | None of the Stage 1a/2/3 designs require it (#709 epic). If discovered, re-add as a focused, well-tested feature instead of carrying vestigial code "just in case". |
| R7 | Production-built `electron/main.ts` keeps `dev:*` `ipcMain.handle(...)` registrations even when `PDX_DEV_MODE` is unset (§3.4 claim 4) | Always present in production builds | Negligible | Renderer cannot reach `ipcMain` directly (sandbox + contextIsolation), and `electron/preload.ts` does not expose the `dev:*` channels in production. The handlers are inert in practice. Out of scope for Stage 1b; tracked as a hardening followup if needed. |

## 8. Verification plan

### 8.1 Automated

```
pnpm install --frozen-lockfile          # from worktree root
pnpm --prefix electron test             # 22 tests, all green
pnpm exec electron-vite build           # no regressions; out/ produced
pnpm --prefix spa exec tsc -p ../electron/tsconfig.json --noEmit
pnpm --prefix spa run lint
pnpm --prefix spa run build
```

### 8.2 Manual on Air (M1)

Same protocol as Stage 0 §8.2, simplified:

1. On Mini, `pnpm run electron:build` — no `PDX_SKIP_MAC_SIGN`
   needed (build-time signing path unchanged; this still produces an
   unsigned bundle in the absence of `PDX_MAC_SIGN_IDENTITY`).
2. Copy `dist/mac-arm64/Purdex.app` to Air's `/Applications/`.
3. Confirm pre-state on Air:
   ```
   codesign -dv /Applications/Purdex.app 2>&1
   # Expected: "code object is not signed at all" (matches project's
   # default unsigned shape)
   ```
4. Launch Stage-1b alpha (alpha.250+).
5. Trigger dev update from Settings → Development.
6. Confirm SPA progress sequence: `downloading → extracting → applying`
   — no `signing` step.
7. Confirm Electron relaunches automatically with the new version.
8. Confirm post-state: `codesign -dv /Applications/Purdex.app 2>&1`
   reports the same as pre-update.

### 8.3 Ad-hoc signed bundle manual verification (Stage 1b gate)

This step is **not** deferred — it directly exercises the §3.4
"signed bundle on same machine" claim.

1. On Mini, `pnpm run electron:build`. Copy
   `dist/mac-arm64/Purdex.app` to Air's `/Applications/`.
2. On Air, ad-hoc sign the locally-installed copy:
   ```
   codesign --force --deep --sign - /Applications/Purdex.app
   ```
3. Confirm pre-state passes verify:
   ```
   codesign --verify --deep --strict --verbose=4 /Applications/Purdex.app
   # Expected: "valid on disk" + "satisfies its Designated Requirement"
   ```
4. Launch Stage-1b alpha (alpha.250+).
5. Trigger dev update from Settings → Development.
6. Confirm SPA progress sequence: `downloading → extracting →
   applying` — no `signing` step.
7. Confirm Electron relaunches automatically with the new version
   (this is the §3.4 same-path-relaunch claim under test).
8. Confirm post-state verify **fails** with resource hash mismatch:
   ```
   codesign --verify --deep --strict --verbose=4 /Applications/Purdex.app
   # Expected: non-zero exit, message like
   # "a sealed resource is missing or invalid" or
   # "resource modified" against Resources/app/out/...
   ```
   This failure is the documented Stage 1b limitation (§6.2,
   §3.4 redistribution scope). The acceptance criterion is that
   relaunch succeeded despite this — not that verify still passes.

If step 7 fails (relaunch broken on ad-hoc signed bundle), Stage 1b
is not safe to ship and the §3.4 same-path-relaunch claim does not
hold for ad-hoc signed bundles. Investigate before merging.

### 8.4 Developer-ID / notarized bundle

Deferred to Stage 3 (which produces such bundles by clean build).
Stage 1b does not claim or test this path.

## 9. Out-of-scope follow-ups (filed under #709)

- Stage 1a + Stage 2 (combined PR): `package.json mac` entitlements +
  `mac.hardenedRuntime: true` + `scripts/build-electron.mjs
  --entitlements` + `Makefile release` daemon signing + self-signed
  cert workflow + cross-machine trust docs.
- Stage 3: GitHub Actions macOS runner + `xcrun notarytool submit
  --wait` + `stapler staple` for `.app` (not daemon).
- Issue #712 (darwin integration test for Stage 0 preflight) — close
  as obsolete in Stage 1b PR.
- Issue #713 (`APP_ID` drift) — close as obsolete in Stage 1b PR
  (constant is deleted).
