import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { HistoryPage } from './HistoryPage'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'
import type { Pane } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'

const makePaneProps = () => {
  const pane: Pane = { id: 'hp-1', content: { kind: 'history' } }
  return { pane, isActive: true }
}

beforeEach(() => {
  cleanup()
  useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('HistoryPage', () => {
  it('renders "No browsing history yet" when empty', () => {
    render(<HistoryPage {...makePaneProps()} />)
    expect(screen.getByText('No browsing history yet')).toBeTruthy()
  })

  it('root fills its mount with h-full (not flex-1) so the overflow-y-auto list is scrollable', () => {
    // Same root cause as the new-tab start screen: the history pane mounts
    // under TabContent's position:absolute (block) wrapper, so a `flex-1` root
    // collapses to content height and its own `overflow-y-auto` never gets a
    // bounded height to scroll within. Must claim height via `h-full`.
    const { container } = render(<HistoryPage {...makePaneProps()} />)
    const root = container.firstChild as HTMLElement
    expect(root.className).toContain('h-full')
    expect(root.className).not.toContain('flex-1')
    expect(root.className).toContain('overflow-y-auto')
  })

  it('renders browse records in reverse chronological order', () => {
    const content1: PaneContent = { kind: 'dashboard' }
    const content2: PaneContent = { kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
    useHistoryStore.setState({
      browseHistory: [
        { tabId: 't1', paneContent: content1, visitedAt: 1000 },
        { tabId: 't2', paneContent: content2, visitedAt: 2000 },
      ],
    })
    render(<HistoryPage {...makePaneProps()} />)
    const buttons = screen.getAllByRole('button')
    expect(buttons).toHaveLength(2)
    // Newest first (reverse order)
    expect(buttons[0].textContent).toContain('session')
    expect(buttons[1].textContent).toContain('dashboard')
  })

  it('shows "Open" for records whose tab still exists', () => {
    const tab = createTab({ kind: 'dashboard' })
    useTabStore.setState({
      tabs: { [tab.id]: tab },
      tabOrder: [tab.id],
      activeTabId: tab.id,
    })
    useHistoryStore.setState({
      browseHistory: [
        { tabId: tab.id, paneContent: { kind: 'dashboard' }, visitedAt: 1000 },
      ],
    })
    render(<HistoryPage {...makePaneProps()} />)
    expect(screen.getByText('Open')).toBeTruthy()
  })

  it('shows "Closed" for records whose tab no longer exists', () => {
    useHistoryStore.setState({
      browseHistory: [
        { tabId: 'gone-tab', paneContent: { kind: 'dashboard' }, visitedAt: 1000 },
      ],
    })
    render(<HistoryPage {...makePaneProps()} />)
    expect(screen.getByText('Closed')).toBeTruthy()
  })
})

// Host ownership H2d-3 (review fix) — a history record of a closed tab would CREATE a tab: its host hidden in this
// workbench (checked at click, live store) → the Hosts page on that host, no tab. An open tab is still focused (rule 4).
describe('HistoryPage — a hidden host (H2d-3)', () => {
  const tmux: PaneContent = { kind: 'tmux-session', hostId: 'h2', sessionCode: 'c1', mode: 'terminal', cachedName: 'x', tmuxInstance: 'default' }

  beforeEach(() => {
    useHostStore.setState({
      hosts: {
        h1: { id: 'h1', name: 'mlab', ip: '1', port: 7860, order: 0 },
        h2: { id: 'h2', name: 'air', ip: '2', port: 7860, order: 1 },
      },
      hostOrder: ['h1', 'h2'],
      activeHostId: 'h1',
    })
  })

  const kinds = () => Object.values(useTabStore.getState().tabs).map((t) => t.layout.type === 'leaf' ? t.layout.pane.content.kind : 'split')

  it('a closed record on a hidden host → the Hosts page on that host; no tab of the record is created', () => {
    useShownHostsStore.setState({ ids: ['h1'] })
    useHistoryStore.setState({ browseHistory: [{ tabId: 'gone', paneContent: tmux, visitedAt: 1 }] })
    render(<HistoryPage {...makePaneProps()} />)
    fireEvent.click(screen.getByText('Closed'))
    expect(kinds()).toEqual(['hosts'])
    expect(useHostStore.getState().activeHostId).toBe('h2')
  })

  it('checks at click, not at render: hidden after the page rendered → still no tab', () => {
    useShownHostsStore.setState({ ids: ['h1', 'h2'] })
    useHistoryStore.setState({ browseHistory: [{ tabId: 'gone', paneContent: tmux, visitedAt: 1 }] })
    render(<HistoryPage {...makePaneProps()} />)
    useShownHostsStore.setState({ ids: ['h1'] })
    fireEvent.click(screen.getByText('Closed'))
    expect(kinds()).toEqual(['hosts'])
  })

  it('a closed record on a shown host → creates its tab', () => {
    useShownHostsStore.setState({ ids: ['h2'] })
    useHistoryStore.setState({ browseHistory: [{ tabId: 'gone', paneContent: tmux, visitedAt: 1 }] })
    render(<HistoryPage {...makePaneProps()} />)
    fireEvent.click(screen.getByText('Closed'))
    expect(kinds()).toEqual(['tmux-session'])
  })

  it('an OPEN tab on a hidden host is still focused (rule 4: hiding never changes existing tabs)', () => {
    useShownHostsStore.setState({ ids: [] })
    const tab = createTab(tmux)
    const other = createTab({ kind: 'dashboard' })
    useTabStore.setState({ tabs: { [tab.id]: tab, [other.id]: other }, tabOrder: [tab.id, other.id], activeTabId: other.id })
    useHistoryStore.setState({ browseHistory: [{ tabId: tab.id, paneContent: tmux, visitedAt: 1 }] })
    render(<HistoryPage {...makePaneProps()} />)
    fireEvent.click(screen.getByText('Open'))
    expect(useTabStore.getState().activeTabId).toBe(tab.id)
    expect(Object.keys(useTabStore.getState().tabs)).toHaveLength(2)
  })
})
