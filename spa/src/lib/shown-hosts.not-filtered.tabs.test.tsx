// spa/src/lib/shown-hosts.not-filtered.tabs.test.tsx — hidden ≠ absent, the tabs (host ownership plan H2d-5 T2, §0.21
// rule 4 and the table's first row). Hiding a host never closes, filters, splits, moves or re-focuses a tab: an air26
// tmux tab and a split [mlab | air26] tab stay in the tab bar (`SortableTab`) and the sidebar (`InlineTab`) with their
// badge, keyboard next / previous and "close others" treat them like any tab, and hiding air26 — locally
// (`setHostShown`) or by applying a synced `settings` payload — leaves the tab, workspace and local-profiles stores the
// SAME objects and every `workspaces` / `tabs.<ws>` section build byte-identical (the real builders).
//
// The panes themselves are NOT compared: a pane on a hidden host renders `HostHiddenPane` by design (H2d-4). Only the
// tab-level surfaces are — each case observes air26 HIDDEN (`ids: [<mlab wire>]`) and SHOWN (`ids: [<mlab wire>,
// <air26 wire>]`) and requires the two observations to be identical.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, render, renderHook, screen } from '@testing-library/react'
import { DndContext } from '@dnd-kit/core'
import { useHostStore, type HostConfig } from '../stores/useHostStore'
import { useTabStore } from '../stores/useTabStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore } from '../stores/useLocalProfilesStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useHostLookStore } from '../stores/useHostLookStore'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHistoryStore } from '../stores/useHistoryStore'
import { TabBar } from '../components/TabBar'
import { InlineTabList } from '../features/workspace/components/InlineTabList'
import { useTabWorkspaceActions } from '../features/workspace/hooks'
import { getVisibleTabIds } from '../features/workspace/lib/getVisibleTabIds'
import { useTabHostBadge } from '../hooks/useTabHostBadge'
import { useShortcuts } from '../hooks/useShortcuts'
import { clearModuleRegistry, registerModule } from './module-registry'
import { syncIdOfSync, identityOfSync } from './profile/host-identity'
import { buildSectionPayload } from './profile/collector'
import { applySectionToStores, readSettingsSources } from './profile/apply-to-stores'
import { masterWorkspaceIds } from './profile/master-world'
import { buildSettingsSection } from './profile/sections'
import type { SettingsPayload } from './profile/types'
import { isRefShownNow, setHostShown } from './shown-hosts'
import type { PaneLayout, Tab, Workspace } from '../types/tab'

vi.mock('../features/workspace/lib/icon-path-cache', () => ({
  getIconPath: () => null,
  isWeightLoaded: () => true,
  prefetchWeight: () => Promise.resolve(),
}))
vi.mock('../hooks/useScrollOverflow', () => ({
  useScrollOverflow: () => ({ containerRef: { current: null }, canScrollLeft: false, canScrollRight: false, scrollLeft: vi.fn(), scrollRight: vi.fn() }),
}))

const MLAB = 'h-mlab'
const AIR = 'h-air26'
const MLAB_DAEMON = 'mini-lab:27bbbb'
const AIR_DAEMON = 'air-lab:26aaaa'
const MLAB_WIRE = syncIdOfSync(MLAB_DAEMON)
const AIR_WIRE = syncIdOfSync(AIR_DAEMON)
const WS = 'ws-main'

type Setting = 'hidden' | 'shown'
const IDS: Record<Setting, string[]> = { hidden: [MLAB_WIRE], shown: [MLAB_WIRE, AIR_WIRE] }
const SETTINGS: Setting[] = ['hidden', 'shown']

const host = (id: string, ip: string, daemonId: string, order: number): HostConfig =>
  ({ id, name: id, ip, port: 7860, token: 'tok', order, daemonId })

const leaf = (paneId: string, hostId: string, sessionCode: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: paneId, content: { kind: 'tmux-session', hostId, sessionCode, mode: 'terminal', cachedName: sessionCode, tmuxInstance: 'inst' } },
})
const tab = (id: string, layout: PaneLayout): Tab => ({ id, pinned: false, locked: false, createdAt: 1, layout })

const T_MLAB = 't-mlab'
const T_AIR = 't-air'
const T_SPLIT = 't-split'
const ORDER = [T_MLAB, T_AIR, T_SPLIT]

