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

import { useWorkspaceSettingsStore } from './useWorkspaceSettingsStore'
import { STORAGE_KEYS } from '../lib/storage/keys'

beforeEach(() => {
  localStorage.clear()
  useWorkspaceSettingsStore.setState({ workspaces: {} })
})

describe('useWorkspaceSettingsStore', () => {
  it('set then get returns the value', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'files', { projectPath: '/a' })
    expect(useWorkspaceSettingsStore.getState().get('wsA', 'files')).toEqual({ projectPath: '/a' })
  })

  it('shallow-merges top-level keys; nested objects are fully replaced', () => {
    const { set, get } = useWorkspaceSettingsStore.getState()
    set('wsA', 'files', { a: 1, b: 2 })
    set('wsA', 'files', { b: 20, c: 3 })
    expect(get('wsA', 'files')).toEqual({ a: 1, b: 20, c: 3 })

    set('wsA', 'editor', { nested: { x: 1 } })
    set('wsA', 'editor', { nested: { y: 2 } })
    expect(get('wsA', 'editor')).toEqual({ nested: { y: 2 } })
  })

  it('different workspaces are isolated', () => {
    const { set, get } = useWorkspaceSettingsStore.getState()
    set('wsA', 'files', { depth: 5 })
    expect(get('wsB', 'files')).toBeUndefined()
  })

  it('different modules under the same workspace are isolated', () => {
    const { set, get } = useWorkspaceSettingsStore.getState()
    set('wsA', 'files', { depth: 5 })
    expect(get('wsA', 'editor')).toBeUndefined()
  })

  it('clearWorkspace(wsA) removes all modules under wsA; wsB untouched', () => {
    const { set, clearWorkspace, get } = useWorkspaceSettingsStore.getState()
    set('wsA', 'files', { k: 'v' })
    set('wsA', 'editor', { k: 'v' })
    set('wsB', 'files', { k: 'v' })
    clearWorkspace('wsA')
    expect(get('wsA', 'files')).toBeUndefined()
    expect(get('wsA', 'editor')).toBeUndefined()
    expect(get('wsB', 'files')).toEqual({ k: 'v' })
  })

  it('clearModule(wsA, m1) removes only m1 under wsA', () => {
    const { set, clearModule, get } = useWorkspaceSettingsStore.getState()
    set('wsA', 'files', { k: 'v1' })
    set('wsA', 'editor', { k: 'v2' })
    clearModule('wsA', 'files')
    expect(get('wsA', 'files')).toBeUndefined()
    expect(get('wsA', 'editor')).toEqual({ k: 'v2' })
  })

  it('persists to localStorage under STORAGE_KEYS.WORKSPACE_SETTINGS', () => {
    useWorkspaceSettingsStore.getState().set('wsA', 'files', { projectPath: '/a' })
    const raw = localStorage.getItem(STORAGE_KEYS.WORKSPACE_SETTINGS)
    expect(raw).toBeTruthy()
    const parsed = JSON.parse(raw!)
    expect(parsed.state.workspaces.wsA.files).toEqual({ projectPath: '/a' })
  })

  it('registers itself with syncManager', () => {
    expect(registerSpy).toHaveBeenCalledWith(STORAGE_KEYS.WORKSPACE_SETTINGS, useWorkspaceSettingsStore)
  })
})
