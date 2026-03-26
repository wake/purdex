import { describe, it, expect, beforeEach } from 'vitest'
import { useHistoryStore } from './useHistoryStore'
import { createTab } from '../types/tab'
import type { PaneContent } from '../types/tab'

function makeContent(kind: PaneContent['kind'] = 'dashboard'): PaneContent {
  switch (kind) {
    case 'session': return { kind: 'session', sessionCode: 'dev001', mode: 'terminal' }
    case 'settings': return { kind: 'settings', scope: 'global' }
    case 'new-tab': return { kind: 'new-tab' }
    case 'dashboard': return { kind: 'dashboard' }
    case 'history': return { kind: 'history' }
    case 'browser': return { kind: 'browser', url: 'https://example.com' }
  }
}

describe('useHistoryStore', () => {
  beforeEach(() => {
    useHistoryStore.setState({ browseHistory: [], closedTabs: [] })
  })

  // 1. recordVisit adds a BrowseRecord
  it('recordVisit adds a BrowseRecord', () => {
    const content = makeContent('dashboard')
    useHistoryStore.getState().recordVisit('tab-1', content)
    const { browseHistory } = useHistoryStore.getState()
    expect(browseHistory).toHaveLength(1)
    expect(browseHistory[0].tabId).toBe('tab-1')
    expect(browseHistory[0].paneContent).toEqual(content)
    expect(typeof browseHistory[0].visitedAt).toBe('number')
  })

  // 2. recordVisit respects 500 limit (oldest dropped)
  it('recordVisit respects 500 limit — oldest entries dropped', () => {
    const content = makeContent('dashboard')
    // Pre-fill with 500 entries (tabId: 'old-0' ... 'old-499')
    const initial = Array.from({ length: 500 }, (_, i) => ({
      tabId: `old-${i}`,
      paneContent: content,
      visitedAt: i,
    }))
    useHistoryStore.setState({ browseHistory: initial })

    // Add one more
    useHistoryStore.getState().recordVisit('new-tab', content)

    const { browseHistory } = useHistoryStore.getState()
    expect(browseHistory).toHaveLength(500)
    // The oldest (old-0) should be dropped
    expect(browseHistory.find((r) => r.tabId === 'old-0')).toBeUndefined()
    // The newest should be present
    expect(browseHistory[browseHistory.length - 1].tabId).toBe('new-tab')
  })

  // 3. recordClose adds a ClosedTabRecord
  it('recordClose adds a ClosedTabRecord', () => {
    const tab = createTab({ kind: 'dashboard' })
    useHistoryStore.getState().recordClose(tab, 'ws-1')
    const { closedTabs } = useHistoryStore.getState()
    expect(closedTabs).toHaveLength(1)
    expect(closedTabs[0].tab).toEqual(tab)
    expect(closedTabs[0].fromWorkspaceId).toBe('ws-1')
    expect(typeof closedTabs[0].closedAt).toBe('number')
    expect(closedTabs[0].reopenedAt).toBeUndefined()
  })

  // 4. recordClose respects 100 limit
  it('recordClose respects 100 limit — oldest entries dropped', () => {
    const tab = createTab({ kind: 'dashboard' })
    // Pre-fill with 100 entries
    const initial = Array.from({ length: 100 }, (_, i) => ({
      tab: createTab({ kind: 'dashboard' }),
      closedAt: i,
      fromWorkspaceId: `old-ws-${i}`,
    }))
    useHistoryStore.setState({ closedTabs: initial })

    useHistoryStore.getState().recordClose(tab)

    const { closedTabs } = useHistoryStore.getState()
    expect(closedTabs).toHaveLength(100)
    // The oldest (fromWorkspaceId: 'old-ws-0') should be dropped
    expect(closedTabs.find((r) => r.fromWorkspaceId === 'old-ws-0')).toBeUndefined()
    // The newest should be the last
    expect(closedTabs[closedTabs.length - 1].tab).toEqual(tab)
  })

  // 5. reopenLast returns most recent unreopened tab
  it('reopenLast returns most recent unreopened tab', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'history' })
    useHistoryStore.setState({
      closedTabs: [
        { tab: tab1, closedAt: 1000 },
        { tab: tab2, closedAt: 2000 },
      ],
    })
    const result = useHistoryStore.getState().reopenLast()
    expect(result).toEqual(tab2)
  })

  // 6. reopenLast sets reopenedAt
  it('reopenLast sets reopenedAt on the returned record', () => {
    const tab = createTab({ kind: 'dashboard' })
    useHistoryStore.setState({
      closedTabs: [{ tab, closedAt: 1000 }],
    })
    useHistoryStore.getState().reopenLast()
    const { closedTabs } = useHistoryStore.getState()
    expect(typeof closedTabs[0].reopenedAt).toBe('number')
    expect(closedTabs[0].reopenedAt).toBeGreaterThan(0)
  })

  // 7. reopenLast returns null when no unreopened records
  it('reopenLast returns null when no unreopened records exist', () => {
    useHistoryStore.setState({ closedTabs: [] })
    const result = useHistoryStore.getState().reopenLast()
    expect(result).toBeNull()
  })

  // 8. reopenLast skips already-reopened records
  it('reopenLast skips already-reopened records', () => {
    const tab1 = createTab({ kind: 'dashboard' })
    const tab2 = createTab({ kind: 'history' })
    useHistoryStore.setState({
      closedTabs: [
        { tab: tab1, closedAt: 1000 },
        { tab: tab2, closedAt: 2000, reopenedAt: 9999 }, // already reopened
      ],
    })
    const result = useHistoryStore.getState().reopenLast()
    // tab2 is already reopened, so tab1 should be returned
    expect(result).toEqual(tab1)
  })

  // 9. clearBrowseHistory empties array
  it('clearBrowseHistory empties browseHistory', () => {
    const content = makeContent('dashboard')
    useHistoryStore.getState().recordVisit('tab-1', content)
    useHistoryStore.getState().recordVisit('tab-2', content)
    useHistoryStore.getState().clearBrowseHistory()
    expect(useHistoryStore.getState().browseHistory).toHaveLength(0)
  })

  // 10. clearClosedTabs empties array
  it('clearClosedTabs empties closedTabs', () => {
    const tab = createTab({ kind: 'dashboard' })
    useHistoryStore.getState().recordClose(tab)
    useHistoryStore.getState().clearClosedTabs()
    expect(useHistoryStore.getState().closedTabs).toHaveLength(0)
  })
})
