// spa/src/lib/task-groups/fresh-tab.test.ts — the one automatic write (task groups spec §5): a tab OPENED while a group is selected joins that group; where the tab object came from decides "opened". Real stores.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { startGroupSelection } from './selection'
import { currentGroupOrderCtx } from './current'
import { groupTabs } from './group-order'
import { useRootCacheStore } from './root-cache'
import { manualGroupKey, UNGROUPED } from './group-key'
import { createTab, takeFreshTab, type Tab } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { useGroupViewStore } from '../../stores/useGroupViewStore'
import { useManualGroupsStore } from '../../stores/useManualGroupsStore'
import { useLayoutStore } from '../../stores/useLayoutStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'

const G = manualGroupKey('aaaaaaaa')
const view = () => useGroupViewStore.getState()
const tabs = () => useTabStore.getState().tabs
const group = (id: string) => tabs()[id]?.group
const dashboard = () => createTab({ kind: 'dashboard' })
/** A tab minted here (so in the fresh set) with fields set on the minted object itself — a spread copy would not be fresh. */
const minted = (over: Partial<Tab>): Tab => Object.assign(dashboard(), over)
let stop: () => void

beforeEach(() => {
  localStorage.clear()
  useTeamRosterStore.getState().reset()
  useManualGroupsStore.setState({ order: ['aaaaaaaa'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 } } })
  // the group already holds an unranked tab of another workspace
  useTabStore.setState({ tabs: { old: { ...dashboard(), id: 'old', createdAt: 5, group: G } }, tabOrder: ['old'], activeTabId: 'old', visitHistory: [] } as never)
  useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'W1', tabs: ['old'], activeTabId: null, moduleConfig: {} }], activeWorkspaceId: 'w1' } as never)
  useGroupViewStore.setState({ selectedGroup: G, lastActiveTab: {}, expanded: {}, lastByView: { workspace: null, group: null } })
  useTeamUiStore.setState({ sidebarView: 'groups' })
  useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'top' })
  stop = startGroupSelection()
})
afterEach(() => { stop() })

describe('a tab this window minted joins the selected group', () => {
  it('addTab (⌘T, + in the bar, a browser tab, a notification, a History reopen all reach it): group set, no rank, the selection stays, and it is last in groupTabs', () => {
    const t = dashboard()
    useTabStore.getState().addTab(t)
    useTabStore.getState().setActiveTab(t.id)
    expect(group(t.id)).toBe(G)
    expect(tabs()[t.id].groupRank).toBeUndefined()
    expect(view().selectedGroup).toBe(G)
    expect(groupTabs(G, currentGroupOrderCtx())).toEqual(['old', t.id]) // after the group's older unranked tab, by createdAt
  })

  it('openSingletonTab (settings, files) places the tab it creates', () => {
    const id = useTabStore.getState().openSingletonTab({ kind: 'settings', scope: 'global' })
    expect(group(id)).toBe(G)
    expect(view().selectedGroup).toBe(G)
  })

  it('detachPane places the tab it makes, in the same update that inserts it', () => {
    const split = { type: 'split', direction: 'h', sizes: [50, 50], children: [
      { type: 'leaf', pane: { id: 'pa', content: { kind: 'dashboard' } } },
      { type: 'leaf', pane: { id: 'pb', content: { kind: 'history' } } },
    ] }
    useTabStore.setState({ tabs: { ...tabs(), sp: { ...dashboard(), id: 'sp', layout: split } } } as never)
    const seen: Array<string | undefined> = []
    const unsub = useTabStore.subscribe((s, prev) => {
      for (const id of Object.keys(s.tabs)) if (!prev.tabs[id] && id !== 'sp') seen.push(s.tabs[id].group)
    })
    const id = useTabStore.getState().detachPane('sp', 'pb')
    unsub()
    expect(id).not.toBeNull()
    expect(group(id!)).toBe(G)
    expect(seen).toEqual([G]) // never visible without its group
  })

  it('a tab whose own group names nothing that exists keeps that group: only a tab with no group is written', () => {
    const gone = manualGroupKey('cccccccc')
    const t = minted({ group: gone })
    useTabStore.getState().addTab(t)
    expect(group(t.id)).toBe(gone)
  })

  it('a tab that already has a group keeps it', () => {
    const other = manualGroupKey('bbbbbbbb')
    useManualGroupsStore.setState({ order: ['aaaaaaaa', 'bbbbbbbb'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 }, bbbbbbbb: { name: 'H', members: [], createdAt: 1 } } })
    const t = minted({ group: other })
    useTabStore.getState().addTab(t)
    expect(group(t.id)).toBe(other)
  })
})

