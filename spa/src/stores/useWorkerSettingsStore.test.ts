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

import { DEFAULT_WORKER_SETTINGS, useWorkerSettingsStore } from './useWorkerSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

function resetStore() {
  // merge-mode reset — do NOT pass `true` (replace mode wipes actions).
  useWorkerSettingsStore.setState({ ...DEFAULT_WORKER_SETTINGS })
}

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
