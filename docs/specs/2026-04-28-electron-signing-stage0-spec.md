# Electron Signing — Stage 0 Hotfix Spec

- **Version**: 1.0.0-alpha.248 (target bump after merge)
- **Date**: 2026-04-28
- **Spec revision**: v1.1 (2026-04-28) — incorporates codex spec review (job `task-moipomzf-ivpqjj`, 9 findings: 4 P1 + 4 P2 + 1 P3, all addressed).
- **Base**: `166bac19` (main @ alpha.247)
- **Author**: claude-code + wake
- **Status**: Draft (pending plan)
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

**Observed result** (alpha.247, 2026-04-28): manual restart shows the
old version, consistent with the rollback `catch` block having
completed or partially completed before SIGKILL took effect. Exact
timing of the rollback relative to SIGKILL is not relied on by this
hotfix; the goal is to prevent the SIGKILL in the first place.

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
**skip codesign when there is no signature to preserve**, while
distinguishing genuinely-unsigned bundles from situations where the
detection itself failed (those should propagate as errors, not be
silently treated as unsigned).

## 3. Proposed change

### 3.1 Three-state detection contract

`resignAppBundle()` gains a pre-flight detection step that classifies
the bundle into one of three states:

| State      | Decision                | Trigger                                                           |
| ---------- | ----------------------- | ----------------------------------------------------------------- |
| `signed`   | Proceed with re-sign    | `codesign -dv` exits with status `0`                              |
| `unsigned` | **Skip** re-sign (new)  | `codesign -dv` exits non-zero **AND** stderr contains `code object is not signed at all` |
| `unknown`  | **Throw** (existing rollback runs) | All other outcomes: `status === null` (signal kill), `error` populated (spawn failed), non-zero status with different stderr (e.g. `bundle format unrecognised`, `not a valid Mach-O`, etc.) |

Rationale:

- **Why `unknown` throws.** Silently treating "we don't know" as
  "skip" turns every detection edge case (PATH issues, broken
  codesign install, unexpected codesign errors, codesign killed by
  signal) into a stale-signature production. The `applyUpdate`
  `try/catch` already handles a thrown signing error by rolling back
  the file replacement. Routing `unknown` through the same path
  preserves rollback semantics for legitimately broken environments
  while specifically targeting the user's confirmed `code object is
  not signed at all` case.
- **Why match on stderr, not just exit code.** `codesign -dv`'s
  non-zero exits cover many distinct failure modes; only the one with
  the canonical "not signed at all" message is the case Stage 0
  intends to bypass.

### 3.2 Implementation shape

The detection logic is extracted as a named, testable helper. To
keep test wiring simple, the helper and `resignAppBundle` are exposed
via a `__testing` namespace export (no public API change):

```ts
type SignedState = 'signed' | 'unsigned' | 'unknown'

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

function resignAppBundle(): void {
  const appBundle = getAppBundlePath()
  if (!appBundle || process.env.PDX_SKIP_MAC_SIGN === '1') return

  const state = detectSignedState(appBundle)
  if (state === 'unsigned') return                        // skip
  if (state === 'unknown') {
    throw new Error('codesign preflight detection failed; aborting re-sign')
  }
  // state === 'signed': existing codesign + verify (unchanged)
  ...
}

