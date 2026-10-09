// spa/src/components/team/TeamPanelArea.test.tsx — the panel area and its team view (plan TI-4 / WA-2a): who it shows for,
// what a row carries, the two modes, resize (draft-then-commit, clamped), enlarge, and that all of it lives in the store
// (survives a tab switch through the real TabContent and a reload). Real stores and the real provider.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, resetTeamStores, seedScene } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import type { TeamRoster } from '../../lib/team/roster'
import { CELL_H, HEADER_H, firstRowCapacity } from './panel-layout'

// The light, the subagent dots and the host chip are TI-3's pieces over the agent store; here they are stand-ins that show
// what the panel asked of them.
vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: ({ sessionCode, subagents }: { sessionCode: string; subagents?: boolean }) => (
    <span data-testid="seat-icon" data-code={sessionCode} data-subagents={String(subagents === true)} />
  ),
  TeamSeatHostBadge: ({ hostId }: { hostId: string }) => <span data-testid="seat-host" data-host={hostId} />,
}))

const MEMBERS: Array<[string, string]> = [['A', 'a-tm'], ['B', 'b-tm'], ['C', 'c-tm']]
const scene = (over: { activeTabId?: string | null; teamName?: string; teamLabel?: string } = {}) => seedScene({
  members: MEMBERS,
  tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]],
  workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }],
  activeTabId: 'lead',
  ...over,
})

const readings = (patch: (t: TeamRoster) => void) => act(() => {
  const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
  patch(roster[0])
  useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
})

const mount = () => render(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const rows = () => screen.getAllByTestId('team-panel-row')
const area = () => screen.getByTestId('team-panel-area')
const STORAGE = 'purdex-team-ui'

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useTeamUiStore.setState({ panel: { width: 312, expanded: false }, teamDrill: {}, workbookTabs: {} })
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
})
afterEach(() => cleanup())

describe('who the panel shows for', () => {
  it('panel shows for a lead tab and a member tab, not for others', () => {
    scene()
    mount()
    expect(screen.queryByTestId('team-panel')).not.toBeNull()
    setActive('ma')
    expect(screen.queryByTestId('team-panel')).not.toBeNull()
    setActive('x')
    expect(screen.queryByTestId('team-panel')).toBeNull()
    setActive(null)
    expect(screen.queryByTestId('team-panel')).toBeNull()
  })

  it('a workbook result (toggle on, or a drill) renders no team view yet', () => {
    scene()
    mount()
    act(() => useTeamUiStore.getState().setWorkbookTab('lead', true))
    expect(screen.queryByTestId('team-panel')).toBeNull()
    act(() => { useTeamUiStore.getState().setWorkbookTab('lead', false); useTeamUiStore.getState().setTeamDrill(KEY, { hostId: HOST, sessionId: 'A' }) })
    expect(screen.queryByTestId('team-panel')).toBeNull()
    act(() => useTeamUiStore.getState().setTeamDrill(KEY, null))
    expect(screen.queryByTestId('team-panel')).not.toBeNull()
  })

  it('active row = the active tab\'s seat', () => {
    scene({ activeTabId: 'mb' })
    mount()
    const active = rows().filter((r) => r.getAttribute('data-active') === 'true')
    expect(active.map((r) => r.getAttribute('data-session-id'))).toEqual(['B'])
    setActive('lead')
    expect(rows().filter((r) => r.getAttribute('data-active') === 'true').map((r) => r.getAttribute('data-session-id'))).toEqual(['L'])
  })
})

describe('typeface', () => {
  it('the panel is UI sans even when the surrounding content box is monospace', () => {
    scene()
    render(<div className="font-mono"><TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider></div>)
    expect(area().className).toMatch(/\bfont-sans\b/)
    expect(area().className).not.toMatch(/\bfont-mono\b/)
  })
})

