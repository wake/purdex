import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, render, screen, fireEvent, within } from '@testing-library/react'
import { TitleBar, REPLAN_CONFIRM_LOCK_MS } from './TitleBar'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { usePaneFocusStore } from '../stores/usePaneFocusStore'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useUnattendedStore } from '../stores/useUnattendedStore'
import { useI18nStore } from '../stores/useI18nStore'
import { compositeKey } from '../lib/composite-key'
import { collectLeaves } from '../lib/pane-tree'
import { createTab } from '../types/tab'
import type { PaneContent, PaneLayout, Tab } from '../types/tab'

describe('TitleBar', () => {
  it('renders the title text', () => {
    render(<TitleBar title="Purdex — purdex2" />)
    expect(screen.getByText('Purdex — purdex2')).toBeDefined()
  })

  it('renders layout pattern buttons', () => {
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('layout-buttons')).toBeDefined()
  })

  it('layout pattern buttons are disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    // Only the 3 layout pattern buttons (CollapseButton lives in the dedicated
    // sidebar-toggle slot on the left).
    expect(buttons).toHaveLength(3)
    for (let i = 0; i < 3; i++) {
      expect(buttons[i]).toHaveProperty('disabled', true)
    }
  })

  it('renders with correct height', () => {
    const { container } = render(<TitleBar title="test" />)
    const bar = container.firstElementChild as HTMLElement
    // Height 36 aligns the drawn bar center with the macOS traffic-light
    // center (y=12 + 6 = 18 ↔ 36/2 = 18).
    expect(bar.getAttribute('style')).toContain('height: 36px')
  })

  it('renders the sidebar-toggle slot containing a collapse button', () => {
    render(<TitleBar title="test" />)
    const slot = screen.getByTestId('sidebar-toggle')
    expect(slot.querySelectorAll('button')).toHaveLength(1)
  })

  it('calls applyLayout when layout button is clicked', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id, visitHistory: [] })

    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    expect(buttons[0]).toHaveProperty('disabled', false)

    // Click "Split horizontal" (second layout pattern button = index 1)
    fireEvent.click(buttons[1])
    const updated = useTabStore.getState().tabs[tab.id]
    expect(updated.layout.type).toBe('split')
  })

  it('keeps layout pattern buttons disabled when no active tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button')
    for (let i = 0; i < 3; i++) {
      expect(buttons[i]).toHaveProperty('disabled', true)
    }
  })

  it('title span uses max-width instead of fixed padding to prevent button overlap', () => {
    render(<TitleBar title="A very long title that could overlap with buttons" />)
    const span = screen.getByText('A very long title that could overlap with buttons')
    expect(span.className).toContain('max-w-')
    expect(span.className).not.toContain('px-20')
  })

  it('all enabled buttons have cursor-pointer class', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.setState({ tabs: { [tab.id]: tab }, tabOrder: [tab.id], activeTabId: tab.id, visitHistory: [] })
    render(<TitleBar title="test" />)
    const buttons = screen.getByTestId('layout-buttons').querySelectorAll('button:not(:disabled)')
    expect(buttons.length).toBeGreaterThan(0)
    for (const btn of buttons) {
      expect(btn.className).toContain('cursor-pointer')
    }
  })

  // The TitleBar sits flush with the window's top edge; without an offset the
  // buttons optically collide with the traffic-light row. Shift both button
  // clusters down 2.5px so they sit on the content-side of the bar instead.
  it('sidebar-toggle cluster is shifted down 2.5px', () => {
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('sidebar-toggle').className).toMatch(/translate-y-\[2\.5px\]/)
  })

  it('layout-buttons cluster is shifted down 2.5px', () => {
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('layout-buttons').className).toMatch(/translate-y-\[2\.5px\]/)
  })
})

