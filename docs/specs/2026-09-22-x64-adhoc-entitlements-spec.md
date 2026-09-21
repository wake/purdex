# x64 ad-hoc bundles crash at launch: missing entitlements

Date: 2026-09-22
Status: Reviewed (round 1 applied)

## 1. Problem

`Purdex.app` built for **x64** cannot launch on an Intel Mac. `dyld` aborts
before `main()`:

```
Termination Reason: Namespace DYLD, Code 1 Library missing
Library not loaded: @rpath/Electron Framework.framework/Electron Framework
Reason: ... code signature in '.../Electron Framework' not valid for use in
process: mapping process and mapped file (non-platform) have different Team IDs
```

Observed 2026-09-22 on **air-2019** (Intel, macOS 14.8.9) with the
alpha.353 x64 bundle installed at `/Applications/Purdex.app`. The arm64
bundle on air-2026 is unaffected.

The error message is misleading: nothing is wrong with Team IDs (both the
main executable and the framework are ad-hoc signed, both have
`TeamIdentifier=not set`), and the bundle is intact —
`codesign --verify` reports `valid on disk`. The real failure is
**Library Validation**.

## 2. Root cause

Measured, not inferred. Comparison of the two slices produced by the *same*
build run (`dist/mac` and `dist/mac-arm64`, both 2026-09-16 00:3x):

| | x64 (crashes) | arm64 (works) |
|---|---|---|
| signature | ad-hoc | ad-hoc |
| `TeamIdentifier` | not set | not set |
| CodeDirectory flags | `0x10002 (adhoc,runtime)` | `0x10002 (adhoc,runtime)` |
| special slots | `hashes=4+3` | `hashes=3+7` |
| `com.apple.security.cs.disable-library-validation` | **absent** | **present** |

The `runtime` flag (Hardened Runtime) enables Library Validation, which
requires every loaded library to share the main executable's Team ID *or*
be a platform binary. Two independently ad-hoc-signed Mach-Os do not
satisfy that, so `Electron Framework` is rejected. The arm64 slice escapes
because it carries `com.apple.security.cs.disable-library-validation`,
which switches the check off. The x64 slice carries **no entitlements at
all**.

Library Validation is a **per-process** property, decided by the
entitlements of the executable the process was started from. Two
consequences shape the rest of this spec:

- `Electron Framework` itself needs no entitlements. The working arm64
  bundle's framework has none either — verified — which also rules out the
  competing hypothesis "the framework needs to be entitled".
- The bundle starts **five** kinds of process, not one: the main app plus
  the four `Purdex Helper*.app` bundles (renderer, GPU, plugin, default).
  Each is its own executable and each needs Library Validation off. Any
  fix and any verification must cover the helpers, not just the top-level
  app — a renderer helper that fails this way shows up as a blank window,
  not as an app that refuses to start.

### Why the two slices differ

Traced through the build pipeline:

1. `pnpm run electron:build` → `scripts/build-electron.mjs`, which invokes
   `electron-builder --mac --<arch>` once per architecture.
2. `app-builder-lib/out/macPackager.js:215` computes
   `fallBackToAdhoc = (arch === Arch.arm64 || arch === Arch.universal) && !forceCodeSigning`.
   With no signing identity configured, the `noIdentity` branch at
   `macPackager.js:249` ad-hoc-signs **arm64 only**; x64 falls through to
   `macPackager.js:253-255`, which reports and `return false`s — x64 is
   left **unsigned** by electron-builder. (The upstream Electron x64
   prebuilt is itself unsigned: `codesign -dv` on the extracted
   `~/Library/Caches/electron/electron-v41.0.4-darwin-x64.zip` returns
   `code object is not signed at all`.)
3. When electron-builder ad-hoc-signs arm64 it passes per-file
   entitlements — `getOptionsForFile()` (`macPackager.js:346-378`) finds no
   `customSignOptions.entitlements` and no `entitlements.mac.plist` in
   `buildResourcesDir` (`build/` holds only icns files), so it falls back
   to `getTemplatePath("entitlements.mac.plist")`, the three-key V8
   template. That is where arm64's `disable-library-validation` comes from,
   for the app *and* for its helpers via the inherit path
   (`macPackager.js:368-377`, same template).
