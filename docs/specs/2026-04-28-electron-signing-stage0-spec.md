# Electron Signing — Stage 0 Hotfix Spec

- **Version**: 1.0.0-alpha.248 (target bump after merge)
- **Date**: 2026-04-28
- **Spec revision**: v1.0 (initial)
- **Base**: `166bac19` (main @ alpha.247)
- **Author**: claude-code + wake
- **Status**: Draft (pending codex spec review)
- **Tracking**: #709 (epic) — Stage 0 deliverable

## 1. Context

PR #672 (commit `7cad0823`, alpha.234) added `resignAppBundle()` to
`electron/updater.ts:applyUpdate` to keep the app bundle signature
valid after dev update mutates `out/main`, `out/preload`, `out/renderer`.
The function unconditionally runs:

```
codesign --force --deep --options runtime \
  --identifier dev.wake.purdex --sign - <bundle>
```

on the **currently running** `Purdex.app`.

For setups where the bundle has never been signed (the user's actual
deployment — `codesign -dv /Applications/Purdex.app` reports `code
object is not signed at all`), this is harmful:

1. codesign rewrites `Contents/MacOS/Purdex`, the running Mach-O whose
   pages are mapped into the live process.
2. macOS AMFI detects the running executable was modified and SIGKILLs
   the process before `app.exit(0)` can run.
3. `app.relaunch()` is never reached — no new instance launches.
4. The renaming of `out/{main,preload,renderer}` already completed
   before signing started, so files are in the new state — but the
   `catch` block partially rolled back before SIGKILL took effect, so
   manual restart shows the **old** version.

User-visible symptom (alpha.247, 2026-04-28):

- SPA progress reaches `applying`, then disappears
- DiagnosticsReporter (macOS crash collector) spawns
- App does not relaunch
- Manual restart shows the old version

The dev update flow had been working in the unsigned state for the
entire project history (since the original `feat: dev auto-update
system` at `a082e100`). PR #672's intent — "keep bundle signature
valid after mutation" — only applies when the bundle **is** signed;
on unsigned bundles the re-sign step is gratuitous and now load-
bearingly broken.

## 2. Problem statement

`resignAppBundle()` runs unconditionally regardless of whether the
target bundle was signed before the update. Re-signing an unsigned
running bundle on macOS:

- requires writing to a Mach-O whose pages are live-mapped
- triggers AMFI's "code modified at runtime" enforcement → SIGKILL
- aborts the entire `applyUpdate` flow before `app.relaunch()`

The fix is **not** to make codesign safer (we cannot sign a running
binary safely on macOS — that is a Stage 1 architectural concern,
addressed by the bundle-swap refactor). The Stage 0 fix is to
**skip codesign when there is no signature to preserve**.

## 3. Proposed change

### 3.1 Behaviour

`resignAppBundle()` gains a pre-flight detection step. If the bundle
is currently unsigned, return early without invoking codesign:

```
function resignAppBundle(): void {
  const appBundle = getAppBundlePath()
  if (!appBundle || process.env.PDX_SKIP_MAC_SIGN === '1') return
  if (!isAppBundleSigned(appBundle)) return  // ← new
  // ... existing codesign + verify
}
```

Detection uses `codesign -dv <bundle>` via `spawnSync`:

- exit code 0 → bundle is signed → proceed with re-sign (existing
  behaviour preserved for signed builds, including future Stage 2/3
  Developer ID setups)
- exit code non-zero (typically 1 with stderr `code object is not
  signed at all`) → skip; bundle stays unsigned post-update

The detection function is extracted as a named, testable helper:

```
function isAppBundleSigned(appBundle: string): boolean {
  const result = spawnSync('codesign', ['-dv', appBundle], {
    stdio: ['ignore', 'ignore', 'ignore'],
  })
  return result.status === 0
}
```

### 3.2 Why detection, not blanket removal

Three reasons to keep `resignAppBundle()` rather than delete it:

1. **Forward compatibility with Stage 1.** Stage 1 may still want to
   re-sign as a safety net during the bundle-swap rollout (defence in
   depth before the runtime resign is fully retired).
2. **Existing test contract** (`signing.test.ts:20-26`) asserts
   `resignAppBundle` exists in `updater.ts`. Removing it requires
   rewriting that test; out of scope for a hotfix.
3. **Local Developer ID dev environments** (Stage 3 and beyond) may
   maintain signed bundles and still benefit from the re-sign step
   while the bundle-swap refactor is pending.

### 3.3 Behaviour matrix after change

| Bundle state pre-update | `PDX_SKIP_MAC_SIGN=1` | Behaviour                  |
| ----------------------- | --------------------- | -------------------------- |
| Unsigned                | (any)                 | Skip codesign (new)        |
| Signed                  | Set                   | Skip codesign (unchanged)  |
| Signed                  | Unset                 | Re-sign + verify (unchanged) |
| Non-darwin              | (any)                 | Return early (unchanged)   |

