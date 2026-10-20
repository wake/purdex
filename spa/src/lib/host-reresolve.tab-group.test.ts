// spa/src/lib/host-reresolve.tab-group.test.ts — the team host inside `tab.group` in the host rewrite (task groups spec
// §4.2, TG-1b-2 task 1): on screen, in the parked master and in a parked slave, for a host recognised later (wire id ->
// local) and for a host removed (local -> wire).
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useHostStore } from '../stores/useHostStore'
import type { HostConfig } from '../stores/useHostStore'
import { rewriteTabsHosts, useTabStore } from '../stores/useTabStore'
import { useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../stores/useLocalProfilesStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { __resetMasterWorldForTest } from './profile/master-world'
import { syncIdOfSync } from './profile/host-identity'
import type { Tab } from '../types/tab'
import { __resetHostReresolveForTest, rewriteHostRefs, runHostReresolve } from './host-reresolve'

const DAEMON = 'air-lab:26aaaa'
const WIRE = syncIdOfSync(DAEMON)
const UNKNOWN = syncIdOfSync('nowhere:000000')
const LOCAL = 'loc001'

const host = (id: string, over: Partial<HostConfig> = {}): HostConfig => ({ id, name: id, ip: '10.0.0.1', port: 7860, order: 0, ...over })
const team = (h: string) => `team\u0000${h}\u0000t1`
const tab = (id: string, group?: string, groupRank?: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 1,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'new-tab' } } },
  ...(group !== undefined ? { group } : {}),
  ...(groupRank !== undefined ? { groupRank } : {}),
})
const world = (tabs: Record<string, Tab>): ParkedWorld => ({ tabs, workspaces: [{ id: 'w', name: 'w', tabs: Object.keys(tabs), activeTabId: null }], activeWorkspaceId: 'w', activeTabId: null })

function seed(from: string): void {
  useHostStore.setState({ hosts: { [LOCAL]: host(LOCAL, { daemonId: DAEMON }) }, hostOrder: [LOCAL], activeHostId: LOCAL })
  useTabStore.setState({
    tabs: { s1: tab('s1', team(from), 'V'), s2: tab('s2', team(UNKNOWN)), s3: tab('s3', 'group\u0000aaaaaaaa'), s4: tab('s4') },
    tabOrder: ['s1', 's2', 's3', 's4'], worldId: 'on', worldEpoch: 1,
  })
  useWorkspaceStore.setState({ workspaces: [{ id: 'won', name: 'on', tabs: ['s1', 's2', 's3', 's4'], activeTabId: 's1' }], activeWorkspaceId: 'won', worldId: 'on', worldEpoch: 1 })
  useLocalProfilesStore.setState({
    activeProfileId: 'on',
    worldEpoch: 1,
    parkedMaster: world({ m1: tab('m1', team(from), 'a') }),
    slaves: {
      on: { id: 'on', name: 'On', createdAt: 1, shownHostIds: [], world: null },
      s1: { id: 's1', name: 'S', createdAt: 1, shownHostIds: [], world: world({ x1: tab('x1', team(from)) }) },
    },
    slaveOrder: ['on', 's1'],
  })
}

const groupOf = (id: string): string | undefined => useTabStore.getState().tabs[id]?.group
const parkedGroup = (): Array<string | undefined> => {
  const { parkedMaster, slaves } = useLocalProfilesStore.getState()
  return [parkedMaster?.tabs.m1.group, slaves.s1.world?.tabs.x1.group]
}

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  __resetHostReresolveForTest()
  useRebuildStore.setState({ lockedBy: null, lockGrant: null })
})

afterEach(() => {
  __resetHostReresolveForTest()
})

describe('tab.group — host recognised later (wire id -> local id)', () => {
  beforeEach(() => seed(WIRE))

  it('on screen, in the parked master and in a parked slave', () => {
    expect(runHostReresolve()).toBe('done')
    expect(groupOf('s1')).toBe(team(LOCAL))
    expect(parkedGroup()).toEqual([team(LOCAL), team(LOCAL)])
  })

  it('keeps the rank, an unknown host, a manual group key and a tab with no group exactly as they were', () => {
    const before = useTabStore.getState().tabs
    runHostReresolve()
    const after = useTabStore.getState().tabs
    expect(after.s1.groupRank).toBe('V')
    expect(after.s2).toBe(before.s2)
    expect(groupOf('s2')).toBe(team(UNKNOWN))
    expect(after.s3).toBe(before.s3)
    expect(after.s4).toBe(before.s4)
    expect(Object.hasOwn(after.s4, 'group')).toBe(false)
  })
})

describe('tab.group — host removed (local id -> wire id)', () => {
  beforeEach(() => seed(LOCAL))

  it('on screen, in the parked master and in a parked slave', () => {
    expect(rewriteHostRefs({ [LOCAL]: WIRE })).toBe('ok')
    expect(groupOf('s1')).toBe(team(WIRE))
    expect(parkedGroup()).toEqual([team(WIRE), team(WIRE)])
    expect(useTabStore.getState().tabs.s1.groupRank).toBe('V')
  })
})

describe('rewriteTabsHosts', () => {
  it('returns the same record when nothing moves, and touches only the tabs whose group names the host', () => {
    const tabs = { a: tab('a', team(LOCAL)), b: tab('b', 'ungrouped') }
    expect(rewriteTabsHosts(tabs, (id) => id)).toBe(tabs)
    const next = rewriteTabsHosts(tabs, (id) => (id === LOCAL ? WIRE : id))
    expect(next.a.group).toBe(team(WIRE))
    expect(next.b).toBe(tabs.b)
  })
})
