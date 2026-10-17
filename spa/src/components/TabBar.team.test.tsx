// spa/src/components/TabBar.team.test.tsx — the team group on the top tab bar (plan TI-2, spec §4.2, §5).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, cleanup, render, screen } from '@testing-library/react'
import type { CollisionDetection, DragEndEvent } from '@dnd-kit/core'
import { TabBar } from './TabBar'
import { TeamDisplayProvider } from './team/TeamDisplayProvider'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useThemeStore } from '../stores/useThemeStore'
import { groupShadow } from './team/group-shadow'
import { useI18nStore } from '../stores/useI18nStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { teamKeyOf } from '../lib/team/team-views'
import type { RosterMember, RosterSession, TeamRoster } from '../lib/team/roster'
import type { PaneLayout, Tab } from '../types/tab'

// The DndContext's drag-end handler is what the bar's rules live in; capture it and call it with synthetic events.
const dnd = vi.hoisted(() => ({ onDragEnd: null as ((e: DragEndEvent) => void) | null, collide: null as CollisionDetection | null }))
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>()
  return {
    ...actual,
    DndContext: (props: React.ComponentProps<typeof actual.DndContext>) => {
      dnd.onDragEnd = props.onDragEnd as (e: DragEndEvent) => void
      dnd.collide = props.collisionDetection ?? null
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
  useThemeStore.setState({ activeThemeId: 'dark' })
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
      expect(overlay.getAttribute('data-shadow')).toBe(groupShadow(overlay.getAttribute('data-team-color')!, 'dark'))
      expect(overlay.getAttribute('data-wash')).toBe('6')
    }
    for (const id of ['plain', 'p2']) {
      expect(container.querySelector(`[data-tab-id="${id}"] [data-testid="team-tab-shadow"]`), id).toBeNull()
    }
  })

  it('group tabs render the V0 shadow in dark and light (darkened base)', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { container } = bar(tabs)
    const el = (id: string) => container.querySelector(`[data-tab-id="${id}"] [data-testid="team-tab-shadow"]`) as HTMLElement
    const color = el('lead').getAttribute('data-team-color')!
    for (const id of ['lead', 'ma', 'mb']) expect(el(id).getAttribute('data-shadow'), id).toBe(groupShadow(color, 'dark'))
    act(() => useThemeStore.setState({ activeThemeId: 'light' }))
    expect(el('lead').getAttribute('data-shadow')).toBe(groupShadow(color, 'light'))
    expect(el('lead').getAttribute('data-shadow')).not.toBe(groupShadow(color, 'dark'))
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

  it('a member never leaves the group nor takes the lead\'s place: those drops snap back', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('ma', 'plain') // out of the group
    drag('ma', 'p2')
    drag('ma', 'lead') // in front of the lead
    drag('mb', 'lead')
    drag('ma', 'team-group:' + KEY)
    expect(useTeamUiStore.getState().memberOrder[KEY]).toBeUndefined()
    expect(h.onReorderTabs).not.toHaveBeenCalled()
  })

  describe('collision domain (TG-1)', () => {
    const container = (id: string) => ({ id, data: { current: undefined }, node: { current: null }, rect: { current: null }, disabled: false })
    const rectAt = (left: number, width = 100) => ({ left, right: left + width, top: 0, bottom: 26, width, height: 26 })
    const collide = (active: string, left: number) => {
      const ids = ['plain', 'lead', 'ma', 'mb', 'p2', 'team-group:' + KEY]
      const rects = new Map(ids.map((id, i) => [id, rectAt(i * 100)]))
      rects.set('team-group:' + KEY, rectAt(100, 300)) // the frame spans lead..mb
      return dnd.collide!({
        active: { id: active }, collisionRect: rectAt(left), droppableRects: rects, pointerCoordinates: null,
        droppableContainers: ids.map(container),
      } as never).map((c) => String(c.id))
    }

    it('a member only collides with the members of its own group', () => {
      const tabs = baseTabs()
      seed([roster()], tabs)
      bar(tabs)
      expect([...collide('ma', 100)].sort()).toEqual(['ma', 'mb'])
      expect([...collide('ma', 300)].sort()).toEqual(['ma', 'mb'])
    })

    it('a member dragged outside the group frame targets itself, so it keeps following the pointer until it snaps back', () => {
      const tabs = baseTabs()
      seed([roster()], tabs)
      bar(tabs)
      expect(collide('ma', 450)).toEqual(['ma']) // over p2
      expect(collide('ma', -200)).toEqual(['ma']) // before the plain tab
    })

    it('a lead/plain drag targets group frames and plain tabs, never the inside of a group', () => {
      const tabs = baseTabs()
      seed([roster()], tabs)
      bar(tabs)
      for (const active of ['team-group:' + KEY, 'plain']) {
        const ids = collide(active, 0)
        expect(ids).not.toContain('lead')
        expect(ids).not.toContain('ma')
        expect(ids).not.toContain('mb')
      }
    })
  })

  it('plain tabs still reorder among themselves', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('plain', 'p2')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['lead', 'ma', 'mb', 'p2', 'plain'])
  })

  it('a plain tab dropped on the group lands before or after the whole group, never inside (TG-1)', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('plain', 'ma') // moving right: behind the group
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['lead', 'ma', 'mb', 'plain', 'p2'])
    drag('p2', 'lead') // moving left: in front of the group
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['plain', 'p2', 'lead', 'ma', 'mb'])
    drag('p2', 'mb')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['plain', 'p2', 'lead', 'ma', 'mb'])
    expect(useTeamUiStore.getState().memberOrder[KEY]).toBeUndefined()
  })

  it('dragging the lead moves the whole run; the group frame id counts as the lead (TG-1)', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('lead', 'p2')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['plain', 'p2', 'lead', 'ma', 'mb'])
    drag('team-group:' + KEY, 'plain')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['lead', 'ma', 'mb', 'plain', 'p2'])
    drag('lead', 'ma') // onto its own member: nothing
    expect(h.onReorderTabs).toHaveBeenCalledTimes(2)
  })

  it('a collapsed group moves together with the members it hides (TG-1)', () => {
    const tabs = baseTabs()
    seed([roster()], tabs)
    act(() => useTeamUiStore.getState().setCollapsed(KEY, true))
    const { h } = bar(tabs)
    drag('lead', 'p2')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['plain', 'p2', 'lead', 'ma', 'mb'])
  })

  it('a group dropped on another group lands beside it, never inside (TG-1)', () => {
    const r2 = roster({ id: 't2', lead: sess('L2', 'lead2-tm') }, [mem('D', 1, 'd-tm')])
    const tabs = [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('mb', 'b-tm'), mk('lead2', 'lead2-tm'), mk('md', 'd-tm')]
    seed([roster(), r2], tabs)
    const { h } = bar(tabs)
    drag('lead', 'md')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['lead2', 'md', 'lead', 'ma', 'mb'])
  })

  it('pinned zone stays apart: a group and a pinned tab never trade places (TG-1)', () => {
    const tabs = [mk('pin', 'p2', 'h1', true), ...baseTabs().slice(0, 4)]
    seed([roster()], tabs)
    const { h } = bar(tabs)
    drag('lead', 'pin')
    drag('pin', 'lead')
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
        hostOrder: ['h1', 'h2'], runtime: { h2: { daemonIdVerified: { endpoint: '2:1', daemonId: 'dm-b' } } } as never,
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

describe('TabBar — the watcher area row never draws a team group (journal v5 §3.7, U10)', () => {
  // The guardian tab shows the watcher's own tmux session, which can be a team's lead.
  const areaTabs = () => [mk('watch-guardian', 'lead-tm'), mk('ma', 'a-tm'), mk('p2', 'p2')]
  function fixedBar(tabs: Tab[]) {
    const h = handlers()
    const utils = render(
      <TeamDisplayProvider>
        <TabBar tabs={tabs} activeTabId={null} fixed {...h} />
      </TeamDisplayProvider>,
    )
    return { ...utils, h }
  }

  it('no group frame and no team shadow on any tab; the same tabs on an ordinary row are a group', () => {
    const tabs = areaTabs()
    seed([roster()], tabs)
    const { container, unmount } = fixedBar(tabs)
    expect(container.querySelector('[data-testid="team-tab-group"]')).toBeNull()
    expect(container.querySelector('[data-testid="team-tab-shadow"]')).toBeNull()
    expect(idsInOrder(container)).toEqual(['watch-guardian', 'ma', 'p2'])
    unmount()
    // the control: without `fixed` the lead and its member are grouped
    const ordinary = bar(tabs)
    expect(ordinary.container.querySelector('[data-testid="team-tab-group"]')).not.toBeNull()
  })

  it('a drag moves the one tab, never a run', () => {
    const tabs = areaTabs()
    seed([roster()], tabs)
    const { h } = fixedBar(tabs)
    drag('watch-guardian', 'p2')
    expect(h.onReorderTabs).toHaveBeenLastCalledWith(['ma', 'p2', 'watch-guardian'])
  })
})
