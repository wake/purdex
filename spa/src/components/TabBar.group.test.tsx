// spa/src/components/TabBar.group.test.tsx — the top bar in the task-group view (task groups spec §6.2, plan TG-4b): the tabs come in the group's order and are drawn as they are — no team run (frame, shadow, collapse) —
// and an empty group says so.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { DragEndEvent } from '@dnd-kit/core'
import { TabBar } from './TabBar'
import { TeamDisplayProvider } from './team/TeamDisplayProvider'
import { registerModule, clearModuleRegistry } from '../lib/module-registry'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'
import { useTabStore } from '../stores/useTabStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useHostStore } from '../stores/useHostStore'
import { useTeamUiStore } from '../stores/useTeamUiStore'
import { useThemeStore } from '../stores/useThemeStore'
import { useI18nStore } from '../stores/useI18nStore'
import { useWorkspaceStore } from '../features/workspace/store'
import { teamKeyOf } from '../lib/team/team-views'
import type { RosterMember, RosterSession, TeamRoster } from '../lib/team/roster'
import type { PaneLayout, Tab } from '../types/tab'

const dnd = vi.hoisted(() => ({ onDragEnd: null as ((e: DragEndEvent) => void) | null, started: 0 }))
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@dnd-kit/core')>()
  return {
    ...actual,
    DndContext: (props: React.ComponentProps<typeof actual.DndContext>) => {
      dnd.onDragEnd = props.onDragEnd as (e: DragEndEvent) => void
      return <actual.DndContext {...props} onDragStart={() => { dnd.started += 1 }} />
    },
  }
})
vi.mock('../hooks/useScrollOverflow', () => ({
  useScrollOverflow: () => ({ containerRef: { current: null }, canScrollLeft: false, canScrollRight: false, scrollLeft: vi.fn(), scrollRight: vi.fn() }),
}))

const sess = (id: string, tmux: string): RosterSession => ({ session_id: id, ref: `_${id}`, address: `mlab/${id}-xx`, title: `title ${id}`, live: true, tmux_session: tmux })
const mem = (id: string, joined: number, tmux: string): RosterMember => ({ ...sess(id, tmux), state: 'active', origin: 'spawned', joined_at: joined })
const roster = (): TeamRoster => ({ id: 't1', host_id: 'daemon', created_at: 1, team_name: '', team_label: '', lead: sess('L', 'lead-tm'), members: [mem('A', 1, 'a-tm'), mem('B', 2, 'b-tm')] })
const leaf = (name: string): PaneLayout => ({ type: 'leaf', pane: { id: `p-${name}`, content: { kind: 'tmux-session', hostId: 'h1', sessionCode: `code-${name}`, mode: 'terminal', cachedName: name, tmuxInstance: 'i' } } })
const mk = (id: string, name: string, pinned = false): Tab => ({ id, pinned, locked: false, createdAt: 0, layout: leaf(name) })
const KEY = teamKeyOf('h1', 't1')
const SESSIONS = ['lead-tm', 'a-tm', 'b-tm', 'other'].map((name) => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' }))

function seed(all: Tab[]) {
  act(() => {
    useTeamRosterStore.setState({ byHost: { h1: [roster()] } })
    useTabStore.setState({ tabs: Object.fromEntries(all.map((t) => [t.id, t])), tabOrder: all.map((t) => t.id), activeTabId: null })
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'w1', tabs: all.map((t) => t.id), activeTabId: null }] as never, activeWorkspaceId: 'w1' })
    useSessionStore.setState({ sessions: { h1: SESSIONS as never } })
  })
}

function bar(tabs: Tab[], flat: boolean, groupDrag?: { locked: ReadonlySet<string>; onDrop: (a: string, o: string) => void }) {
  const onReorderTabs = vi.fn()
  const utils = render(
    <TeamDisplayProvider>
      <TabBar tabs={tabs} flat={flat} groupDrag={groupDrag} activeTabId={null} onSelectTab={vi.fn()} onCloseTab={vi.fn()} onAddTab={vi.fn()} onReorderTabs={onReorderTabs} onMiddleClick={vi.fn()} onContextMenu={vi.fn()} />
    </TeamDisplayProvider>,
  )
  return { ...utils, onReorderTabs }
}
const idsInOrder = (root: HTMLElement) => Array.from(root.querySelectorAll('[data-tab-id]')).map((el) => el.getAttribute('data-tab-id')!)

