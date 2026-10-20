// spa/src/stores/useTabStore.group.test.ts — a tab's placement and rank (task groups spec §4.2, TG-1b-1 task 2).
import { beforeEach, describe, expect, it } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

function addTab(name: string) {
  const tab = createTab({ kind: 'tmux-session', hostId: 'test-host', sessionCode: name, mode: 'terminal', cachedName: '', tmuxInstance: '' })
  useTabStore.getState().addTab(tab)
  return tab
}

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
})

describe('setTabGroup / setTabGroupRank', () => {
  it('a new tab has neither field (no key at all)', () => {
    const a = addTab('a')
    expect(Object.hasOwn(useTabStore.getState().tabs[a.id], 'group')).toBe(false)
    expect(Object.hasOwn(useTabStore.getState().tabs[a.id], 'groupRank')).toBe(false)
  })

  it('sets and clears the group; clearing removes the key', () => {
    const a = addTab('a')
    useTabStore.getState().setTabGroup(a.id, 'group\u0000aaaaaaaa')
    expect(useTabStore.getState().tabs[a.id].group).toBe('group\u0000aaaaaaaa')
    useTabStore.getState().setTabGroup(a.id, undefined)
    expect(Object.hasOwn(useTabStore.getState().tabs[a.id], 'group')).toBe(false)
  })

  it('sets and clears the rank; clearing removes the key', () => {
    const a = addTab('a')
    useTabStore.getState().setTabGroupRank(a.id, 'V')
    expect(useTabStore.getState().tabs[a.id].groupRank).toBe('V')
    useTabStore.getState().setTabGroupRank(a.id, undefined)
    expect(Object.hasOwn(useTabStore.getState().tabs[a.id], 'groupRank')).toBe(false)
  })

  it('touches only that tab, and a no-op returns the same state (no persist, no notify)', () => {
    const a = addTab('a')
    const b = addTab('b')
    useTabStore.getState().setTabGroup(a.id, 'ungrouped')
    const before = useTabStore.getState()
    expect(before.tabs[b.id]).toBe(useTabStore.getState().tabs[b.id])
    useTabStore.getState().setTabGroup(a.id, 'ungrouped')
    expect(useTabStore.getState()).toBe(before)
    useTabStore.getState().setTabGroupRank(a.id, undefined)
    expect(useTabStore.getState()).toBe(before)
  })

  it('an unknown tab id is ignored', () => {
    const before = useTabStore.getState()
    useTabStore.getState().setTabGroup('nope', 'ungrouped')
    useTabStore.getState().setTabGroupRank('nope', 'V')
    expect(useTabStore.getState()).toBe(before)
  })

  it('persists the fields with the tab', () => {
    const a = addTab('a')
    useTabStore.getState().setTabGroup(a.id, 'ungrouped')
    useTabStore.getState().setTabGroupRank(a.id, 'V')
    const raw = JSON.parse(localStorage.getItem('purdex-tabs') ?? '{}') as { state?: { tabs?: Record<string, { group?: string; groupRank?: string }> } }
    expect(raw.state?.tabs?.[a.id]).toMatchObject({ group: 'ungrouped', groupRank: 'V' })
  })
})

describe('setTabGroupRanks', () => {
  it('writes several ranks in one state change; unknown tabs are skipped', () => {
    const a = addTab('a')
    const b = addTab('b')
    let notified = 0
    const unsub = useTabStore.subscribe(() => { notified += 1 })
    useTabStore.getState().setTabGroupRanks([{ tabId: a.id, rank: 'F' }, { tabId: b.id, rank: 'M' }, { tabId: 'gone', rank: 'T' }])
    unsub()
    expect(notified).toBe(1)
    expect(useTabStore.getState().tabs[a.id].groupRank).toBe('F')
    expect(useTabStore.getState().tabs[b.id].groupRank).toBe('M')
    expect(useTabStore.getState().tabs).not.toHaveProperty('gone')
  })

  it('writes that change nothing return the same state', () => {
    const a = addTab('a')
    useTabStore.getState().setTabGroupRank(a.id, 'F')
    const before = useTabStore.getState().tabs
    useTabStore.getState().setTabGroupRanks([{ tabId: a.id, rank: 'F' }])
    expect(useTabStore.getState().tabs).toBe(before)
  })
})
