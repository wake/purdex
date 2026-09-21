# macOS Signing & Release Roadmap

Tracking issue: [#709](https://github.com/wake/purdex/issues/709)

## TL;DR

`Purdex.app` (Electron shell) and the `pdx` daemon (Go binary) will eventually
ship with Hardened Runtime + Apple-Developer-ID-signed + notarized binaries,
distributed via Homebrew formula and direct curl download. **No DMG
packaging** — locked 2026-04-29 per #709 epic decision.

The roadmap is split into four stages; two are shipped, one is half done,
two remain.

| Stage | Status | Ships at | Scope |
|-------|--------|----------|-------|
| 0 | ✅ shipped | alpha.248 ([#711](https://github.com/wake/purdex/pull/711)) | Stop dev-update SIGKILL on unsigned bundles via three-state preflight |
| 1b | ✅ shipped | alpha.250 ([#720](https://github.com/wake/purdex/pull/720)) | Retire runtime codesign entirely — Option β. Preflight + resign helpers deleted; three-layer `PDX_DEV_MODE === '1'` boundary unified |
| 1a | 🟡 partial | app half TBD-bump ([#TBD](https://github.com/wake/purdex/pulls)) | Entitlements + Hardened Runtime + daemon signing pipeline. **App half shipped** (see below); daemon pipeline still pending |
| 2 | ⏳ pending (optional) | TBD | Self-signed cert workflow + cross-machine trust docs |
| 3 | ⏳ pending | TBD | Apple Developer ID + notarytool + GitHub Actions release |

## Foundational decisions (locked)

- **Distribution**: brew formula + `curl` direct-download. No DMG / PKG.
- **Daemon signing**: same Developer ID as `.app`. Daemon will be notarized
  but **not stapled** — `xcrun stapler staple` requires DMG/PKG containers,
  and our distribution channels (brew bypasses Gatekeeper for formulae;
  curl users get online-lookup Gatekeeper assessment) accept the unstapled
  notarization.
- **Dev update safety claim** ([Stage 1b spec §3.4](../specs/2026-04-29-electron-signing-stage1b-spec.md)):
  the runtime app-mutation safety claim is **scoped to same-machine
  same-path relaunch on a first-launch-cleared bundle**. Bundles modified
  by dev update are explicitly not redistributable; their `CodeResources`
  hash record becomes stale on disk, but macOS does not re-verify it on
  same-path relaunch. This is the foundation that lets Stage 1b retire
  runtime codesign.

## Shipped stages (history)

### Stage 0 — Preflight skip (alpha.248)

PR [#711](https://github.com/wake/purdex/pull/711). Adds `detectSignedState()`
preflight to `electron/updater.ts` that classifies the running bundle into
`signed` / `unsigned` / `unknown` before deciding whether to call
`codesign --sign`. On the project's actual deployment shape (always-unsigned
bundles), the preflight returns `unsigned` and skips codesign — preventing
the SIGKILL that PR #672 inadvertently introduced.

Stage 0 was a hotfix; the preflight itself was always vestigial because
unsigned bundles never needed codesign at all.

Spec/plan: [`docs/specs/2026-04-28-electron-signing-stage0-spec.md`](../specs/2026-04-28-electron-signing-stage0-spec.md), [`...-plan.md`](../specs/2026-04-28-electron-signing-stage0-plan.md).

### Stage 1b — Runtime codesign retirement (alpha.250)

PR [#720](https://github.com/wake/purdex/pull/720). Retires the entire
runtime-codesign concept (Option β). Production-side changes:

- `electron/updater.ts` — removed `~80` lines: `detectSignedState`,
  `resignAppBundle`, `getAppBundlePath`, `APP_ID`, `SignedState`,
  `PREFLIGHT_TIMEOUT_MS`, `NOT_SIGNED_PATTERN`, `stripAnsi`, `__testing`
  export, the `progress('signing')` call, the `node:child_process` import.
- `electron/preload.ts` — gate tightened from truthy `process.env.PDX_DEV_MODE ? ... : {}`
  to strict `=== '1'` (Round-2 attacker fix; pre-existing bug).
- `electron/main.ts` — `dev:*` `ipcMain.handle(...)` registrations wrapped
  in `if (process.env.PDX_DEV_MODE === '1')`. Closes the boundary residual
  surface that earlier revisions left out-of-scope (Round-2 defender fix).
- `spa/src/components/settings/DevEnvironmentSection.tsx` — dropped dead
  `signing` and `restarting` `stepLabels` entries.
- `electron/signing.test.ts` — 281 → 65 lines; 4 new gate guards (preload /
  main / daemon / SPA-absence) lock the contract.

Closes [#712](https://github.com/wake/purdex/issues/712) (preflight
integration test obsolete) and [#713](https://github.com/wake/purdex/issues/713)
(`APP_ID` constant drift; constant deleted).

Spec/plan: [`docs/specs/2026-04-29-electron-signing-stage1b-spec.md`](../specs/2026-04-29-electron-signing-stage1b-spec.md), [`...-plan.md`](../specs/2026-04-29-electron-signing-stage1b-plan.md).

## Remaining stages

### Stage 1a — Entitlements + Hardened Runtime + daemon pipeline

Pure technical work, **independent of which signing identity will be used**.
Configures the bundle so codesign produces a hardened, entitled binary —
then any cert (ad-hoc `-`, self-signed, Developer ID) plugs in via env var.

#### App half — 🟡 shipped 2026-09-22

Shipped ahead of the rest of the stage because its absence was a **launch
crash**, not a gap: x64 bundles were ad-hoc signed with `--options runtime`
and *no* entitlements, so Library Validation rejected the ad-hoc-signed
`Electron Framework` and `dyld` aborted before `main()` on Intel Macs.
arm64 was unaffected because electron-builder ad-hoc-signs that slice
itself, with its default entitlements template.

- `electron/entitlements.mac.plist` — the three V8 keys
  (`allow-jit`, `allow-unsigned-executable-memory`,
  `disable-library-validation`), now the single source of truth for both
  slices
- `package.json build.mac` — `entitlements` + `entitlementsInherit` point at
  it (`hardenedRuntime` stays unset; electron-builder already defaults it to
  `true` for non-MAS builds)
- `scripts/mac-sign.mjs` (new) — `codesign` argv gains `--entitlements` and
  **loses** `--identifier`, which combined with `--deep` had been flattening
  every nested bundle's identifier
- `scripts/build-electron.mjs` — asserts on **every** produced bundle that
  Hardened Runtime implies `disable-library-validation`, so a regression
  breaks the build instead of the user's launch

Spec/plan: [`docs/specs/2026-09-22-x64-adhoc-entitlements-spec.md`](../specs/2026-09-22-x64-adhoc-entitlements-spec.md),
[`...-plan.md`](../specs/2026-09-22-x64-adhoc-entitlements-plan.md).

#### Still pending

- `com.apple.security.cs.allow-dyld-environment-variables` — deliberately
  **not** added by the app half: it would have changed the (working) arm64
  entitlement set, and nothing in the current dev-launch path sets a
  `DYLD_*` variable
- `electron/entitlements.daemon.plist` — minimal/empty (Go binaries don't
  need V8 relaxations)
- New `Makefile release` target — signs `pdx` daemon with `--options runtime`
  and the daemon entitlements
- Manual verification for the daemon path, as the app path already has
  (`codesign -d --entitlements -` on both slices, plus a real Intel launch)

**Risk**: misconfigured entitlements cause V8 to refuse to launch with
cryptic errors. Mitigated by always running ad-hoc-signed verification
before shipping.

### Stage 2 — Self-signed cert workflow (optional)

Adds operational tooling for distributing internally-signed bundles across
machines you control:

- Self-signed `Developer ID Application`-shaped certificate generation
  script (or doc)
- Cross-machine trust setup docs — how to import the public cert into
  another Mac's Keychain so `Purdex.app` and `pdx` are trusted there
- Same env-var entry point (`PDX_MAC_SIGN_IDENTITY`) as Stage 3, just with
  a self-signed identity instead of Developer ID

**Whether to do Stage 2 depends on Apple Developer Program timing** — see
"Decision: which path?" below.

### Stage 3 — Apple Developer ID + notarization (release-line)

The actual ship-to-the-public stage. Depends on:

- **Apple Developer Program** subscription ($99/year). Once provisioned,
  the user obtains a `Developer ID Application: <Your Name> (TEAMID)`
  certificate and installs it in Keychain.

Concrete deliverables on top of Stage 1a:

- Set `PDX_MAC_SIGN_IDENTITY="Developer ID Application: ..."` in CI
- `xcrun notarytool submit --wait` for each artifact (`.app` + `pdx`
  daemon) using app-specific password / API key
- `xcrun stapler staple Purdex.app` (`.app` only — daemon notarized but
  not stapled per "Foundational decisions")
- GitHub Actions macOS runner workflow (`.github/workflows/release.yml`)
  builds + signs + notarizes + uploads release artifacts on tag push
- Homebrew tap with formula referencing the GitHub release artifacts
- `curl` install script in repo root or release page

If Stage 1a is in place, Stage 3 is **substantively just an env-var swap +
notarytool CLI integration**. Most of the signing pipeline already exists.

## Decision: which path forward?

Two viable orderings:

| Route | Ship sequence | Best when |
|-------|---------------|-----------|
| **A. 1a → wait for Developer → 3** | Skip Stage 2 entirely | Apple Developer Program is "soon" (weeks). Stage 2's self-signed cross-machine work would be discarded. Use ad-hoc cert (`PDX_MAC_SIGN_IDENTITY=-`) for entitlement validation in the meantime. |
| **B. 1a + 2 bundled** | Ship them together | Apple Developer Program is "later" (months) or uncertain. Stage 2 lets you internally distribute signed bundles across Tailnet machines (e.g., Mini → Air) without each one doing the cross-machine trust dance manually. |

Original kickoff bundled 1a + 2 because Stage 1a "needs a real signing path
to test entitlements with". But Stage 1b §8.3 manual verification proved
ad-hoc signing also exercises entitlements — so Stage 1a can be validated
without Stage 2's self-signed infrastructure.

**Default recommendation**: Route A (Stage 1a now, Stage 3 once Developer
ID is in hand). Pivot to Route B only if Apple Developer is deferred and
you actually need cross-machine self-signed distribution.

## Operational notes

### Dev workflow after Stage 1a

Almost unchanged. `pnpm run electron:build` still works, with one of three
modes selected by env vars:

| Mode | Env vars | Result |
|------|----------|--------|
| Skip codesign entirely | `PDX_SKIP_MAC_SIGN=1` | No signature; no entitlements applied; Hardened Runtime config is moot |
| Ad-hoc sign (default) | (neither var set) | `codesign --sign -`; entitlements applied; Hardened Runtime active; bundle runs locally but is not trusted on other Macs |
| Trusted sign | `PDX_MAC_SIGN_IDENTITY="Developer ID Application: ..."` | Real signature; entitlements applied; bundle runs anywhere Gatekeeper accepts (post-Stage-3 once notarized) |

The dev experience changes by ~5–10 seconds per build (codesign overhead).

### Risk: skipping codesign in dev

Setting `PDX_SKIP_MAC_SIGN=1` skips codesign entirely, which means
**entitlements are never applied or validated**. If `entitlements.mac.plist`
has a typo or missing key, you won't notice until release time when V8
refuses to launch.

Mitigation: keep `PDX_SKIP_MAC_SIGN=1` only for niche cases (broken
codesign install, unusual environments). The default ad-hoc path applies
entitlements, so misconfigurations surface early.

### Three-layer `PDX_DEV_MODE === '1'` gate (post-Stage-1b)

Dev update is gated identically at three boundaries — all use strict string
comparison `=== '1'`:

| Layer | File | Behaviour when gate fails |
|-------|------|--------------------------|
| Renderer API | `electron/preload.ts` | `applyUpdate` / `checkUpdate` / `streamCheck` / `onUpdateProgress` not exposed on `window.electronAPI` |
| Main process | `electron/main.ts` | `dev:apply-update` / `dev:check-update` / `dev:stream-check` `ipcMain.handle(...)` not registered |
| Daemon HTTP | `internal/module/dev/module.go` | `/api/dev/update/*` routes not registered |

Production builds (no `PDX_DEV_MODE` env var) cannot reach dev update via
any of these surfaces. Dev requires `PDX_DEV_MODE=1` literally — `=0`,
`=false`, `=true`, etc. all fail the gate.

## Pending operational items

- **Air manual verification of Stage 1b**: spec §8.2 (Run A unsigned) +
  §8.3 (Run B ad-hoc signed) per [Stage 1b plan §5](../specs/2026-04-29-electron-signing-stage1b-plan.md).
  Run B post-update `codesign --verify --deep --strict` failure with
  sealed-resource mismatch is the **expected** outcome; relaunch success
  is the acceptance criterion. A relaunch failure on either run is a
  rollback trigger.
- **Stage 1a planning**: write spec + plan when ready to start.
- **Apple Developer Program decision**: schedule subscription or commit to
  Route B (Stage 2) if subscription is deferred indefinitely.

## References

- Tracking issue: [#709](https://github.com/wake/purdex/issues/709)
- Stage 0 PR [#711](https://github.com/wake/purdex/pull/711) — preflight
- Stage 1b PR [#720](https://github.com/wake/purdex/pull/720) — retirement
- Stage 0 spec [`docs/specs/2026-04-28-electron-signing-stage0-spec.md`](../specs/2026-04-28-electron-signing-stage0-spec.md)
- Stage 0 plan [`docs/specs/2026-04-28-electron-signing-stage0-plan.md`](../specs/2026-04-28-electron-signing-stage0-plan.md)
- Stage 1b spec v1.3 [`docs/specs/2026-04-29-electron-signing-stage1b-spec.md`](../specs/2026-04-29-electron-signing-stage1b-spec.md)
- Stage 1b plan v1.1 [`docs/specs/2026-04-29-electron-signing-stage1b-plan.md`](../specs/2026-04-29-electron-signing-stage1b-plan.md)
- Apple Code Signing Guide — Hardened Runtime entitlements
- Apple TN2206 — Code Signing in Depth
- electron-builder mac options reference