beforeEach(() => {
  cleanup()
  dnd.onDragEnd = null
  dnd.started = 0
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: () => null }] })
  useTeamUiStore.setState({ memberOrder: {}, collapsed: {}, panelMode: {}, ghostWorkspace: {}, teamBeadHost: true })
  useThemeStore.setState({ activeThemeId: 'dark' })
  useTeamRosterStore.getState().reset()
  useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'H1', ip: '1', port: 1, daemonId: 'daemon' } } as never, hostOrder: ['h1'], runtime: {} })
  useI18nStore.getState().setLocale('en')
})

describe('TabBar — the task-group view (TG-4b)', () => {
  const tabs = () => [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('mb', 'b-tm'), mk('plain', 'other')]

  it('the tabs are drawn in the order given, with no team frame', () => {
    seed(tabs())
    const { container } = bar(tabs(), true)
    expect(idsInOrder(container)).toEqual(['lead', 'ma', 'mb', 'plain'])
    expect(container.querySelector('[data-testid="team-tab-group"]')).toBeNull()
    expect(container.querySelector('[data-testid="team-tab-shadow"]')).toBeNull()
  })

  it('a collapsed team still lists every tab (the collapse is a sidebar matter in the workspace view only)', () => {
    seed(tabs())
    act(() => useTeamUiStore.setState({ collapsed: { [KEY]: true } }))
    const flat = bar(tabs(), true)
    expect(idsInOrder(flat.container)).toEqual(['lead', 'ma', 'mb', 'plain'])
    cleanup()
    // the workspace bar, handed the same tabs, hides the members the team collapsed
    const ws = bar(tabs(), false)
    expect(idsInOrder(ws.container)).toEqual(['lead', 'plain'])
  })

  it('a seat with two tabs, and tabs from two workspaces, are drawn as the group lists them', () => {
    const list = [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('ma2', 'a-tm'), mk('mb', 'b-tm'), mk('plain', 'other')]
    seed(list)
    act(() => useWorkspaceStore.setState({
      workspaces: [
        { id: 'w1', name: 'w1', tabs: ['lead', 'ma', 'plain'], activeTabId: null },
        { id: 'w2', name: 'w2', tabs: ['ma2', 'mb'], activeTabId: null },
      ] as never,
    }))
    const { container } = bar(list, true)
    expect(idsInOrder(container)).toEqual(['lead', 'ma', 'ma2', 'mb', 'plain'])
  })

  it('a pinned lead is drawn first, in the pinned zone', () => {
    const list = [mk('lead', 'lead-tm', true), mk('ma', 'a-tm'), mk('plain', 'other')]
    seed(list)
    const { container } = bar(list, true)
    expect(idsInOrder(container)).toEqual(['lead', 'ma', 'plain'])
  })

  it('a manual group\'s pinned tabs keep the pinned zone and its divider before the others', () => {
    const list = [mk('p1', 'other', true), mk('p2', 'lead-tm', true), mk('x', 'a-tm'), mk('y', 'b-tm')]
    seed(list)
    const { container } = bar(list, true)
    expect(idsInOrder(container)).toEqual(['p1', 'p2', 'x', 'y'])
    const parent = (id: string) => container.querySelector(`[data-tab-id="${id}"]`)!.parentElement
    expect(parent('p1')).toBe(parent('p2'))
    expect(parent('x')).not.toBe(parent('p1'))
    expect(parent('p1')!.nextElementSibling!.className).toContain('w-px') // the divider after the pinned zone
  })

  it('an unpinned lead stays first even when other tabs of the team are pinned: one zone, in the order given', () => {
    const list = [mk('lead', 'lead-tm'), mk('pa', 'a-tm', true), mk('mb', 'b-tm'), mk('plain', 'other')]
    seed(list)
    const { container } = bar(list, true)
    expect(idsInOrder(container)).toEqual(['lead', 'pa', 'mb', 'plain'])
    const parent = (id: string) => container.querySelector(`[data-tab-id="${id}"]`)!.parentElement
    expect(new Set(['lead', 'pa', 'mb', 'plain'].map(parent)).size).toBe(1)
  })

  it('a pinned tab keeps its pinned look (a button), also outside the pinned zone', () => {
    const list = [mk('lead', 'lead-tm'), mk('pa', 'a-tm', true), mk('x', 'b-tm')]
    seed(list)
    const { container } = bar(list, true)
    expect(container.querySelector('[data-tab-id="pa"]')!.tagName).toBe('BUTTON')
    expect(container.querySelector('[data-tab-id="x"]')!.tagName).not.toBe('BUTTON')
  })

  it('no tab can be picked up: a press that moves starts no drag and shifts nothing', () => {
    const list = [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('plain', 'other')]
    seed(list)
    const { container } = bar(list, true)
    const el = container.querySelector('[data-tab-id="ma"]') as HTMLElement
    fireEvent.pointerDown(el, { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 60, clientY: 10 })
    expect(dnd.started).toBe(0)
    for (const id of ['lead', 'ma', 'plain']) expect((container.querySelector(`[data-tab-id="${id}"]`) as HTMLElement).style.transform).toBe('')
    fireEvent.pointerUp(document)
  })

  it('a pinned tab of the pinned zone cannot be picked up either', () => {
    const list = [mk('p1', 'other', true), mk('x', 'a-tm')]
    seed(list)
    const { container } = bar(list, true)
    const el = container.querySelector('[data-tab-id="p1"]') as HTMLElement
    fireEvent.pointerDown(el, { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
    fireEvent.pointerMove(document, { clientX: 60, clientY: 10 })
    expect(dnd.started).toBe(0)
    expect(el.style.transform).toBe('')
    fireEvent.pointerUp(document)
  })

  describe('dragging (TG-4c)', () => {
    const press = (el: HTMLElement) => {
      fireEvent.pointerDown(el, { button: 0, isPrimary: true, clientX: 10, clientY: 10 })
      fireEvent.pointerMove(document, { clientX: 60, clientY: 10 })
    }

    it('with a group drag, a tab is picked up — the locked lead is not, and a pinned-zone tab is picked up too', () => {
      const list = [mk('lead', 'lead-tm', true), mk('p', 'other', true), mk('ma', 'a-tm')]
      seed(list)
      const { container } = bar(list, true, { locked: new Set(['lead']), onDrop: vi.fn() })
      press(container.querySelector('[data-tab-id="lead"]') as HTMLElement)
      expect(dnd.started).toBe(0)
      fireEvent.pointerUp(document)
      press(container.querySelector('[data-tab-id="ma"]') as HTMLElement)
      expect(dnd.started).toBe(1)
      fireEvent.pointerUp(document)
      dnd.started = 0
      press(container.querySelector('[data-tab-id="p"]') as HTMLElement)
      expect(dnd.started).toBe(1)
      fireEvent.pointerUp(document)
    })

    it('an unpinned lead (in the one zone with the others) is locked as well', () => {
      const list = [mk('lead', 'lead-tm'), mk('ma', 'a-tm')]
      seed(list)
      const { container } = bar(list, true, { locked: new Set(['lead']), onDrop: vi.fn() })
      press(container.querySelector('[data-tab-id="lead"]') as HTMLElement)
      expect(dnd.started).toBe(0)
      fireEvent.pointerUp(document)
      press(container.querySelector('[data-tab-id="ma"]') as HTMLElement)
      expect(dnd.started).toBe(1)
      fireEvent.pointerUp(document)
    })

    it('a drop is handed to onDrop(active, over) and never to onReorderTabs', () => {
      const list = [mk('lead', 'lead-tm'), mk('ma', 'a-tm'), mk('mb', 'b-tm')]
      seed(list)
      const onDrop = vi.fn()
      const { onReorderTabs } = bar(list, true, { locked: new Set(['lead']), onDrop })
      act(() => dnd.onDragEnd!({ active: { id: 'mb' }, over: { id: 'ma' } } as unknown as DragEndEvent))
      expect(onDrop).toHaveBeenCalledWith('mb', 'ma')
      expect(onReorderTabs).not.toHaveBeenCalled()
      act(() => dnd.onDragEnd!({ active: { id: 'ma' }, over: { id: 'ma' } } as unknown as DragEndEvent))
      expect(onDrop).toHaveBeenCalledTimes(1)
    })
  })

  it('an empty group says so, and keeps the + button', () => {
    seed([])
    bar([], true)
    expect(screen.getByTestId('tab-bar-group-empty')).toHaveTextContent('No tabs in this task group')
    expect(screen.getByTitle('New tab')).toBeInTheDocument()
  })

  it('the workspace bar with no tabs shows no such text', () => {
    seed([])
    bar([], false)
    expect(screen.queryByTestId('tab-bar-group-empty')).toBeNull()
  })

  it('a drag in the group bar reorders nothing yet (TG-4c): the order is the group\'s, not tabOrder', () => {
    seed(tabs())
    const { onReorderTabs } = bar(tabs(), true)
    act(() => dnd.onDragEnd!({ active: { id: 'plain' }, over: { id: 'lead' } } as unknown as DragEndEvent))
    expect(onReorderTabs).not.toHaveBeenCalled()
  })
})
