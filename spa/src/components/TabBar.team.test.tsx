// spa/src/components/TabBar.team.test.tsx — the team group on the top tab bar (plan TI-2, spec §4.2, §5).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import type { DragEndEvent } from '@dnd-kit/core'
import { TabBar } from './TabBar'
import { TeamDisplayProvider } from './team/TeamDisplayProvider'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { teamKeyOf } from '../lib/team/team-views'
import type { RosterMember, RosterSession, TeamRoster } from '../lib/team/roster'
import type { PaneLayout, Tab } from '../types/tab'

// The DndContext's drag-end handler is what the bar's rules live in; capture it and call it with synthetic events.
const dnd = vi.hoisted(() => ({ onDragEnd: null as ((e: DragEndEvent) => void) | null }))
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>()
  return {
    ...actual,
    DndContext: (props: React.ComponentProps<typeof actual.DndContext>) => {
      dnd.onDragEnd = props.onDragEnd as (e: DragEndEvent) => void
      return <actual.DndContext {...props} />
    },
  }
})
vi.mock('../hooks/useScrollOverflow', () => ({
  useScrollOverflow: () => ({ containerRef: { current: null }, canScrollLeft: false, canScrollRight: false, scrollLeft: vi.fn(), scrollRight: vi.fn() }),
}))

const sess = (id: string, tmux: string, extra: Partial<RosterSession> = {}): RosterSession => ({
  session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, title: `title ${id}`, live: true, tmux_session: tmux, ...extra,
})
const mem = (id: string, joined: number, tmux: string, extra: Partial<RosterMember> = {}): RosterMember => ({
  ...sess(id, tmux), state: 'active', origin: 'spawned', joined_at: joined, ...extra,
})
const roster = (over: Partial<TeamRoster> = {}, members = [mem('A', 1, 'a-tm'), mem('B', 2, 'b-tm'), mem('C', 3, 'c-tm')]): TeamRoster => ({
  id: 't1', host_id: 'daemon', created_at: 1, team_name: '', team_label: '', lead: sess('L', 'lead-tm'), members, ...over,
})
const leaf = (host: string, name: string): PaneLayout => ({
  type: 'leaf',
  pane: { id: `p-${name}`, content: { kind: 'tmux-session', hostId: host, sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i' } },
})
const mk = (id: string, name: string, host = 'h1', pinned = false): Tab => ({ id, pinned, locked: false, createdAt: 0, layout: leaf(host, name) })
const KEY = teamKeyOf('h1', 't1')
const SESSIONS = ['lead-tm', 'a-tm', 'b-tm', 'other', 'p2'].map((name) => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' }))

const baseTabs = () => [mk('plain', 'other'), mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('mb', 'b-tm'), mk('p2', 'p2')]
const handlers = () => ({
  onSelectTab: vi.fn(), onCloseTab: vi.fn(), onAddTab: vi.fn(), onReorderTabs: vi.fn(), onMiddleClick: vi.fn(), onContextMenu: vi.fn(),
})

function seed(teams: TeamRoster[], all: Tab[], workspaces?: Array<{ id: string; tabs: string[] }>) {
  act(() => {
    useTeamRosterStore.setState({ byHost: { h1: teams } })
    useTabStore.setState({ tabs: Object.fromEntries(all.map((t) => [t.id, t])), tabOrder: all.map((t) => t.id), activeTabId: null })
    useWorkspaceStore.setState({
      workspaces: (workspaces ?? [{ id: 'w1', tabs: all.map((t) => t.id) }]).map((w) => ({ ...w, name: w.id, activeTabId: null })) as never,
      activeWorkspaceId: 'w1',
    })
    useSessionStore.setState({ sessions: { h1: SESSIONS as never, h2: [{ code: 'code-r-tm', name: 'r-tm', mode: 'terminal', cwd: '~' }] as never } })
  })
}

function bar(tabs: Tab[], over: { activeTabId?: string | null } = {}) {
  const h = handlers()
  const utils = render(
    <TeamDisplayProvider>
      <TabBar tabs={tabs} activeTabId={over.activeTabId ?? null} {...h} />
    </TeamDisplayProvider>,
  )
  return { ...utils, h }
}

const idsInOrder = (root: HTMLElement) =>
  Array.from(root.querySelectorAll('[data-tab-id]')).map((el) => el.getAttribute('data-tab-id')!)
const drag = (active: string, over: string) => act(() => dnd.onDragEnd!({ active: { id: active }, over: { id: over } } as unknown as DragEndEvent))

beforeEach(() => {
  cleanup()
  dnd.onDragEnd = null
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, teamBeadHost: true })
  useTeamRosterStore.getState().reset()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'H1', ip: '1', port: 1, daemonId: 'daemon' } } as never, hostOrder: ['h1'], runtime: {} })
})

