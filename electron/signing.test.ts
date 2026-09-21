import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

// @ts-expect-error -- plain ESM build script, no type declarations
import { ENTITLEMENTS_PATH, buildSignArgs } from '../scripts/mac-sign.mjs'

const root = resolve(__dirname, '..')

describe('Electron macOS signing configuration (static)', () => {
  it('does not explicitly disable macOS signing', () => {
    const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
    expect(pkg.build?.mac?.identity).not.toBeNull()
  })

  it('signs with hardened runtime AND an explicit entitlements file', () => {
    // Behavioural, not a substring match on the build script: a bundle signed
    // with --options runtime but no entitlements cannot load its own ad-hoc
    // signed Electron Framework and aborts before main() on Intel.
    // Spec: docs/specs/2026-09-22-x64-adhoc-entitlements-spec.md §5.5.
    const args: string[] = buildSignArgs({ appPath: '/tmp/Purdex.app', identity: '-' })

    const entitlementsIdx = args.indexOf('--entitlements')
    expect(entitlementsIdx).toBeGreaterThan(-1)
    expect(args[entitlementsIdx + 1]).toBe(ENTITLEMENTS_PATH)
    expect(ENTITLEMENTS_PATH).toBe(resolve(root, 'electron/entitlements.mac.plist'))

    const optionsIdx = args.indexOf('--options')
    expect(optionsIdx).toBeGreaterThan(-1)
    expect(args[optionsIdx + 1]).toBe('runtime')

    // --identifier plus --deep flattens every nested bundle's identifier.
    expect(args).not.toContain('--identifier')

    const script = readFileSync(resolve(root, 'scripts/build-electron.mjs'), 'utf8')
    expect(script).toContain('PDX_MAC_SIGN_IDENTITY')
  })

  it('points electron-builder at the repo entitlements file, app and helpers alike', () => {
    // electron-builder signs the arm64 slice itself. Without these two keys it
    // silently falls back to app-builder-lib's bundled template, so the repo
    // plist stops being the single source of truth (spec §5.2, goal G2) and an
    // edit to it would reach only x64.
    //
    // `entitlementsInherit` is the helper path: the four Purdex Helper*.app
    // bundles are separate processes and each needs Library Validation off
    // (spec §2).
    const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))

    expect(pkg.build?.mac?.entitlements).toBeTypeOf('string')
    expect(resolve(root, pkg.build.mac.entitlements)).toBe(ENTITLEMENTS_PATH)

    expect(pkg.build?.mac?.entitlementsInherit).toBeTypeOf('string')
    expect(resolve(root, pkg.build.mac.entitlementsInherit)).toBe(ENTITLEMENTS_PATH)
  })

  it('updater no longer ships runtime signing helpers', () => {
    const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
    expect(updater).not.toContain('detectSignedState')
    expect(updater).not.toContain('resignAppBundle')
    expect(updater).not.toMatch(/\bcodesign\b/)
    expect(updater).not.toContain('child_process')
  })

  it('updater applyUpdate emits exactly downloading → extracting → applying in order', () => {
    const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
    // Total progress( call count — catches any progress(stepVar) /
    // progress(STEP_X) / progress(remoteStep) that the literal-only
    // regex below would silently miss.
    const allCalls = updater.match(/\bprogress\(/g) ?? []
    expect(allCalls).toHaveLength(3)
    // Literal calls must be the exact ordered triple.
    const literals = Array.from(
      updater.matchAll(/progress\(\s*['"]([^'"]+)['"]\s*\)/g),
      (m) => m[1],
    )
    expect(literals).toEqual(['downloading', 'extracting', 'applying'])
  })

  it('preload gates dev update API on PDX_DEV_MODE !== "0"', () => {
    const preload = readFileSync(resolve(root, 'electron/preload.ts'), 'utf8')
    // Dev features are on by default; only PDX_DEV_MODE=0 disables (spec
    // 2026-09-14 D6). Must match main.ts and the daemon's devmode.Enabled().
    expect(preload).toMatch(/process\.env\.PDX_DEV_MODE\s*!==\s*['"]0['"]/)
    expect(preload).toMatch(/applyUpdate:/)
    expect(preload).toMatch(/checkUpdate:/)
    expect(preload).toMatch(/onUpdateProgress:/)
    // The gate must precede applyUpdate in source order
    const gateIdx = preload.search(/process\.env\.PDX_DEV_MODE\s*!==\s*['"]0['"]/)
    const applyIdx = preload.indexOf('applyUpdate:')
    expect(gateIdx).toBeGreaterThan(-1)
    expect(applyIdx).toBeGreaterThan(gateIdx)
  })

  it('daemon gates /api/dev/update routes behind devmode.Enabled()', () => {
    const mod = readFileSync(resolve(root, 'internal/module/dev/module.go'), 'utf8')
    expect(mod).toMatch(/devmode\.Enabled\(\)/)
    expect(mod).toMatch(/\/api\/dev\/update\/check/)
    expect(mod).toMatch(/\/api\/dev\/update\/download/)
  })

  it('main.ts gates dev:* IPC handler registration on PDX_DEV_MODE !== "0"', () => {
    const main = readFileSync(resolve(root, 'electron/main.ts'), 'utf8')
    // Dev features are on by default; only PDX_DEV_MODE=0 disables (spec
    // 2026-09-14 D6). Must match main.ts and the daemon's devmode.Enabled().
    expect(main).toMatch(/process\.env\.PDX_DEV_MODE\s*!==\s*['"]0['"]/)
    expect(main).toMatch(/ipcMain\.handle\(['"]dev:apply-update['"]/)
    // The gate must precede every dev:* handler registration.
    const gateIdx = main.search(/process\.env\.PDX_DEV_MODE\s*!==\s*['"]0['"]/)
    const applyIdx = main.indexOf(`ipcMain.handle('dev:apply-update'`)
    const checkIdx = main.indexOf(`ipcMain.handle('dev:check-update'`)
    const streamIdx = main.indexOf(`ipcMain.handle('dev:stream-check'`)
    expect(gateIdx).toBeGreaterThan(-1)
    expect(applyIdx).toBeGreaterThan(gateIdx)
    expect(checkIdx).toBeGreaterThan(gateIdx)
    expect(streamIdx).toBeGreaterThan(gateIdx)
  })

  it('SPA stepLabels no longer carries the signing entry', () => {
    const tsx = readFileSync(
      resolve(root, 'spa/src/components/settings/DevEnvironmentSection.tsx'),
      'utf8',
    )
    expect(tsx).not.toMatch(/signing\s*:\s*['"]Signing app/)
  })
})