// ── Layout buttons: "change this tab to this layout" (shell cleanup spec §10, rules D.1 / D.1a) ──
describe('TitleBar layout buttons', () => {
  const HOST = 'h1'
  const leafOf = (id: string, content: PaneContent): PaneLayout => ({ type: 'leaf', pane: { id, content } })
  const splitOf = (dir: 'h' | 'v', ...children: PaneLayout[]): PaneLayout => ({
    type: 'split', id: `s-${children.length}-${dir}`, direction: dir, children, sizes: children.map(() => 100 / children.length),
  })
  const terminal = (code: string): PaneContent => ({
    kind: 'tmux-session', hostId: HOST, sessionCode: code, mode: 'terminal', cachedName: `term-${code}`, tmuxInstance: '',
  })
  const editor: PaneContent = { kind: 'editor', source: { type: 'inapp' }, filePath: '/src/notes.md' }
  const blank: PaneContent = { kind: 'new-tab' }

  /** Claude Code detected in a tmux session, the way the agent event handler records it. */
  const setAgent = (code: string) =>
    useAgentStore.setState((s) => ({ agentTypes: { ...s.agentTypes, [compositeKey(HOST, code)]: 'cc' } }))

  const TAB = 'tab-1'
  const showTab = (layout: PaneLayout) => {
    const tab: Tab = { id: TAB, pinned: false, locked: false, createdAt: 0, layout }
    useTabStore.setState({ tabs: { [TAB]: tab }, tabOrder: [TAB], activeTabId: TAB, visitHistory: [] })
  }
  const layoutNow = () => useTabStore.getState().tabs[TAB].layout
  const leafIds = () => collectLeaves(layoutNow()).map((p) => p.id)
  const button = (name: string) => screen.getByTitle(name) as HTMLButtonElement
  const SINGLE = 'Single Pane'
  const SPLIT_H = 'Split Horizontal'
  const SPLIT_V = 'Split Vertical'

  /** Let the guard window of a dialog opened by a re-plan run out. */
  const waitOutLock = () => act(() => { vi.advanceTimersByTime(REPLAN_CONFIRM_LOCK_MS) })

  beforeEach(() => {
    vi.useFakeTimers()
    useAgentStore.setState({ agentTypes: {} })
    usePaneFocusStore.setState({ recent: {} })
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('labels the three buttons through i18n', () => {
    showTab(leafOf('a', terminal('x')))
    render(<TitleBar title="t" />)
    expect(button(SINGLE)).toBeTruthy()
    expect(button(SPLIT_H)).toBeTruthy()
    expect(button(SPLIT_V)).toBeTruthy()
    expect(screen.getByTestId('layout-buttons').querySelectorAll('button')).toHaveLength(3)
  })

  it.each([
    ['single', leafOf('a', terminal('x')), SINGLE],
    ['split-h', splitOf('h', leafOf('a', terminal('x')), leafOf('b', blank)), SPLIT_H],
    ['split-v', splitOf('v', leafOf('a', terminal('x')), leafOf('b', blank)), SPLIT_V],
  ] as const)('the %s button is pressed when the tab has that layout', (_name, layout, pressed) => {
    showTab(layout)
    render(<TitleBar title="t" />)
    for (const name of [SINGLE, SPLIT_H, SPLIT_V]) {
      const btn = button(name)
      expect(btn.getAttribute('aria-pressed')).toBe(String(name === pressed))
      expect(btn.className.includes('text-accent-base bg-accent-base/10')).toBe(name === pressed)
    }
  })

  // Shell polish spec §4 (rule F): a mouse press on a layout button leaves focus on the pane. jsdom does not focus on
  // mousedown, so `fireEvent.mouseDown(...) === false` proves the button is wired to `keepFocus`; the helper itself is
  // proven in a real browser (spec §5).
  it('a mouse press on any layout button keeps focus where it was; every button stays in the tab order', () => {
    showTab(leafOf('a', terminal('x')))
    render(<TitleBar title="t" />)
    for (const name of [SINGLE, SPLIT_H, SPLIT_V]) {
      expect(fireEvent.mouseDown(button(name)), name).toBe(false)
      expect(button(name).tabIndex, name).toBeGreaterThanOrEqual(0)
    }
  })

  it('a press then a click still changes the layout', () => {
    showTab(leafOf('a', terminal('x')))
    render(<TitleBar title="t" />)
    fireEvent.mouseDown(button(SPLIT_V))
    fireEvent.click(button(SPLIT_V))
    expect(button(SPLIT_V).getAttribute('aria-pressed')).toBe('true')
    expect(leafIds()).toHaveLength(2)
  })

  it('no button is pressed for a layout no pattern describes (three panes)', () => {
    showTab(splitOf('h', leafOf('a', blank), leafOf('b', blank), leafOf('c', blank)))
    render(<TitleBar title="t" />)
    for (const name of [SINGLE, SPLIT_H, SPLIT_V]) expect(button(name).getAttribute('aria-pressed')).toBe('false')
  })

  it('the pressed state follows the layout as it changes', () => {
    showTab(leafOf('a', terminal('x')))
    render(<TitleBar title="t" />)
    fireEvent.click(button(SPLIT_V))
    expect(button(SPLIT_V).getAttribute('aria-pressed')).toBe('true')
    expect(button(SINGLE).getAttribute('aria-pressed')).toBe('false')
  })

  it('clicking the pressed button does nothing', () => {
    showTab(splitOf('h', leafOf('a', terminal('x')), leafOf('b', blank)))
    const before = layoutNow()
    render(<TitleBar title="t" />)
    fireEvent.click(button(SPLIT_H))
    expect(layoutNow()).toBe(before)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('case 1: one terminal + a blank pane → single applies at once, keeping the terminal', () => {
    showTab(splitOf('h', leafOf('blank', blank), leafOf('term', terminal('x'))))
    render(<TitleBar title="t" />)
    fireEvent.click(button(SINGLE))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(leafIds()).toEqual(['term'])
  })

  describe('case 2: exactly k agent panes → confirm', () => {
    const threePanes = () => splitOf('h', leafOf('ed', editor), leafOf('cc', terminal('cc1')), leafOf('plain', terminal('plain')))

    it('one CC terminal + an editor + a plain terminal → single asks, listing what closes', () => {
      setAgent('cc1')
      showTab(threePanes())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(screen.getByTestId('layout-apply-dialog')).toBeTruthy()
      const closing = within(screen.getByTestId('layout-apply-closing')).getAllByRole('listitem').map((li) => li.textContent)
      expect(closing).toEqual(['notes.md', 'term-plain'])
      expect(screen.getByTestId('layout-apply-dialog').textContent).toContain('keep running')
      expect(screen.getByTestId('layout-apply-editor-note')).toBeTruthy()
      expect(layoutNow().type).toBe('split')
    })

    it('cancel leaves the layout untouched', () => {
      setAgent('cc1')
      showTab(threePanes())
      const before = layoutNow()
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(screen.getByTestId('layout-apply-cancel'))
      expect(screen.queryByTestId('layout-apply-dialog')).toBeNull()
      expect(layoutNow()).toBe(before)
    })

    it('confirm keeps the CC terminal', () => {
      setAgent('cc1')
      showTab(threePanes())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(screen.queryByTestId('layout-apply-dialog')).toBeNull()
      expect(leafIds()).toEqual(['cc'])
    })

    it('one CC terminal + an editor → single keeps the CC terminal after the confirm, even when it is second', () => {
      setAgent('cc1')
      showTab(splitOf('h', leafOf('ed', editor), leafOf('cc', terminal('cc1'))))
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(leafIds()).toEqual(['cc'])
    })

    it('a pane that closes while the confirm is up: confirm plans again instead of applying the stale plan', () => {
      setAgent('cc1')
      showTab(threePanes())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      act(() => useTabStore.getState().closePane(TAB, 'plain'))
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      // Still asking, now about the editor only; nothing applied yet.
      const closing = within(screen.getByTestId('layout-apply-closing')).getAllByRole('listitem').map((li) => li.textContent)
      expect(closing).toEqual(['notes.md'])
      expect(leafIds()).toEqual(['ed', 'cc'])
      waitOutLock()
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(leafIds()).toEqual(['cc'])
    })

    it('a closing pane that shows something else by Confirm (same pane, same plan) is listed again before it closes', () => {
      setAgent('cc1')
      showTab(threePanes())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      act(() => useTabStore.getState().setPaneContent(TAB, 'ed', { ...editor, filePath: '/src/todo.md' }))
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      const closing = within(screen.getByTestId('layout-apply-closing')).getAllByRole('listitem').map((li) => li.textContent)
      expect(closing).toEqual(['todo.md', 'term-plain'])
      expect(leafIds()).toEqual(['ed', 'cc', 'plain'])
    })
  })

  describe('case 3: the keep picker', () => {
    const box = (id: string) => screen.getByTestId(`layout-keep-option-${id}`) as HTMLInputElement

    it('two CC terminals → single opens the picker with the most recently focused one ticked', () => {
      setAgent('cc-a')
      setAgent('cc-b')
      showTab(splitOf('h', leafOf('a', terminal('cc-a')), leafOf('b', terminal('cc-b'))))
      usePaneFocusStore.setState({ recent: { [TAB]: ['b', 'a'] } })
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(screen.getByTestId('layout-keep-dialog')).toBeTruthy()
      expect(screen.queryByTestId('layout-apply-dialog')).toBeNull()
      expect(box('b').checked).toBe(true)
      expect(box('a').checked).toBe(false)
    })

    it('the preselection follows the focus record', () => {
      setAgent('cc-a')
      setAgent('cc-b')
      showTab(splitOf('h', leafOf('a', terminal('cc-a')), leafOf('b', terminal('cc-b'))))
      usePaneFocusStore.setState({ recent: { [TAB]: ['a', 'b'] } })
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(box('a').checked).toBe(true)
      expect(box('b').checked).toBe(false)
    })

    it('confirm is disabled at the wrong count, and the result keeps the ticked pane', () => {
      setAgent('cc-a')
      setAgent('cc-b')
      showTab(splitOf('h', leafOf('a', terminal('cc-a')), leafOf('b', terminal('cc-b'))))
      usePaneFocusStore.setState({ recent: { [TAB]: ['b'] } })
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(box('b'))
      expect((screen.getByTestId('layout-keep-confirm') as HTMLButtonElement).disabled).toBe(true)
      fireEvent.click(box('a'))
      fireEvent.click(screen.getByTestId('layout-keep-confirm'))
      expect(screen.queryByTestId('layout-keep-dialog')).toBeNull()
      expect(leafIds()).toEqual(['a'])
    })

    it('three panes → a split keeps the two ticked ones, in layout order', () => {
      setAgent('cc-a')
      setAgent('cc-b')
      setAgent('cc-c')
      showTab(splitOf('v', leafOf('a', terminal('cc-a')), leafOf('b', terminal('cc-b')), leafOf('c', terminal('cc-c'))))
      usePaneFocusStore.setState({ recent: { [TAB]: ['c', 'a'] } })
      render(<TitleBar title="t" />)
      fireEvent.click(button(SPLIT_H))
      fireEvent.click(box('a'))
      fireEvent.click(box('b'))
      fireEvent.click(screen.getByTestId('layout-keep-confirm'))
      const layout = layoutNow()
      expect(layout.type === 'split' && layout.direction).toBe('h')
      expect(leafIds()).toEqual(['b', 'c'])
    })

    it('an editor + a plain terminal (no agent) → single opens the picker', () => {
      showTab(splitOf('h', leafOf('ed', editor), leafOf('plain', terminal('plain'))))
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(screen.getByTestId('layout-keep-dialog')).toBeTruthy()
    })

    it('cancel leaves the layout untouched', () => {
      showTab(splitOf('h', leafOf('ed', editor), leafOf('plain', terminal('plain'))))
      const before = layoutNow()
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(screen.getByTestId('layout-keep-cancel'))
      expect(screen.queryByTestId('layout-keep-dialog')).toBeNull()
      expect(layoutNow()).toBe(before)
    })
  })

  // Shell polish spec §4: the layout buttons leave focus on the pane (rule F), so the dialog they open takes it itself —
  // onto its panel, not a button — and gives it back to the pane when it closes. A textarea stands in for the pane.
  describe('a dialog opened from a layout button takes focus, and gives it back', () => {
    const renderWithPane = () => render(<><textarea data-testid="pane" /><TitleBar title="t" /></>)
    const pane = () => screen.getByTestId('pane')

    it.each([
      ['case 2 confirm', 'layout-apply', () => { setAgent('cc1'); showTab(splitOf('h', leafOf('ed', editor), leafOf('cc', terminal('cc1')))) }],
      ['case 3 picker', 'layout-keep', () => showTab(splitOf('h', leafOf('ed', editor), leafOf('plain', terminal('plain'))))],
    ] as const)('%s: a press and a click from the focused pane → the panel has focus; Cancel → the pane again', (_name, prefix, seed) => {
      seed()
      renderWithPane()
      pane().focus()
      expect(fireEvent.mouseDown(button(SINGLE))).toBe(false)
      fireEvent.click(button(SINGLE))
      expect(document.activeElement).toBe(screen.getByTestId(`${prefix}-panel`))
      fireEvent.click(screen.getByTestId(`${prefix}-cancel`))
      expect(screen.queryByTestId(`${prefix}-dialog`)).toBeNull()
      expect(document.activeElement).toBe(pane())
    })

    it('Escape closes it and the pane has focus again', () => {
      setAgent('cc1')
      showTab(splitOf('h', leafOf('ed', editor), leafOf('cc', terminal('cc1'))))
      renderWithPane()
      pane().focus()
      fireEvent.mouseDown(button(SINGLE))
      fireEvent.click(button(SINGLE))
      fireEvent.keyDown(document.activeElement!, { key: 'Escape' })
      expect(screen.queryByTestId('layout-apply-dialog')).toBeNull()
      expect(document.activeElement).toBe(pane())
    })
  })

  // The dialog belongs to the tab it was opened for: once another tab is shown (a shortcut, a notification, a deep
  // link), applying it would rebuild a tab the user is not looking at.
  describe('the dialog closes when its tab is no longer active', () => {
    const OTHER = 'tab-2'
    /** `TAB` (active, holding `layout`) plus a second tab with two plain terminals. */
    const showTwoTabs = (layout: PaneLayout) => {
      showTab(layout)
      const other: Tab = {
        id: OTHER, pinned: false, locked: false, createdAt: 0,
        layout: splitOf('h', leafOf('o1', terminal('o1')), leafOf('o2', terminal('o2'))),
      }
      useTabStore.setState((s) => ({ tabs: { ...s.tabs, [OTHER]: other }, tabOrder: [TAB, OTHER] }))
    }
    const otherLayout = () => useTabStore.getState().tabs[OTHER].layout

    it('switching to another tab while the picker is up closes it, and switching back does not bring it back', () => {
      showTwoTabs(splitOf('h', leafOf('ed', editor), leafOf('plain', terminal('plain'))))
      const before = layoutNow()
      const otherBefore = otherLayout()
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(screen.getByTestId('layout-keep-dialog')).toBeTruthy()

      act(() => useTabStore.getState().setActiveTab(OTHER))
      expect(screen.queryByTestId('layout-keep-dialog')).toBeNull()
      expect(screen.queryByRole('dialog')).toBeNull()

      act(() => useTabStore.getState().setActiveTab(TAB))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(layoutNow()).toBe(before)
      expect(otherLayout()).toBe(otherBefore)
    })

    it('the new tab\'s pane took focus before the dialog closed → focus stays on it, not back on the old pane', () => {
      showTwoTabs(splitOf('h', leafOf('ed', editor), leafOf('plain', terminal('plain'))))
      render(<><textarea data-testid="old-pane" /><textarea data-testid="new-pane" /><TitleBar title="t" /></>)
      screen.getByTestId('old-pane').focus()
      fireEvent.click(button(SINGLE))
      expect(document.activeElement).toBe(screen.getByTestId('layout-keep-panel'))

      const newPane = screen.getByTestId('new-pane')
      act(() => {
        newPane.focus()
        useTabStore.getState().setActiveTab(OTHER)
      })
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(document.activeElement).toBe(newPane)
    })

    it('a tab switch that lands in the same click as Confirm applies nothing', () => {
      setAgent('cc1')
      showTwoTabs(splitOf('h', leafOf('ed', editor), leafOf('cc', terminal('cc1')), leafOf('plain', terminal('plain'))))
      const before = layoutNow()
      const otherBefore = otherLayout()
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      // The switch runs ahead of React's own click handling (a capture listener on the document), so the handler still
      // sees the render from before it: only a live read of the store can tell.
      const switchTab = () => useTabStore.getState().setActiveTab(OTHER)
      document.addEventListener('click', switchTab, { capture: true, once: true })
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      document.removeEventListener('click', switchTab, { capture: true })

      expect(screen.queryByRole('dialog')).toBeNull()
      expect(layoutNow()).toBe(before)
      expect(otherLayout()).toBe(otherBefore)
    })
  })

  // Agent detection, exit and transfer change only the agent store, never the panes, so Confirm plans again with the
  // live agent set: what the dialog promised to keep must still be what rule D.1a keeps.
  describe('Confirm plans again with the live agent set', () => {
    const box = (id: string) => screen.getByTestId(`layout-keep-option-${id}`) as HTMLInputElement
    const closingNow = (prefix: string) =>
      within(screen.getByTestId(`${prefix}-closing`)).getAllByRole('listitem').map((li) => li.textContent)
    /** Replace the whole agent set: these sessions have an agent, every other one has none. */
    const agentsNow = (...codes: string[]) =>
      act(() => useAgentStore.setState({ agentTypes: Object.fromEntries(codes.map((c) => [compositeKey(HOST, c), 'cc'])) }))
    const threeTerminals = () => splitOf('h', leafOf('a', terminal('a')), leafOf('b', terminal('b')), leafOf('c', terminal('c')))
    const twoTerminals = () => splitOf('h', leafOf('a', terminal('a')), leafOf('b', terminal('b')))

    it('case 2: the agent moved from A to B under the confirm → Confirm does not close B; the dialog now keeps B', () => {
      setAgent('a')
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(closingNow('layout-apply')).toEqual(['term-b', 'term-c'])

      agentsNow('b')
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(leafIds()).toEqual(['a', 'b', 'c'])
      expect(closingNow('layout-apply')).toEqual(['term-a', 'term-c'])

      waitOutLock()
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(leafIds()).toEqual(['b'])
    })

    it('case 2: the only agent exited under the confirm → Confirm opens the picker instead of closing panes', () => {
      setAgent('a')
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))

      agentsNow()
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(leafIds()).toEqual(['a', 'b', 'c'])
      expect(screen.queryByTestId('layout-apply-dialog')).toBeNull()
      expect(screen.getByTestId('layout-keep-dialog')).toBeTruthy()
    })

    it('case 3 → 2: a plain terminal became the only agent under the picker → Confirm asks to keep the agent, ignoring the stale tick', () => {
      showTab(twoTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(box('b'))
      expect(box('b').checked).toBe(true)

      agentsNow('a')
      fireEvent.click(screen.getByTestId('layout-keep-confirm'))
      expect(leafIds()).toEqual(['a', 'b'])
      expect(screen.queryByTestId('layout-keep-dialog')).toBeNull()
      expect(closingNow('layout-apply')).toEqual(['term-b'])

      waitOutLock()
      fireEvent.click(screen.getByTestId('layout-apply-confirm'))
      expect(leafIds()).toEqual(['a'])
    })

    it('still case 3 with the same candidates → neither the agent change nor a new preselection matters; the user\'s ticks apply', () => {
      showTab(twoTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      fireEvent.click(box('b'))

      agentsNow('a', 'b')
      // A fresh plan would now preselect B instead of A; the picker is still the same question.
      act(() => usePaneFocusStore.setState({ recent: { [TAB]: ['b'] } }))
      fireEvent.click(screen.getByTestId('layout-keep-confirm'))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(leafIds()).toEqual(['b'])
    })
  })

  // A double-click on Confirm sends two clicks. When the first finds the plan changed, the fresh dialog opens in the
  // same place, so the second would land on its Confirm and apply a plan the user never saw (closing panes they were
  // never shown). A dialog opened by a re-plan keeps its Confirm inert for a short guard window.
  describe('a dialog opened by a re-plan ignores the rest of the click that opened it', () => {
    const box = (id: string) => screen.getByTestId(`layout-keep-option-${id}`) as HTMLInputElement
    const confirmButton = (prefix: string) => screen.getByTestId(`${prefix}-confirm`) as HTMLButtonElement
    const closingNow = (prefix: string) =>
      within(screen.getByTestId(`${prefix}-closing`)).getAllByRole('listitem').map((li) => li.textContent)
    const agentsNow = (...codes: string[]) =>
      act(() => useAgentStore.setState({ agentTypes: Object.fromEntries(codes.map((c) => [compositeKey(HOST, c), 'cc'])) }))
    const threeTerminals = () => splitOf('h', leafOf('a', terminal('a')), leafOf('b', terminal('b')), leafOf('c', terminal('c')))
    /**
     * Pending timers, once jsdom's own zero-delay ones have run: every `focus()` queues its `selectionchange` event on a
     * `setTimeout(0)`, and the dialog focuses its panel each time it opens (shell polish spec §4). The lock timer is
     * `REPLAN_CONFIRM_LOCK_MS` long, so a zero-length tick never fires it.
     */
    const pendingTimers = () => {
      act(() => { vi.advanceTimersByTime(0) })
      return vi.getTimerCount()
    }
    /** Two clicks at the same place: each finds whatever Confirm is there by then. */
    const clickTwice = (prefix: string) => {
      fireEvent.click(confirmButton(prefix))
      fireEvent.click(confirmButton(prefix))
    }

    it('confirm → confirm: the second click applies nothing; once the window is over a click applies the new plan', () => {
      setAgent('a')
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))

      agentsNow('b')
      clickTwice('layout-apply')
      expect(leafIds()).toEqual(['a', 'b', 'c'])
      expect(closingNow('layout-apply')).toEqual(['term-a', 'term-c'])
      expect(confirmButton('layout-apply').disabled).toBe(true)

      act(() => { vi.advanceTimersByTime(REPLAN_CONFIRM_LOCK_MS - 1) })
      fireEvent.click(confirmButton('layout-apply'))
      expect(leafIds()).toEqual(['a', 'b', 'c'])

      act(() => { vi.advanceTimersByTime(1) })
      expect(confirmButton('layout-apply').disabled).toBe(false)
      fireEvent.click(confirmButton('layout-apply'))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(leafIds()).toEqual(['b'])
    })

    it('a native double-click (two clicks, then dblclick) applies nothing either', () => {
      setAgent('a')
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))

      agentsNow('b')
      clickTwice('layout-apply')
      fireEvent.dblClick(confirmButton('layout-apply'))
      expect(leafIds()).toEqual(['a', 'b', 'c'])
      expect(screen.getByTestId('layout-apply-dialog')).toBeTruthy()
    })

    it('picker → picker: a pane closed under the picker; the second click applies nothing; later the new picker applies', () => {
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(box('a').checked).toBe(true)

      act(() => useTabStore.getState().closePane(TAB, 'c'))
      clickTwice('layout-keep')
      expect(leafIds()).toEqual(['a', 'b'])
      expect(screen.getByTestId('layout-keep-dialog')).toBeTruthy()
      expect(screen.queryByTestId('layout-keep-option-c')).toBeNull()
      // The fresh picker has its full count ticked: only the guard window holds Confirm.
      expect(box('a').checked).toBe(true)
      expect(confirmButton('layout-keep').disabled).toBe(true)

      waitOutLock()
      expect(confirmButton('layout-keep').disabled).toBe(false)
      fireEvent.click(confirmButton('layout-keep'))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(leafIds()).toEqual(['a'])
    })

    it('a dialog opened by a button press is not locked: an immediate Confirm applies', () => {
      setAgent('a')
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(confirmButton('layout-apply').disabled).toBe(false)
      fireEvent.click(confirmButton('layout-apply'))
      expect(leafIds()).toEqual(['a'])
    })

    it('a picker opened by a button press is not locked either', () => {
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      expect(confirmButton('layout-keep').disabled).toBe(false)
      fireEvent.click(confirmButton('layout-keep'))
      expect(leafIds()).toEqual(['a'])
    })

    it('cancelling a locked dialog drops its timer; the next button press opens an unlocked one', () => {
      setAgent('a')
      showTab(threeTerminals())
      render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      agentsNow('b')
      const othersTimers = pendingTimers()
      fireEvent.click(confirmButton('layout-apply'))
      expect(confirmButton('layout-apply').disabled).toBe(true)
      expect(pendingTimers()).toBe(othersTimers + 1)

      fireEvent.click(screen.getByTestId('layout-apply-cancel'))
      expect(pendingTimers()).toBe(othersTimers)
      fireEvent.click(button(SINGLE))
      expect(confirmButton('layout-apply').disabled).toBe(false)
      fireEvent.click(confirmButton('layout-apply'))
      expect(leafIds()).toEqual(['b'])
    })

    it('unmounting with a locked dialog up drops its timer', () => {
      setAgent('a')
      showTab(threeTerminals())
      const { unmount } = render(<TitleBar title="t" />)
      fireEvent.click(button(SINGLE))
      agentsNow('b')
      const othersTimers = pendingTimers()
      fireEvent.click(confirmButton('layout-apply'))
      expect(pendingTimers()).toBe(othersTimers + 1)
      unmount()
      expect(pendingTimers()).toBe(othersTimers)
    })
  })
})

