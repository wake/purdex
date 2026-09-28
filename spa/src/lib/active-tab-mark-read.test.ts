import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { useAgentStore } from '../stores/useAgentStore'
import { useHostStore } from '../stores/useHostStore'
import { createTab } from '../types/tab'
import type { Tab } from '../types/tab'
import { compositeKey } from './composite-key'
import { startActiveTabMarkRead } from './active-tab-mark-read'

const execTab: Tab = {
  id: 'tx', pinned: false, locked: false, createdAt: 0,
  layout: { type: 'leaf', pane: { id: 'px', content: { kind: 'execution', executionId: 'e1', host: 'h1' } } },
}

let stop: () => void = () => {}

beforeEach(() => {
  useHostStore.setState({ hostOrder: ['h1'] })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useAgentStore.setState({ unread: {} })
})
afterEach(() => stop())

describe('startActiveTabMarkRead', () => {
  it('activating exec tab marks read', () => {
    const ck = compositeKey('h1', 'exec-e1')
    useAgentStore.setState({ unread: { [ck]: true } })
    stop = startActiveTabMarkRead()
    useTabStore.setState({ tabs: { tx: execTab }, activeTabId: 'tx' })
    expect(useAgentStore.getState().unread[ck]).toBeUndefined()
  })

  it('activating a tmux tab marks read (unchanged)', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'h1', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    const ck = compositeKey('h1', 'dev')
    useAgentStore.setState({ unread: { [ck]: true } })
    stop = startActiveTabMarkRead()
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    expect(useAgentStore.getState().unread[ck]).toBeUndefined()
  })

  it('a tab-store change that keeps the same active tab does not mark read again', () => {
    const ck = compositeKey('h1', 'exec-e1')
    useTabStore.setState({ tabs: { tx: execTab }, activeTabId: 'tx' })
    stop = startActiveTabMarkRead()
    useAgentStore.setState({ unread: { [ck]: true } })
    useTabStore.setState({ tabOrder: ['tx'] })
    expect(useAgentStore.getState().unread[ck]).toBe(true)
  })
})
