// spa/src/components/team/TeamTitleBar.test.tsx — the panel area's title-bar state (WA-2a′, team spec §4.4 Round 3): the
// strip that replaces the window title, the Notebook button, the moves between title bar and pane, the memory per team,
// and that a tab switch between a title-bar team and a pane team keeps each one (real TabContent). Real stores and provider.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { TabContent } from '../TabContent'
import { TitleBar } from '../TitleBar'
import { TeamDisplayProvider } from './TeamDisplayProvider'
import { TeamPanelArea } from './TeamPanelArea'
import { HOST, KEY, member, resetTeamStores, seedScene, sess, tabOn } from '../../lib/team/__tests__/team-fixture'
import { clearModuleRegistry, registerModule } from '../../lib/module-registry'
import { useTabStore } from '../../stores/useTabStore'
import { useTeamRosterStore } from '../../stores/useTeamRosterStore'
import { useTeamUiStore } from '../../stores/useTeamUiStore'
import { useShownHostsStore } from '../../stores/useShownHostsStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { HEADER_GAP, PLUS_CHIP_W } from './panel-layout'
import { TITLE_LEFT_RESERVE, TITLE_MIN_W, TITLE_RIGHT_EDGE, titleBarLayout } from './title-bar-layout'

vi.mock('./TeamSeatIcon', () => ({
  TeamSeatIcon: () => <span data-testid="seat-icon" />,
  TeamSeatHostBadge: () => <span data-testid="seat-host" />,
}))