describe('a tab that is not freshly minted here is never placed', () => {
  it('a tab object from another window (a JSON copy: an Electron single-tab transfer or a workspace transfer)', () => {
    const copy: Tab = JSON.parse(JSON.stringify(dashboard()))
    useTabStore.getState().addTab(copy)
    expect(group(copy.id)).toBeUndefined()
  })

  it('a reopened tab (⌘⇧T restores the closed tab object): it left the fresh set when it was first inserted', () => {
    const t = dashboard()
    useTabStore.getState().addTab(t)
    expect(group(t.id)).toBe(G) // placed once, when it was opened
    useTabStore.setState({ tabs: Object.fromEntries(Object.entries(tabs()).filter(([id]) => id !== t.id)) } as never)
    useTabStore.getState().addTab(t) // the closed tab object comes back
    expect(group(t.id)).toBeUndefined()
    expect(takeFreshTab(t)).toBe(false)
  })

  it('an apply (a store write that brings tabs in) places nothing', () => {
    useTabStore.setState({ tabs: { ...tabs(), applied: { ...dashboard(), id: 'applied' } } } as never)
    expect(group('applied')).toBeUndefined()
  })
})

describe('a tab that shows a team seat is the team\'s, not the selected group\'s (rule 1, before the tab is in the store)', () => {
  const seatPane = { kind: 'tmux-session', hostId: 'h1', sessionCode: 'S1', mode: 'terminal', cachedName: 'name-S1', tmuxInstance: 'I' } as const
  beforeEach(() => {
    useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } }, hostOrder: ['h1'], runtime: { h1: { status: 'connected' } } } as never)
    useSessionStore.setState({ sessions: { h1: [{ code: 'S1', name: 'name-S1' }] as never } })
    useTeamRosterStore.getState().apply('h1', [{
      id: 'team-1', host_id: 'd', created_at: 1, team_name: 'Payments', team_label: '',
      lead: { session_id: 'L', ref: '_L', address: 'mlab/L-x', live: true, tmux_session: 'name-S1', title: 'L' }, members: [],
    } as never])
  })

  it('addTab: a fresh tab showing the seat is not written the selected manual group', () => {
    const t = createTab({ ...seatPane })
    useTabStore.getState().addTab(t)
    expect(group(t.id)).toBeUndefined()
  })

  it('detachPane: a pane that shows the seat, detached into its own tab, is not written the selected manual group', () => {
    const split = { type: 'split', direction: 'h', sizes: [50, 50], children: [
      { type: 'leaf', pane: { id: 'pa', content: { kind: 'dashboard' } } },
      { type: 'leaf', pane: { id: 'pb', content: { ...seatPane } } },
    ] }
    useTabStore.setState({ tabs: { ...tabs(), sp: { ...dashboard(), id: 'sp', layout: split } } } as never)
    const id = useTabStore.getState().detachPane('sp', 'pb')
    expect(id).not.toBeNull()
    expect(group(id!)).toBeUndefined()
  })
})

describe('the write needs the task-group view, a real selected group, and no earlier placement', () => {
  const add = () => { const t = dashboard(); useTabStore.getState().addTab(t); return t.id }

  it('the workspace view, the narrow sidebar and tabs on the left place nothing', () => {
    useTeamUiStore.setState({ sidebarView: 'workspace' })
    expect(group(add())).toBeUndefined()
    useTeamUiStore.setState({ sidebarView: 'groups' })
    useLayoutStore.setState({ activityBarWidth: 'narrow' })
    expect(group(add())).toBeUndefined()
    useLayoutStore.setState({ activityBarWidth: 'wide', tabPosition: 'left' })
    expect(group(add())).toBeUndefined()
  })

  it('「未分組」 selected, nothing selected, or a selected group that no longer exists place nothing', () => {
    view().selectGroup(UNGROUPED)
    expect(group(add())).toBeUndefined()
    useGroupViewStore.setState({ selectedGroup: null })
    expect(group(add())).toBeUndefined()
    useGroupViewStore.setState({ selectedGroup: manualGroupKey('cccccccc') })
    expect(group(add())).toBeUndefined()
  })

  it('a watcher-area tab is never placed', () => {
    const t = minted({ id: 'watch-jobs' })
    useTabStore.getState().addTab(t)
    expect(group('watch-jobs')).toBeUndefined()
  })

  it('a tab a known root already places (rule 2) is not written a group', () => {
    useManualGroupsStore.setState({ order: ['aaaaaaaa', 'bbbbbbbb'], groups: { aaaaaaaa: { name: 'G', members: [], createdAt: 1 }, bbbbbbbb: { name: 'H', members: [{ host: 'h1', root: 'ROOT' }], createdAt: 1 } } })
    const t = createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'S', mode: 'terminal', cachedName: 'n', tmuxInstance: 'i', rebuild: { sessionName: 'n', tmuxInstance: 'i', capturedAt: 1, agent: { type: 'cc', sessionId: 'AG', updatedAt: 1 } } })
    useRootCacheStore.getState().ingest('h1', [{ session_id: 'AG', root_session_id: 'ROOT' }])
    useTabStore.getState().addTab(t)
    expect(group(t.id)).toBeUndefined()
  })
})
