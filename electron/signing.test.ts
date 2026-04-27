import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = resolve(__dirname, '..')

describe('Electron macOS signing configuration', () => {
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

  it('re-signs the app bundle after dev update mutates bundled resources', () => {
    const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
    expect(updater).toContain('resignAppBundle')
    expect(updater).toContain('codesign')
    expect(updater).toContain('--identifier')
    expect(updater).toContain('dev.wake.purdex')
  })
})
