import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { HistoryPage } from './HistoryPage'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'
import type { Pane } from '../types/tab'

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

  it('renders browse records in reverse chronological order', () => {
    const content1: PaneContent = { kind: 'dashboard' }
    const content2: PaneContent = { kind: 'session', hostId: 'test-host', sessionCode: 'dev001', mode: 'terminal', cachedName: '', tmuxInstance: '' }
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
