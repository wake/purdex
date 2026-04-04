import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from '../stores/useTabStore'
import { createTab } from '../types/tab'
import { getActiveSessionCode } from './active-session'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, activeTabId: null, tabOrder: [] })
})

describe('getActiveSessionCode', () => {
  it('returns sessionCode when active tab is a session', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'dev', mode: 'terminal', cachedName: '', tmuxInstance: '' }), id: 't1' }
    useTabStore.setState({ tabs: { t1: tab }, activeTabId: 't1' })
    expect(getActiveSessionCode()).toBe('dev')
  })

  it('returns sessionCode for stream-mode session tab', () => {
    const tab = { ...createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: 'box', mode: 'stream', cachedName: '', tmuxInstance: '' }), id: 't2' }
    useTabStore.setState({ tabs: { t2: tab }, activeTabId: 't2' })
    expect(getActiveSessionCode()).toBe('box')
  })

  it('returns null when active tab is not a session', () => {
    const tab = { ...createTab({ kind: 'settings', scope: 'global' }), id: 't3' }
    useTabStore.setState({ tabs: { t3: tab }, activeTabId: 't3' })
    expect(getActiveSessionCode()).toBeNull()
  })

  it('returns null when no active tab', () => {
    expect(getActiveSessionCode()).toBeNull()
  })

  it('returns null when activeTabId points to missing tab', () => {
    useTabStore.setState({ tabs: {}, activeTabId: 'nonexistent' })
    expect(getActiveSessionCode()).toBeNull()
  })
})
