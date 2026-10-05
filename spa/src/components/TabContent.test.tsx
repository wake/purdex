import { describe, it, expect, beforeEach } from 'vitest'
import { useLayoutEffect, useRef, type ComponentType } from 'react'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import { TabContent } from './TabContent'
import { registerModule, clearModuleRegistry, type PaneRendererProps } from '../lib/module-registry'
import { useActivationFocus } from '../hooks/useActivationFocus'
import { useUISettingsStore } from '../stores/useUISettingsStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { createTab } from '../types/tab'
import type { Tab, Pane } from '../types/tab'

const MockSessionRenderer = ({ pane }: { pane: Pane }) => (
  <div data-testid="session-renderer">Session: {pane.content.kind === 'tmux-session' ? pane.content.sessionCode : ''}</div>
)
const MockDashboardRenderer = () => (
  <div data-testid="dashboard-renderer">Dashboard</div>
)

beforeEach(() => {
  cleanup()
  clearModuleRegistry()
  registerModule({ id: 'session', name: 'Session', panes: [{ kind: 'tmux-session', component: MockSessionRenderer }] })
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component: MockDashboardRenderer }] })
  useUISettingsStore.setState({ keepAliveCount: 0 })
  useShownHostsStore.setState({ ids: ['test-host'] }) // shown: a hidden host's pane is gated (H2d-4)
})

const sessionTab: Tab = {
  ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }),
  id: 't1',
}

const dashboardTab: Tab = {
  ...createTab({ kind: 'dashboard' }),
  id: 't3',
}

describe('TabContent', () => {
  it('renders registered session renderer', () => {
    render(<TabContent activeTab={sessionTab} allTabs={[sessionTab]} />)
    expect(screen.getByTestId('session-renderer')).toBeTruthy()
  })

  it('renders registered dashboard renderer', () => {
    render(<TabContent activeTab={dashboardTab} allTabs={[dashboardTab]} />)
    expect(screen.getByTestId('dashboard-renderer')).toBeTruthy()
  })

  it('renders empty state when no active tab', () => {
    render(<TabContent activeTab={null} allTabs={[]} />)
    // i18n (`tab.empty_state`), not the hard-coded Chinese string it used to be
    expect(screen.getByText('Select or create a tab to get started')).toBeTruthy()
    expect(screen.queryByText(/選擇或建立/)).toBeNull()
  })

  it('uses visibility:hidden for inactive keep-alive tabs (not left:-9999em)', () => {
    // keepAliveCount must be > 0 so inactive tabs remain in the alive pool
    useUISettingsStore.setState({ keepAliveCount: 3 })
    const { container, rerender } = render(
      <TabContent activeTab={dashboardTab} allTabs={[sessionTab, dashboardTab]} />,
    )
    rerender(
      <TabContent activeTab={sessionTab} allTabs={[sessionTab, dashboardTab]} />,
    )
    const wrappers = container.querySelectorAll('[class*="absolute"]')
    const inactiveWrapper = Array.from(wrappers).find((w) => w.querySelector('[data-testid="dashboard-renderer"]'))
    expect(inactiveWrapper).not.toBeNull()
    expect((inactiveWrapper as HTMLElement).style.visibility).toBe('hidden')
    expect((inactiveWrapper as HTMLElement).style.left).not.toBe('-9999em')
    // Active tab should be visible
    const activeWrapper = Array.from(wrappers).find((w) => w.querySelector('[data-testid="session-renderer"]'))
    expect((activeWrapper as HTMLElement).style.visibility).toBe('visible')
  })

  it('sets inert on non-active keep-alive tabs', () => {
    // First render with dashboard active to put it in the alive pool
    const { container, rerender } = render(
      <TabContent activeTab={dashboardTab} allTabs={[sessionTab, dashboardTab]} />,
    )
    // Switch to session tab — dashboard stays in pool but becomes inactive
    rerender(
      <TabContent activeTab={sessionTab} allTabs={[sessionTab, dashboardTab]} />,
    )
    const wrappers = container.querySelectorAll('[class*="absolute"]')
    // Active tab (session) should NOT have inert
    const activeWrapper = Array.from(wrappers).find((w) => w.querySelector('[data-testid="session-renderer"]'))
    expect(activeWrapper).not.toBeNull()
    expect(activeWrapper?.getAttribute('inert')).toBeNull()
    // Inactive tab (dashboard) should have inert
    const inactiveWrapper = Array.from(wrappers).find((w) => w.querySelector('[data-testid="dashboard-renderer"]'))
    expect(inactiveWrapper).not.toBeNull()
    // jsdom may not reflect `inert` as an HTML attribute; check the React prop via data attribute fallback
    expect(inactiveWrapper?.getAttribute('inert')).not.toBeNull()
  })
})

// --- Shell polish spec §4: a tab that goes inactive lets go of focus and the text selection -------------------------
//
// WebKit keeps routing key presses into a focused field whose tab went `visibility: hidden` + `inert`, even though
// `activeElement` reports body; blurring it and clearing the selection stops that. jsdom implements neither `inert`
// nor the browser's focus fixup, so here a field in a tab that goes inactive stays `document.activeElement` unless
// TabContent blurs it — which is exactly what these tests observe.

const leafTab = (id: string, paneId: string): Tab => ({
  id, pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: paneId, content: { kind: 'dashboard' } } },
})
const tabA = leafTab('tA', 'pA')
const tabB = leafTab('tB', 'pB')
const BOTH = [tabA, tabB]