describe('full mode', () => {
  // A browser moves focus to the focusable row unless mousedown is default-prevented; jsdom does not, so do it by hand.
  const pressRow = (row: HTMLElement) => { if (fireEvent.mouseDown(row)) row.focus() }
  const withTerm = (fn: (term: HTMLTextAreaElement) => void) => {
    scene()
    mount()
    const term = document.createElement('textarea') // a stand-in for the terminal's input
    document.body.appendChild(term)
    term.focus()
    try { fn(term) } finally { term.remove() }
  }

  it('pressing the lead row (not draggable) keeps the keyboard in the terminal', () => {
    withTerm((term) => {
      pressRow(rows()[0])
      expect(document.activeElement).toBe(term)
    })
  })

  it('pressing a draggable row does not prevent mousedown (HTML5 drag needs it); click gives focus back to the terminal', () => {
    withTerm((term) => {
      const row = rows()[1]
      pressRow(row)
      expect(document.activeElement).toBe(row)
      fireEvent.mouseUp(row)
      expect(document.activeElement).toBe(term)
      pressRow(row)
      fireEvent.click(row)
      expect(document.activeElement).toBe(term)
    })
  })

  it('a finished drag (dragend) gives focus back to the terminal', () => {
    withTerm((term) => {
      const row = rows()[1]
      pressRow(row)
      fireEvent.dragStart(row, { dataTransfer: { setData: vi.fn(), effectAllowed: '' } })
      fireEvent.dragEnd(row)
      expect(document.activeElement).toBe(term)
    })
  })

  it('releasing the mouse outside the row (no mouseup / click / dragend on it) still gives focus back', () => {
    withTerm((term) => {
      pressRow(rows()[1])
      fireEvent.mouseUp(document.body)
      expect(document.activeElement).toBe(term)
    })
  })

  it('the window losing focus mid-press gives focus back', () => {
    withTerm((term) => {
      pressRow(rows()[1])
      fireEvent.blur(window)
      expect(document.activeElement).toBe(term)
    })
  })

  it('a row unmounted mid-press gives focus back', () => {
    withTerm((term) => {
      pressRow(rows()[1])
      cleanup()
      expect(document.activeElement).toBe(term)
    })
  })

  it('does not take focus from a focusable the person deliberately landed on', () => {
    withTerm(() => {
      const other = document.createElement('button')
      document.body.appendChild(other)
      pressRow(rows()[1])
      other.focus()
      fireEvent.mouseUp(document.body)
      expect(document.activeElement).toBe(other)
      other.remove()
    })
  })

  it('focus is not restored to a terminal that has been removed', () => {
    withTerm((term) => {
      const row = rows()[1]
      pressRow(row)
      term.remove()
      fireEvent.mouseUp(row)
      expect(document.activeElement).toBe(row)
    })
  })

  it('lead row is first and not draggable; members are', () => {
    scene()
    mount()
    expect(rows().map((r) => r.getAttribute('data-session-id'))).toEqual(['L', 'A', 'B', 'C'])
    expect(rows()[0].getAttribute('data-role')).toBe('lead')
    expect(rows()[0].getAttribute('draggable')).not.toBe('true')
    expect(rows().slice(1).every((r) => r.getAttribute('draggable') === 'true')).toBe(true)
  })

  it('full rows carry host chip / title / model / effort / context / light / subagents', () => {
    scene()
    readings((t) => {
      t.lead.model = 'claude-opus-5-5'
      t.lead.effort = 'high'
      t.lead.context = { used_percentage: 42.4, window: 1000, at: 1 }
      t.members[0].context = { used_percentage: 7, window: 1000, model_id: 'claude-sonnet-5-5', effort: 'low', at: 1 }
    })
    mount()
    const lead = within(rows()[0])
    expect(lead.getByTestId('seat-icon').getAttribute('data-subagents')).toBe('true') // the dots + light
    expect(lead.getByTestId('seat-host')).toBeTruthy()
    expect(lead.getByText('title L')).toBeTruthy()
    expect(lead.getAllByTestId('model-icon-opus').length).toBeGreaterThan(0)
    expect(lead.getByTestId('team-panel-model').textContent).toBe('Opus')
    expect(lead.getByTestId('team-panel-effort').textContent).toBe('high')
    expect(lead.getByTestId('team-panel-ctx').textContent).toBe('42%')
    expect(lead.getByTestId('context-ring')).toBeTruthy()
    const a = within(rows()[1])
    expect(a.getByTestId('team-panel-model').textContent).toBe('Sonnet') // model_id of the context sample
    expect(a.getByTestId('team-panel-effort').textContent).toBe('low')
    expect(a.getByTestId('team-panel-ctx').textContent).toBe('7%')
  })

  it('header shows panelName and its tooltip "<name> (<label>)"', () => {
    scene({ teamName: 'Alpha Team', teamLabel: 'alpha' })
    mount()
    const name = screen.getByTestId('team-panel-name')
    expect(name.textContent).toBe('Alpha Team')
    expect(name.getAttribute('title')).toBe('Alpha Team (alpha)')
    expect(screen.getByTestId('team-panel-count').textContent).toContain('3')
  })

  it('missing model or context shows —, never 0', () => {
    scene()
    mount()
    for (const r of rows()) {
      const row = within(r)
      expect(row.getByTestId('team-panel-model').textContent).toBe('—')
      expect(row.getByTestId('team-panel-effort').textContent).toBe('—')
      expect(row.getByTestId('team-panel-ctx').textContent).toBe('—')
    }
    expect(area().textContent).not.toContain('0%')
  })

  it('new team starts full', () => {
    scene()
    mount()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
  })

  it('row click opens an unopened member', () => {
    scene()
    mount()
    const before = Object.keys(useTabStore.getState().tabs).length
    fireEvent.click(rows()[3]) // C has no tab
    const tabs = useTabStore.getState().tabs
    expect(Object.keys(tabs)).toHaveLength(before + 1)
    expect(Object.values(tabs).some((t) => t.layout.type === 'leaf' && t.layout.pane.content.kind === 'tmux-session' && t.layout.pane.content.cachedName === 'c-tm')).toBe(true)
  })

  it('row click on an opened member switches to its tab', () => {
    scene()
    mount()
    fireEvent.click(rows()[2]) // B
    expect(useTabStore.getState().activeTabId).toBe('mb')
  })

  it('drag reorders (setMemberOrder), the lead never joins the order', () => {
    scene()
    mount()
    fireEvent.dragStart(rows()[1], { dataTransfer: { setData: vi.fn(), effectAllowed: '' } }) // A
    fireEvent.dragOver(rows()[3], { dataTransfer: { dropEffect: '' } }) // over C
    fireEvent.drop(rows()[3], { dataTransfer: {} })
    expect(useTeamUiStore.getState().memberOrder[KEY]).toEqual(['B', 'A', 'C'])
    expect(rows().map((r) => r.getAttribute('data-session-id'))).toEqual(['L', 'B', 'A', 'C'])
  })

  it('the header switches to one-line, and the mode is per team in the store', () => {
    scene()
    mount()
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    expect(useTeamUiStore.getState().panelMode[KEY]).toBe('line')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    fireEvent.click(screen.getByTestId('team-panel-to-full'))
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
  })
})