const TITLE = 'Purdex window title'
const scene = (activeTabId: string | null = 'lead') => seedScene({
  members: [['A', 'a-tm'], ['B', 'b-tm'], ['C', 'c-tm']],
  tabs: [['lead', 'lead-tm'], ['ma', 'a-tm'], ['mb', 'b-tm'], ['x', null]],
  workspaces: [{ id: 'w1', tabs: ['lead', 'ma', 'mb', 'x'] }],
  activeTabId,
})
const mode = (key = KEY) => useTeamUiStore.getState().panelMode[key] ?? 'full'
const setActive = (id: string | null) => act(() => { useTabStore.setState({ activeTabId: id }) })
const noDrag = (el: HTMLElement) => (el.style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion === 'no-drag'
const strip = () => screen.queryByTestId('team-title-strip')
/** The cells the strip really shows (the hidden measuring row holds the ones that went into +N). */
const shownCells = () => within(screen.getByTestId('team-strip-cells')).queryAllByTestId('team-panel-cell')
const button = () => screen.queryByTestId('team-notebook-button')
const press = () => fireEvent.click(screen.getByTestId('team-notebook-button'))
const mountBar = () => render(<TeamDisplayProvider><TitleBar title={TITLE} /><TeamPanelArea /></TeamDisplayProvider>)

beforeEach(() => {
  cleanup()
  localStorage.clear()
  resetTeamStores()
  useShownHostsStore.setState({ ids: [HOST] })
  clearModuleRegistry()
})
afterEach(() => { cleanup(); vi.restoreAllMocks(); act(() => useI18nStore.getState().setLocale('en')) })

describe('the Notebook button', () => {
  it('is absent when the active tab has no team (and with no active tab)', () => {
    scene('x')
    mountBar()
    expect(button()).toBeNull()
    setActive(null)
    expect(button()).toBeNull()
    setActive('ma')
    expect(button()).not.toBeNull()
  })

  it('sits left of the unattended button, with the same button style, in a no-drag wrapper', () => {
    scene()
    mountBar()
    const wrap = screen.getByTestId('team-notebook-wrap')
    expect(noDrag(wrap)).toBe(true)
    expect(wrap.compareDocumentPosition(screen.getByTestId('unattended-buttons')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(button()!.className).toContain('p-1 rounded') // title-bar-styles BUTTON
  })

  it('is dim while the area is in the pane and lit while it is in the title bar', () => {
    scene()
    mountBar()
    expect(button()!.getAttribute('aria-pressed')).toBe('false')
    expect(button()!.className).toContain('text-text-secondary')
    press()
    expect(button()!.getAttribute('aria-pressed')).toBe('true')
    expect(button()!.className).toContain('text-accent-base')
  })

  it('moves the area between the title bar and the pane state it left (line, full or max)', () => {
    scene()
    mountBar()
    for (const left of ['full', 'line', 'max'] as const) {
      act(() => useTeamUiStore.getState().setPanelMode(KEY, left))
      press()
      expect(mode()).toBe('titlebar')
      expect(screen.queryByTestId('team-panel-area')).toBeNull()
      press()
      expect(mode()).toBe(left)
      expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe(left)
    }
  })
})

describe('the strip', () => {
  it('appears only while the area is in the title bar, and the window title stays throughout (round 5)', () => {
    scene()
    mountBar()
    expect(screen.queryByText(TITLE)).not.toBeNull()
    expect(strip()).toBeNull()
    press()
    expect(strip()).not.toBeNull()
    expect(screen.queryByText(TITLE)).not.toBeNull()
    press()
    expect(strip()).toBeNull()
    expect(screen.queryByText(TITLE)).not.toBeNull()
  })

  it('sits at the right, right before the buttons (Notebook first), while the title stays in the centred overlay', () => {
    scene()
    mountBar()
    press()
    const s = strip()!
    const title = screen.getByText(TITLE)
    const overlay = title.parentElement as HTMLElement
    // the strip is not in the title's overlay; the overlay is the absolute, centred one
    expect(overlay.contains(s)).toBe(false)
    expect(overlay.className).toContain('absolute')
    expect(overlay.className).toContain('justify-center')
    expect(title.className).toContain('truncate') // the title is what gives way first
    // DOM order: window title overlay, then the strip, then the right-hand buttons (Notebook, unattended, layout)
    const after = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
    expect(after(overlay, s)).toBe(true)
    expect(after(s, screen.getByTestId('team-notebook-wrap'))).toBe(true)
    expect(after(s, screen.getByTestId('layout-buttons'))).toBe(true)
    const right = screen.getByTestId('title-bar-right')
    expect(s.nextElementSibling).toBe(right) // nothing between the strip and the button group
    expect(right.firstElementChild).toBe(screen.getByTestId('team-notebook-wrap'))
    expect(noDrag(s)).toBe(false) // the box itself drags the window; only its content is no-drag
  })

  it('draws the team name and one cell per person', () => {
    scene()
    mountBar()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    const s = strip()!
    expect(within(s).getByTestId('team-strip-name').textContent).toBeTruthy()
    expect(within(s).getAllByTestId('team-panel-cell').map((c) => c.getAttribute('data-session-id'))).toEqual(['L', 'A', 'B', 'C'])
    expect(within(s).queryByTestId('team-strip-more')).toBeNull() // nothing measured: everyone is drawn
  })

  it('the team name brings the area back to the pane state it left', () => {
    scene()
    mountBar()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'max'))
    press()
    fireEvent.click(screen.getByTestId('team-strip-name'))
    expect(mode()).toBe('max')
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('true')
  })

  it('a cell opens its seat (R3 + R10) and leaves the area where it is', () => {
    scene()
    mountBar()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    const cell = within(strip()!).getAllByTestId('team-panel-cell').find((c) => c.getAttribute('data-session-id') === 'B')!
    fireEvent.click(cell)
    expect(useTabStore.getState().activeTabId).toBe('mb')
    expect(mode()).toBe('titlebar')
  })

  it('the divider is a flex child of the cells row with no margin: the row gap leaves 4px on each side of it', () => {
    scene()
    mountBar()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    const row = screen.getByTestId('team-strip-cells')
    expect(row.style.columnGap).toBe('4px')
    const sep = row.querySelector<HTMLElement>('[data-testid="cell-sep"]')!
    expect(sep.parentElement).toBe(row)
    expect(sep.style.marginInline).toBe('')
    expect(sep.previousElementSibling?.querySelector('[data-session-id="L"]')).not.toBeNull()
    expect(sep.nextElementSibling?.querySelector('[data-session-id="A"]')).not.toBeNull()
  })

  it('only the strip\'s content is no-drag: the box and its blank space keep dragging the window', () => {
    scene()
    mountBar()
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
    const s = strip()!
    expect(noDrag(s)).toBe(false)
    expect(s.className).toContain('pointer-events-none') // blank space is the title bar's
    expect(noDrag(screen.getByTestId('team-strip-name'))).toBe(true)
    expect(noDrag(screen.getByTestId('team-strip-cells'))).toBe(true)
    expect(screen.getByTestId('team-strip-name').className).toContain('pointer-events-auto')
    expect(screen.getByTestId('team-strip-cells').className).toContain('pointer-events-auto')
    // the overlay that holds the strip is not no-drag either
    expect(noDrag(s.parentElement as HTMLElement)).toBe(false)
  })

  describe('when not everyone fits', () => {
    let avail = 0
    beforeEach(() => {
      const id = (el: Element) => el.getAttribute('data-testid')
      vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) { return id(this) === 'team-panel-cell' ? 43 : 0 })
      vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) { return id(this) === 'team-title-strip' ? avail : 0 })
    })

    it('shows as many cells as fit, then 「+N」 for the rest', () => {
      scene()
      avail = 190 // less the name (84 + 2): 104 -> two cells (43 + 8 + 9 + 43 = 103); with the chip kept free (32): one
      mountBar()
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
      const s = strip()!
      expect(shownCells()).toHaveLength(1)
      const more = within(s).getByTestId('team-strip-more')
      expect(more.textContent).toBe('+3')
      expect(noDrag(more)).toBe(true)
      expect(PLUS_CHIP_W).toBeGreaterThan(0)
    })

    it('a single seat that does not fit at all goes into 「+N」: no cell, and the strip stays inside its allotted width', () => {
      seedScene({ members: [], tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }], activeTabId: 'lead', teamName: 'A very long team name indeed' })
      avail = 60 // narrower than the team name's worst case alone
      mountBar()
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
      const s = strip()!
      expect(shownCells()).toHaveLength(0)
      expect(within(s).getByTestId('team-strip-more').textContent).toBe('+1')
      // structure: capped to the allotted width, clipped, and the name is the part that gives way
      expect(s.className).toContain('min-w-0')
      expect(s.className).toContain('overflow-hidden')
      expect(screen.getByTestId('team-strip-name').className).not.toContain('flex-shrink-0')
      expect(screen.getByTestId('team-strip-name').className).toContain('min-w-0')
      expect(screen.getByTestId('team-strip-more').className).toContain('flex-shrink-0')
    })

    it.each([['zh-TW', '還有 3 位，點開面板'], ['en', '3 more — open the panel']])('「+N」 title and aria-label carry the real number (%s)', (locale, text) => {
      scene()
      avail = 190
      act(() => useI18nStore.getState().setLocale(locale))
      mountBar()
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
      const more = screen.getByTestId('team-strip-more')
      expect(more.getAttribute('title')).toBe(text)
      expect(more.getAttribute('aria-label')).toBe(text)
      expect(more.getAttribute('title')).not.toContain('{')
    })

    it.each([[20, 4, null], [70, 3, '+1']] as const)('a seat that joins is counted at its own width, hidden or not (%ipx -> %i shown)', (width, shown, chip) => {
      vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
        return this.getAttribute('data-testid') === 'team-panel-cell' ? (this.getAttribute('data-session-id') === 'W' ? width : 43) : 0
      })
      seedScene({ members: [['A', 'a-tm'], ['B', 'b-tm']], tabs: [['lead', 'lead-tm']], workspaces: [{ id: 'w1', tabs: ['lead'] }], activeTabId: 'lead' })
      avail = 286 // less the name: 200 -> three cells take 3 x 43 + 2 x 8 + 9 = 154, a 20px fourth makes 182; a 70px one would make 232
      mountBar()
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
      expect(shownCells()).toHaveLength(3)
      act(() => { // a fourth seat joins
        const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
        roster[0].members.push(member('W', 9, 'w-tm'))
        useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
        useSessionStore.setState({ sessions: { [HOST]: [...useSessionStore.getState().sessions[HOST], { code: 'code-w-tm', name: 'w-tm', mode: 'terminal', cwd: '~' }] as never } })
      })
      expect(shownCells()).toHaveLength(shown)
      expect(screen.queryByTestId('team-strip-more')?.textContent ?? null).toBe(chip)
      // a seat past the capacity is still drawn, hidden and out of reach, so its width counts
      if (chip) {
        const measure = screen.getByTestId('team-strip-measure')
        expect(within(measure).getByTestId('team-panel-cell').getAttribute('data-session-id')).toBe('W')
        expect(measure.getAttribute('aria-hidden')).toBe('true')
        expect(measure.style.visibility).toBe('hidden')
      }
    })

    it('「+N」 brings the area back to the pane', () => {
      scene()
      avail = 190
      mountBar()
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'line'))
      press()
      fireEvent.click(screen.getByTestId('team-strip-more'))
      expect(mode()).toBe('line')
    })

    it('draws everyone, with no 「+N」, when they all fit', () => {
      scene()
      avail = 400
      mountBar()
      act(() => useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'))
      expect(shownCells()).toHaveLength(4)
      expect(screen.queryByTestId('team-strip-more')).toBeNull()
      expect(screen.queryByTestId('team-strip-measure')).toBeNull()
    })
  })
})