/** The master world on screen (settled), one workspace holding an mlab tab, an air26 tab and a split [mlab | air26]. */
function arrange(setting: Setting): void {
  localStorage.clear()
  cleanup()
  useHostStore.setState({
    hosts: { [MLAB]: host(MLAB, '100.64.0.2', MLAB_DAEMON, 0), [AIR]: host(AIR, '100.64.0.4', AIR_DAEMON, 1) },
    hostOrder: [MLAB, AIR],
    activeHostId: MLAB,
    runtime: {},
  })
  useHostLookStore.setState({ looks: { [MLAB_WIRE]: { color: '#ff6633', icon: 'Desktop' }, [AIR_WIRE]: { color: '#3366ff', icon: 'Laptop' } } })
  useUISettingsStore.setState({ hostBadgeTabBarEnabled: true, hostBadgeSidebarEnabled: true })
  useSessionStore.setState({ sessions: {}, activeHostId: null, activeCode: null })
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  const tabs: Record<string, Tab> = {
    [T_MLAB]: tab(T_MLAB, leaf('p-mlab', MLAB, 'mmm')),
    [T_AIR]: tab(T_AIR, leaf('p-air', AIR, 'aaa')),
    [T_SPLIT]: tab(T_SPLIT, { type: 'split', id: 's1', direction: 'h', sizes: [50, 50], children: [leaf('p-split-m', MLAB, 'mm2'), leaf('p-split-a', AIR, 'aa2')] }),
  }
  const ws: Workspace = { id: WS, name: 'Main', tabs: [...ORDER], activeTabId: T_MLAB }
  useTabStore.setState({ tabs, tabOrder: [...ORDER], activeTabId: T_MLAB, visitHistory: [], worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useWorkspaceStore.setState({ workspaces: [ws], activeWorkspaceId: WS, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
  useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0 })
  useShownHostsStore.setState({ ids: IDS[setting] })
  // The premise: the setting really hides / shows air26; mlab is shown in both.
  expect(isRefShownNow(AIR)).toBe(setting === 'shown')
  expect(isRefShownNow(MLAB)).toBe(true)
}

/** What App hands the tab bar and the "close others" range (`App.tsx`: `getVisibleTabIds` → `displayTabs`). */
function displayTabs(): Tab[] {
  const t = useTabStore.getState()
  const w = useWorkspaceStore.getState()
  return getVisibleTabIds({ tabs: t.tabs, tabOrder: t.tabOrder, activeTabId: t.activeTabId, workspaces: w.workspaces, activeWorkspaceId: w.activeWorkspaceId })
    .map((id) => t.tabs[id])
}

beforeEach(() => {
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
})

afterEach(() => {
  cleanup()
  clearModuleRegistry()
  delete (window as unknown as Record<string, unknown>).electronAPI
  useShownHostsStore.setState({ ids: [] })
  useHostLookStore.setState({ looks: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null })
  useHostStore.getState().reset()
})

describe('hidden ≠ absent — the tabs stay (H2d-5 T2)', () => {
  it('the tab bar renders every tab (SortableTab) with its badge — identical hidden and shown', () => {
    const observe = (setting: Setting) => {
      arrange(setting)
      const view = render(
        <TabBar tabs={displayTabs()} activeTabId={T_MLAB} onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()}
          onReorderTabs={vi.fn()} onMiddleClick={vi.fn()} onContextMenu={vi.fn()} />,
      )
      const rows = screen.getAllByRole('tab').map((el) => ({
        id: el.getAttribute('data-tab-id'),
        badge: el.querySelector('[data-host-badge]') !== null,
      }))
      view.unmount()
      return rows
    }
    const hidden = observe('hidden')
    const shown = observe('shown')
    expect(hidden).toEqual(ORDER.map((id) => ({ id, badge: true })))
    expect(hidden).toEqual(shown)
  })

  it('the sidebar renders every tab (InlineTab) with its badge — identical hidden and shown', () => {
    const observe = (setting: Setting) => {
      arrange(setting)
      const view = render(
        <DndContext>
          <InlineTabList tabIds={useWorkspaceStore.getState().workspaces[0].tabs} tabsById={useTabStore.getState().tabs}
            activeTabId={T_MLAB} sourceWsId={WS} onSelect={vi.fn()} onClose={vi.fn()} onMiddleClick={vi.fn()} onContextMenu={vi.fn()} />
        </DndContext>,
      )
      const rows = screen.getAllByTestId('inline-tab-row').map((el) => ({
        title: el.querySelector('[data-testid="inline-tab-title"]')?.textContent ?? null,
        badge: el.querySelector('[data-host-badge]') !== null,
      }))
      view.unmount()
      return rows
    }
    const hidden = observe('hidden')
    const shown = observe('shown')
    expect(hidden).toHaveLength(3)
    expect(hidden.every((r) => r.badge)).toBe(true)
    expect(hidden).toEqual(shown)
  })

  it.each(SETTINGS)('`useTabHostBadge` is non-null for the air26 tab and the split tab (%s)', (setting) => {
    arrange(setting)
    const tabs = useTabStore.getState().tabs
    const air = renderHook(() => useTabHostBadge(tabs[T_AIR]))
    const split = renderHook(() => useTabHostBadge(tabs[T_SPLIT]))
    expect(air.result.current).not.toBeNull()
    expect(air.result.current?.icon).toBe('Laptop')
    expect(split.result.current).not.toBeNull()
  })

  it('keyboard next / previous tab walks through the air26 and split tabs — identical hidden and shown', () => {
    const observe = (setting: Setting) => {
      arrange(setting)
      let fire: ((payload: { action: string }) => void) | null = null
      ;(window as unknown as Record<string, unknown>).electronAPI = {
        onShortcut: (cb: (payload: { action: string }) => void) => { fire = cb; return () => {} },
        signalReady: () => {},
      }
      const view = renderHook(() => useShortcuts())
      const visited: Array<string | null> = []
      for (const action of ['next-tab', 'next-tab', 'next-tab', 'prev-tab', 'prev-tab']) {
        act(() => { fire!({ action }) })
        visited.push(useTabStore.getState().activeTabId)
      }
      view.unmount()
      return { visited, tabs: Object.keys(useTabStore.getState().tabs).sort() }
    }
    const hidden = observe('hidden')
    const shown = observe('shown')
    expect(hidden.visited).toEqual([T_AIR, T_SPLIT, T_MLAB, T_SPLIT, T_AIR])
    expect(hidden.tabs).toEqual([...ORDER].sort())
    expect(hidden).toEqual(shown)
  })

  it('"close others" on the mlab tab closes the air26 and split tabs like any tab — identical hidden and shown', () => {
    const observe = (setting: Setting) => {
      arrange(setting)
      const { result, unmount } = renderHook(() => useTabWorkspaceActions(displayTabs()))
      act(() => { result.current.handleContextMenu({ preventDefault() {}, clientX: 0, clientY: 0 } as unknown as React.MouseEvent, T_MLAB) })
      act(() => { result.current.handleContextAction('closeOthers') })
      unmount()
      return { tabs: Object.keys(useTabStore.getState().tabs), wsTabs: useWorkspaceStore.getState().workspaces[0].tabs }
    }
    const hidden = observe('hidden')
    const shown = observe('shown')
    expect(hidden).toEqual({ tabs: [T_MLAB], wsTabs: [T_MLAB] })
    expect(hidden).toEqual(shown)
  })
})

describe('hidden ≠ absent — hiding writes no tab world (H2d-5 T2)', () => {
  const sections = () => ['workspaces', `tabs.${WS}`].map((key) => JSON.stringify(buildSectionPayload(key as 'workspaces')))
  const stores = () => [useTabStore.getState(), useWorkspaceStore.getState(), useLocalProfilesStore.getState()]
  const expectSame = (before: unknown[], after: unknown[]) => after.forEach((s, i) => expect(s).toBe(before[i]))

  it('`setHostShown(air26, false)`: the tab, workspace and local-profiles stores stay the same objects; workspaces / tabs.<ws> builds unchanged', () => {
    arrange('shown')
    const built = sections()
    expect(built.every((s) => s !== 'null')).toBe(true) // the world is settled: the builders did build
    const before = stores()

    setHostShown(AIR, false)

    expect(isRefShownNow(AIR)).toBe(false)
    expectSame(before, stores())
    expect(sections()).toEqual(built)
    expect(Object.keys(useTabStore.getState().tabs)).toEqual(ORDER)
  })

  it('applying a `settings` payload that hides air26: the same objects; workspaces / tabs.<ws> builds unchanged', async () => {
    arrange('shown')
    const identity = identityOfSync(useHostStore.getState().hosts)
    const ids = masterWorkspaceIds()
    expect(ids).not.toBeNull()
    const current = JSON.parse(JSON.stringify(buildSettingsSection(readSettingsSources(), ids!, identity))) as SettingsPayload
    const payload = { ...current, 'purdex-shown-hosts': { ids: [MLAB_WIRE] } } as SettingsPayload
    const built = sections()
    const before = stores()

    expect(await applySectionToStores('settings', payload, { masterHostId: MLAB })).toMatchObject({ ok: true })

    expect(useShownHostsStore.getState().ids).toEqual([MLAB_WIRE])
    expect(isRefShownNow(AIR)).toBe(false)
    expectSame(before, stores())
    expect(sections()).toEqual(built)
  })
})