/** Some text and a field; takes no focus of its own. */
function FieldPane({ pane }: PaneRendererProps) {
  return (
    <div>
      <p data-testid={`text-${pane.id}`}>text of {pane.id}</p>
      <textarea data-testid={`field-${pane.id}`} />
    </div>
  )
}

/** Takes focus at activation the way the terminal and the reply box do (P5: `useActivationFocus` with a frame). */
function ActivatingPane({ pane, isActive, isFocusTarget = false }: PaneRendererProps) {
  const ref = useRef<HTMLTextAreaElement>(null)
  useActivationFocus(isActive, isFocusTarget, () => ref.current?.focus(), { raf: true })
  return <textarea data-testid={`field-${pane.id}`} ref={ref} />
}

/** Takes focus in a layout effect when shown: earlier in the same commit than the old tab letting go. */
function EagerPane({ pane, isActive }: PaneRendererProps) {
  const ref = useRef<HTMLTextAreaElement>(null)
  useLayoutEffect(() => {
    if (isActive) ref.current?.focus()
  }, [isActive])
  return <textarea data-testid={`field-${pane.id}`} ref={ref} />
}

function registerDashboardPane(component: ComponentType<PaneRendererProps>) {
  clearModuleRegistry()
  registerModule({ id: 'dashboard', name: 'Dashboard', panes: [{ kind: 'dashboard', component }] })
}

/** Selects from `anchor`'s first text node (offset 0) to `focus`'s first text node (offset 2). */
function select(anchor: HTMLElement, focus: HTMLElement = anchor): Selection {
  const sel = document.getSelection()!
  sel.removeAllRanges()
  sel.setBaseAndExtent(anchor.firstChild!, 0, focus.firstChild!, 2)
  return sel
}

/** Stand-ins for things outside the tabs (the sidebar, a dialog) that may hold focus or a selection. */
function Shell({ active }: { active: Tab }) {
  return (
    <>
      <input data-testid="outside-field" />
      <p data-testid="outside-text">outside the tabs</p>
      <TabContent activeTab={active} allTabs={BOTH} />
    </>
  )
}

describe('TabContent — a tab that goes inactive lets go of focus and the selection (shell polish §4)', () => {
  beforeEach(() => {
    registerDashboardPane(FieldPane)
    document.getSelection()?.removeAllRanges()
    ;(document.activeElement as HTMLElement | null)?.blur?.()
  })

  it('blurs the focused element inside the tab that goes inactive', () => {
    const { rerender } = render(<TabContent activeTab={tabA} allTabs={BOTH} />)
    const field = screen.getByTestId('field-pA')
    field.focus()
    expect(document.activeElement).toBe(field)

    rerender(<TabContent activeTab={tabB} allTabs={BOTH} />)
    expect(document.activeElement).not.toBe(field)
    expect(document.activeElement).toBe(document.body)
  })

  it('clears a text selection inside the tab that goes inactive', () => {
    const { rerender } = render(<TabContent activeTab={tabA} allTabs={BOTH} />)
    const sel = select(screen.getByTestId('text-pA'))
    expect(sel.rangeCount).toBe(1)

    rerender(<TabContent activeTab={tabB} allTabs={BOTH} />)
    expect(sel.rangeCount).toBe(0)
  })

  it('clears a selection that only ends inside the tab (anchor outside, focus inside)', () => {
    const { rerender } = render(<Shell active={tabA} />)
    const sel = select(screen.getByTestId('outside-text'), screen.getByTestId('text-pA'))
    expect(sel.rangeCount).toBe(1)

    rerender(<Shell active={tabB} />)
    expect(sel.rangeCount).toBe(0)
  })

  it('leaves focus and a selection outside that tab alone', () => {
    const { rerender } = render(<Shell active={tabA} />)
    const outsideField = screen.getByTestId('outside-field')
    outsideField.focus()
    const outsideText = screen.getByTestId('outside-text')
    const sel = select(outsideText)

    rerender(<Shell active={tabB} />)
    expect(document.activeElement).toBe(outsideField)
    expect(sel.rangeCount).toBe(1)
    expect(sel.anchorNode).toBe(outsideText.firstChild)
  })

  it('keeps a focus the newly active tab took earlier in the same commit', () => {
    registerDashboardPane(EagerPane)
    const { rerender } = render(<TabContent activeTab={tabA} allTabs={BOTH} />)
    expect(document.activeElement).toBe(screen.getByTestId('field-pA'))

    rerender(<TabContent activeTab={tabB} allTabs={BOTH} />)
    expect(document.activeElement).toBe(screen.getByTestId('field-pB'))
  })

  it('the newly active tab still takes its activation focus (P5: useActivationFocus, next frame)', async () => {
    registerDashboardPane(ActivatingPane)
    const { rerender } = render(<TabContent activeTab={tabA} allTabs={BOTH} />)
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('field-pA')))

    rerender(<TabContent activeTab={tabB} allTabs={BOTH} />)
    await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('field-pB')))
  })

  it('does nothing while the tab stays active', () => {
    const { rerender } = render(<TabContent activeTab={tabA} allTabs={BOTH} />)
    const field = screen.getByTestId('field-pA')
    field.focus()
    const sel = select(screen.getByTestId('text-pA'))

    rerender(<TabContent activeTab={tabA} allTabs={[...BOTH]} />)
    expect(document.activeElement).toBe(field)
    expect(sel.rangeCount).toBe(1)
  })
})