describe('the state lives in the store (tab-hosted rule)', () => {
  const T2 = 't2'
  const KEY2 = `${HOST}\u0000${T2}`
  /** A second team (lead L2 + member D) on the same host, each with a tab. */
  function addSecondTeam() {
    const roster = structuredClone(useTeamRosterStore.getState().byHost[HOST])
    roster.push({ id: T2, host_id: 'daemon', created_at: 2, team_name: 'Second', team_label: '', lead: sess('L2', 'lead2-tm'), members: [member('D', 1, 'd-tm')] })
    useTeamRosterStore.setState({ byHost: { [HOST]: roster } })
    const sessions = useSessionStore.getState().sessions[HOST]
    useSessionStore.setState({ sessions: { [HOST]: [...sessions, ...['lead2-tm', 'd-tm'].map((name) => ({ code: `code-${name}`, name, mode: 'terminal', cwd: '~' }))] as never } })
    const extra = [tabOn('lead2', 'lead2-tm'), tabOn('md', 'd-tm')]
    const tabs = useTabStore.getState()
    useTabStore.setState({ tabs: { ...tabs.tabs, ...Object.fromEntries(extra.map((t) => [t.id, t])) }, tabOrder: [...tabs.tabOrder, 'lead2', 'md'] })
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'w1', tabs: ['lead', 'ma', 'mb', 'x', 'lead2', 'md'], activeTabId: null, moduleConfig: {} }], activeWorkspaceId: 'w1' })
  }

  function Shell() {
    const tabs = useTabStore((s) => s.tabs)
    const order = useTabStore((s) => s.tabOrder)
    const activeId = useTabStore((s) => s.activeTabId)
    return (
      <TeamDisplayProvider>
        <TitleBar title={TITLE} />
        <div className="relative">
          <TabContent activeTab={activeId ? tabs[activeId] ?? null : null} allTabs={order.map((id) => tabs[id]).filter(Boolean)} />
          <TeamPanelArea />
        </div>
      </TeamDisplayProvider>
    )
  }

  beforeEach(() => registerModule({ id: 'term', name: 'Term', panes: [{ kind: 'tmux-session', component: () => <div data-testid="pane" /> }] }))

  it('switching between a title-bar team, a pane team and a team-less tab keeps each one\'s state', () => {
    scene('lead')
    addSecondTeam()
    act(() => { useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'); useTeamUiStore.getState().setPanelMode(KEY2, 'line') })
    render(<Shell />)
    expect(strip()).not.toBeNull()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
    expect(button()!.getAttribute('aria-pressed')).toBe('true')

    setActive('lead2') // the pane team
    expect(strip()).toBeNull()
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(button()!.getAttribute('aria-pressed')).toBe('false')
    expect(screen.queryByText(TITLE)).not.toBeNull()

    setActive('x') // no team: no button, no strip, no panel
    expect(button()).toBeNull()
    expect(strip()).toBeNull()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()

    setActive('mb') // back to the title-bar team (via a member tab)
    expect(strip()).not.toBeNull()
    expect(screen.queryByTestId('team-panel-area')).toBeNull()
    setActive('md') // and to the pane team again
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('line')
    expect(mode(KEY)).toBe('titlebar')
    expect(mode(KEY2)).toBe('line')
  })

  it('a team put back in the pane while another stays in the title bar does not move the other', () => {
    scene('lead')
    addSecondTeam()
    act(() => { useTeamUiStore.getState().setPanelMode(KEY, 'titlebar'); useTeamUiStore.getState().setPanelMode(KEY2, 'titlebar') })
    render(<Shell />)
    press() // lead's team back to the pane (full)
    expect(screen.getByTestId('team-panel').getAttribute('data-mode')).toBe('full')
    setActive('lead2')
    expect(strip()).not.toBeNull()
    expect(mode(KEY2)).toBe('titlebar')
  })

  it('survives a reload', () => {
    scene('lead')
    const first = render(<Shell />)
    act(() => useTeamUiStore.getState().setPanelMode(KEY, 'max'))
    press() // -> title bar, remembering max
    first.unmount()
    const saved = localStorage.getItem('purdex-team-ui')!
    act(() => useTeamUiStore.setState({ panelMode: {}, panelLast: {} }))
    localStorage.setItem('purdex-team-ui', saved)
    act(() => { void useTeamUiStore.persist.rehydrate() })
    render(<Shell />)
    expect(strip()).not.toBeNull()
    press()
    expect(screen.getByTestId('team-panel-area').getAttribute('data-expanded')).toBe('true')
  })
})