describe('header height (TI-6)', () => {
  const header = () => screen.getByTestId('team-panel-header')
  const scene5 = (extra: number) => seedScene({
    members: Array.from({ length: extra }, (_, i) => [`M${i}`, `m${i}-tm`] as [string, string]),
    tabs: [['lead', 'lead-tm']],
    workspaces: [{ id: 'w1', tabs: ['lead'] }],
    activeTabId: 'lead',
  })

  it('both modes use the same fixed header height', () => {
    scene()
    mount()
    const full = header().style.height
    expect(full).toBe(`${HEADER_H}px`)
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    expect(header().style.height).toBe(full)
  })

  it('lead + 3 members stay in the header row; there is no region under it', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    expect(within(header()).getAllByTestId('team-panel-cell')).toHaveLength(4)
    expect(screen.queryByTestId('team-panel-more')).toBeNull()
  })

  it('the 5th seat wraps into a region under the header, which keeps its height', () => {
    scene5(4)
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    expect(within(header()).getAllByTestId('team-panel-cell')).toHaveLength(4)
    const more = screen.getByTestId('team-panel-more')
    expect(header().contains(more)).toBe(false)
    expect(more.className).toContain('flex-wrap')
    expect(within(more).getAllByTestId('team-panel-cell')).toHaveLength(1)
    expect(header().style.height).toBe(`${HEADER_H}px`)
  })

  it('a big team keeps the header row to the capacity and wraps the rest', () => {
    scene5(8)
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    expect(within(header()).getAllByTestId('team-panel-cell')).toHaveLength(firstRowCapacity(312))
    expect(within(screen.getByTestId('team-panel-more')).getAllByTestId('team-panel-cell')).toHaveLength(9 - firstRowCapacity(312))
  })

  describe('measured room', () => {
    let cellW = 0
    let availW = 0
    beforeEach(() => {
      const testid = (el: Element) => el.getAttribute('data-testid')
      vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) { return testid(this) === 'team-panel-cell' ? cellW : 0 })
      vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) { return testid(this) === 'team-panel-cells' ? availW : 0 })
    })
    afterEach(() => vi.restoreAllMocks())
    const inHeader = () => within(header()).getAllByTestId('team-panel-cell').length

    it('a wider cell (status light + icon) lowers the capacity', () => {
      scene5(8)
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
      availW = 165
      cellW = 38
      const { unmount } = mount()
      expect(inHeader()).toBe(4)
      unmount()
      cellW = 50 // the iconDot style: ~12px wider per cell
      mount()
      expect(inHeader()).toBe(3)
      expect(within(screen.getByTestId('team-panel-more')).getAllByTestId('team-panel-cell')).toHaveLength(6)
    })

    it('a wider boundary cell does not make the capacity oscillate (capacity comes from the widths, not from what is shown)', () => {
      const err = vi.spyOn(console, 'error').mockImplementation(() => {})
      const widths: Record<string, number> = { L: 38, M0: 38, M1: 38, M2: 50 } // the 4th seat is the wide one (status light)
      vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
        return this.getAttribute('data-testid') === 'team-panel-cell' ? widths[this.getAttribute('data-session-id') ?? ''] ?? 38 : 0
      })
      scene5(8)
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
      availW = 165
      const { rerender } = mount()
      const count = () => within(header()).getAllByTestId('team-panel-cell').length
      const first = count()
      expect(first).toBe(3) // (165 - 5 + 2) / (50 + 2) -> 3, and it stays
      for (let i = 0; i < 5; i++) {
        rerender(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
        expect(count()).toBe(first)
      }
      expect(err).not.toHaveBeenCalled() // no "Maximum update depth exceeded"
    })

    describe('the capacity follows the current seats', () => {
      let widths: Record<string, number> = {}
      beforeEach(() => {
        vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
          return this.getAttribute('data-testid') === 'team-panel-cell' ? widths[this.getAttribute('data-session-id') ?? ''] ?? 38 : 0
        })
        availW = 165
      })
      const seed = (ids: string[]) => seedScene({
        members: ids.map((id) => [id, `${id}-tm`] as [string, string]),
        tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }], activeTabId: 'lead',
      })
      const count = () => within(header()).getAllByTestId('team-panel-cell').length

      it('removing the widest seat brings the capacity back', () => {
        widths = { M2: 50 }
        seed(['M0', 'M1', 'M2', 'M3', 'M4', 'M5'])
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        mount()
        expect(count()).toBe(3)
        act(() => seed(['M0', 'M1', 'M3', 'M4', 'M5']))
        expect(count()).toBe(4)
      })

      it('another team in the same box and style starts from scratch', () => {
        widths = { M2: 50 }
        seed(['M0', 'M1', 'M2', 'M3', 'M4', 'M5'])
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        mount()
        expect(count()).toBe(3)
        widths = {}
        act(() => {
          const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
          roster[0].id = 't2'
          useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
          useTeamUiStore.getState().setPanelMode(`${HOST}\u0000t2`, 'line')
        })
        expect(count()).toBe(4)
      })
    })

    it('an enlarged panel with a narrow box and a big team wraps under the header', () => {
      scene5(8)
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
      act(() => useTeamUiStore.getState().setPanelExpanded(true))
      availW = 130
      cellW = 38
      mount()
      expect(inHeader()).toBe(3)
      expect(screen.getByTestId('team-panel-more')).toBeTruthy()
    })

    describe('capacity from each cell\'s own width', () => {
      let widths: Record<string, number> = {}
      let roCallbacks: Array<() => void> = []
      beforeEach(() => {
        widths = {}
        roCallbacks = []
        vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
          return this.getAttribute('data-testid') === 'team-panel-cell' ? widths[this.getAttribute('data-session-id') ?? ''] ?? 38 : 0
        })
        vi.stubGlobal('ResizeObserver', class { constructor(cb: () => void) { roCallbacks.push(cb) } observe() {} disconnect() {} })
      })
      afterEach(() => vi.unstubAllGlobals())

      it('a wider remote cell (+12px host icon) as the 4th seat still leaves 4 in the first row when there is room', () => {
        widths = { M2: 50 }
        scene5(8)
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        availW = 180 // 38*3 + 50 + 3*2 + 5 = 175
        mount()
        expect(inHeader()).toBe(4)
      })

      it('too little room for the wide cell drops it to the second row', () => {
        widths = { M2: 50 }
        scene5(8)
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        availW = 170
        mount()
        expect(inHeader()).toBe(3)
      })

      it('the capacity depends on the wrapped cells too (narrow 5th seat fits)', () => {
        widths = { M3: 20 }
        scene5(8)
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        availW = 190 // 4 * 38 + 20 + 4*2 + 5 = 185
        mount()
        expect(inHeader()).toBe(5)
      })

      it('repeated ResizeObserver callbacks and re-renders keep the capacity put', () => {
        const err = vi.spyOn(console, 'error').mockImplementation(() => {})
        widths = { M2: 50, M4: 12 }
        scene5(8)
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        availW = 180
        const { rerender } = mount()
        const first = inHeader()
        for (let i = 0; i < 6; i++) {
          act(() => roCallbacks.forEach((cb) => cb()))
          rerender(<TeamDisplayProvider><TeamPanelArea /></TeamDisplayProvider>)
          expect(inHeader()).toBe(first)
        }
        expect(err).not.toHaveBeenCalled()
      })

      it('seats added or removed re-measure without oscillating', () => {
        const err = vi.spyOn(console, 'error').mockImplementation(() => {})
        widths = { M2: 50 }
        availW = 165
        const seed = (n: number) => seedScene({
          members: Array.from({ length: n }, (_, i) => [`M${i}`, `m${i}-tm`] as [string, string]),
          tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }], activeTabId: 'lead',
        })
        seed(8)
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        mount()
        expect(inHeader()).toBe(3)
        act(() => seed(2)) // the wide seat is gone
        expect(inHeader()).toBe(3)
        act(() => seed(8))
        expect(inHeader()).toBe(3)
        expect(err).not.toHaveBeenCalled()
      })

      it('an enlarged panel uses the same widths', () => {
        widths = { M2: 50 }
        scene5(8)
        act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
        act(() => useTeamUiStore.getState().setPanelExpanded(true))
        availW = 130 // 38*2 + 2 + 5 = 83; + 2 + 38 = 123 -> 3 fit, 4th (50) does not
        mount()
        expect(inHeader()).toBe(3)
      })
    })

    it('an enlarged panel with room keeps everyone in the header row', () => {
      scene5(8)
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
      act(() => useTeamUiStore.getState().setPanelExpanded(true))
      availW = 900
      cellW = 38
      mount()
      expect(inHeader()).toBe(9)
      expect(screen.queryByTestId('team-panel-more')).toBeNull()
    })
  })

  it('a cell is a fixed height inside the header row', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    expect(screen.getAllByTestId('team-panel-cell')[0].style.height).toBe(`${CELL_H}px`)
  })
})

