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

import { useGlobalSettingsStore } from './useGlobalSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

async function rehydrateGlobalSettingsStore() {
  await useGlobalSettingsStore.persist.rehydrate()
}

beforeEach(() => {
  localStorage.clear()
  useGlobalSettingsStore.setState({ modules: {} })
})

describe('useGlobalSettingsStore', () => {
  it('set then get returns the merged value', () => {
    useGlobalSettingsStore.getState().set('files', { projectPath: '/tmp' })
    expect(useGlobalSettingsStore.getState().get('files')).toEqual({ projectPath: '/tmp' })
  })

  it('shallow-merges top-level keys; nested objects are fully replaced', () => {
    const { set, get } = useGlobalSettingsStore.getState()
    set('files', { a: 1, b: 2 })
    set('files', { b: 20, c: 3 })
    expect(get('files')).toEqual({ a: 1, b: 20, c: 3 })

    set('editor', { nested: { x: 1 } })
    set('editor', { nested: { y: 2 } })
    expect(get('editor')).toEqual({ nested: { y: 2 } })
  })

  it('returns undefined for an unknown module', () => {
    expect(useGlobalSettingsStore.getState().get('nope')).toBeUndefined()
  })

  it('clear(moduleId) removes only that module', () => {
    const { set, clear, get } = useGlobalSettingsStore.getState()
    set('m1', { k: 'v1' })
    set('m2', { k: 'v2' })
    clear('m1')
    expect(get('m1')).toBeUndefined()
    expect(get('m2')).toEqual({ k: 'v2' })
  })

  it('clear() with no argument removes all modules', () => {
    const { set, clear, get } = useGlobalSettingsStore.getState()
    set('m1', { k: 'v1' })
    set('m2', { k: 'v2' })
    clear()
    expect(get('m1')).toBeUndefined()
    expect(get('m2')).toBeUndefined()
  })

  it('keeps two modules independent', () => {
    const { set, get } = useGlobalSettingsStore.getState()
    set('files', { depth: 5 })
    set('editor', { wrap: true })
    expect(get('files')).toEqual({ depth: 5 })
    expect(get('editor')).toEqual({ wrap: true })
  })

  it('persists to localStorage under STORAGE_KEYS.GLOBAL_SETTINGS', () => {
    useGlobalSettingsStore.getState().set('files', { projectPath: '/tmp' })
    const raw = localStorage.getItem(STORAGE_KEYS.GLOBAL_SETTINGS)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.modules.files).toEqual({ projectPath: '/tmp' })
  })

  it('registers itself with syncManager', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.GLOBAL_SETTINGS, useGlobalSettingsStore)
  })

  it('heals malformed rehydrate shapes so get/set/clear stay usable', async () => {
    localStorage.setItem(
      STORAGE_KEYS.GLOBAL_SETTINGS,
      JSON.stringify({
        state: {
          modules: {
            valid: { enabled: true },
            broken: null,
          },
        },
        version: 1,
      }),
    )

    await rehydrateGlobalSettingsStore()

    expect(useGlobalSettingsStore.getState().modules).toEqual({
      valid: { enabled: true },
    })

    useGlobalSettingsStore.getState().set('editor', { wrap: true })
    expect(useGlobalSettingsStore.getState().get('editor')).toEqual({ wrap: true })

    useGlobalSettingsStore.getState().clear('valid')
    expect(useGlobalSettingsStore.getState().get('valid')).toBeUndefined()
  })

  it('resets a non-object modules root during rehydrate', async () => {
    localStorage.setItem(
      STORAGE_KEYS.GLOBAL_SETTINGS,
      JSON.stringify({
        state: { modules: null },
        version: 1,
      }),
    )

    await rehydrateGlobalSettingsStore()

    expect(useGlobalSettingsStore.getState().modules).toEqual({})
    useGlobalSettingsStore.getState().set('files', { projectPath: '/tmp' })
    expect(useGlobalSettingsStore.getState().get('files')).toEqual({ projectPath: '/tmp' })
  })
})