4. `scripts/build-electron.mjs:43` then calls `signAndVerifyApp()` on each
   output. arm64 already has a valid `Identifier=dev.wake.purdex`
   signature, so the `hasValidSignature()` early-return at line 23 keeps
   it. x64 is unsigned, so the script signs it itself with:

   ```
   codesign --force --deep --options runtime --identifier dev.wake.purdex \
            --sign - --timestamp=none <app>
   ```

   **`--entitlements` is missing.** Hardened Runtime is switched on with an
   empty entitlement set, which is exactly the crashing configuration.

So the defect is ours, in `scripts/build-electron.mjs`, not in
electron-builder. It has existed since `7cad0823`
("fix(electron): stabilize mac signing for updates") introduced the
`--options runtime` ad-hoc sign; it went unnoticed because air-2026
(arm64) is the only machine that regularly runs a freshly built bundle.

`hasValidSignature()`'s early return is not itself defective: each build
moves a *fresh* bundle out of `dist-<arch>/`, so x64 is always unsigned and
arm64 is always electron-builder-signed. Its one weakness — it compares
only the identifier and never looks at entitlements — is what let this ship
silently, and is closed by §5.4.

This is the unshipped half of **Stage 1a** of
[`docs/release/macos-signing.md`](../release/macos-signing.md), whose
deliverable list already names it: *"`scripts/build-electron.mjs` — codesign
command gains `--entitlements`"*.

### Verification of the diagnosis

On air-2019, re-signing the installed alpha.353 bundle changed one thing
and fixed the crash:

```
codesign --force --deep --sign - --options runtime \
         --entitlements <plist> /Applications/Purdex.app
```

The app launched and stayed up.

That command is **not** the command the build runs: it has no
`--identifier`. `--identifier` combined with `--deep` is not inert — it
overrides the identifier of *every* nested bundle, which is why x64's
`Electron Framework` is signed as `dev.wake.purdex` while arm64's keeps
`com.github.Electron.framework`. Measured on a synthetic bundle, holding
everything else equal:

| | top-level app | nested helper `.app` | entitlements reach helper |
|---|---|---|---|
| with `--identifier dev.wake.purdex` | `dev.wake.purdex` | `dev.wake.purdex` | yes |
| without | `dev.wake.purdex` | `dev.wake.purdex.helper.GPU` | yes |

Dropping `--identifier` therefore (a) makes the shipped command identical
to the one actually verified on the target machine, (b) restores nested
identifiers to match arm64, and (c) costs nothing: the top-level identifier
still resolves to `dev.wake.purdex` from `Info.plist`, so
`hasValidSignature()`'s `Identifier=` comparison keeps working. §5.3 takes
that path.

## 3. Goals

- **G1** — A freshly built **x64** bundle launches on an Intel Mac running
  macOS 14 without any manual post-processing, with a rendering UI (i.e.
  the renderer helper works too), not merely a process that stays alive.
- **G2** — Both slices are signed from **one** entitlements file that lives
  in the repo, and produce the same shape of nested signature, so x64 and
  arm64 cannot drift again.
- **G3** — The build **fails loudly** if any produced bundle ends up with
  Hardened Runtime but without `disable-library-validation`, instead of
  silently shipping a bundle that dies at launch.
- **G4** — The arm64 entitlement set is **unchanged** by this work.

## 4. Non-Goals

- **Not** the rest of Stage 1a. Daemon signing (`electron/entitlements.daemon.plist`,
  the `Makefile release` target signing `pdx` with `--options runtime`) stays
  pending; it is unrelated to this crash.
- **No** `com.apple.security.cs.allow-dyld-environment-variables`. Stage 1a
  lists it, but adding it here would change the arm64 entitlement set and
  violate G4. Nothing in the current dev-launch path
  (`PDX_DEV_MODE=1`, direct bundle exec) sets a `DYLD_*` variable. Deferred
  to the full Stage 1a.