## 4. Test contract changes

`electron/signing.test.ts` currently has three tests. Stage 0 keeps
all three with the third updated to reflect the new contract:

**Existing test (renamed/updated):**

```ts
it('re-signs the app bundle only when previously signed', () => {
  const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
  expect(updater).toContain('resignAppBundle')
  expect(updater).toContain('isAppBundleSigned')   // ← new requirement
  expect(updater).toContain('codesign')
  expect(updater).toContain('--identifier')
  expect(updater).toContain('dev.wake.purdex')
})
```

**New runtime test** for the detection function (mocking `spawnSync`):

```ts
it('isAppBundleSigned returns false when codesign reports unsigned', () => {
  // spawnSync returns { status: 1 } when codesign -dv exits non-zero
  // resignAppBundle short-circuits — no codesign --sign invocation
})

it('isAppBundleSigned returns true when codesign reports signed', () => {
  // spawnSync returns { status: 0 }
  // resignAppBundle proceeds to invoke codesign --sign
})
```

The runtime tests require extracting `isAppBundleSigned` and
`resignAppBundle` such that `spawnSync` and `execFileSync` can be
substituted (either via `vi.mock('child_process')` or DI).
**Preferred approach: `vi.mock('node:child_process')`**, matching the
style already used elsewhere in this codebase.

## 5. Non-goals

- **Removing the runtime resign** — that is the Stage 1 architectural
  fix (bundle-swap refactor). Stage 0 only stops the bleeding for
  unsigned bundles.
- **Adding entitlements / Hardened Runtime configuration** — Stage 1.
- **Self-signed cert workflow** — Stage 2.
- **Apple Developer ID / notarization** — Stage 3.
- **Behaviour for signed bundles** — unchanged. If a user has a
  Developer-ID-signed Purdex.app today, Stage 0 does not affect
  their dev update flow (broken or otherwise).
- **Reworking rollback semantics** in `applyUpdate` — Stage 1 takes
  this on holistically when the swap model changes.

## 6. Acceptance criteria

- `resignAppBundle()` short-circuits before invoking codesign when
  `codesign -dv <bundle>` exits non-zero on the running bundle.
- For an unsigned `Purdex.app`, dev update flow reaches `app.relaunch()`
  and the new instance launches with the new `out/{main,preload,renderer}`.
- For a signed `Purdex.app` (hypothetical Stage 2/3 setup), no
  observable behaviour change.
- `PDX_SKIP_MAC_SIGN=1` continues to bypass everything.
- `electron/signing.test.ts` passes with the updated contract.
- New runtime tests for `isAppBundleSigned` pass.
- `pnpm --prefix electron test` green.
- No changes to daemon-side build/check/download endpoints.
- No changes to `scripts/build-electron.mjs` (Stage 0 scope is
  `electron/updater.ts` + `electron/signing.test.ts` only).

## 7. Risk assessment

| Risk | Likelihood | Impact | Mitigation |
| ---- | ---------- | ------ | ---------- |
| Detection misclassifies a signed bundle as unsigned (false negative on `-dv`) | Low | Medium — re-sign would be skipped, signature stale after update | `codesign -dv` is the canonical detection tool used in `build-electron.mjs:hasValidSignature`; same primitive. |
| `spawnSync` itself throws on non-darwin | Low | Low | `getAppBundlePath()` already returns `null` on non-darwin; detection never runs. |
| Future signed setups regress because we skipped a re-sign | Low | Low | Stage 1 retires this path entirely; window is short. |
| Hotfix lands but Stage 1 takes long → users live on a "duct-taped" flow | Medium | Low | Stage 1 explicitly tracked in #709; Stage 0 is documented as transitional. |

## 8. Verification plan

1. Unit: `pnpm --prefix electron test` — all tests including new
   runtime tests pass.
2. Manual on Air (M1, unsigned bundle, the affected user setup):
   - Trigger dev update from a fresh alpha.247 → alpha.248 build.
   - Confirm SPA progress reaches `restarting`.
   - Confirm app relaunches automatically with new version.
   - Confirm `codesign -dv /Applications/Purdex.app` still reports
     unsigned (we did not accidentally sign it).
3. Optional manual on a signed bundle: skip — no signed setup exists
   in this project at Stage 0; signed-path coverage is a Stage 2
   concern.

## 9. Out-of-scope follow-ups (filed under #709)

- Stage 1: dev update bundle-swap refactor + entitlements.
- Stage 2: self-signed cert flow.
- Stage 3: Developer ID + notarization.