describe('one-line mode', () => {
  it('one-line cells + name, wraps', () => {
    scene()
    readings((t) => { t.lead.model = 'claude-opus-5-5'; t.lead.context = { used_percentage: 30, window: 1, at: 1 } })
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    const cells = screen.getAllByTestId('team-panel-cell')
    expect(cells.map((c) => c.getAttribute('data-session-id'))).toEqual(['L', 'A', 'B', 'C'])
    expect(screen.getByTestId('team-panel-name').textContent).toBeTruthy()
    expect(screen.getByTestId('team-panel-cells').className).not.toContain('flex-wrap') // the first row never wraps
    expect(within(cells[0]).getByTestId('seat-icon').getAttribute('data-subagents')).toBe('false')
    expect(within(cells[0]).getByTestId('context-ring')).toBeTruthy()
    expect(within(cells[0]).getByTestId('model-icon-opus')).toBeTruthy()
    expect(within(cells[1]).getByTestId('model-icon-unknown')).toBeTruthy() // no reading: the dashed "?"
  })

  it('a cell click opens the seat', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    fireEvent.click(screen.getAllByTestId('team-panel-cell')[2]) // B
    expect(useTabStore.getState().activeTabId).toBe('mb')
  })
})

describe('resize and enlarge', () => {
  const drag = (from: number, to: number, commit = true) => {
    fireEvent.mouseDown(screen.getByTestId('resize-hit'), { clientX: from })
    fireEvent.mouseMove(document, { clientX: to })
    if (commit) fireEvent.mouseUp(document)
  }

  it('resize is draft-then-commit: the store changes on mouseup only', () => {
    scene()
    mount()
    drag(500, 420, false) // the left edge moves left 80px: 312 -> 392
    expect(area().style.width).toBe('392px')
    expect(useTeamUiStore.getState().panel.width).toBe(312)
    fireEvent.mouseUp(document)
    expect(useTeamUiStore.getState().panel.width).toBe(392)
    expect(area().style.width).toBe('392px')
  })

  it('a drag cut short by the panel going away leaves no draft and commits nothing later', () => {
    scene()
    const { unmount } = mount()
    drag(500, 420, false)
    unmount()
    fireEvent.mouseMove(document, { clientX: 100 })
    fireEvent.mouseUp(document)
    expect(useTeamUiStore.getState().panel.width).toBe(312)
    expect(document.body.style.cursor).toBe('')
    mount()
    expect(area().style.width).toBe('312px')
  })

  it('enlarging in the middle of a drag drops the draft: nothing commits and the width is unchanged', () => {
    scene()
    mount()
    drag(500, 420, false)
    act(() => { useTeamUiStore.getState().setPanelExpanded(true) })
    fireEvent.mouseMove(document, { clientX: 100 })
    fireEvent.mouseUp(document)
    act(() => { useTeamUiStore.getState().setPanelExpanded(false) })
    expect(useTeamUiStore.getState().panel.width).toBe(312)
    expect(area().style.width).toBe('312px')
  })

  it('resize clamps to 280-720', () => {
    scene()
    mount()
    drag(500, -2000)
    expect(useTeamUiStore.getState().panel.width).toBe(720)
    drag(500, 5000)
    expect(useTeamUiStore.getState().panel.width).toBe(280)
  })

  it('a click on the edge without moving writes nothing', () => {
    scene()
    mount()
    const before = useTeamUiStore.getState()
    fireEvent.mouseDown(screen.getByTestId('resize-hit'), { clientX: 500 })
    fireEvent.mouseUp(document)
    expect(useTeamUiStore.getState().panel).toBe(before.panel)
  })

  it('both modes take the area\'s width', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelWidth(600))
    mount()
    expect(area().style.width).toBe('600px')
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    expect(area().style.width).toBe('600px')
  })

  it('enlarge toggles expanded and keeps the width apart', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelWidth(500))
    mount()
    expect(area().getAttribute('data-expanded')).toBe('false')
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().panel).toEqual({ width: 500, expanded: true })
    expect(area().getAttribute('data-expanded')).toBe('true')
    expect(screen.queryByTestId('resize-hit')).toBeNull() // nothing to resize while it fills the area
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().panel).toEqual({ width: 500, expanded: false })
    expect(area().style.width).toBe('500px')
  })

  it('expanded works from one-line mode too', () => {
    scene()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
    mount()
    fireEvent.click(screen.getByTestId('team-panel-expand'))
    expect(useTeamUiStore.getState().panel.expanded).toBe(true)
  })
})

