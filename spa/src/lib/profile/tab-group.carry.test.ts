// spa/src/lib/profile/tab-group.carry.test.ts — a tab's `group` / `groupRank` ride the tab itself (task groups spec §4.2,
// TG-1b-2 task 2): the world switch parks and restores whole tabs, 「複製為 slave」 copies them (group keys name no tabs, so
// nothing is remapped), and a device-local interface tab keeps them across a remote apply. (The Electron tab transfer is
// covered next to the other IPC cases in hooks/useElectronIpc.test.ts.)
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { ParkedWorld } from '../../stores/useLocalProfilesStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import { useTabStore } from '../../stores/useTabStore'
import type { Tab, Workspace } from '../../types/tab'
import { applyTabs } from './applier'
import { __resetMasterWorldForTest } from './master-world'
import { buildTabsSection } from './sections'
import { copyMasterAsSlave, switchActiveProfile } from './switch-active'

const TEAM = 'team\u0000h1\u0000t1'
const MANUAL = 'group\u0000aaaaaaaa'

const tmux = (id: string, extra: Partial<Tab> = {}): Tab => ({
  id, pinned: false, locked: false, createdAt: 1,
  layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `c-${id}`, mode: 'terminal', cachedName: id, tmuxInstance: 'inst' } } },
  ...extra,
})
const ws = (id: string, tabs: string[]): Workspace => ({ id, name: id, tabs, activeTabId: tabs[0] ?? null })
const world = (workspace: Workspace, tabs: Tab[]): ParkedWorld => ({
  workspaces: [workspace], tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), activeWorkspaceId: workspace.id, activeTabId: tabs[0]?.id ?? null,
})

const masterWorld = (): ParkedWorld => world(ws('mws', ['m1', 'm2', 'm3']), [tmux('m1', { group: TEAM, groupRank: 'V' }), tmux('m2', { group: MANUAL, groupRank: 'a' }), tmux('m3')])
const slaveWorld = (): ParkedWorld => world(ws('sws', ['s1']), [tmux('s1', { group: 'ungrouped' })])

function putOnScreen(w: ParkedWorld, worldId: string, epoch: number): void {
  useTabStore.setState({ tabs: w.tabs, tabOrder: w.workspaces.flatMap((x) => x.tabs), activeTabId: w.activeTabId, visitHistory: [], worldId, worldEpoch: epoch })
  useWorkspaceStore.setState({ workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId, worldId, worldEpoch: epoch })
}

beforeEach(() => {
  localStorage.clear()
  __resetMasterWorldForTest()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '10.0.0.1', port: 7860, order: 0 } }, hostOrder: ['h1'], activeHostId: 'h1', devHostId: null, runtime: {} })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useLocalProfilesStore.setState({ slaves: { s1: { id: 's1', name: 'S', createdAt: 1, shownHostIds: [], world: slaveWorld() } }, slaveOrder: ['s1'], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  putOnScreen(masterWorld(), MASTER_PROFILE_ID, 0)
})

afterEach(() => {
  __resetMasterWorldForTest()
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  putOnScreen({ workspaces: [], tabs: {}, activeWorkspaceId: null, activeTabId: null }, MASTER_PROFILE_ID, 0)
})

describe('world switch park / restore', () => {
  it('master -> slave -> master: the master\'s tabs come back with their group and rank, the slave\'s own stay on the slave', async () => {
    expect(await switchActiveProfile('s1')).toEqual({ ok: true })
    expect(useLocalProfilesStore.getState().parkedMaster!.tabs.m1).toMatchObject({ group: TEAM, groupRank: 'V' })
    expect(useTabStore.getState().tabs.s1).toMatchObject({ group: 'ungrouped' })
    expect(Object.hasOwn(useTabStore.getState().tabs, 'm1')).toBe(false)

    expect(await switchActiveProfile(MASTER_PROFILE_ID)).toEqual({ ok: true })
    expect(useTabStore.getState().tabs).toEqual(masterWorld().tabs)
    expect(useTabStore.getState().tabs.m2).toMatchObject({ group: MANUAL, groupRank: 'a' })
    expect(Object.hasOwn(useTabStore.getState().tabs.m3, 'group')).toBe(false)
    expect(useLocalProfilesStore.getState().slaves.s1.world!.tabs.s1).toMatchObject({ group: 'ungrouped' })
  })
})

describe('「複製為 slave」', () => {
  it('copies every tab with its group and rank (new tab ids, same values — a group key names no tab)', () => {
    const result = copyMasterAsSlave('Copy')
    if (!result.ok) throw new Error(result.reason)
    const copy = useLocalProfilesStore.getState().slaves[result.id].world!
    const byOrder = copy.workspaces[0].tabs.map((id) => copy.tabs[id])
    expect(byOrder.map((t) => t.group)).toEqual([TEAM, MANUAL, undefined])
    expect(byOrder.map((t) => t.groupRank)).toEqual(['V', 'a', undefined])
    expect(byOrder.map((t) => t.id)).not.toContain('m1')
    // and the master on screen is untouched
    expect(useTabStore.getState().tabs.m1).toMatchObject({ group: TEAM, groupRank: 'V' })
  })
})

describe('a device-local interface tab across a remote apply', () => {
  it('keeps its group and rank (and the very same tab object); synced tabs take the payload\'s', () => {
    const settings: Tab = { id: 'sx', pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf', pane: { id: 'p-sx', content: { kind: 'settings', scope: 'global' } } }, group: MANUAL, groupRank: 'k' }
    const local = { tabs: { a: tmux('a', { group: TEAM, groupRank: 'V' }), sx: settings }, workspaces: [ws('wa', ['a', 'sx'])] }
    // the SOT's version of `a` has moved to another group and lost its rank
    const incoming = buildTabsSection(ws('wa', ['a']), { a: tmux('a', { group: 'ungrouped' }) })
    const { next } = applyTabs(local, 'wa', incoming)
    expect(next.tabs.sx).toBe(settings)
    expect(next.tabs.sx).toMatchObject({ group: MANUAL, groupRank: 'k' })
    expect(next.tabs.a.group).toBe('ungrouped')
    expect(Object.hasOwn(next.tabs.a, 'groupRank')).toBe(false)
    expect(next.workspaces[0].tabs).toEqual(['a', 'sx'])
  })
})
