# Plan — x64 ad-hoc bundles crash at launch: missing entitlements

Date: 2026-09-22
Spec: [`2026-09-22-x64-adhoc-entitlements-spec.md`](./2026-09-22-x64-adhoc-entitlements-spec.md)
Branch: `worktree-x64-codesign`
Worktree: `/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-x64-codesign`

One phase, one PR. Estimated diff ≈ 300 lines across 8 files — well inside
the 800-line / 20-file limit.

## Files

| File | Change |
|---|---|
| `electron/entitlements.mac.plist` | new — three-key V8 template (spec §5.1) |
| `scripts/mac-sign.mjs` | new — importable signing helpers (spec §5.3) |
| `scripts/mac-sign_test.mjs` | new — TDD test, 4 cases (spec §6) |
| `scripts/build-electron.mjs` | use the helpers; assert every bundle; injectable collaborators (spec §5.4) |
| `electron/signing.test.ts` | replace the substring case with a `buildSignArgs` assertion (spec §5.5) |
| `package.json` | `build.mac.entitlements` + `entitlementsInherit`; `test:mac-sign` script |
| `docs/release/macos-signing.md` | Stage 1a → partial; record the app half |
| `docs/specs/2026-09-22-*` | spec + this plan |

## Tasks

Each task is one commit. TDD: the test commit precedes the implementation
commit and must be observed failing first.

### T1 — Entitlements file

Add `electron/entitlements.mac.plist` with exactly the three keys from
spec §5.1. Comments may reference this spec instead of the upstream issues
(spec §5.1: `codesign` normalizes the plist, so only the key/value set
matters).

**Verify:** `plutil -lint electron/entitlements.mac.plist` → `OK`, and

```
diff <(plutil -convert json -o - electron/entitlements.mac.plist) \
     <(plutil -convert json -o - <app-builder-lib>/templates/entitlements.mac.plist)
```

→ empty.

### T2 — Failing test

Write `scripts/mac-sign_test.mjs` per spec §6 (four cases, including the
nested-helper coverage and the G3 wiring case). Add
`"test:mac-sign": "node scripts/mac-sign_test.mjs"` to root `package.json`
scripts.

**Verify:** `pnpm run test:mac-sign` **fails** (module absent). Capture the
output in the commit message.

### T3 — `scripts/mac-sign.mjs`

Implement the five exports in spec §5.3. No top-level side effects: the
module must be safe to `import` from the test.

- `buildSignArgs` is pure; it must not touch the filesystem. It adds
  `--entitlements` and **omits** `--identifier`.
- `readEntitlements` returns `''` (not a throw) when the bundle has no
  entitlements — measured behaviour, `codesign` exits 0 in that case.
- `assertLibraryValidationDisabled` throws an `Error` naming the app path
  and what was missing.

**Verify:** `pnpm run test:mac-sign` passes, cases 1–3. Case 4 still fails
(it needs T4).

### T4 — Wire into the build

Rewrite `scripts/build-electron.mjs` to import from `scripts/mac-sign.mjs`:
sign with `ENTITLEMENTS_PATH`, and call `assertLibraryValidationDisabled()`
on every produced bundle — both the freshly signed path and the
`hasValidSignature()` early-return path (spec §5.4). `PDX_SKIP_MAC_SIGN=1`
returns before both. Export `signAndVerifyApp` with injectable
`sign` / `assert` collaborators so case 4 can drive it.

Update `electron/signing.test.ts` per spec §5.5: drop the
`'codesign'` / `'--verify'` substring assertions, assert instead that
`buildSignArgs()` yields `--options runtime` and an adjacent
`--entitlements <path>` pair. Keep the `PDX_MAC_SIGN_IDENTITY` assertion.

**Verify:**
- `node --check scripts/build-electron.mjs`
- `pnpm run test:mac-sign` — all four cases pass
- `pnpm --dir electron test` — green (this is the suite T4 would otherwise
  break; root `package.json` has no `test` script, so it is not run for you)

### T5 — `package.json` build config

Add `entitlements` + `entitlementsInherit` under `build.mac`, both pointing
at `electron/entitlements.mac.plist` (spec §5.2). Do **not** add
`hardenedRuntime` (spec §5.2 rationale).

**Verify:** `node -e "JSON.parse(require('fs').readFileSync('package.json','utf8'))"`.

### T6 — Full build + real-machine verification

Not a code commit; the gate before opening the PR.

**Preconditions, assert before starting:** `PDX_MAC_SIGN_IDENTITY` and
`PDX_SKIP_MAC_SIGN` must both be unset — `PDX_MAC_SIGN_IDENTITY` forces the
identity branch and would invalidate the G4 comparison. All paths below are
absolute, because the worktree and the main checkout both have a `dist/`
and the stale one is the easy mistake:

- `MAIN=/Users/wake/Workspace/wake/purdex`
- `WT=/Users/wake/Workspace/wake/purdex/.claude/worktrees/worktree-x64-codesign`

Every entitlements dump uses `codesign -d --entitlements - --xml` (without
`--xml` the format is a different, human-readable tree and cannot be
diffed).

1. **Baseline (pre-change, G4).** Dump entitlements of
   `$MAIN/dist/mac-arm64/Purdex.app` and its four
   `Contents/Frameworks/Purdex Helper*.app`. Save to the scratchpad.
   Also dump their `Identifier=` lines.
2. `cd $WT && pnpm run electron:build`.
3. For **both** `$WT/dist/mac/Purdex.app` and `$WT/dist/mac-arm64/Purdex.app`:
   dump entitlements for the app **and each of the four helpers** — three
   keys present in all ten bundles.
4. Diff the new arm64 dumps against the step-1 baseline → identical (G4).
5. Dump `Identifier=` for each nested bundle of both slices. x64's
   `Electron Framework` must now read `com.github.Electron.framework`, and
   each helper its own `dev.wake.purdex.helper.*` — i.e. x64 converged onto
   arm64's shape (spec §2, §7).
6. `codesign --verify --deep --strict --verbose=4` on both → valid.
7. `scp` the x64 bundle to air-2019, install, launch. Acceptance is **not**
   "the process stays up": the window must render the UI and be
   interactive (a renderer helper missing entitlements shows a blank
   window while the main process lives), and
   `~/Library/Logs/DiagnosticReports/` must gain no new `Purdex*.ips` —
   helper crash reports included.

Record every command's output in the PR body.

### T7 — Docs

Update `docs/release/macos-signing.md`: Stage 1a row → "🟡 partial", with a
sub-entry describing the app-side entitlements shipped here and an explicit
list of what remains (daemon plist, `Makefile release`,
`allow-dyld-environment-variables`). Link this spec.

## Review

- Spec + plan: one focused subagent review — **done, round 1 applied**
  (8 findings, all accepted; the `--identifier` finding was independently
  re-measured before acceptance).
- PR: one full subagent review after T6.
- No codex dispatch — pure build configuration, no runtime logic, no
  daemon/SPA surface.

## Out of scope / follow-ups

- Rebuilding and redeploying air-2019 to the current alpha — operational,
  after merge.
- Remaining Stage 1a work (daemon signing pipeline).
- The air-2019 bundle was hand-re-signed on 2026-09-22 to unblock the user;
  that machine's `/Applications/Purdex.app` is not reproducible from a
  build until it takes a rebuilt bundle.
