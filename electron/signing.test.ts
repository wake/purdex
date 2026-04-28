import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

const root = resolve(__dirname, '..')

vi.mock('electron', () => ({
  app: {
    getPath: vi.fn((name: string) => {
      if (name === 'exe') return '/Applications/Purdex.app/Contents/MacOS/Purdex'
      if (name === 'temp') return '/tmp'
      return '/'
    }),
  },
}))

vi.mock('node:child_process', () => ({
  spawnSync: vi.fn(),
  execFileSync: vi.fn(),
}))

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

  it('updater exposes signing preflight + re-sign helpers', () => {
    const updater = readFileSync(resolve(root, 'electron/updater.ts'), 'utf8')
    expect(updater).toContain('detectSignedState')
    expect(updater).toContain('resignAppBundle')
    expect(updater).toContain("'node:child_process'")
  })
})

describe('updater signing preflight (runtime)', () => {
  let originalPlatform: PropertyDescriptor
  let originalEnv: NodeJS.ProcessEnv

  beforeEach(() => {
    vi.resetModules()
    vi.clearAllMocks()
    originalPlatform = Object.getOwnPropertyDescriptor(process, 'platform')!
    Object.defineProperty(process, 'platform', { ...originalPlatform, value: 'darwin' })
    originalEnv = { ...process.env }
    delete process.env.PDX_SKIP_MAC_SIGN
    delete process.env.PDX_MAC_SIGN_IDENTITY
  })

  afterEach(() => {
    Object.defineProperty(process, 'platform', originalPlatform)
    process.env = originalEnv
  })

  // ── detectSignedState (5 cases per spec §4.2) ───────────────────────

  it('detectSignedState returns "signed" when codesign exits 0', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: 0,
      stderr: 'Identifier=dev.wake.purdex\n',
    })
    const { __testing } = await import('./updater')
    expect(__testing.detectSignedState('/Applications/Purdex.app')).toBe('signed')
  })

  it('detectSignedState returns "unsigned" on canonical "not signed at all" stderr', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: 1,
      stderr: '/Applications/Purdex.app: code object is not signed at all\n',
    })
    const { __testing } = await import('./updater')
    expect(__testing.detectSignedState('/Applications/Purdex.app')).toBe('unsigned')
  })

  it('detectSignedState returns "unknown" on non-zero exit with unrelated stderr', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: 1,
      stderr: 'bundle format unrecognized, invalid, or unsuitable\n',
    })
    const { __testing } = await import('./updater')
    expect(__testing.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  it('detectSignedState returns "unknown" when codesign killed by signal (status null)', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: null,
      signal: 'SIGTERM',
      stderr: '',
    })
    const { __testing } = await import('./updater')
    expect(__testing.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  it('detectSignedState returns "unknown" when spawnSync reports an error', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: null,
      error: new Error('ENOENT: codesign not found'),
      stderr: '',
    })
    const { __testing } = await import('./updater')
    expect(__testing.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  // ── resignAppBundle (6 cases per spec §4.3) ─────────────────────────

  it('resignAppBundle skips everything when PDX_SKIP_MAC_SIGN=1', async () => {
    process.env.PDX_SKIP_MAC_SIGN = '1'
    const cp = await import('node:child_process')
    const { __testing } = await import('./updater')
    __testing.resignAppBundle()
    expect(cp.spawnSync).not.toHaveBeenCalled()
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle skips on non-darwin (getAppBundlePath returns null)', async () => {
    Object.defineProperty(process, 'platform', { ...originalPlatform, value: 'linux' })
    const cp = await import('node:child_process')
    const { __testing } = await import('./updater')
    __testing.resignAppBundle()
    expect(cp.spawnSync).not.toHaveBeenCalled()
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle skips codesign when bundle is unsigned', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: 1,
      stderr: '/Applications/Purdex.app: code object is not signed at all\n',
    })
    const { __testing } = await import('./updater')
    __testing.resignAppBundle()
    expect(cp.spawnSync).toHaveBeenCalledTimes(1)
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle throws when detection returns "unknown"', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({
      status: null,
      signal: 'SIGTERM',
      stderr: '',
    })
    const { __testing } = await import('./updater')
    expect(() => __testing.resignAppBundle()).toThrow(/codesign preflight/i)
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle uses ad-hoc identity when PDX_MAC_SIGN_IDENTITY unset', async () => {
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({ status: 0, stderr: 'Identifier=dev.wake.purdex\n' })
    const { __testing } = await import('./updater')
    __testing.resignAppBundle()
    expect(cp.execFileSync).toHaveBeenCalledTimes(2)
    const signCall = (cp.execFileSync as Mock).mock.calls[0]
    expect(signCall[0]).toBe('codesign')
    expect(signCall[1]).toContain('--sign')
    expect(signCall[1][signCall[1].indexOf('--sign') + 1]).toBe('-')
    expect(signCall[1]).toContain('--timestamp=none')
    expect(signCall[1]).toContain('--identifier')
    expect(signCall[1][signCall[1].indexOf('--identifier') + 1]).toBe('dev.wake.purdex')
    const verifyCall = (cp.execFileSync as Mock).mock.calls[1]
    expect(verifyCall[1]).toContain('--verify')
  })

  it('resignAppBundle uses forced identity when PDX_MAC_SIGN_IDENTITY set', async () => {
    process.env.PDX_MAC_SIGN_IDENTITY = 'Developer ID Application: Test (XYZ123)'
    const cp = await import('node:child_process')
    ;(cp.spawnSync as Mock).mockReturnValue({ status: 0, stderr: 'Identifier=dev.wake.purdex\n' })
    const { __testing } = await import('./updater')
    __testing.resignAppBundle()
    expect(cp.execFileSync).toHaveBeenCalledTimes(2)
    const signCall = (cp.execFileSync as Mock).mock.calls[0]
    expect(signCall[1][signCall[1].indexOf('--sign') + 1]).toBe('Developer ID Application: Test (XYZ123)')
    expect(signCall[1]).not.toContain('--timestamp=none')
  })
})
