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

import { useModuleEnabledStore } from './useModuleEnabledStore'
import { STORAGE_KEYS } from '../lib/storage/keys'
import { clearModuleRegistry, registerModule } from '../lib/module-registry'

function resetStore() {
  // merge-mode reset — do NOT pass `true` (replace mode wipes actions).
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
}

beforeEach(() => {
  localStorage.clear()
  clearModuleRegistry()
  resetStore()
})

describe('useModuleEnabledStore', () => {
  it('T1-1: disableable:true module without persist defaults to enabled', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    expect(useModuleEnabledStore.getState().isEnabled('editor')).toBe(true)
  })

  it('T1-2: non-disableable module is always enabled regardless of persist', () => {
    registerModule({ id: 'sessions', name: 'Sessions' /* no disableable */ })
    expect(useModuleEnabledStore.getState().isEnabled('sessions')).toBe(true)
    // Even if a stale persist entry said false, non-disableable wins.
    useModuleEnabledStore.setState({ enabled: { sessions: false }, baseline: null })
    expect(useModuleEnabledStore.getState().isEnabled('sessions')).toBe(true)
  })

  it('T1-3: setEnabled(id,false) → isEnabled returns false', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    useModuleEnabledStore.getState().setEnabled('editor', false)
    expect(useModuleEnabledStore.getState().isEnabled('editor')).toBe(false)
  })

  it('T1-4: setEnabled back to true restores', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    const { setEnabled, isEnabled } = useModuleEnabledStore.getState()
    setEnabled('editor', false)
    setEnabled('editor', true)
    expect(isEnabled('editor')).toBe(true)
  })

  it('T1-5: setEnabled persists to localStorage under MODULE_ENABLED', () => {
    useModuleEnabledStore.getState().setEnabled('editor', false)
    const raw = localStorage.getItem(STORAGE_KEYS.MODULE_ENABLED)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.enabled.editor).toBe(false)
  })

  it('T1-6: rehydrate — store reflects persisted value from localStorage', async () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    localStorage.setItem(
      STORAGE_KEYS.MODULE_ENABLED,
      JSON.stringify({ state: { enabled: { editor: false } }, version: 1 }),
    )
    await useModuleEnabledStore.persist.rehydrate()
    expect(useModuleEnabledStore.getState().isEnabled('editor')).toBe(false)
  })

  it('T1-7: captureBaseline first call sets baseline to the given snapshot', () => {
    useModuleEnabledStore.getState().captureBaseline({ editor: true, files: true })
    expect(useModuleEnabledStore.getState().baseline).toEqual({ editor: true, files: true })
  })

  it('T1-8: captureBaseline repeat call is a no-op (first value retained)', () => {
    const { captureBaseline } = useModuleEnabledStore.getState()
    captureBaseline({ editor: true })
    captureBaseline({ editor: false })
    expect(useModuleEnabledStore.getState().baseline).toEqual({ editor: true })
  })

  it('T1-9: hasPendingChanges is false when enabled matches baseline', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    const { captureBaseline, setEnabled, hasPendingChanges } = useModuleEnabledStore.getState()
    captureBaseline({ editor: true })
    setEnabled('editor', true)
    expect(hasPendingChanges()).toBe(false)
  })

  it('T1-10: hasPendingChanges is true after user toggles diff from baseline', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    const { captureBaseline, setEnabled, hasPendingChanges } = useModuleEnabledStore.getState()
    captureBaseline({ editor: true })
    setEnabled('editor', false)
    expect(hasPendingChanges()).toBe(true)
  })

  it('T1-11: hasPendingChanges is false when baseline is null (never captured)', () => {
    expect(useModuleEnabledStore.getState().hasPendingChanges()).toBe(false)
  })

  it('T1-12: resetAll clears enabled; baseline is unchanged', () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    registerModule({ id: 'browser', name: 'Browser', disableable: true })
    const { captureBaseline, setEnabled, resetAll } = useModuleEnabledStore.getState()
    captureBaseline({ editor: false })
    setEnabled('browser', false)
    resetAll()
    const state = useModuleEnabledStore.getState()
    expect(state.enabled).toEqual({})
    expect(state.baseline).toEqual({ editor: false })
  })

  // AR-1 (codex adversarial review #617): rehydrate must reject malformed
  // payloads so a corrupted localStorage cannot silently hide / un-hide
  // module settings contributions.
  it('T1-13: rehydrate drops non-boolean values', async () => {
    registerModule({ id: 'editor', name: 'Editor', disableable: true })
    localStorage.setItem(
      STORAGE_KEYS.MODULE_ENABLED,
      JSON.stringify({
        state: { enabled: { editor: 'false', files: 1, browser: null, memory: false } },
        version: 1,
      }),
    )
    await useModuleEnabledStore.persist.rehydrate()
    const state = useModuleEnabledStore.getState()
    expect(state.enabled).toEqual({ memory: false })
  })

  it('T1-14: rehydrate drops prototype-polluting and non-string keys', async () => {
    localStorage.setItem(
      STORAGE_KEYS.MODULE_ENABLED,
      JSON.stringify({
        state: {
          enabled: {
            __proto__: true,
            constructor: false,
            prototype: true,
            '': false,
            safe: true,
          },
        },
        version: 1,
      }),
    )
    await useModuleEnabledStore.persist.rehydrate()
    const state = useModuleEnabledStore.getState()
    expect(state.enabled).toEqual({ safe: true })
    // Prototype integrity: no accidental pollution.
    expect(({} as Record<string, unknown>).__proto__).toBe(Object.prototype)
  })

  it('T1-15: rehydrate tolerates non-object payloads without throwing', async () => {
    // All three shapes represent real corruption modes: enabled missing,
    // enabled is a string, outer state is a primitive.
    const payloads = [
      { state: {}, version: 1 },
      { state: { enabled: 'not-an-object' }, version: 1 },
      { state: { enabled: null }, version: 1 },
    ]
    for (const p of payloads) {
      localStorage.setItem(STORAGE_KEYS.MODULE_ENABLED, JSON.stringify(p))
      await useModuleEnabledStore.persist.rehydrate()
      expect(useModuleEnabledStore.getState().enabled).toEqual({})
    }
  })
})
