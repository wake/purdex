// useWorkspaceWindowActions — a workspace torn off / merged to another window marks its tabs as moving (#2140), so the
// team lifecycle does not read the lead's departure as a close.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useWorkspaceWindowActions } from './useWorkspaceWindowActions'
import { isTabMoving, resetMovingTabs } from '../lib/team/moving-tabs'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { resetTeamStores, seedScene } from '../lib/team/__tests__/team-fixture'

beforeEach(() => {
  resetTeamStores()
  resetMovingTabs()
  seedScene({
    members: [],
    tabs: [['lead', null], ['m', null]],
    workspaces: [{ id: 'w1', tabs: ['lead', 'm'] }, { id: 'w2', tabs: [] }],
    activeTabId: 'lead',
  })
})
afterEach(() => {
  delete (window as unknown as Record<string, unknown>).electronAPI
})

describe('workspace tear-off / merge', () => {
  it('marks the workspace\'s tabs as moving when it tears off', async () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffWorkspace: vi.fn().mockResolvedValue(undefined) }
    const { result } = renderHook(() => useWorkspaceWindowActions())
    await act(async () => { await result.current.handleWsTearOff('w1') })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual([])
    expect(isTabMoving('lead')).toBe(true)
    expect(isTabMoving('m')).toBe(true)
    expect(useWorkspaceStore.getState().workspaces.some((w) => w.id === 'w1')).toBe(false)
  })

  it('marks them when it merges to another window', async () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { mergeWorkspace: vi.fn().mockResolvedValue(undefined) }
    const { result } = renderHook(() => useWorkspaceWindowActions())
    await act(async () => { await result.current.handleWsMergeTo('w1', 'win-2') })
    expect(isTabMoving('lead')).toBe(true)
  })

  it('marks nothing when the IPC fails (the tabs stay)', async () => {
    ;(window as unknown as Record<string, unknown>).electronAPI = { tearOffWorkspace: vi.fn().mockRejectedValue(new Error('x')) }
    const { result } = renderHook(() => useWorkspaceWindowActions())
    await act(async () => { await result.current.handleWsTearOff('w1') })
    expect(isTabMoving('lead')).toBe(false)
    expect(Object.keys(useTabStore.getState().tabs).sort()).toEqual(['lead', 'm'])
  })
})
