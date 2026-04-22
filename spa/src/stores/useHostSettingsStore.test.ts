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

import { useHostSettingsStore } from './useHostSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

async function rehydrateHostSettingsStore() {
  await useHostSettingsStore.persist.rehydrate()
}

beforeEach(() => {
  localStorage.clear()
  useHostSettingsStore.setState({ hosts: {} })
})

describe('useHostSettingsStore', () => {
  it('set then get returns the value', () => {
    useHostSettingsStore.getState().set('hostA', 'files', { projectPath: '/a' })
    expect(useHostSettingsStore.getState().get('hostA', 'files')).toEqual({ projectPath: '/a' })
  })

  it('shallow-merges top-level keys; nested objects are fully replaced', () => {
    const { set, get } = useHostSettingsStore.getState()
    set('hostA', 'files', { a: 1, b: 2 })
    set('hostA', 'files', { b: 20, c: 3 })
    expect(get('hostA', 'files')).toEqual({ a: 1, b: 20, c: 3 })

    set('hostA', 'editor', { nested: { x: 1 } })
    set('hostA', 'editor', { nested: { y: 2 } })
    expect(get('hostA', 'editor')).toEqual({ nested: { y: 2 } })
  })

  it('different hosts are isolated', () => {
    const { set, get } = useHostSettingsStore.getState()
    set('hostA', 'files', { depth: 5 })
    expect(get('hostB', 'files')).toBeUndefined()
  })

  it('different modules under the same host are isolated', () => {
    const { set, get } = useHostSettingsStore.getState()
    set('hostA', 'files', { depth: 5 })
    expect(get('hostA', 'editor')).toBeUndefined()
  })

  it('clearHost(hostA) removes all modules under hostA; hostB untouched', () => {
    const { set, clearHost, get } = useHostSettingsStore.getState()
    set('hostA', 'files', { k: 'v' })
    set('hostA', 'editor', { k: 'v' })
    set('hostB', 'files', { k: 'v' })
    clearHost('hostA')
    expect(get('hostA', 'files')).toBeUndefined()
    expect(get('hostA', 'editor')).toBeUndefined()
    expect(get('hostB', 'files')).toEqual({ k: 'v' })
  })

  it('clearModule(hostA, m1) removes only m1 under hostA', () => {
    const { set, clearModule, get } = useHostSettingsStore.getState()
    set('hostA', 'files', { k: 'v1' })
    set('hostA', 'editor', { k: 'v2' })
    clearModule('hostA', 'files')
    expect(get('hostA', 'files')).toBeUndefined()
    expect(get('hostA', 'editor')).toEqual({ k: 'v2' })
  })

  it('persists to localStorage under STORAGE_KEYS.HOST_SETTINGS', () => {
    useHostSettingsStore.getState().set('hostA', 'files', { projectPath: '/a' })
    const raw = localStorage.getItem(STORAGE_KEYS.HOST_SETTINGS)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.hosts.hostA.files).toEqual({ projectPath: '/a' })
  })

  it('registers itself with syncManager', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.HOST_SETTINGS, useHostSettingsStore)
  })

  it('heals malformed host slots during rehydrate so get/set/clear stay usable', async () => {
    localStorage.setItem(
      STORAGE_KEYS.HOST_SETTINGS,
      JSON.stringify({
        state: {
          hosts: {
            hostA: {
              files: { projectPath: '/a' },
              broken: null,
            },
            hostB: [],
          },
        },
        version: 1,
      }),
    )

    await rehydrateHostSettingsStore()

    expect(useHostSettingsStore.getState().hosts).toEqual({
      hostA: {
        files: { projectPath: '/a' },
      },
    })

    useHostSettingsStore.getState().set('hostA', 'editor', { wrap: true })
    expect(useHostSettingsStore.getState().get('hostA', 'editor')).toEqual({ wrap: true })

    useHostSettingsStore.getState().clearHost('hostA')
    expect(useHostSettingsStore.getState().get('hostA', 'files')).toBeUndefined()
  })

  describe('immutability (#540)', () => {
    it('get() returns a frozen snapshot', () => {
      const { set, get } = useHostSettingsStore.getState()
      set('hostA', 'editor', { homePath: '/home/x' })
      const snapshot = get('hostA', 'editor')!
      expect(Object.isFrozen(snapshot)).toBe(true)
    })

    it('mutating the returned snapshot does not leak into store state', () => {
      const { set, get } = useHostSettingsStore.getState()
      set('hostA', 'editor', { homePath: '/home/x' })
      const snapshot = get('hostA', 'editor')!
      expect(() => {
        ;(snapshot as Record<string, unknown>).homePath = '/hacked'
      }).toThrow()
      expect(get('hostA', 'editor')).toEqual({ homePath: '/home/x' })
    })

    it('set() still patches after prior get() returned frozen snapshot', () => {
      const { set, get } = useHostSettingsStore.getState()
      set('hostA', 'editor', { homePath: '/home/x' })
      void get('hostA', 'editor')
      set('hostA', 'editor', { homePath: '/home/y' })
      expect(get('hostA', 'editor')).toEqual({ homePath: '/home/y' })
    })
  })

  it('resets a non-object hosts root during rehydrate', async () => {
    localStorage.setItem(
      STORAGE_KEYS.HOST_SETTINGS,
      JSON.stringify({
        state: { hosts: [] },
        version: 1,
      }),
    )

    await rehydrateHostSettingsStore()

    expect(useHostSettingsStore.getState().hosts).toEqual({})
    useHostSettingsStore.getState().set('hostA', 'files', { projectPath: '/a' })
    expect(useHostSettingsStore.getState().get('hostA', 'files')).toEqual({ projectPath: '/a' })
  })
})
