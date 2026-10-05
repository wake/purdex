import { beforeEach, describe, expect, it } from 'vitest'
import type { ExecutionContent, ExecutionViewMode } from '../../types/tab'
import { createTab } from '../../types/tab'
import { collectLeaves, findPane, getPrimaryPane } from '../pane-tree'
import { useTabStore } from '../../stores/useTabStore'
import { useHostStore } from '../../stores/useHostStore'
import { setExecutionPaneMode, viewModeOf, withViewMode } from './view-mode'

describe('viewModeOf', () => {
  it('reads an absent mode as room', () => {
    expect(viewModeOf({ kind: 'execution', executionId: 'e1' })).toBe('room')
    expect(viewModeOf({ kind: 'execution', executionId: 'e1', mode: 'chat' })).toBe('chat')
  })

  it('reads an unknown mode as room', () => {
    // an older or newer client may have written a mode this build does not know
    const content = { kind: 'execution', executionId: 'e1', mode: 'terminal' as unknown as ExecutionViewMode } satisfies ExecutionContent
    expect(viewModeOf(content)).toBe('room')
  })
})

describe('withViewMode', () => {
  it('keeps from and host when setting the mode', () => {
    const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
    const content: ExecutionContent = { kind: 'execution', executionId: 'e1', host: 'h1', from }
    const next = withViewMode(content, 'chat')
    expect(next).toEqual({ kind: 'execution', executionId: 'e1', host: 'h1', from, mode: 'chat' })
    expect(content.mode).toBeUndefined() // the input is not mutated
    expect(withViewMode(next, 'room')).toEqual({ kind: 'execution', executionId: 'e1', host: 'h1', from, mode: 'room' })
  })
})

// Shell cleanup P6 (T6.5): one guarded write, shared by the pane wrapper's
// view menu and the status bar's mode buttons.
describe('setExecutionPaneMode', () => {
  const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
  let tabId: string
  let primaryId: string
  let secondId: string
  const contentOf = (paneId: string) => findPane(useTabStore.getState().tabs[tabId].layout, paneId)!.content

  beforeEach(() => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    const tab = createTab({ kind: 'execution', executionId: 'e1', host: 'h1', from })
    useTabStore.getState().addTab(tab)
    tabId = tab.id
    primaryId = getPrimaryPane(tab.layout).id
    useTabStore.getState().splitPane(tabId, primaryId, 'h', { kind: 'execution', executionId: 'e2', host: 'h1' })
    secondId = collectLeaves(useTabStore.getState().tabs[tabId].layout).find((p) => p.id !== primaryId)!.id
  })

  it('writes the mode on the named pane only, keeping the rest of its content', () => {
    setExecutionPaneMode(tabId, secondId, { executionId: 'e2', host: 'h1' }, 'chat')
    expect(contentOf(secondId)).toEqual({ kind: 'execution', executionId: 'e2', host: 'h1', mode: 'chat' })
    expect(contentOf(primaryId)).toEqual({ kind: 'execution', executionId: 'e1', host: 'h1', from })
    setExecutionPaneMode(tabId, secondId, { executionId: 'e2', host: 'h1' }, 'room')
    expect(contentOf(secondId)).toEqual({ kind: 'execution', executionId: 'e2', host: 'h1', mode: 'room' })
  })

  it('reads the content the store holds now, so a `from` rewrite that landed since is kept', () => {
    const from2 = { ...from, tmuxInstance: 'inst-2' }
    useTabStore.getState().setPaneContent(tabId, primaryId, { kind: 'execution', executionId: 'e1', host: 'h1', from: from2 })
    setExecutionPaneMode(tabId, primaryId, { executionId: 'e1', host: 'h1' }, 'chat')
    expect(contentOf(primaryId)).toEqual({ kind: 'execution', executionId: 'e1', host: 'h1', from: from2, mode: 'chat' })
  })

  // P6 review A2: the execution store keys by host + execution id, so the same id on another host is another worker.
  it('is a no-op when the pane now shows the same execution id on another host', () => {
    useTabStore.getState().setPaneContent(tabId, primaryId, { kind: 'execution', executionId: 'e1', host: 'h2', from })
    const before = useTabStore.getState().tabs
    setExecutionPaneMode(tabId, primaryId, { executionId: 'e1', host: 'h1' }, 'chat')
    expect(contentOf(primaryId)).toEqual({ kind: 'execution', executionId: 'e1', host: 'h2', from })
    expect(useTabStore.getState().tabs).toBe(before)
  })

  it('resolves an absent host like the pane does (the first host)', () => {
    const prev = useHostStore.getState().hostOrder
    useHostStore.setState({ hostOrder: ['h1', 'h2'] })
    try {
      useTabStore.getState().setPaneContent(tabId, secondId, { kind: 'execution', executionId: 'e2' })
      setExecutionPaneMode(tabId, secondId, { executionId: 'e2', host: 'h2' }, 'chat') // the pane resolves to h1
      expect(contentOf(secondId)).toEqual({ kind: 'execution', executionId: 'e2' })
      setExecutionPaneMode(tabId, secondId, { executionId: 'e2', host: 'h1' }, 'chat')
      expect(contentOf(secondId)).toEqual({ kind: 'execution', executionId: 'e2', mode: 'chat' })
    } finally {
      useHostStore.setState({ hostOrder: prev })
    }
  })

  it('is a no-op when the pane no longer shows that execution, or is not an execution at all', () => {
    setExecutionPaneMode(tabId, secondId, { executionId: 'e1', host: 'h1' }, 'chat') // the pane shows e2
    expect(contentOf(secondId)).toEqual({ kind: 'execution', executionId: 'e2', host: 'h1' })
    useTabStore.getState().setPaneContent(tabId, secondId, { kind: 'new-tab' })
    const before = useTabStore.getState().tabs
    setExecutionPaneMode(tabId, secondId, { executionId: 'e2', host: 'h1' }, 'chat')
    expect(contentOf(secondId)).toEqual({ kind: 'new-tab' })
    expect(useTabStore.getState().tabs).toBe(before)
  })

  it('is a no-op for a tab or pane that is gone', () => {
    const before = useTabStore.getState().tabs
    setExecutionPaneMode('no-such-tab', primaryId, { executionId: 'e1', host: 'h1' }, 'chat')
    setExecutionPaneMode(tabId, 'no-such-pane', { executionId: 'e1', host: 'h1' }, 'chat')
    expect(useTabStore.getState().tabs).toBe(before)
  })
})
