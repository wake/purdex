import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import { HistoryPage } from './HistoryPage'
import { useHistoryStore } from '../stores/useHistoryStore'
import { useTabStore } from '../stores/useTabStore'
import { openWatchArea } from '../features/workspace/lib/open-watch-area'

vi.mock('../features/workspace/lib/open-watch-area', () => ({ openWatchArea: vi.fn() }))

const props = { pane: { id: 'hp', content: { kind: 'history' as const } }, isActive: true }

beforeEach(() => {
  cleanup()
  vi.mocked(openWatchArea).mockClear()
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('HistoryPage and the watcher area', () => {
  it('a watch-host record opens the area on that host and never creates a tab', () => {
    useHistoryStore.setState({ browseHistory: [{ tabId: 'gone', paneContent: { kind: 'watch-host', poolHostId: 'h1' }, visitedAt: 1 }], closedTabs: [] })
    render(<HistoryPage {...props} />)
    fireEvent.click(screen.getAllByRole('button')[0])
    expect(openWatchArea).toHaveBeenCalledWith({ poolHost: { hostId: 'h1' } })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual([])
  })

  it('a record from before the pool (no pool host id) opens the first tab of the area', () => {
    useHistoryStore.setState({ browseHistory: [{ tabId: 'gone', paneContent: { kind: 'watch-host' }, visitedAt: 1 }], closedTabs: [] })
    render(<HistoryPage {...props} />)
    fireEvent.click(screen.getAllByRole('button')[0])
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'guardian' })
  })

  it('a guardian or events record opens the guardian target, no tab', () => {
    useHistoryStore.setState({ browseHistory: [{ tabId: 'gone', paneContent: { kind: 'watch-events' }, visitedAt: 1 }], closedTabs: [] })
    render(<HistoryPage {...props} />)
    fireEvent.click(screen.getAllByRole('button')[0])
    expect(openWatchArea).toHaveBeenCalledWith({ tab: 'guardian' })
    expect(Object.keys(useTabStore.getState().tabs)).toEqual([])
  })
})