// ── 無人值守模式 (unattended spec D-U23-5, D-U23-6; plan PU-2b): the toggle's slot, and its state across a remount ──
describe('TitleBar unattended button', () => {
  const H = 'h1'
  beforeEach(() => {
    useHostStore.setState({
      hosts: { [H]: { id: H, name: 'mlab', ip: '1', port: 1, token: 't', order: 0 } },
      hostOrder: [H], activeHostId: H, runtime: { [H]: { status: 'connected' } },
    })
    useShownHostsStore.setState({ ids: [H] })
    useUnattendedStore.setState({ byHost: { [H]: { support: 'yes', state: { on: true, since: 1, changed_at: 1 } } } })
  })
  afterEach(() => {
    useHostStore.getState().reset()
    useUnattendedStore.getState().reset()
  })

  it('the unattended button sits in a no-drag wrapper before the layout buttons', () => {
    render(<TitleBar title="test" />)
    const wrapper = screen.getByTestId('unattended-buttons')
    expect((wrapper.style as unknown as { WebkitAppRegion?: string }).WebkitAppRegion).toBe('no-drag')
    expect(within(wrapper).getByTestId('unattended-toggle')).toBeDefined()
    const layout = screen.getByTestId('layout-buttons')
    expect(wrapper.nextElementSibling).toBe(layout)
    expect(wrapper.compareDocumentPosition(layout) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('TitleBar remounted keeps the button\'s state', () => {
    const first = render(<TitleBar title="test" />)
    expect(screen.getByTestId('unattended-toggle')).toHaveAttribute('data-state', 'on')
    first.unmount()
    render(<TitleBar title="test" />)
    expect(screen.getByTestId('unattended-toggle')).toHaveAttribute('data-state', 'on')
    expect(screen.getByTestId('unattended-toggle')).toHaveTextContent(useI18nStore.getState().t('unattended.on_label'))
  })
})