describe('the panel state lives in the store (tab-hosted rule)', () => {
  it('mode remembered per team and survives a reload', () => {
    scene()
    const first = mount()
    fireEvent.click(screen.getByTestId('team-panel-to-line'))
    act(() => { useTeamUiStore.getState().setPanelWidth(555); useTeamUiStore.getState().setPanelExpanded(true) })
    first.unmount()
    // A reload: the in-memory state is gone, the persisted copy is read back.
    const saved = localStorage.getItem(STORAGE)!
    act(() => useTeamUiStore.setState({ panelMode: {}, panel: { width: 312, expanded: false } }))
    localStorage.setItem(STORAGE, saved)
    act(() => { void useTeamUiStore.persist.rehydrate() })
    mount()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(area().getAttribute('data-expanded')).toBe('true')
    act(() => useTeamUiStore.getState().setPanelExpanded(false))
    expect(area().style.width).toBe('555px')
  })

  it('switch tabs away and back keeps the mode, the width and the view (real TabContent)', () => {
    registerModule({ id: 'term', name: 'Term', panes: [{ kind: 'tmux-session', component: () => <div data-testid="pane" /> }] })
    function Shell() {
      const tabs = useTabStore((s) => s.tabs)
      const order = useTabStore((s) => s.tabOrder)
      const activeId = useTabStore((s) => s.activeTabId)
      return (
        <TeamDisplayProvider>
          <div className="relative">
            <TabContent activeTab={activeId ? tabs[activeId] ?? null : null} allTabs={order.map((id) => tabs[id]).filter(Boolean)} />
            <TeamPanelArea />
          </div>
        </TeamDisplayProvider>
      )
    }
    scene()
    act(() => { useTeamUiStore.getState().setPanelMode(KEY, 'line'); useTeamUiStore.getState().setPanelWidth(480) })
    render(<Shell />)
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    setActive('x')
    expect(screen.queryByTestId('team-panel')).toBeNull()
    setActive('mb')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(area().style.width).toBe('480px')
    setActive('lead')
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
  })
})
