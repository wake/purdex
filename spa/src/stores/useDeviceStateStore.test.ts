import { describe, it, expect, beforeEach } from 'vitest'
// The device name moved to `useDeviceNameStore`; the name suites below still
// go through this module's re-exports, which is what its remaining callers use.
import {
  useDeviceNameStore,
  useDeviceStateStore,
  effectiveDeviceName,
  normalizeDeviceName,
} from './useDeviceStateStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

const store = () => ({ ...useDeviceNameStore.getState(), ...useDeviceStateStore.getState() })

beforeEach(() => {
  useDeviceNameStore.setState({ deviceName: null, defaultDeviceName: 'Browser' })
  useDeviceStateStore.setState({ status: { kind: 'idle' } })
  localStorage.clear()
})

describe('useDeviceStateStore — defaults', () => {
  it('starts with no custom name, Browser default and idle status', () => {
    expect(store().deviceName).toBeNull()
    expect(store().defaultDeviceName).toBe('Browser')
    expect(store().status).toEqual({ kind: 'idle' })
  })

  it('uses the purdex-device-state storage key', () => {
    expect(STORAGE_KEYS.DEVICE_STATE).toBe('purdex-device-state')
    expect(useDeviceNameStore.persist.getOptions().name).toBe('purdex-device-state')
  })
})

describe('useDeviceStateStore — setDeviceName', () => {
  it('trims the name', () => {
    store().setDeviceName('  Office Mac  ')
    expect(store().deviceName).toBe('Office Mac')
  })

  it('treats an empty or blank name as null', () => {
    store().setDeviceName('Office Mac')
    store().setDeviceName('')
    expect(store().deviceName).toBeNull()
    store().setDeviceName('Office Mac')
    store().setDeviceName('   ')
    expect(store().deviceName).toBeNull()
  })

  it('accepts null to reset to the default', () => {
    store().setDeviceName('Office Mac')
    store().setDeviceName(null)
    expect(store().deviceName).toBeNull()
  })

  it('keeps a 64-code-point name intact', () => {
    const name = '機'.repeat(64)
    store().setDeviceName(name)
    expect(store().deviceName).toBe(name)
  })

  it('truncates a 65-code-point CJK name to 64 code points', () => {
    store().setDeviceName('機'.repeat(65))
    expect(Array.from(store().deviceName!)).toHaveLength(64)
    expect(store().deviceName).toBe('機'.repeat(64))
  })

  it('truncates by code point, never splitting a surrogate pair', () => {
    store().setDeviceName('😀'.repeat(65))
    const name = store().deviceName!
    expect(Array.from(name)).toHaveLength(64)
    expect(name).toBe('😀'.repeat(64))
    expect(name.length).toBe(128)
  })

  it('trims before truncating', () => {
    store().setDeviceName('   ' + 'a'.repeat(64) + '   ')
    expect(store().deviceName).toBe('a'.repeat(64))
  })
})

describe('useDeviceStateStore — setStatus', () => {
  it('replaces the status', () => {
    store().setStatus({ kind: 'ok', at: 123, hostId: 'h1' })
    expect(store().status).toEqual({ kind: 'ok', at: 123, hostId: 'h1' })
    store().setStatus({ kind: 'error', message: 'boom' })
    expect(store().status).toEqual({ kind: 'error', message: 'boom' })
  })
})

describe('useDeviceStateStore — persistence', () => {
  it('persists only deviceName', () => {
    useDeviceNameStore.setState({ deviceName: 'Office Mac', defaultDeviceName: 'mlab' })
    const partialize = useDeviceNameStore.persist.getOptions().partialize!
    expect(partialize(useDeviceNameStore.getState())).toEqual({ deviceName: 'Office Mac' })
  })
})

describe('effectiveDeviceName', () => {
  it('returns the custom name when set', () => {
    expect(effectiveDeviceName({ deviceName: 'Office Mac', defaultDeviceName: 'mlab' })).toBe('Office Mac')
  })

  it('falls back to the default name when no custom name', () => {
    expect(effectiveDeviceName({ deviceName: null, defaultDeviceName: 'mlab' })).toBe('mlab')
  })

  it('truncates an over-long default name to 64 code points', () => {
    const long = '字'.repeat(100)
    const result = effectiveDeviceName({ deviceName: null, defaultDeviceName: long })
    expect(Array.from(result)).toHaveLength(64)
    expect(result).toBe('字'.repeat(64))
  })

  it('trims a whitespace-padded default name', () => {
    expect(effectiveDeviceName({ deviceName: null, defaultDeviceName: '  mlab.local \n' })).toBe('mlab.local')
  })

  it("falls back to 'Browser' when the default name is whitespace-only", () => {
    expect(effectiveDeviceName({ deviceName: null, defaultDeviceName: '   ' })).toBe('Browser')
  })

  it('ignores a blank custom name and uses the normalized default', () => {
    expect(effectiveDeviceName({ deviceName: '  ', defaultDeviceName: ' mlab ' })).toBe('mlab')
  })
})

describe('normalizeDeviceName', () => {
  it('trims surrounding whitespace', () => {
    expect(normalizeDeviceName('  Office Mac  ')).toBe('Office Mac')
  })

  it('returns null for empty or whitespace-only input', () => {
    expect(normalizeDeviceName('')).toBeNull()
    expect(normalizeDeviceName(' \t\n ')).toBeNull()
  })

  it('keeps names at exactly 64 code points', () => {
    const name = 'a'.repeat(64)
    expect(normalizeDeviceName(name)).toBe(name)
  })

  it('truncates to 64 code points without splitting emoji', () => {
    const result = normalizeDeviceName('😀'.repeat(70))
    expect(result).toBe('😀'.repeat(64))
  })
})