export const __testing = { detectSignedState, resignAppBundle }
```

**Module imports unified to `node:` prefix.** As part of this change,
`electron/updater.ts` switches `import ... from 'child_process'` to
`import ... from 'node:child_process'` (line 2). This makes
`vi.mock('node:child_process')` reliably intercept both
`execFileSync` and `spawnSync` in tests. No runtime behaviour change.

### 3.3 Behaviour matrix

Reachable states after Stage 0:

| Pre-update bundle | `PDX_SKIP_MAC_SIGN` | `PDX_MAC_SIGN_IDENTITY` | Behaviour                                  | Change |
| ----------------- | ------------------- | ----------------------- | ------------------------------------------ | ------ |
| Non-darwin        | (any)               | (any)                   | `getAppBundlePath()` returns null → skip   | unchanged |
| Any               | `=1`                | (any)                   | Skip codesign                              | unchanged |
| Signed            | unset               | unset                   | Re-sign with ad-hoc `-`, then verify       | unchanged |
| Signed            | unset               | set                     | Re-sign with given identity, then verify   | unchanged |
| Unsigned (`code object is not signed at all`) | unset | unset            | **Skip codesign (new)**                    | **new** |
| Unsigned          | unset               | set                     | **Skip codesign (new)** — preflight wins   | **new** (see §3.4) |
| Detection error / unknown | unset       | (any)                   | Throw → existing rollback runs             | (was: blanket sign attempt) |

### 3.4 Precedence: preflight over forced identity

`PDX_MAC_SIGN_IDENTITY` does **not** override the unsigned preflight.
Even if a user sets `PDX_MAC_SIGN_IDENTITY="Developer ID Application: ..."`,
an unsigned running bundle still skips re-sign. Rationale:

- This is a hotfix; the architectural fix (Stage 1 bundle-swap)
  decouples signing from runtime entirely. Forced-identity users on
  unsigned running bundles are accepting Stage 0's "leave as
  unsigned" outcome as the lesser evil vs. the SIGKILL.
- Once Stage 1 ships, dev update never signs at runtime, so this
  precedence rule becomes moot.
- `PDX_SKIP_MAC_SIGN=1` remains the highest-priority bypass (skip
  unconditionally, do not even run preflight).

### 3.5 Why detection, not blanket removal

Three reasons to keep `resignAppBundle()` rather than delete it:

1. **Forward compatibility with Stage 1.** Stage 1 may still want to
   re-sign as a safety net during the bundle-swap rollout (defence in
   depth before the runtime resign is fully retired).
2. **Existing test contract** (`signing.test.ts:20-26`) asserts
   `resignAppBundle` exists in `updater.ts`. Removing it requires
   rewriting that test; this hotfix updates the assertion shape, not
   the existence claim.
3. **Local Developer ID dev environments** (Stage 3 and beyond) may
   maintain signed bundles and still benefit from the re-sign step
   while the bundle-swap refactor is pending.

## 4. Test contract changes

`electron/signing.test.ts` keeps three tests in restructured form,
plus new runtime tests for `detectSignedState` and `resignAppBundle`.

### 4.1 Static smoke (existing test, simplified)

The third static grep test is downgraded to a smoke guard — runtime
tests now own the behaviour contract:

```ts
it('updater exposes signing preflight + re-sign helpers', () => {
  const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
  expect(updater).toContain('detectSignedState')
  expect(updater).toContain('resignAppBundle')
  expect(updater).toContain("'node:child_process'")
})
```

Existing static tests for `package.json` and `build-electron.mjs`
(`signing.test.ts:8-18`) remain unchanged — they belong to a
different surface (build pipeline) and Stage 0 does not touch
`scripts/build-electron.mjs`.

### 4.2 Runtime tests for `detectSignedState`

Mocking strategy: `vi.mock('node:child_process')` plus `vi.mock('electron', ...)` to satisfy the top-level `import { app } from 'electron'`. The updater is `dynamic-import`ed inside each test after mocks are set, accessed via `__testing`.

Test cases (one per row):

| Mock outcome                                                            | Expected `detectSignedState` |
| ----------------------------------------------------------------------- | ---------------------------- |
| `{ status: 0, stderr: 'Identifier=dev.wake.purdex\n...' }`              | `'signed'`                   |
| `{ status: 1, stderr: '/Applications/Purdex.app: code object is not signed at all\n' }` | `'unsigned'`                 |
| `{ status: 1, stderr: 'bundle format unrecognized, invalid, or unsuitable\n' }` | `'unknown'`                  |
| `{ status: null, signal: 'SIGTERM' }`                                   | `'unknown'`                  |
| `{ error: new Error('ENOENT: codesign not found') }`                    | `'unknown'`                  |

### 4.3 Runtime tests for `resignAppBundle` integration

| Setup                                                              | Expected behaviour |
| ------------------------------------------------------------------ | ------------------ |
| `process.env.PDX_SKIP_MAC_SIGN = '1'`                              | No `spawnSync` or `execFileSync` call. |
| `getAppBundlePath()` returns null (non-darwin path)                | No `spawnSync` or `execFileSync` call. |
| Detection returns `'unsigned'`                                     | No `execFileSync` (codesign --sign) call. Function returns normally. |
| Detection returns `'unknown'`                                      | Throws. No `execFileSync` (codesign --sign) call. |
| Detection returns `'signed'`, `PDX_MAC_SIGN_IDENTITY` unset        | `execFileSync` invoked with `--sign -` and `--timestamp=none`, then `--verify`. |
| Detection returns `'signed'`, `PDX_MAC_SIGN_IDENTITY = 'X'`        | `execFileSync` invoked with `--sign X`, no `--timestamp=none`, then `--verify`. |

### 4.4 Coverage of acceptance §6

Each acceptance criterion in §6 maps to at least one runtime test in
§4.2 / §4.3. The signed-path acceptance is verified at mock level
only; runtime manual verification on a signed bundle is deferred to
Stage 2/3 (no signed Stage 0 setup exists).

## 5. Non-goals

- **Removing the runtime resign** — that is the Stage 1 architectural
  fix (bundle-swap refactor). Stage 0 only stops the bleeding for
  unsigned bundles.
- **Adding entitlements / Hardened Runtime configuration** — Stage 1.
- **Self-signed cert workflow** — Stage 2.
- **Apple Developer ID / notarization** — Stage 3.
- **Behaviour for signed bundles** — unchanged at runtime (Stage 0
  verifies signed-path mock contract only).
- **Reworking rollback semantics** in `applyUpdate` — Stage 1 takes
  this on holistically when the swap model changes.
- **Modifying `scripts/build-electron.mjs`** — out of scope. Stage 0
  touches `electron/updater.ts` and `electron/signing.test.ts` only.

## 6. Acceptance criteria

Verifiable in CI:

- New `detectSignedState` helper exists and is exported via
  `__testing` namespace.
- `electron/updater.ts` imports from `node:child_process` (not
  `child_process`).
- `electron/signing.test.ts` runtime tests for the 5 detection cases
  in §4.2 pass.
- `electron/signing.test.ts` runtime tests for the 6 `resignAppBundle`
  paths in §4.3 pass.
- Existing static tests for `package.json mac.identity` and
  `build-electron.mjs` continue to pass.
- `pnpm --prefix electron test` green.
- `pnpm exec electron-vite build` green (no type/build regressions).

Verifiable manually:

- For an unsigned `Purdex.app` (reproducible setup in §8.2), dev
  update flow reaches `app.relaunch()` and the new instance launches
  with the new `out/{main,preload,renderer}`.
- After update, `codesign -dv /Applications/Purdex.app` still reports
  unsigned (we did not accidentally sign it).

Not verified by Stage 0 (deferred):

- Observable runtime behaviour on a signed bundle. Stage 0 verifies
  signed-path contract at mock level; full runtime path lives with
  Stage 2/3.

## 7. Risk assessment

| # | Risk | Likelihood | Impact | Mitigation |
| - | ---- | ---------- | ------ | ---------- |
| R1 | False negative: detection classifies a real signed bundle as `unsigned` (stderr unexpectedly contains the canonical phrase) | Very low | Medium — re-sign skipped, signature stale after update | The phrase `code object is not signed at all` is documented codesign output; Apple has not changed it across recent macOS releases. Treated as design assumption, revisit if codesign output format changes. |
| R2 | False positive: detection classifies a corrupt/expired/revoked bundle as `signed` (status 0 but signature actually broken) | Low | Low | Stage 0 intentionally uses a weaker signature-presence check than `build-electron.mjs:hasValidSignature()` (which also runs `codesign --verify --deep --strict` and matches `Identifier=`). Updater only needs to decide whether a prior signature exists, not whether it is trusted/notarized/valid. The downstream `codesign --verify` step will catch broken signatures and throw, hitting rollback. |
| R3 | `unknown` state throws too eagerly in CI / sandbox environments where codesign behaves unexpectedly | Low | Low | Test suite covers `unknown` as a thrown error; rollback path is the existing one. Real-world `unknown` is rare on real macOS hardware. |
| R4 | Forced-identity users on unsigned bundles get silently skipped | Low | Low | §3.4 documents this as design intent. Stage 1 bundle-swap removes the rule. |
| R5 | macOS version drift: future codesign returns different exit codes or stderr phrasing | Low | Low | Detection is centralised in one helper with full test coverage; updating is a one-line change. |
| R6 | Hotfix lands but Stage 1 takes long → users live on a "duct-taped" flow | Medium | Low | Stage 1 explicitly tracked in #709 with no deferred dependency. Stage 0 is documented as transitional. |

## 8. Verification plan

### 8.1 Automated

`pnpm --prefix electron test` — runs the updated `signing.test.ts`
including all 11 runtime cases (5 detection + 6 resign integration).

### 8.2 Manual on Air (M1, the affected user setup)

Prerequisites — produce a stably-unsigned bundle:

1. On Mini, run `PDX_SKIP_MAC_SIGN=1 pnpm run electron:build`.
   This forces `scripts/build-electron.mjs:signAndVerifyApp` to early-
   return without signing.
2. Copy `dist/mac-arm64/Purdex.app` to Air's `/Applications/`.
3. Confirm pre-state on Air:
   ```
   codesign -dv /Applications/Purdex.app 2>&1
   # Expected: "/Applications/Purdex.app: code object is not signed at all"
   ```

Test:

4. Launch alpha.247 (or whatever version contains the bug + the
   Stage 0 fix candidate).
5. Trigger dev update from Settings → Development.
6. Confirm SPA progress sequence: `downloading → extracting →
   applying → signing` (note: `signing` IS the last visible step;
   `restarting` is never sent because `app.exit(0)` kills the process
   before IPC delivers — see `electron/updater.ts:213-216`).
7. Confirm Electron relaunches automatically with the new version.
8. Confirm post-state: `codesign -dv /Applications/Purdex.app 2>&1`
   still reports `code object is not signed at all` — we did not
   accidentally sign it.

### 8.3 Signed-bundle runtime path

Deferred. No signed Stage 0 setup exists in this project. Mock-level
contract is verified by §4.3 runtime tests; full runtime verification
is part of Stage 2 (self-signed cert) or Stage 3 (Developer ID).

## 9. Out-of-scope follow-ups (filed under #709)

- Stage 1: dev update bundle-swap refactor + entitlements + Hardened
  Runtime config.
- Stage 2: self-signed cert flow + cross-machine trust docs.
- Stage 3: Developer ID + notarization in CI release pipeline.