- **No** change to signing identity, notarization, or Gatekeeper posture.
  The bundles stay ad-hoc signed; `PDX_MAC_SIGN_IDENTITY` keeps its current
  meaning.
- **No** universal (fat) binary target. The per-arch `dir` targets stay.
- **No** rebuild/redeploy of the bundle already installed on air-2019 —
  that is an operational follow-up, tracked separately.

## 5. Design

### 5.1 `electron/entitlements.mac.plist` (new)

The same three keys the working arm64 slice already carries:

- `com.apple.security.cs.allow-jit`
- `com.apple.security.cs.allow-unsigned-executable-memory`
- `com.apple.security.cs.disable-library-validation`

The requirement is that the **key/value set** matches
`app-builder-lib/templates/entitlements.mac.plist`, not that the file is a
byte-for-byte copy. `codesign` normalizes the plist before embedding it —
measured: the template (multi-line, two XML comments, `http` DOCTYPE) and a
reformatted three-key file produce *identical*
`codesign -d --entitlements - --xml` output. Comments may therefore point
at this spec instead of at the upstream issues.

Location follows the Stage 1a deliverable list (`electron/`), not
`build/`. Placing it in `build/` would make electron-builder pick it up
*implicitly* by filename (`getOptionsForFile()` scans
`buildResourcesDir`); an explicit path in `package.json` is easier to
trace from the crash back to the file.

### 5.2 `package.json` — `build.mac`

```jsonc
"mac": {
  "icon": "build/icon.icns",
  "entitlements": "electron/entitlements.mac.plist",
  "entitlementsInherit": "electron/entitlements.mac.plist",
  "target": [ ... ]
}
```

This makes arm64's electron-builder signing read the repo's file instead of
the bundled template. Since the key set is identical, the produced arm64
signature is unchanged (G4) — but the file is now the single source of
truth, and editing it affects both slices (G2).

Pointing `entitlementsInherit` at the same file is a no-op relative to
today: the inherit branch's own fallback is that same template
(`macPackager.js:368-377`), and the four arm64 helpers currently dump
exactly these three keys.

`hardenedRuntime` is left unset: `getOptionsForFile()` (`macPackager.js:381`)
already defaults it to `true` for non-MAS builds, and pinning it would be a
no-op restatement.

### 5.3 `scripts/mac-sign.mjs` (new)

The signing logic moves out of `build-electron.mjs` into a module with no
top-level side effects, so it is importable by a test:

| Export | Responsibility |
|---|---|
| `ENTITLEMENTS_PATH` | absolute path to §5.1 |
| `LIBRARY_VALIDATION_ENTITLEMENT` | the `com.apple.security.cs.disable-library-validation` key name |
| `buildSignArgs({ appPath, identity, entitlements })` | pure — returns the `codesign` argv |
| `signApp(appPath, { identity, entitlements })` | runs `codesign` with the `buildSignArgs` argv, then `verifyApp` |
| `verifyApp(appPath)` | runs `codesign --verify --deep --strict`; throws when invalid |
| `readEntitlements(appPath)` | runs `codesign -d --entitlements - --xml`, returns the plist text (`''` when none) |
| `readEntitlementsObject(appPath)` | decodes that plist through `plutil -convert json`; `{}` when none |
| `hasHardenedRuntime(appPath)` | parses the `CodeDirectory … flags=` line for `runtime` |
| `assertLibraryValidationDisabled(appPath)` | one bundle: throws unless *(no Hardened Runtime)* **or** *(`disable-library-validation` is present **and `true`**)* |
| `assertBundleTreeLibraryValidationDisabled(appPath)` | the above over the app **and** every nested `Contents/Frameworks/*.app` |

The value, not the key, is what the guard keys off: a plist that spells the
key with `<false/>` signs just as cleanly and leaves Library Validation on,
so a substring match over the plist text would wave the crashing
configuration through. Hence `readEntitlementsObject()` and the `=== true`
comparison.

Two changes to the argv relative to today:

