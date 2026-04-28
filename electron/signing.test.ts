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

  it('preload still gates dev update API behind PDX_DEV_MODE', () => {
    const preload = readFileSync(resolve(root, 'electron/preload.ts'), 'utf8')
    expect(preload).toMatch(/PDX_DEV_MODE/)
    expect(preload).toMatch(/applyUpdate:/)
    expect(preload).toMatch(/checkUpdate:/)
    expect(preload).toMatch(/onUpdateProgress:/)
    const gateIdx = preload.indexOf('PDX_DEV_MODE')
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
})

// ── Test helpers ─────────────────────────────────────────────────────

type SpawnSyncResult = {
  status?: number | null
  signal?: string | null
  stdout?: string
  stderr?: string
  error?: Error
}

async function loadTesting() {
  return (await import('./updater')).__testing
}

async function mockCodesign(result: SpawnSyncResult): Promise<void> {
  const cp = await import('node:child_process')
  ;(cp.spawnSync as Mock).mockReturnValue({
    status: result.status ?? null,
    signal: result.signal ?? null,
    stdout: result.stdout ?? '',
    stderr: result.stderr ?? '',
    ...(result.error ? { error: result.error } : {}),
  })
}

async function getCp() {
  return await import('node:child_process')
}

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

  // ── detectSignedState core cases (spec §4.2) ────────────────────────

  it('returns "signed" when codesign exits 0', async () => {
    await mockCodesign({ status: 0, stderr: 'Identifier=dev.wake.purdex\n' })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('signed')
  })

  it('returns "unsigned" on canonical "not signed at all" stderr', async () => {
    await mockCodesign({
      status: 1,
      stderr: '/Applications/Purdex.app: code object is not signed at all\n',
    })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unsigned')
  })

  it('returns "unknown" on non-zero exit with unrelated stderr', async () => {
    await mockCodesign({ status: 1, stderr: 'bundle format unrecognized, invalid, or unsuitable\n' })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  it('returns "unknown" when codesign killed by signal (status null)', async () => {
    await mockCodesign({ status: null, signal: 'SIGTERM' })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  it('returns "unknown" when spawnSync reports an error', async () => {
    await mockCodesign({ status: null, error: new Error('ENOENT: codesign not found') })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  // ── detectSignedState resilience cases (Round-2 F1) ────────────────

  it('returns "unsigned" when canonical phrase has different case', async () => {
    await mockCodesign({
      status: 1,
      stderr: '/Applications/Purdex.app: CODE OBJECT IS NOT SIGNED AT ALL\n',
    })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unsigned')
  })

  it('returns "unsigned" when canonical phrase is wrapped in ANSI escape codes', async () => {
    await mockCodesign({
      status: 1,
      stderr: '\x1b[31m/Applications/Purdex.app: code object is not signed at all\x1b[0m\n',
    })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unsigned')
  })

  it('returns "unsigned" when canonical phrase appears on stdout instead of stderr', async () => {
    await mockCodesign({
      status: 1,
      stdout: '/Applications/Purdex.app: code object is not signed at all\n',
      stderr: '',
    })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unsigned')
  })

  it('returns "unsigned" when canonical phrase has irregular whitespace', async () => {
    await mockCodesign({
      status: 1,
      stderr: 'code object\tis  not\nsigned at all\n',
    })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unsigned')
  })

  it('returns "unknown" when codesign times out (Round-2 F2)', async () => {
    await mockCodesign({
      status: null,
      signal: 'SIGTERM',
      error: Object.assign(new Error('ETIMEDOUT'), { code: 'ETIMEDOUT' }),
    })
    const t = await loadTesting()
    expect(t.detectSignedState('/Applications/Purdex.app')).toBe('unknown')
  })

  it('passes a timeout to spawnSync to prevent hangs', async () => {
    await mockCodesign({ status: 0 })
    const t = await loadTesting()
    t.detectSignedState('/Applications/Purdex.app')
    const cp = await getCp()
    const opts = (cp.spawnSync as Mock).mock.calls[0][2]
    expect(opts.timeout).toBeGreaterThanOrEqual(1_000)
    expect(opts.timeout).toBeLessThanOrEqual(60_000)
  })

  // ── resignAppBundle dispatch (spec §4.3) ────────────────────────────

  it('resignAppBundle skips everything when PDX_SKIP_MAC_SIGN=1', async () => {
    process.env.PDX_SKIP_MAC_SIGN = '1'
    const cp = await getCp()
    const t = await loadTesting()
    t.resignAppBundle()
    expect(cp.spawnSync).not.toHaveBeenCalled()
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle skips on non-darwin (getAppBundlePath returns null)', async () => {
    Object.defineProperty(process, 'platform', { ...originalPlatform, value: 'linux' })
    const cp = await getCp()
    const t = await loadTesting()
    t.resignAppBundle()
    expect(cp.spawnSync).not.toHaveBeenCalled()
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle skips codesign when bundle is unsigned', async () => {
    await mockCodesign({
      status: 1,
      stderr: '/Applications/Purdex.app: code object is not signed at all\n',
    })
    const cp = await getCp()
    const t = await loadTesting()
    t.resignAppBundle()
    expect(cp.spawnSync).toHaveBeenCalledTimes(1)
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle throws actionable error on unknown', async () => {
    await mockCodesign({ status: null, signal: 'SIGTERM' })
    const cp = await getCp()
    const t = await loadTesting()
    expect(() => t.resignAppBundle()).toThrow(/preflight detection failed/i)
    expect(() => t.resignAppBundle()).toThrow(/PDX_SKIP_MAC_SIGN/) // remediation hint
    expect(cp.execFileSync).not.toHaveBeenCalled()
  })

  it('resignAppBundle uses ad-hoc identity when PDX_MAC_SIGN_IDENTITY unset', async () => {
    await mockCodesign({ status: 0, stderr: 'Identifier=dev.wake.purdex\n' })
    const cp = await getCp()
    const t = await loadTesting()
    t.resignAppBundle()
    expect(cp.execFileSync).toHaveBeenCalledTimes(2)
    const signCall = (cp.execFileSync as Mock).mock.calls[0]
    expect(signCall[0]).toBe('codesign')
    expect(signCall[1][signCall[1].indexOf('--sign') + 1]).toBe('-')
    expect(signCall[1]).toContain('--timestamp=none')
    expect(signCall[1][signCall[1].indexOf('--identifier') + 1]).toBe('dev.wake.purdex')
    const verifyCall = (cp.execFileSync as Mock).mock.calls[1]
    expect(verifyCall[1]).toContain('--verify')
  })

  it('resignAppBundle uses forced identity when PDX_MAC_SIGN_IDENTITY set', async () => {
    process.env.PDX_MAC_SIGN_IDENTITY = 'Developer ID Application: Test (XYZ123)'
    await mockCodesign({ status: 0, stderr: 'Identifier=dev.wake.purdex\n' })
    const cp = await getCp()
    const t = await loadTesting()
    t.resignAppBundle()
    expect(cp.execFileSync).toHaveBeenCalledTimes(2)
    const signCall = (cp.execFileSync as Mock).mock.calls[0]
    expect(signCall[1][signCall[1].indexOf('--sign') + 1]).toBe('Developer ID Application: Test (XYZ123)')
    expect(signCall[1]).not.toContain('--timestamp=none')
  })
})
