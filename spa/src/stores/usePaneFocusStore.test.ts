import { describe, it, expect, beforeEach } from 'vitest'
import { usePaneFocusStore, installPaneFocusCleanup, PANE_FOCUS_CAP } from './usePaneFocusStore'
import { useTabStore } from './useTabStore'
import { createTab, type Tab } from '../types/tab'

function tab(): Tab {
  return createTab({ kind: 'dashboard' })
}

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  usePaneFocusStore.setState({ recent: {}, focusRequest: null })
})

describe('usePaneFocusStore.touch', () => {
  it('puts the touched pane first, most recent first', () => {
    const { touch } = usePaneFocusStore.getState()
    touch('t1', 'a')
    touch('t1', 'b')
    touch('t1', 'c')
    expect(usePaneFocusStore.getState().recent.t1).toEqual(['c', 'b', 'a'])
  })

  it('moves an already-recorded pane to the front without duplicating it', () => {
    const { touch } = usePaneFocusStore.getState()
    touch('t1', 'a')
    touch('t1', 'b')
    touch('t1', 'a')
    expect(usePaneFocusStore.getState().recent.t1).toEqual(['a', 'b'])
  })

  it('keeps tabs apart', () => {
    const { touch } = usePaneFocusStore.getState()
    touch('t1', 'a')
    touch('t2', 'x')
    expect(usePaneFocusStore.getState().recent).toEqual({ t1: ['a'], t2: ['x'] })
  })

  it(`caps a tab's record at ${PANE_FOCUS_CAP}, dropping the oldest`, () => {
    const { touch } = usePaneFocusStore.getState()
    for (let i = 0; i < PANE_FOCUS_CAP + 3; i++) touch('t1', `p${i}`)
    const list = usePaneFocusStore.getState().recent.t1
    expect(PANE_FOCUS_CAP).toBe(16)
    expect(list).toHaveLength(PANE_FOCUS_CAP)
    expect(list[0]).toBe(`p${PANE_FOCUS_CAP + 2}`)
    expect(list).not.toContain('p0')
    expect(list).not.toContain('p2')
    expect(list).toContain('p3')
  })

  it('touching the pane that is already first writes nothing (no re-render for repeated clicks)', () => {
    const { touch } = usePaneFocusStore.getState()
    touch('t1', 'a')
    const before = usePaneFocusStore.getState().recent
    touch('t1', 'a')
    expect(usePaneFocusStore.getState().recent).toBe(before)
  })

  it('forgetTab drops one tab and keeps the others', () => {
    const { touch, forgetTab } = usePaneFocusStore.getState()
    touch('t1', 'a')
    touch('t2', 'x')
    forgetTab('t1')
    expect(usePaneFocusStore.getState().recent).toEqual({ t2: ['x'] })
  })
})

describe('usePaneFocusStore cleanup subscription (spec §8.1)', () => {
  it('closeTab clears the closed tab\'s record', () => {
    const a = tab()
    const b = tab()
    useTabStore.getState().addTab(a)
    useTabStore.getState().addTab(b)
    usePaneFocusStore.getState().touch(a.id, 'p1')
    usePaneFocusStore.getState().touch(b.id, 'p2')

    useTabStore.getState().closeTab(a.id)

    expect(usePaneFocusStore.getState().recent).toEqual({ [b.id]: ['p2'] })
  })

  it('a wholesale useTabStore.setState that drops two tabs clears both; the surviving tab keeps its record', () => {
    const a = tab()
    const b = tab()
    const c = tab()
    useTabStore.setState({ tabs: { [a.id]: a, [b.id]: b, [c.id]: c }, tabOrder: [a.id, b.id, c.id] })
    const { touch } = usePaneFocusStore.getState()
    touch(a.id, 'pa')
    touch(b.id, 'pb')
    touch(c.id, 'pc')

    // Not through closeTab: a rehydrate / Profile Sync apply replaces the tab world.
    useTabStore.setState({ tabs: { [c.id]: c }, tabOrder: [c.id] })

    expect(usePaneFocusStore.getState().recent).toEqual({ [c.id]: ['pc'] })
  })

  it('a tab-store write that keeps every recorded tab leaves the record object untouched', () => {
    const a = tab()
    useTabStore.getState().addTab(a)
    usePaneFocusStore.getState().touch(a.id, 'pa')
    const before = usePaneFocusStore.getState().recent

    useTabStore.getState().addTab(tab())
    useTabStore.getState().setActiveTab(a.id)

    expect(usePaneFocusStore.getState().recent).toBe(before)
  })

  it('installing again replaces the subscription instead of stacking a second one', () => {
    const uninstall = installPaneFocusCleanup()
    const a = tab()
    useTabStore.getState().addTab(a)
    usePaneFocusStore.getState().touch(a.id, 'pa')
    useTabStore.getState().closeTab(a.id)
    expect(usePaneFocusStore.getState().recent).toEqual({})

    // With the subscription removed, nothing prunes any more.
    uninstall()
    const b = tab()
    useTabStore.getState().addTab(b)
    usePaneFocusStore.getState().touch(b.id, 'pb')
    useTabStore.getState().closeTab(b.id)
    expect(usePaneFocusStore.getState().recent).toEqual({ [b.id]: ['pb'] })

    installPaneFocusCleanup()
  })
})

describe('usePaneFocusStore.requestFocus (#1840 review A1)', () => {
  const s = () => usePaneFocusStore.getState()

  it('makes the pane its tab\'s most recent one and posts a request with a larger nonce every time', () => {
    s().requestFocus('t1', 'a')
    const first = s().focusRequest!
    expect(s().recent.t1).toEqual(['a'])
    expect(first).toMatchObject({ tabId: 't1', paneId: 'a', taken: false })
    s().requestFocus('t1', 'a')
    expect(s().focusRequest!.nonce).toBeGreaterThan(first.nonce)
  })

  it('takeFocusRequest claims a request once; release undoes a claim; a replaced request is never claimed', () => {
    s().requestFocus('t1', 'a')
    const n = s().focusRequest!.nonce
    expect(s().takeFocusRequest(n)).toBe(true)
    expect(s().takeFocusRequest(n)).toBe(false)
    s().releaseFocusRequest(n)
    expect(s().takeFocusRequest(n)).toBe(true)
    s().requestFocus('t1', 'b')
    expect(s().takeFocusRequest(n)).toBe(false)
    s().releaseFocusRequest(n) // names the old request: the newer one is not touched
    expect(s().focusRequest).toMatchObject({ paneId: 'b', taken: false })
  })
})
