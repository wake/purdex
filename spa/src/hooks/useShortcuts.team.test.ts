// spa/src/hooks/useShortcuts.team.test.ts — stepping and ⌘1–8 work on the VISIBLE tabs: the members of a collapsed team are
// skipped (spec R8).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))

import { renderHook } from '@testing-library/react'
import { useShortcuts } from './useShortcuts'
import { useTabStore } from '../stores/useTabStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { KEY, resetTeamStores, seedScene } from '../lib/team/__tests__/team-fixture'

function mockElectronAPI() {
  let cb: ((p: { action: string }) => void) | null = null
  ;(window as unknown as Record<string, unknown>).electronAPI = {
    onShortcut: (f: (p: { action: string }) => void) => { cb = f; return vi.fn() },
    signalReady: () => {},
  }
  return (action: string) => cb?.({ action })
}

const active = () => useTabStore.getState().activeTabId

beforeEach(() => {
  resetTeamStores()
  seedScene({
    members: [['A', 'a-tm'], ['B', 'b-tm']],
    tabs: [['x', null], ['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['z', null]],
    workspaces: [{ id: 'w1', tabs: ['x', 'lead', 'ma', 'mb', 'z'] }],
    activeTabId: 'lead',
  })
})
afterEach(() => { delete (window as unknown as Record<string, unknown>).electronAPI })

describe('useShortcuts with a collapsed team', () => {
  it('next-tab skips hidden members', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    fire('next-tab')
    expect(active()).toBe('ma') // expanded: the member is the next tab
    useTeamUiStore.getState().setCollapsed(KEY, true)
    useTabStore.getState().setActiveTab('lead')
    fire('next-tab')
    expect(active()).toBe('z') // collapsed: ma and mb are skipped
  })

  it('prev-tab skips hidden members and wraps over the visible list', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    useTeamUiStore.getState().setCollapsed(KEY, true)
    useTabStore.getState().setActiveTab('z')
    fire('prev-tab')
    expect(active()).toBe('lead')
    fire('prev-tab')
    expect(active()).toBe('x')
    fire('prev-tab')
    expect(active()).toBe('z') // wrapped past the hidden members
  })

  it('switch-tab-3 counts visible tabs only; switch-tab-last is the last visible', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    fire('switch-tab-3')
    expect(active()).toBe('ma') // expanded: x, lead, ma
    useTeamUiStore.getState().setCollapsed(KEY, true)
    fire('switch-tab-3')
    expect(active()).toBe('z') // collapsed: x, lead, z
    fire('switch-tab-4')
    expect(active()).toBe('z') // there is no 4th visible tab: unchanged
    fire('switch-tab-2')
    fire('switch-tab-last')
    expect(active()).toBe('z')
  })

  it('a team that is not collapsed changes nothing', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    useTabStore.getState().setActiveTab('x')
    fire('next-tab'); expect(active()).toBe('lead')
    fire('next-tab'); expect(active()).toBe('ma')
    fire('next-tab'); expect(active()).toBe('mb')
  })

  it('close-tab still reaches a hidden member that is the active tab (its workspace owns it)', () => {
    const fire = mockElectronAPI()
    renderHook(() => useShortcuts())
    useTeamUiStore.getState().setCollapsed(KEY, true)
    useTabStore.getState().setActiveTab('mb')
    fire('close-tab')
    expect(useTabStore.getState().tabs.mb).toBeUndefined()
  })
})
