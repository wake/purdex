// spa/src/lib/host-reresolve.task-groups.test.ts — the manual task groups in the host re-resolve pass and the host
// deletion's rewrite (task groups spec §4.1, TG-1a-2 task 1).
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import type { HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useManualGroupsStore, type ManualGroup } from '../stores/useManualGroupsStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { __resetMasterWorldForTest } from './profile/master-world'
import { syncIdOfSync } from './profile/host-identity'
import { STORAGE_KEYS } from './storage'
import { __resetHostReresolveForTest, rewriteHostRefs, runHostReresolve } from './host-reresolve'

const DAEMON = 'air-lab:26aaaa'
const WIRE = syncIdOfSync(DAEMON)
const UNKNOWN = syncIdOfSync('nowhere:000000')
const LOCAL = 'loc001'

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...over })
const group = (members: ManualGroup['members'], name = 'G'): ManualGroup => ({ name, members, createdAt: 1 })
const groups = () => useManualGroupsStore.getState().groups

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  useRebuildStore.setState({ lockedBy: null, lockGrant: null })
  useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }) }, hostOrder: [LOCAL], activeHostId: LOCAL })
  // A settled world, nothing in it.
  useTabStore.setState({ tabs: {}, tabOrder: [], worldId: 'on', worldEpoch: 1 })
  useWorkspaceStore.setState({ workspaces: [{ id: 'won', name: 'on', tabs: [], activeTabId: null }], activeWorkspaceId: 'won', worldId: 'on', worldEpoch: 1 })
  useLocalProfilesStore.setState({ activeProfileId: 'on', worldEpoch: 1, parkedMaster: { tabs: {}, workspaces: [{ id: 'w', name: 'w', tabs: [], activeTabId: null }], activeWorkspaceId: 'w', activeTabId: null }, slaves: { on: { id: 'on', name: 'On', createdAt: 1, shownHostIds: [], world: null } }, slaveOrder: ['on'] })
  useManualGroupsStore.setState({ order: [], groups: {} })
})

afterEach(() => {
  __resetHostReresolveForTest()
})

describe('manual task groups — re-resolve pass (wire id -> local id)', () => {
  it('a kept wire id becomes the local id once the host is recognised; an unknown one stays', () => {
    useManualGroupsStore.getState().addGroup('aaaaaaaa', group([{ host: WIRE, root: 'r1' }, { host: UNKNOWN, root: 'r2' }]))
    expect(runHostReresolve()).toBe('done')
    expect(groups().aaaaaaaa.members).toEqual([{ host: LOCAL, root: 'r1' }, { host: UNKNOWN, root: 'r2' }])
  })

  it('a rewrite that makes two members equal leaves one — in a group and across groups (first in order wins)', () => {
    const s = useManualGroupsStore.getState()
    s.addGroup('aaaaaaaa', group([{ host: LOCAL, root: 'r1' }, { host: WIRE, root: 'r1' }, { host: WIRE, root: 'r2' }]))
    s.addGroup('bbbbbbbb', group([{ host: WIRE, root: 'r2' }, { host: WIRE, root: 'r3' }]))
    runHostReresolve()
    expect(groups().aaaaaaaa.members).toEqual([{ host: LOCAL, root: 'r1' }, { host: LOCAL, root: 'r2' }])
    expect(groups().bbbbbbbb.members).toEqual([{ host: LOCAL, root: 'r3' }])
  })

  it('keeps the group, its order and its other fields; touches nothing when nothing moves', () => {
    useManualGroupsStore.getState().addGroup('aaaaaaaa', { ...group([{ host: LOCAL, root: 'r1' }]), color: '#112233' })
    const before = useManualGroupsStore.getState()
    runHostReresolve()
    expect(useManualGroupsStore.getState().groups).toBe(before.groups)
    expect(useManualGroupsStore.getState().order).toBe(before.order)
  })

  it('re-reads the store from storage first: another window\'s write is the base of the rewrite', () => {
    useManualGroupsStore.getState().addGroup('aaaaaaaa', group([{ host: LOCAL, root: 'r1' }]))
    localStorage.setItem(STORAGE_KEYS.TASK_GROUPS, JSON.stringify({
      state: { order: ['aaaaaaaa', 'cccccccc'], groups: { aaaaaaaa: group([{ host: WIRE, root: 'r1' }]), cccccccc: group([{ host: WIRE, root: 'r9' }], 'C') } },
      version: 1,
    }))
    runHostReresolve()
    expect(useManualGroupsStore.getState().order).toEqual(['aaaaaaaa', 'cccccccc'])
    expect(groups().cccccccc.members).toEqual([{ host: LOCAL, root: 'r9' }])
  })
})

describe('manual task groups — host deletion (rewriteHostRefs, local id -> wire id)', () => {
  it('a removed host\'s members go out as wire ids', () => {
    useManualGroupsStore.getState().addGroup('aaaaaaaa', group([{ host: LOCAL, root: 'r1' }, { host: 'other', root: 'r2' }]))
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    expect(groups().aaaaaaaa.members).toEqual([{ host: WIRE, root: 'r1' }, { host: 'other', root: 'r2' }])
  })

  it('a member held under the local id and under the wire id collapses to one', () => {
    useManualGroupsStore.getState().addGroup('aaaaaaaa', group([{ host: WIRE, root: 'r1' }, { host: LOCAL, root: 'r1' }]))
    rewriteHostRefs({ [LOCAL]: WIRE })
    expect(groups().aaaaaaaa.members).toEqual([{ host: WIRE, root: 'r1' }])
  })
})
