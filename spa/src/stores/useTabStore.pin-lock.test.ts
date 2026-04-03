import { describe, it, expect, beforeEach } from 'vitest'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'

function addTab(name: string) {
  const tab = createTab({ kind: 'session', hostId: 'test-host', sessionCode: name, mode: 'terminal', cachedName: '', tmuxInstance: '' })
  useTabStore.getState().addTab(tab)
  return tab
}

function reset() {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
}

describe('togglePin', () => {
  beforeEach(reset)

  it('togglePin pins an unpinned tab and moves to end of pinned zone', () => {
    const a = addTab('a')
    const b = addTab('b')
    const c = addTab('c')
    useTabStore.getState().togglePin(b.id)

    const state = useTabStore.getState()
    expect(state.tabs[b.id].pinned).toBe(true)
    expect(state.tabs[b.id].locked).toBe(false)
    expect(state.tabOrder).toEqual([b.id, a.id, c.id])
  })

  it('togglePin preserves existing locked=true', () => {
    const a = addTab('a')
    useTabStore.getState().toggleLock(a.id)
    useTabStore.getState().togglePin(a.id)
    expect(useTabStore.getState().tabs[a.id].pinned).toBe(true)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
  })

  it('togglePin unpins a pinned tab and moves to start of normal zone', () => {
    const a = addTab('a')
    addTab('b')
    useTabStore.getState().toggleLock(a.id)
    useTabStore.getState().togglePin(a.id)
    useTabStore.getState().togglePin(a.id) // unpin

    const state = useTabStore.getState()
    expect(state.tabs[a.id].pinned).toBe(false)
    expect(state.tabs[a.id].locked).toBe(true) // locked not affected
    expect(state.tabOrder[0]).toBe(a.id)
  })

  it('togglePin is no-op for nonexistent tab', () => {
    useTabStore.getState().togglePin('nonexistent')
    expect(useTabStore.getState().tabOrder).toHaveLength(0)
  })

  it('multiple pins maintain correct order', () => {
    const a = addTab('a')
    const b = addTab('b')
    addTab('c')
    useTabStore.getState().togglePin(a.id)
    useTabStore.getState().togglePin(b.id)
    const orderBefore = [...useTabStore.getState().tabOrder]
    // a and b are pinned, order should be [a, b, c]
    expect(orderBefore[0]).toBe(a.id)
    expect(orderBefore[1]).toBe(b.id)
  })
})

describe('toggleLock', () => {
  beforeEach(reset)

  it('toggleLock locks an unlocked tab', () => {
    const a = addTab('a')
    useTabStore.getState().toggleLock(a.id)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(true)
  })

  it('toggleLock unlocks a locked tab', () => {
    const a = addTab('a')
    useTabStore.getState().toggleLock(a.id)
    useTabStore.getState().toggleLock(a.id)
    expect(useTabStore.getState().tabs[a.id].locked).toBe(false)
  })

  it('toggleLock on pinned tab only affects locked', () => {
    const a = addTab('a')
    useTabStore.getState().toggleLock(a.id)
    useTabStore.getState().togglePin(a.id)
    useTabStore.getState().toggleLock(a.id) // unlock
    expect(useTabStore.getState().tabs[a.id].locked).toBe(false)
    expect(useTabStore.getState().tabs[a.id].pinned).toBe(true)
  })
})

describe('locked tab blocks close', () => {
  beforeEach(reset)

  it('closeTab on locked tab is no-op', () => {
    const a = addTab('a')
    useTabStore.getState().toggleLock(a.id)
    useTabStore.getState().closeTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeDefined()
  })

  it('closeTab on unlocked tab still works', () => {
    const a = addTab('a')
    useTabStore.getState().closeTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeUndefined()
  })

  it('pinned + unlocked tab can be closed', () => {
    const a = addTab('a')
    useTabStore.getState().togglePin(a.id)
    useTabStore.getState().closeTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeUndefined()
  })

  it('pinned + locked tab cannot be closed', () => {
    const a = addTab('a')
    useTabStore.getState().toggleLock(a.id)
    useTabStore.getState().togglePin(a.id)
    useTabStore.getState().closeTab(a.id)
    expect(useTabStore.getState().tabs[a.id]).toBeDefined()
  })
})
