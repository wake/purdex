// useMasterOnScreen / useMasterScreen / useMasterWorkspaces over the REAL stores: whose world the sidebar shows, read
// through `readMasterWorld` — re-rendered when any of its three stores moves.
import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { useWorkspaceStore } from '../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type ParkedWorld } from '../stores/useLocalProfilesStore'
import { useTabStore } from '../stores/useTabStore'
import { useMasterOnScreen, useMasterScreen, useMasterWorkspaces } from './useMasterOnScreen'

const MASTER_WS = [{ id: 'm-ws', name: 'Mine', tabs: [], activeTabId: null }]
const SLAVE_WS = [{ id: 's-ws', name: 'Theirs', tabs: [], activeTabId: null }]
const PARKED: ParkedWorld = { workspaces: MASTER_WS, tabs: {}, activeWorkspaceId: 'm-ws', activeTabId: null }

function putMasterOnScreen(): void {
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: MASTER_WS, activeWorkspaceId: 'm-ws', worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
}

function putSlaveOnScreen(): void {
  useLocalProfilesStore.setState({
    slaves: { s1: { id: 's1', name: 'Slave', createdAt: 1, shownHostIds: [], world: null } },
    slaveOrder: ['s1'],
    activeProfileId: 's1',
    parkedMaster: PARKED,
    worldEpoch: 1,
  })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: 's1', worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: SLAVE_WS, activeWorkspaceId: 's-ws', worldId: 's1', worldEpoch: 1 })
}

beforeEach(() => putMasterOnScreen())

describe('useMasterScreen', () => {
  it('master on screen → "master"; a slave → "slave"; epochs apart → "unsettled"; and it follows the stores', () => {
    const { result } = renderHook(() => ({ screen: useMasterScreen(), onScreen: useMasterOnScreen() }))
    expect(result.current).toEqual({ screen: 'master', onScreen: true })
    act(() => putSlaveOnScreen())
    expect(result.current).toEqual({ screen: 'slave', onScreen: false })
    act(() => useTabStore.setState({ worldEpoch: 2 }))
    expect(result.current).toEqual({ screen: 'unsettled', onScreen: false })
    act(() => putMasterOnScreen())
    expect(result.current).toEqual({ screen: 'master', onScreen: true })
  })
})

describe('useMasterWorkspaces', () => {
  it('the MASTER world\'s workspaces — parked while a slave is on screen — or null while unsettled', () => {
    const { result } = renderHook(() => useMasterWorkspaces())
    expect(result.current).toBe(MASTER_WS)
    act(() => putSlaveOnScreen())
    expect(result.current).toBe(MASTER_WS)
    act(() => useTabStore.setState({ worldEpoch: 2 }))
    expect(result.current).toBeNull()
  })
})