describe('TabBar — team group (TI-2)', () => {
  it('group renders lead -> members in team order, no label', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { container } = bar(tabs)
    expect(idsInOrder(container)).toEqual(['plain', 'lead', 'ma', 'mb', 'p2'])
    act(() => useTeamUiStore.getState().setMemberOrder(KEY, ['B', 'A', 'C']))
    // the tab list is re-ordered by the lifecycle subscriber; here we feed the bar the order it would hold
    cleanup()
    const reordered = [tabs[0], tabs[1], tabs[3], tabs[2], tabs[4]]
    const second = bar(reordered)
    expect(idsInOrder(second.container)).toEqual(['plain', 'lead', 'mb', 'ma', 'p2'])
  })

  it('no label (round 2): the group starts with the lead tab and nothing in the frame is a button of its own', () => {
    const tabs = baseTabs()
    seed([roster({ team_name: 'Release train', team_label: '發版' })], tabs)
    const { container } = bar(tabs)
    expect(screen.queryByTestId('team-group-label')).toBeNull()
    expect(screen.queryByTestId('team-group-hidden')).toBeNull()
    const frame = container.querySelector('[data-testid="team-tab-group"]') as HTMLElement
    expect(frame.firstElementChild).toHaveAttribute('data-tab-id', 'lead')
    expect(frame.textContent).not.toContain('發版')
  })

  it('collapsed (from the shared state): the lead only, no label, no +N, no collapse control on the bar', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    const { container } = bar(tabs)
    expect(idsInOrder(container)).toEqual(['plain', 'lead', 'p2'])
    expect(screen.queryByTestId('team-group-label')).toBeNull()
    expect(container.querySelector('[data-testid="team-tab-group"] [aria-expanded]')).toBeNull()
    expect(container.querySelector('[data-testid="team-tab-group"]')!.textContent).not.toContain('+2')
  })

  it('the bar follows the shared collapse: collapse from the sidebar side hides members, expand shows them again', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { container } = bar(tabs)
    expect(idsInOrder(container)).toEqual(['plain', 'lead', 'ma', 'mb', 'p2'])
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    expect(idsInOrder(container)).toEqual(['plain', 'lead', 'p2'])
    act(() => useTeamUiStore.getState().setCollapsed(KEY, false))
    expect(idsInOrder(container)).toEqual(['plain', 'lead', 'ma', 'mb', 'p2'])
  })

  it('every group tab has the shadow and wash; a non-group tab has neither', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { container } = bar(tabs)
    for (const id of ['lead', 'ma', 'mb']) {
      const overlay = container.querySelector(`[data-tab-id="${id}"] [data-testid="team-tab-shadow"]`) as HTMLElement
      expect(overlay, id).not.toBeNull()
      expect(overlay.className).toContain('pointer-events-none')
      expect(overlay.getAttribute('data-shadow')).toBe('1px -1px 0 color-mix(in oklab, #a78bfa 70%, transparent)'.replace('#a78bfa', overlay.getAttribute('data-team-color')!))
      expect(overlay.getAttribute('data-wash')).toBe('6')
    }
    for (const id of ['plain', 'p2']) {
      expect(container.querySelector(`[data-tab-id="${id}"] [data-testid="team-tab-shadow"]`), id).toBeNull()
    }
  })

  it('no separator inside the group or after it', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    // the active tab is far from the group, so a separator drawn inside it WOULD be visible
    const { container } = bar(tabs, { activeTabId: 'plain' })
    const frame = container.querySelector('[data-testid="team-tab-group"]') as HTMLElement
    expect(frame.querySelectorAll('[data-testid="tab-separator"]')).toHaveLength(0)
    const visible = (el: Element | null) => !!el && el.getAttribute('data-testid') === 'tab-separator' && el.className.includes('bg-border-default')
    expect(visible(frame.nextElementSibling)).toBe(false)
    expect(visible(frame.previousElementSibling)).toBe(false)
  })

  it('member drag reorders memberOrder', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('mb', 'ma')
    expect(useTeamUiStore.getState().memberOrder[KEY]).toEqual(['B', 'A', 'C'])
    expect(h.onReorderTabs).not.toHaveBeenCalled()
  })

  it('drag across the group boundary snaps back', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('ma', 'plain') // out of the group
    drag('ma', 'p2')
    drag('plain', 'ma') // a plain tab into it
    drag('plain', 'lead')
    drag('p2', 'mb')
    expect(useTeamUiStore.getState().memberOrder[KEY]).toBeUndefined()
    expect(h.onReorderTabs).not.toHaveBeenCalled()
    drag('plain', 'p2') // plain tabs still reorder among themselves
    expect(h.onReorderTabs).toHaveBeenCalledTimes(1)
  })

  it('lead cannot be dragged behind a member', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('lead', 'ma')
    drag('lead', 'mb')
    drag('ma', 'lead') // nor a member in front of the lead
    drag('lead', 'p2') // the group as a whole is not draggable in v1
    expect(useTeamUiStore.getState().memberOrder[KEY]).toBeUndefined()
    expect(h.onReorderTabs).not.toHaveBeenCalled()
  })

  it('pinned tab never grouped', () => {
    const tabs = [mk('lead', 'lead-tm', 'h1', true), mk('ma', 'a-tm'), mk('plain', 'other')]
    seed([roster()], tabs)
    const { container } = bar(tabs)
    expect(screen.queryByTestId('team-group-label')).toBeNull()
    expect(container.querySelector('[data-testid="team-tab-shadow"]')).toBeNull()
    expect(container.querySelector('[data-testid="team-tab-group"]')).toBeNull()
  })

  it('member tab of a team whose lead is in another workspace stays ungrouped', () => {
    const tabs = [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('plain', 'other')]
    seed([roster()], tabs, [{ id: 'w1', tabs: ['ma', 'plain'] }, { id: 'w2', tabs: ['lead'] }])
    const { container } = bar([tabs[1], tabs[2]])
    expect(screen.queryByTestId('team-group-label')).toBeNull()
    expect(container.querySelector('[data-tab-id="ma"] [data-testid="team-tab-shadow"]')).toBeNull()
  })

  it('a collapsed team: the member whose lead is in another workspace is still drawn (same rule as stepping)', () => {
    const tabs = [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('mb', 'b-tm'), mk('plain', 'other')]
    seed([roster()], tabs, [{ id: 'w1', tabs: ['ma', 'plain'] }, { id: 'w2', tabs: ['lead', 'mb'] }])
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    const first = bar([tabs[1], tabs[3]])
    expect(idsInOrder(first.container)).toEqual(['ma', 'plain'])
    first.unmount()
    const second = bar([tabs[0], tabs[2]]) // the lead's own workspace: the member behind it is hidden
    expect(idsInOrder(second.container)).toEqual(['lead'])
  })

  describe('a LOCAL member that is joining', () => {
    it('gets the state word only: no alias, no dangling separator', () => {
      const { t } = useI18nStore.getState()
      const tabs = [mk('lead', 'lead-tm'), mk('ma', 'a-tm')]
      seed([roster({}, [mem('A', 1, 'a-tm', { state: 'joining' })])], tabs)
      const { container } = bar(tabs)
      const tab = container.querySelector('[data-tab-id="ma"]') as HTMLElement
      const label = tab.getAttribute('aria-label')!
      expect(label.endsWith(` · ${t('team.seat_state.joining')}`)).toBe(true)
      expect(label).not.toMatch(/· [:：]/)
      expect(tab).toHaveAttribute('data-seat-state', 'joining')
    })
  })

  describe('a member on another host', () => {
    const remote = (state: string) => roster({}, [mem('R', 1, 'r-tm', { state, host_id: 'dm-b', host_alias: 'b26' } as Partial<RosterMember>)])
    const scene = (state: string) => {
      useHostStore.setState({
        hosts: {
          h1: { id: 'h1', name: 'H1', ip: '1', port: 1, daemonId: 'daemon' },
          h2: { id: 'h2', name: 'b26', ip: '2', port: 1, daemonId: 'dm-b' },
        } as never,
        hostOrder: ['h1', 'h2'], runtime: {},
      })
      const tabs = [mk('lead', 'lead-tm'), mk('mr', 'r-tm', 'h2')]
      seed([remote(state)], tabs)
      return tabs
    }
    // What the tab draws, minus the two attributes that carry the state.
    const drawn = (container: HTMLElement) =>
      (container.querySelector('[data-tab-id="mr"]') as HTMLElement).outerHTML.replace(/ (aria-label|data-seat-state|aria-describedby)="[^"]*"/g, '')

    it('joining/releasing/killing member tab carries the state in tooltip and aria, no extra visual', () => {
      const { t } = useI18nStore.getState()
      let tabs = scene('active')
      const base = bar(tabs)
      const baseline = drawn(base.container)
      expect(base.container.querySelector('[data-tab-id="mr"]')).not.toHaveAttribute('data-seat-state')
      expect(base.container.querySelector('[data-tab-id="mr"]')!.getAttribute('aria-label') ?? '').not.toContain('b26')
      base.unmount()
      for (const state of ['joining', 'releasing', 'killing']) {
        tabs = scene(state)
        const { container, unmount } = bar(tabs)
        const tab = container.querySelector('[data-tab-id="mr"]') as HTMLElement
        const suffix = t('team.seat_state_suffix', { alias: 'b26', state: t(`team.seat_state.${state}`) })
        expect(suffix).toContain('b26')
        expect(tab.getAttribute('aria-label')).toContain(suffix)
        expect(tab).toHaveAttribute('data-seat-state', state)
        expect(drawn(container)).toBe(baseline)
        unmount()
      }
    })
  })
})