describe('room for the strip and the centred title (round 5)', () => {
  it('titleBarLayout: the title keeps at least TITLE_MIN_W centred; the strip gets what is left of the right half', () => {
    const wide = titleBarLayout({ barW: 1200, btnsW: 120, stripW: 300 })
    // half of the bar, less half the title's minimum, the bar's edge padding, the buttons and a gap
    expect(wide.room).toBe(600 - TITLE_MIN_W / 2 - TITLE_RIGHT_EDGE - 120 - HEADER_GAP)
    // the title's side padding covers the strip and the buttons, so the (centred) title never reaches them
    expect(wide.titlePad).toBe(300 + 120 + TITLE_RIGHT_EDGE + HEADER_GAP)
    // a short strip does not push the padding below what the left side needs
    expect(titleBarLayout({ barW: 1200, btnsW: 20, stripW: 10 }).titlePad).toBe(TITLE_LEFT_RESERVE)
    // never a negative room
    expect(titleBarLayout({ barW: 300, btnsW: 120, stripW: 50 }).room).toBe(0)
    // with a narrow window the room shrinks first, so the title keeps its minimum before the strip fills
    const narrow = titleBarLayout({ barW: 700, btnsW: 120, stripW: 9999 })
    expect(narrow.room).toBeLessThan(wide.room)
    expect(700 - 2 * narrow.titlePad).toBeGreaterThanOrEqual(TITLE_MIN_W)
  })

  it('titleBarLayout: the title floor wins in a narrow bar, whatever the strip, the room or the buttons ask for', () => {
    for (const barW of [200, 250, 300, 350]) {
      for (const [btnsW, stripW] of [[120, 300], [120, 0], [20, 10], [600, 9999], [400, 50]]) {
        const { room, titlePad } = titleBarLayout({ barW, btnsW, stripW })
        expect(room).toBeGreaterThanOrEqual(0)
        expect(titlePad).toBeGreaterThanOrEqual(0)
        // the centred title keeps TITLE_MIN_W (or the whole bar when the bar itself is narrower)
        expect(barW - 2 * titlePad).toBeGreaterThanOrEqual(Math.min(TITLE_MIN_W, barW))
      }
    }
    // barW=300: before the cap the padding was 112 and the title got 76px
    expect(titleBarLayout({ barW: 300, btnsW: 120, stripW: 50 }).titlePad).toBe((300 - TITLE_MIN_W) / 2)
    // room=0 (the strip is gone): the padding is still bounded
    const none = titleBarLayout({ barW: 300, btnsW: 120, stripW: 50 })
    expect(none.room).toBe(0)
    expect(300 - 2 * none.titlePad).toBe(TITLE_MIN_W)
    // an over-wide button group does not squeeze the title either
    const wideBtns = titleBarLayout({ barW: 350, btnsW: 600, stripW: 40 })
    expect(wideBtns.room).toBe(0)
    expect(350 - 2 * wideBtns.titlePad).toBeGreaterThanOrEqual(TITLE_MIN_W)
    // a bar narrower than the title floor: no negative padding
    expect(titleBarLayout({ barW: 50, btnsW: 20, stripW: 10 }).titlePad).toBe(0)
    // roomy bars are unchanged
    expect(titleBarLayout({ barW: 1200, btnsW: 20, stripW: 10 }).titlePad).toBe(TITLE_LEFT_RESERVE)
  })

  describe('in the bar', () => {
    const W = 1000
    const BTNS = 130
    const STRIP = 260
    beforeEach(() => {
      vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
        const id = this.getAttribute('data-testid')
        return id === 'title-bar' ? W : id === 'team-title-strip' ? 400 : 0
      })
      vi.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockImplementation(function (this: HTMLElement) {
        const id = this.getAttribute('data-testid')
        return id === 'title-bar-right' ? BTNS : id === 'team-strip-inner' ? STRIP : id === 'team-panel-cell' ? 43 : 0
      })
    })

    it('the title keeps its centre: padding covers the strip and the buttons, and the strip gets only the room that is left', () => {
      scene()
      mountBar()
      press()
      const title = screen.getByText(TITLE)
      const overlay = title.parentElement as HTMLElement
      const { titlePad, room } = titleBarLayout({ barW: W, btnsW: BTNS, stripW: STRIP })
      expect(overlay.style.paddingInline).toBe(`${titlePad}px`)
      expect(strip()!.style.width).toBe(`${room}px`)
      expect(title.className).not.toContain('27rem') // the strip's reserve replaces the old blanket max-width
      expect(W - 2 * titlePad).toBeGreaterThanOrEqual(TITLE_MIN_W)
    })

    it('without a strip the overlay is the old one (no padding override)', () => {
      scene()
      mountBar()
      const overlay = screen.getByText(TITLE).parentElement as HTMLElement
      expect(overlay.style.paddingInline).toBe('')
      expect(screen.getByText(TITLE).className).toContain('27rem')
    })
  })
})
