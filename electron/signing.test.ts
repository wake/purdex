import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = resolve(__dirname, '..')

describe('Electron macOS signing configuration (static)', () => {
  it('does not explicitly disable macOS signing', () => {
    const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8'))
    expect(pkg.build?.mac?.identity).not.toBeNull()
  })

  it('signs and verifies the final moved app bundles', () => {
    const script = readFileSync(resolve(root, 'scripts/build-electron.mjs'), 'utf8')
    expect(script).toContain('PDX_MAC_SIGN_IDENTITY')
    expect(script).toContain('codesign')
    expect(script).toContain('--verify')
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
    const literals = Array.from(
      updater.matchAll(/progress\(\s*['"]([^'"]+)['"]\s*\)/g),
      (m) => m[1],
    )
    expect(literals).toEqual(['downloading', 'extracting', 'applying'])
  })

  it('preload gates dev update API behind strict PDX_DEV_MODE === "1"', () => {
    const preload = readFileSync(resolve(root, 'electron/preload.ts'), 'utf8')
    // Strict equality — accepts only PDX_DEV_MODE='1', not any truthy
    // string ('0', 'false', 'no' would all pass a truthy ternary).
    // Must match daemon-side gate in internal/module/dev/module.go.
    expect(preload).toMatch(/process\.env\.PDX_DEV_MODE\s*===\s*['"]1['"]/)
    expect(preload).toMatch(/applyUpdate:/)
    expect(preload).toMatch(/checkUpdate:/)
    expect(preload).toMatch(/onUpdateProgress:/)
    // The strict gate must precede applyUpdate in source order
    const gateIdx = preload.search(/process\.env\.PDX_DEV_MODE\s*===\s*['"]1['"]/)
    const applyIdx = preload.indexOf('applyUpdate:')
    expect(gateIdx).toBeGreaterThan(-1)
    expect(applyIdx).toBeGreaterThan(gateIdx)
  })

  it('daemon still gates /api/dev/update routes behind PDX_DEV_MODE=1', () => {
    const mod = readFileSync(resolve(root, 'internal/module/dev/module.go'), 'utf8')
    expect(mod).toMatch(/os\.Getenv\("PDX_DEV_MODE"\)\s*!=\s*"1"/)
    expect(mod).toMatch(/\/api\/dev\/update\/check/)
    expect(mod).toMatch(/\/api\/dev\/update\/download/)
  })

  it('SPA stepLabels no longer carries the signing entry', () => {
    const tsx = readFileSync(
      resolve(root, 'spa/src/components/settings/DevEnvironmentSection.tsx'),
      'utf8',
    )
    expect(tsx).not.toMatch(/signing\s*:\s*['"]Signing app/)
  })
})
