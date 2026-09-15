import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { parseUserAgentName, resolveDefaultDeviceName } from './device-name'

const UA = {
  chromeMac:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36',
  safariMac:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15',
  firefoxWin: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:146.0) Gecko/20100101 Firefox/146.0',
  edgeWin:
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36 Edg/145.0.0.0',
  chromeLinux:
    'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36',
  safariIos:
    'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
  chromeAndroid:
    'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Mobile Safari/537.36',
}

describe('parseUserAgentName', () => {
  it.each([
    ['Chrome on macOS', UA.chromeMac, 'Chrome · macOS'],
    ['Safari on macOS', UA.safariMac, 'Safari · macOS'],
    ['Firefox on Windows', UA.firefoxWin, 'Firefox · Windows'],
    ['Edge on Windows', UA.edgeWin, 'Edge · Windows'],
    ['Chrome on Linux', UA.chromeLinux, 'Chrome · Linux'],
    ['Safari on iOS', UA.safariIos, 'Safari · iOS'],
    ['Chrome on Android', UA.chromeAndroid, 'Chrome · Android'],
  ])('%s', (_label, ua, expected) => {
    expect(parseUserAgentName(ua)).toBe(expected)
  })

  it('detects iPad as iOS', () => {
    const ua =
      'Mozilla/5.0 (iPad; CPU OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1'
    expect(parseUserAgentName(ua)).toBe('Safari · iOS')
  })

  it('returns Browser for an unknown UA', () => {
    expect(parseUserAgentName('curl/8.0')).toBe('Browser')
    expect(parseUserAgentName('')).toBe('Browser')
  })

  it('returns just the browser name when the OS is unknown', () => {
    expect(parseUserAgentName('Mozilla/5.0 (Unknown) Gecko/20100101 Firefox/146.0')).toBe('Firefox')
  })

  it('pairs an unknown browser with a known OS', () => {
    expect(parseUserAgentName('SomeBot/1.0 (Windows NT 10.0)')).toBe('Browser · Windows')
  })
})

describe('resolveDefaultDeviceName', () => {
  const originalApi = window.electronAPI

  beforeEach(() => {
    vi.spyOn(navigator, 'userAgent', 'get').mockReturnValue(UA.chromeMac)
  })

  afterEach(() => {
    window.electronAPI = originalApi
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  function mockElectron(localDaemonStatus: () => Promise<unknown>) {
    window.electronAPI = { localDaemonStatus } as unknown as typeof window.electronAPI
  }

  it('uses the Electron hostname when available', async () => {
    mockElectron(() => Promise.resolve({ hostname: '  mlab  ' }))
    await expect(resolveDefaultDeviceName()).resolves.toBe('mlab')
  })

  it('falls back to the UA name when hostname is empty', async () => {
    mockElectron(() => Promise.resolve({ hostname: '   ' }))
    await expect(resolveDefaultDeviceName()).resolves.toBe('Chrome · macOS')
  })

  it('falls back to the UA name when hostname is not a string', async () => {
    mockElectron(() => Promise.resolve({}))
    await expect(resolveDefaultDeviceName()).resolves.toBe('Chrome · macOS')
  })

  it('falls back to the UA name when localDaemonStatus rejects', async () => {
    mockElectron(() => Promise.reject(new Error('ipc failed')))
    await expect(resolveDefaultDeviceName()).resolves.toBe('Chrome · macOS')
  })

  it('falls back to the UA name when localDaemonStatus throws synchronously', async () => {
    mockElectron(() => {
      throw new Error('sync boom')
    })
    await expect(resolveDefaultDeviceName()).resolves.toBe('Chrome · macOS')
  })

  it('falls back to the UA name after 1500 ms when localDaemonStatus never resolves', async () => {
    vi.useFakeTimers()
    mockElectron(() => new Promise(() => {}))
    let result: string | undefined
    void resolveDefaultDeviceName().then((r) => {
      result = r
    })
    await vi.advanceTimersByTimeAsync(1499)
    expect(result).toBeUndefined()
    await vi.advanceTimersByTimeAsync(1)
    expect(result).toBe('Chrome · macOS')
  })

  it('uses the UA name when there is no electronAPI', async () => {
    window.electronAPI = undefined
    await expect(resolveDefaultDeviceName()).resolves.toBe('Chrome · macOS')
  })
})
