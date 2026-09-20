import { describe, it, expect, afterEach, vi } from 'vitest'
import {
  DEVICE_NAME_MAX_CODE_POINTS,
  effectiveDeviceName,
  normalizeDeviceName,
  parseUserAgentName,
  resolveDefaultDeviceName,
} from './device-name'
import * as legacyLib from './device-state/device-name'
import * as legacyStore from '../stores/useDeviceStateStore'

describe('normalizeDeviceName', () => {
  it('trims surrounding whitespace', () => {
    expect(normalizeDeviceName('  Office Mac \n')).toBe('Office Mac')
  })

  it('returns null for empty or whitespace-only input', () => {
    expect(normalizeDeviceName('')).toBeNull()
    expect(normalizeDeviceName(' \t\n ')).toBeNull()
  })

  it('keeps exactly 64 code points and truncates the 65th', () => {
    expect(DEVICE_NAME_MAX_CODE_POINTS).toBe(64)
    expect(normalizeDeviceName('a'.repeat(64))).toBe('a'.repeat(64))
    expect(normalizeDeviceName('a'.repeat(65))).toBe('a'.repeat(64))
  })

  it('truncates by code point, never splitting a surrogate pair', () => {
    const result = normalizeDeviceName('😀'.repeat(70))!
    expect(result).toBe('😀'.repeat(64))
    expect(result.length).toBe(128)
  })

  it('trims before truncating', () => {
    expect(normalizeDeviceName('   ' + 'a'.repeat(64) + '   ')).toBe('a'.repeat(64))
  })
})

describe('effectiveDeviceName — fallback order', () => {
  it('prefers the custom name over the default', () => {
    expect(effectiveDeviceName({ deviceName: 'Office Mac', defaultDeviceName: 'mlab' })).toBe('Office Mac')
  })

  it('falls back to the default when there is no custom name', () => {
    expect(effectiveDeviceName({ deviceName: null, defaultDeviceName: 'mlab' })).toBe('mlab')
  })

  it('ignores a blank custom name and normalizes the default', () => {
    expect(effectiveDeviceName({ deviceName: '  ', defaultDeviceName: ' mlab \n' })).toBe('mlab')
  })

  it("falls back to 'Browser' when both are blank", () => {
    expect(effectiveDeviceName({ deviceName: null, defaultDeviceName: '   ' })).toBe('Browser')
  })

  it('truncates an over-long default', () => {
    expect(effectiveDeviceName({ deviceName: null, defaultDeviceName: '字'.repeat(100) })).toBe('字'.repeat(64))
  })

  it('takes a structural {deviceName, defaultDeviceName} and rejects anything else at compile time', () => {
    // Compile-time only (tsc covers test files): never invoked.
    const wrongStoreState = { status: { kind: 'idle' } }
    // @ts-expect-error — a state without the two name fields (e.g. the wrong store) must not compile
    const call = () => effectiveDeviceName(wrongStoreState)
    expect(call).toBeTypeOf('function')
  })
})

describe('resolveDefaultDeviceName', () => {
  const originalApi = window.electronAPI

  afterEach(() => {
    window.electronAPI = originalApi
    vi.restoreAllMocks()
  })

  it('uses the trimmed Electron hostname when available', async () => {
    window.electronAPI = {
      localDaemonStatus: () => Promise.resolve({ hostname: '  mlab  ' }),
    } as unknown as typeof window.electronAPI
    await expect(resolveDefaultDeviceName()).resolves.toBe('mlab')
  })

  it('falls back to the UA name without Electron', async () => {
    window.electronAPI = undefined as unknown as typeof window.electronAPI
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(
      'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:146.0) Gecko/20100101 Firefox/146.0',
    )
    await expect(resolveDefaultDeviceName()).resolves.toBe('Firefox · Windows')
  })
})

describe('legacy paths re-export the same functions', () => {
  it('lib/device-state/device-name', () => {
    expect(legacyLib.resolveDefaultDeviceName).toBe(resolveDefaultDeviceName)
    expect(legacyLib.parseUserAgentName).toBe(parseUserAgentName)
  })

  it('stores/useDeviceStateStore', () => {
    expect(legacyStore.normalizeDeviceName).toBe(normalizeDeviceName)
    expect(legacyStore.effectiveDeviceName).toBe(effectiveDeviceName)
    expect(legacyStore.DEVICE_NAME_MAX_CODE_POINTS).toBe(DEVICE_NAME_MAX_CODE_POINTS)
  })
})
