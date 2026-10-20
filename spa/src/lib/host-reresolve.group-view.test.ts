// spa/src/lib/host-reresolve.group-view.test.ts — the task-group view's memory in the host re-resolve pass and the host deletion's rewrite (task groups spec §6.2, TG-3b task 1): a team key carries a local host id, so a host
// recognised or removed moves it, once, for the whole device.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import type { HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useGroupViewStore } from '../stores/useGroupViewStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { __resetMasterWorldForTest } from './profile/master-world'
import { syncIdOfSync } from './profile/host-identity'
import { STORAGE_KEYS } from './storage'
import { manualGroupKey, teamGroupKey } from './task-groups/group-key'
import { __resetHostReresolveForTest, rewriteHostRefs, runHostReresolve } from './host-reresolve'

const DAEMON = 'air-lab:26aaaa'
const WIRE = syncIdOfSync(DAEMON)
const LOCAL = 'loc001'
const MANUAL = manualGroupKey('aaaaaaaa')
const teamOf = (host: string) => teamGroupKey(`${host}\u0000t1`)
const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...over })
const state = () => useGroupViewStore.getState()
const seed = (hostId: string) => useGroupViewStore.setState({
  selectedGroup: teamOf(hostId), lastActiveTab: { [teamOf(hostId)]: 't1', [MANUAL]: 't2' }, expanded: { [teamOf(hostId)]: false }, lastByView: { workspace: 'w1', group: teamOf(hostId) },
})

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  useRebuildStore.setState({ lockedBy: null, lockGrant: null })
  useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }) }, hostOrder: [LOCAL], activeHostId: LOCAL })
  useTabStore.setState({ tabs: {}, tabOrder: [], worldId: 'on', worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: [{ id: 'won', name: 'on', tabs: [], activeTabId: null }], activeWorkspaceId: 'won', worldId: 'on', worldEpoch: 1 })
  useLocalProfilesStore.setState({ activeProfileId: 'on', worldEpoch: 1, parkedMaster: { tabs: {}, workspaces: [{ id: 'w', name: 'w', tabs: [], activeTabId: null }], activeWorkspaceId: 'w', activeTabId: null }, slaves: { on: { id: 'on', name: 'On', createdAt: 1, shownHostIds: [], world: null } }, slaveOrder: ['on'] })
  useGroupViewStore.setState({ selectedGroup: null, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
})

afterEach(() => {
  __resetHostReresolveForTest()
})

describe('group view memory — re-resolve pass (wire id -> local id)', () => {
  it('a team key held under the wire id becomes the local id once the host is recognised', () => {
    seed(WIRE)
    expect(runHostReresolve()).toBe('done')
    expect(state().selectedGroup).toBe(teamOf(LOCAL))
    expect(state().lastActiveTab).toEqual({ [teamOf(LOCAL)]: 't1', [MANUAL]: 't2' })
    expect(state().expanded).toEqual({ [teamOf(LOCAL)]: false })
    expect(state().lastByView).toEqual({ workspace: 'w1', group: teamOf(LOCAL) })
  })

  it('touches nothing when nothing moves', () => {
    seed(LOCAL)
    const before = state()
    runHostReresolve()
    expect(state().lastActiveTab).toBe(before.lastActiveTab)
    expect(state().expanded).toBe(before.expanded)
  })

  it('re-reads the store from storage first: another window\'s write is the base of the rewrite', () => {
    seed(LOCAL)
    localStorage.setItem(STORAGE_KEYS.GROUP_VIEW, JSON.stringify({
      state: { selectedGroup: teamOf(WIRE), lastActiveTab: { [teamOf(WIRE)]: 'from-b' }, expanded: {}, lastByView: { workspace: null, group: null } },
      version: 1,
    }))
    runHostReresolve()
    expect(state().selectedGroup).toBe(teamOf(LOCAL))
    expect(state().lastActiveTab).toEqual({ [teamOf(LOCAL)]: 'from-b' })
  })
})

describe('group view memory — host deletion (rewriteHostRefs, local id -> wire id)', () => {
  it('a removed host\'s team keys go out as wire ids; another host\'s stay', () => {
    useGroupViewStore.setState({ selectedGroup: teamOf(LOCAL), lastActiveTab: { [teamOf(LOCAL)]: 't1', [teamOf('other')]: 't2' }, expanded: {}, lastByView: { workspace: null, group: null } })
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    expect(state().selectedGroup).toBe(teamOf(WIRE))
    expect(state().lastActiveTab).toEqual({ [teamOf(WIRE)]: 't1', [teamOf('other')]: 't2' })
  })
})