- **adds** `--entitlements <path>` — the fix.
- **drops** `--identifier <appId>` — per the measurement in §2. The
  top-level identifier still comes out as `dev.wake.purdex` from
  `Info.plist`, and nested bundles keep their own identifiers instead of
  being flattened.

`--timestamp=none` still applies only to the ad-hoc identity. `appId` is no
longer needed for signing; `hasValidSignature()` in `build-electron.mjs`
keeps using it for its `Identifier=` comparison.

### 5.4 `scripts/build-electron.mjs`

- `signAndVerifyApp()` passes `ENTITLEMENTS_PATH` when it signs.
- `assertBundleTreeLibraryValidationDisabled(app)` runs on **every** produced
  bundle — including the `hasValidSignature()` early-return path that keeps
  electron-builder's arm64 signature. That is G3: if a future
  electron-builder stops passing entitlements, the build breaks at build
  time rather than at the user's launch time.
- The guard acts on the **bundle tree**, not just the top-level app: the
  four `Purdex Helper*.app` are separate processes (§2) and a helper that
  loses the entitlement shows up as a blank renderer window rather than as a
  failure to launch, so checking only the app would let that ship. Nested
  `.framework` bundles are excluded (§5.6).
- `PDX_SKIP_MAC_SIGN=1` keeps skipping everything, assertion included; it
  is the documented escape hatch for non-signing environments.
- The module-is-entry-point guard resolves `process.argv[1]` with
  `realpathSync()` before comparing it to `import.meta.url` (which is always
  symlink-resolved), and logs a line to stderr when it declines to build.
  Without both, invoking the script through a symlink builds nothing and
  still exits 0.

To keep G3 from being a claim rather than a behaviour, the two collaborators
(`sign` and `assert`) are injectable, and the test asserts that **both**
code paths call the assertion.

### 5.5 `electron/signing.test.ts`

`electron/signing.test.ts:12-18` asserts by substring that
`scripts/build-electron.mjs` contains `'codesign'` and `'--verify'`. Moving
the logic into `mac-sign.mjs` removes those strings and turns the test red.

Rather than re-point the substring match at the new file — which would
preserve a test that cannot distinguish a working build from a broken one —
the case is replaced by a behavioural assertion over `buildSignArgs()`:
the argv must contain `--entitlements` followed by the plist path, and must
carry `--options runtime`. The `PDX_MAC_SIGN_IDENTITY` assertion stays
(that string remains in `build-electron.mjs`).

### 5.6 Why `--deep` with one entitlements file is acceptable

`codesign --deep --entitlements` applies the same entitlements to every
nested **bundle**, whereas electron-builder distinguishes the app
(`entitlements`) from helpers (`entitlementsInherit`). For this bundle the
two sets are identical anyway (§5.2 points both at one file). Measured on a
synthetic bundle, `--deep --entitlements` does reach a nested helper
`.app`; a nested `.framework` gets no entitlements, which is correct — it
is not a process entry point, and the working arm64 bundle has the same
shape.

Splitting app and helper entitlements only becomes meaningful once a real
Developer ID and sandbox entitlements are in play — i.e. Stage 3.

## 6. Tests (written first)

`scripts/mac-sign_test.mjs`, run with `node`. It exercises **real
`codesign`**, not string assertions about the source file:

1. **`buildSignArgs` shape.** Pure-function check that `--entitlements
   <path>` is present as an adjacent pair, that `--options runtime` is
   present, and that `--identifier` is **absent** (§5.3).
2. **Regression, end-to-end, including a nested helper.** Build a throwaway
   `.app` in a temp dir containing `Contents/Info.plist`, a copied system
   binary, and a nested `Contents/Frameworks/T Helper.app` with its own
   identifier. Sign it with the argv from `buildSignArgs`, then assert:
   - `readEntitlements()` reports `disable-library-validation` for the app
     **and for the nested helper** (drops `--deep` → helper fails);
   - `assertLibraryValidationDisabled()` returns for both;
   - the helper's `Identifier=` is its own, not the app's (regression guard
     for the `--identifier` removal);
   - re-signing the same bundle with the entitlements argument removed
     makes `assertLibraryValidationDisabled()` **throw** — measured: a
     `--force` re-sign without `--entitlements` *clears* the previous
     entitlements rather than leaving them in place, so this case cannot
     go vacuously green.
