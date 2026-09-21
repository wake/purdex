// scripts/mac-sign.mjs — macOS code-signing helpers for the Electron bundles.
//
// Spec: docs/specs/2026-09-22-x64-adhoc-entitlements-spec.md §5.3.
//
// This module has no top-level side effects so that scripts/mac-sign_test.mjs
// can import it and drive the real `codesign` against throwaway bundles.

import { execFileSync, spawnSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')

/** The single entitlements file both slices are signed with (spec §5.1). */
export const ENTITLEMENTS_PATH = resolve(root, 'electron/entitlements.mac.plist')

/**
 * The entitlement that switches Library Validation off. Without it, a bundle
 * signed with Hardened Runtime refuses to map its own ad-hoc-signed
 * `Electron Framework` and dyld aborts before main().
 */
export const LIBRARY_VALIDATION_ENTITLEMENT = 'com.apple.security.cs.disable-library-validation'

/**
 * Pure: the `codesign` argv used to sign a produced bundle.
 *
 * Two deliberate properties (spec §5.3):
 *   - `--entitlements` is always passed; `--options runtime` without it is the
 *     configuration that crashes on Intel.
 *   - `--identifier` is NOT passed. Combined with `--deep` it overrides the
 *     identifier of every nested bundle; leaving it out keeps nested
 *     identifiers intact while the top-level one still resolves to the
 *     bundle's own CFBundleIdentifier.
 */
export function buildSignArgs({ appPath, identity = '-', entitlements = ENTITLEMENTS_PATH } = {}) {
  if (!appPath) throw new Error('buildSignArgs: appPath is required')
  if (!entitlements) throw new Error('buildSignArgs: entitlements path is required')

  const args = [
    '--force',
    '--deep',
    '--options', 'runtime',
    '--entitlements', entitlements,
    '--sign', identity,
  ]
  // Only the ad-hoc identity skips the timestamp server.
  if (identity === '-') args.push('--timestamp=none')
  args.push(appPath)
  return args
}

/** `codesign --verify --deep --strict` — throws when the bundle is not valid. */
export function verifyApp(appPath) {
  execFileSync('codesign', ['--verify', '--deep', '--strict', '--verbose=4', appPath], { stdio: 'inherit' })
}

/** Signs `appPath` with {@link buildSignArgs} and verifies the result. */
export function signApp(appPath, { identity = '-', entitlements = ENTITLEMENTS_PATH } = {}) {
  execFileSync('codesign', buildSignArgs({ appPath, identity, entitlements }), { stdio: 'inherit' })
  verifyApp(appPath)
}

/**
 * The embedded entitlements of `appPath` as XML plist text.
 *
 * Returns `''` when the bundle carries none: `codesign -d --entitlements`
 * exits 0 with empty output in that case (measured), so an empty result is
 * a fact about the bundle, not an error.
 */
export function readEntitlements(appPath) {
  const r = spawnSync('codesign', ['-d', '--entitlements', '-', '--xml', appPath], { encoding: 'utf8' })
  if (r.error) throw r.error
  if (r.status !== 0) {
    throw new Error(`codesign could not read entitlements of ${appPath}: ${(r.stderr || '').trim()}`)
  }
  return (r.stdout || '').trim()
}

/**
 * Whether `appPath` was signed with Hardened Runtime, read off the
 * `CodeDirectory … flags=0x…(adhoc,runtime)` line.
 */
export function hasHardenedRuntime(appPath) {
  const r = spawnSync('codesign', ['-dv', '--verbose=4', appPath], { encoding: 'utf8' })
  if (r.error) throw r.error
  if (r.status !== 0) {
    throw new Error(`codesign could not read the signature of ${appPath}: ${(r.stderr || '').trim()}`)
  }
  const output = `${r.stdout || ''}${r.stderr || ''}`
  const match = output.match(/\bflags=0x[0-9a-fA-F]+\(([^)]*)\)/)
  if (!match) return false
  return match[1].split(',').map((f) => f.trim()).includes('runtime')
}

/**
 * Build-time guard (goal G3): fails loudly when a produced bundle would die at
 * the user's launch time.
 *
 * Passes when the bundle either has no Hardened Runtime (no Library Validation
 * to disable) or carries {@link LIBRARY_VALIDATION_ENTITLEMENT}.
 */
export function assertLibraryValidationDisabled(appPath) {
  if (!hasHardenedRuntime(appPath)) return

  const entitlements = readEntitlements(appPath)
  if (entitlements.includes(LIBRARY_VALIDATION_ENTITLEMENT)) return

  throw new Error(
    `${appPath}: signed with Hardened Runtime but without ${LIBRARY_VALIDATION_ENTITLEMENT}. ` +
      'Library Validation will reject the ad-hoc signed Electron Framework and the app will abort ' +
      `before main(). Sign with --entitlements ${ENTITLEMENTS_PATH}. ` +
      `Embedded entitlements: ${entitlements || '(none)'}`,
  )
}
