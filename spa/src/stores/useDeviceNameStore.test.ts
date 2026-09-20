import { describe, it, expect, beforeEach, vi } from 'vitest'
import {
  __resetDefaultDeviceNameForTest,
  ensureDefaultDeviceName,
  useDeviceNameStore,
} from './useDeviceNameStore'
import * as legacyStore from './useDeviceStateStore'
import { resolveDefaultDeviceName } from '../lib/device-name'

vi.mock('../lib/device-name', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/device-name')>()),
  resolveDefaultDeviceName: vi.fn(),
}))

const resolve = vi.mocked(resolveDefaultDeviceName)
const store = () => useDeviceNameStore.getState()

beforeEach(() => {
  __resetDefaultDeviceNameForTest()
  resolve.mockReset()
  resolve.mockResolvedValue('Mac')
  useDeviceNameStore.setState({ deviceName: null, defaultDeviceName: 'Browser' })
  localStorage.clear()
})

describe('useDeviceNameStore — defaults and setDeviceName', () => {
  it('starts with no custom name and the Browser default', () => {
    expect(store().deviceName).toBeNull()
    expect(store().defaultDeviceName).toBe('Browser')
  })

  it('normalizes on set: trims, blank → null, null resets', () => {
    store().setDeviceName('  Office Mac  ')
    expect(store().deviceName).toBe('Office Mac')
    store().setDeviceName('   ')
    expect(store().deviceName).toBeNull()
    store().setDeviceName('Office Mac')
    store().setDeviceName(null)
    expect(store().deviceName).toBeNull()
  })

  it('truncates to 64 code points', () => {
    store().setDeviceName('😀'.repeat(65))
    expect(store().deviceName).toBe('😀'.repeat(64))
  })
})

describe('useDeviceNameStore — persistence is byte-identical to the old store', () => {
  it('keeps the legacy storage key and version', () => {
    const options = useDeviceNameStore.persist.getOptions()
    expect(options.name).toBe('purdex-device-state')
    expect(options.version).toBe(1)
  })

  it('partializes to {deviceName} only', () => {
    useDeviceNameStore.setState({ deviceName: 'Office Mac', defaultDeviceName: 'mlab' })
    const partialize = useDeviceNameStore.persist.getOptions().partialize!
    expect(partialize(store())).toEqual({ deviceName: 'Office Mac' })
  })

  it('writes exactly the bytes the old store wrote', () => {
    useDeviceNameStore.setState({ defaultDeviceName: 'mlab' })
    store().setDeviceName('Office Mac')
    expect(localStorage.getItem('purdex-device-state')).toBe('{"state":{"deviceName":"Office Mac"},"version":1}')
  })

  it('rehydrates a value written by the old store, without migration', async () => {
    localStorage.setItem('purdex-device-state', '{"state":{"deviceName":"Old Name"},"version":1}')
    await useDeviceNameStore.persist.rehydrate()
    expect(store().deviceName).toBe('Old Name')
    expect(store().defaultDeviceName).toBe('Browser')
  })
})

describe('legacy useDeviceStateStore', () => {
  it('re-exports the same store instance and ensure function', () => {
    expect(legacyStore.useDeviceNameStore).toBe(useDeviceNameStore)
    expect(legacyStore.ensureDefaultDeviceName).toBe(ensureDefaultDeviceName)
  })

  it('is status-only and no longer persisted', () => {
    expect(Object.keys(legacyStore.useDeviceStateStore.getState()).sort()).toEqual(['setStatus', 'status'])
    expect('persist' in legacyStore.useDeviceStateStore).toBe(false)
  })
})

describe('ensureDefaultDeviceName', () => {
  it('does not resolve at module load', () => {
    expect(resolve).not.toHaveBeenCalled()
    expect(store().defaultDeviceName).toBe('Browser')
  })

  it('stores the normalized resolved name', async () => {
    resolve.mockResolvedValue(`  ${'字'.repeat(100)}  `)
    await ensureDefaultDeviceName()
    expect(store().defaultDeviceName).toBe('字'.repeat(64))
  })

  it("stores 'Browser' when the resolved name is blank", async () => {
    useDeviceNameStore.setState({ defaultDeviceName: 'stale' })
    resolve.mockResolvedValue('   ')
    await ensureDefaultDeviceName()
    expect(store().defaultDeviceName).toBe('Browser')
  })

  it('is single-flight: two concurrent calls resolve once', async () => {
    let release!: (name: string) => void
    resolve.mockReturnValue(new Promise<string>((r) => (release = r)))
    const a = ensureDefaultDeviceName()
    const b = ensureDefaultDeviceName()
    expect(resolve).toHaveBeenCalledTimes(1)
    release('Mac')
    await Promise.all([a, b])
    expect(resolve).toHaveBeenCalledTimes(1)
    expect(store().defaultDeviceName).toBe('Mac')
  })

  it('does not resolve again once resolved', async () => {
    await ensureDefaultDeviceName()
    resolve.mockResolvedValue('Other')
    await ensureDefaultDeviceName()
    expect(resolve).toHaveBeenCalledTimes(1)
    expect(store().defaultDeviceName).toBe('Mac')
  })

  it('keeps the fallback on failure, does not throw, and retries next time', async () => {
    resolve.mockRejectedValueOnce(new Error('boom'))
    await expect(ensureDefaultDeviceName()).resolves.toBeUndefined()
    expect(store().defaultDeviceName).toBe('Browser')
    await ensureDefaultDeviceName()
    expect(resolve).toHaveBeenCalledTimes(2)
    expect(store().defaultDeviceName).toBe('Mac')
  })
})