3. **Guard is not vacuous.** A bundle signed ad-hoc *without*
   `--options runtime` must pass `assertLibraryValidationDisabled()` even
   with no entitlements, proving the guard keys off Hardened Runtime and
   not merely off the presence of a plist.
4. **G3 wiring.** Drive `signAndVerifyApp()` with injected `sign` / `assert`
   doubles and assert the assertion is invoked on **both** paths: the
   freshly-signed path and the `hasValidSignature()` early-return path.
   Without this, G3 is only a claim in prose.

5. **The value is read, not just the key.** Sign a bundle with a plist whose
   `disable-library-validation` is `<false/>`; `readEntitlementsObject()`
   must decode `false`, `assertLibraryValidationDisabled()` must throw, and
   its message must distinguish "present but not true" from "absent".
6. **The guard covers the bundle tree.** Deep-sign an app with a nested
   helper `.app` and a nested `.framework`; re-sign the helper alone,
   hardened and unentitled → the single-bundle guard on the app still
   passes but `assertBundleTreeLibraryValidationDisabled()` throws and names
   the helper. Then strip the framework's entitlements → the tree guard must
   still pass (§5.6).
7. **The defaults are the real collaborators.** Drive `signAndVerifyApp(app)`
   with **no** second argument against an ad-hoc, hardened, `--identifier`-ed
   but unentitled bundle — the production early-return path — and assert it
   throws. Case 4 injects doubles and therefore cannot see the defaults.
8. **`PDX_MAC_SIGN_IDENTITY` means re-sign.** With the variable set (in the
   child process only), an already-validly-signed bundle must still reach
   `sign`, with the identity forwarded, and `assert` must run afterwards.
9. **The entry-point guard.** `isMainModule()` is exported and returns true
   for the script's own path *and* for a symlink to it, false for an
   unrelated path, an unresolvable path and a missing `argv[1]`; importing
   the module prints the "skipping the build" line to stderr.

Skipped with a clear message on non-darwin (`process.platform !== 'darwin'`).

`electron/signing.test.ts` additionally pins `build.mac.entitlements` and
`build.mac.entitlementsInherit` in `package.json` to `ENTITLEMENTS_PATH`:
without them electron-builder silently falls back to its bundled template
and the repo plist stops being the single source of truth (§5.2, G2).

Note on what "fails first" means here: on today's `main` the test fails
because `scripts/mac-sign.mjs` does not exist. The assertion that pins the
*actual defect* is case 2's final step (entitlements removed → throw) and
case 2's helper coverage; those are the ones to watch go from red to green
while the module exists.

Manual verification is specified in the plan's T6.

## 7. Risks

- **Entitlements typo bricks both slices.** Mitigated by §6.2 running real
  `codesign` plus the mandatory manual launch on air-2019 before merge.
  Same risk the roadmap already flags for Stage 1a.
- **Dropping `--identifier` changes nested signatures on x64.** That is the
  intent (it converges x64 onto arm64's shape), but it is a real change to
  the shipped artifact and is why T6 dumps nested identifiers for both
  slices rather than only the top level.
- **`codesign` output format drift.** `readEntitlements()` uses `--xml` and
  a substring match on the key name; `hasHardenedRuntime()` parses the
  `flags=0x…(…)` parenthesised list. Both are stable across the macOS
  14 → 26 range in use here and are covered by §6. `codesign -d
  --entitlements` exits **0** with empty output when a bundle has no
  entitlements (measured), which is why `readEntitlements()` returns `''`
  instead of throwing.

## 8. Rollout

Ordinary PR to `main`, then a bump PR. No daemon change, no migration, no
SPA change — `pnpm run electron:build` output is the only artifact
affected. Machines running an already-installed bundle are unaffected until
they take a rebuilt one.
