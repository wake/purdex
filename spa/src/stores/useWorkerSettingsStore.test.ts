import { describe, it, expect, beforeEach, vi } from 'vitest'

const registerSpy = vi.hoisted(() => vi.fn())
const notifySpy = vi.hoisted(() => vi.fn())

vi.mock('../lib/storage/sync', () => ({
  syncManager: {
    register: registerSpy,
    notify: notifySpy,
    destroy: vi.fn(),
  },
  createSyncManager: vi.fn(),
}))

import { DEFAULT_WORKER_SETTINGS, PERMISSION_TIMEOUT_MINUTES, useWorkerSettingsStore } from './useWorkerSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

function resetStore() {
  // merge-mode reset — do NOT pass `true` (replace mode wipes actions).
  useWorkerSettingsStore.setState({ ...DEFAULT_WORKER_SETTINGS, permissionTimeoutMin: 0 })
}

const persisted = () => JSON.parse(localStorage.getItem(STORAGE_KEYS.WORKER_SETTINGS) ?? 'null') as { state: Record<string, unknown> } | null

beforeEach(() => {
  localStorage.clear()
  resetStore()
})

describe('useWorkerSettingsStore', () => {
  it('defaults: purdex theme, mono icon, no custom icon', () => {
    const s = useWorkerSettingsStore.getState()
    expect(s.theme).toBe('purdex')
    expect(s.iconStyle).toBe('mono')
    expect(s.customIcon).toBe('')
  })

  it('registers with syncManager under WORKER_SETTINGS', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.WORKER_SETTINGS, useWorkerSettingsStore)
  })

  it('setTheme / setIconStyle / setCustomIcon update the store', () => {
    const { setTheme, setIconStyle, setCustomIcon } = useWorkerSettingsStore.getState()
    setTheme('other')
    setIconStyle('color')
    setCustomIcon('Robot')
    const s = useWorkerSettingsStore.getState()
    expect(s.theme).toBe('other')
    expect(s.iconStyle).toBe('color')
    expect(s.customIcon).toBe('Robot')
  })

  it('rehydrate: malformed persisted fields fall back to defaults, valid ones are kept', async () => {
    localStorage.setItem(
      STORAGE_KEYS.WORKER_SETTINGS,
      JSON.stringify({ state: { theme: 'custom-theme', iconStyle: 'not-a-style', customIcon: 42 }, version: 1 }),
    )
    await useWorkerSettingsStore.persist.rehydrate()
    const s = useWorkerSettingsStore.getState()
    expect(s.theme).toBe('custom-theme')
    expect(s.iconStyle).toBe('mono')
    expect(s.customIcon).toBe('')
  })

  it('rehydrate: a persisted `null` customIcon (pre-string-type shape) sanitizes to \'\'', async () => {
    localStorage.setItem(
      STORAGE_KEYS.WORKER_SETTINGS,
      JSON.stringify({ state: { theme: 'purdex', iconStyle: 'mono', customIcon: null }, version: 1 }),
    )
    await useWorkerSettingsStore.persist.rehydrate()
    expect(useWorkerSettingsStore.getState().customIcon).toBe('')
  })

  // Permission channel spec §5.5 / plan Task 6: the approval timeout is a device-local setting (never projected into
  // Profile Sync — projections.test.ts pins that side), persisted like the rest of the store.
  describe('permissionTimeoutMin', () => {
    it('defaults to 0 (never time out) and offers 0 / 5 / 15 / 30 / 60', () => {
      expect(useWorkerSettingsStore.getState().permissionTimeoutMin).toBe(0)
      expect([...PERMISSION_TIMEOUT_MINUTES]).toEqual([0, 5, 15, 30, 60])
    })

    it('setPermissionTimeoutMin updates the store and persists it', () => {
      useWorkerSettingsStore.getState().setPermissionTimeoutMin(15)
      expect(useWorkerSettingsStore.getState().permissionTimeoutMin).toBe(15)
      expect(persisted()?.state.permissionTimeoutMin).toBe(15)
    })

    it('survives a reload (rehydrate restores a persisted value)', async () => {
      localStorage.setItem(
        STORAGE_KEYS.WORKER_SETTINGS,
        JSON.stringify({ state: { theme: 'purdex', iconStyle: 'mono', customIcon: '', permissionTimeoutMin: 60 }, version: 1 }),
      )
      await useWorkerSettingsStore.persist.rehydrate()
      expect(useWorkerSettingsStore.getState().permissionTimeoutMin).toBe(60)
    })

    it.each([[7], ['15'], [null], [-5], [15.5], [true]])('a persisted %j sanitizes to 0', async (bad) => {
      useWorkerSettingsStore.setState({ permissionTimeoutMin: 30 })
      localStorage.setItem(
        STORAGE_KEYS.WORKER_SETTINGS,
        JSON.stringify({ state: { theme: 'purdex', iconStyle: 'mono', customIcon: '', permissionTimeoutMin: bad }, version: 1 }),
      )
      await useWorkerSettingsStore.persist.rehydrate()
      expect(useWorkerSettingsStore.getState().permissionTimeoutMin).toBe(0)
    })

    it('a Profile Sync apply of the synced fields (setState + rehydrate, apply-to-stores.ts) leaves it alone', async () => {
      useWorkerSettingsStore.getState().setPermissionTimeoutMin(30)
      useWorkerSettingsStore.setState({ theme: 'mono-dark', iconStyle: 'color' })
      await useWorkerSettingsStore.persist.rehydrate()
      expect(useWorkerSettingsStore.getState()).toMatchObject({ theme: 'mono-dark', iconStyle: 'color', permissionTimeoutMin: 30 })
    })
  })

  it('rehydrate: happy-path persisted values are restored', async () => {
    localStorage.setItem(
      STORAGE_KEYS.WORKER_SETTINGS,
      JSON.stringify({ state: { theme: 'purdex', iconStyle: 'custom', customIcon: 'Star' }, version: 1 }),
    )
    await useWorkerSettingsStore.persist.rehydrate()
    const s = useWorkerSettingsStore.getState()
    expect(s.theme).toBe('purdex')
    expect(s.iconStyle).toBe('custom')
    expect(s.customIcon).toBe('Star')
  })
})
