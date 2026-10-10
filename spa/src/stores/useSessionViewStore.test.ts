import { describe, it, expect, beforeEach } from 'vitest'
import { useSessionViewStore, selectSessionView, viewKey, installSessionViewCleanup } from './useSessionViewStore'
import { useTabStore } from './useTabStore'
import { createTab } from '../types/tab'
import { STORAGE_KEYS } from '../lib/storage'

beforeEach(() => {
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [] })
  useSessionViewStore.setState({ byPane: {} })
})

describe('selectSessionView', () => {
  it('is the terminal when nothing is recorded', () => {
    expect(selectSessionView('t1', 'p1', 'code')(useSessionViewStore.getState())).toBe('terminal')
  })

  it('returns the recorded view for the same session', () => {
    useSessionViewStore.getState().setView('t1', 'p1', 'code', 'deck')
    expect(selectSessionView('t1', 'p1', 'code')(useSessionViewStore.getState())).toBe('deck')
  })

  // D1: the view is bound to the session the pane showed when it was chosen
  it('starts at the terminal when the pane is rebound to another session', () => {
    useSessionViewStore.getState().setView('t1', 'p1', 'code', 'chat')
    expect(selectSessionView('t1', 'p1', 'other')(useSessionViewStore.getState())).toBe('terminal')
  })

  it('keeps panes and tabs apart', () => {
    useSessionViewStore.getState().setView('t1', 'p1', 'code', 'deck')
    expect(selectSessionView('t1', 'p2', 'code')(useSessionViewStore.getState())).toBe('terminal')
    expect(selectSessionView('t2', 'p1', 'code')(useSessionViewStore.getState())).toBe('terminal')
  })
})

describe('setView', () => {
  it('the terminal is the absence of a record', () => {
    const { setView } = useSessionViewStore.getState()
    setView('t1', 'p1', 'code', 'deck')
    setView('t1', 'p1', 'code', 'terminal')
    expect(useSessionViewStore.getState().byPane).toEqual({})
  })

  it('setting the terminal on a pane with no record changes nothing (same state object)', () => {
    const before = useSessionViewStore.getState()
    before.setView('t1', 'p1', 'code', 'terminal')
    expect(useSessionViewStore.getState()).toBe(before)
  })
})

describe('persistence', () => {
  it('is device-local under its own key and stores only the records', () => {
    useSessionViewStore.getState().setView('t1', 'p1', 'code', 'chat')
    const raw = localStorage.getItem(STORAGE_KEYS.SESSION_VIEW)
    expect(raw).toBeTruthy()
    const state = JSON.parse(raw as string).state
    expect(Object.keys(state)).toEqual(['byPane'])
    expect(state.byPane[viewKey('t1', 'p1')]).toEqual({ view: 'chat', sessionCode: 'code' })
  })

  it('restores from storage', async () => {
    localStorage.setItem(STORAGE_KEYS.SESSION_VIEW, JSON.stringify({
      state: { byPane: { [viewKey('t1', 'p1')]: { view: 'deck', sessionCode: 'code' } } }, version: 0,
    }))
    await useSessionViewStore.persist.rehydrate()
    expect(selectSessionView('t1', 'p1', 'code')(useSessionViewStore.getState())).toBe('deck')
  })

  it('drops a damaged record instead of throwing', async () => {
    localStorage.setItem(STORAGE_KEYS.SESSION_VIEW, JSON.stringify({
      state: { byPane: { a: { view: 'sideways', sessionCode: 'c' }, b: 5, c: { view: 'deck', sessionCode: 'ok' } } }, version: 0,
    }))
    await useSessionViewStore.persist.rehydrate()
    expect(useSessionViewStore.getState().byPane).toEqual({ c: { view: 'deck', sessionCode: 'ok' } })
  })
})

describe('cleanup when a tab or a pane goes away', () => {
  it('drops the records of tabs that no longer exist, keeps the rest', () => {
    const keep = createTab({ kind: 'dashboard' })
    const gone = createTab({ kind: 'dashboard' })
    installSessionViewCleanup()
    useTabStore.setState({ tabs: { [keep.id]: keep, [gone.id]: gone } })
    const { setView } = useSessionViewStore.getState()
    setView(keep.id, keep.layout.type === 'leaf' ? keep.layout.pane.id : '', 'c', 'deck')
    setView(gone.id, gone.layout.type === 'leaf' ? gone.layout.pane.id : '', 'c', 'chat')
    useTabStore.setState({ tabs: { [keep.id]: keep } })
    const keys = Object.keys(useSessionViewStore.getState().byPane)
    expect(keys).toHaveLength(1)
    expect(keys[0].startsWith(keep.id)).toBe(true)
  })

  it('drops the record of a pane that left a tab that stays', () => {
    const tab = createTab({ kind: 'dashboard' })
    installSessionViewCleanup()
    useTabStore.setState({ tabs: { [tab.id]: tab } })
    useSessionViewStore.getState().setView(tab.id, 'no-such-pane', 'c', 'deck')
    // a layout write that keeps the tab: the stale pane record goes
    useTabStore.setState({ tabs: { [tab.id]: { ...tab } } })
    expect(useSessionViewStore.getState().byPane).toEqual({})
  })

  it('does not wipe the records while the tab world is still empty (before hydration)', () => {
    installSessionViewCleanup()
    useSessionViewStore.getState().setView('t1', 'p1', 'c', 'deck')
    useTabStore.setState({ tabs: {}, tabOrder: ['x'] })
    expect(Object.keys(useSessionViewStore.getState().byPane)).toHaveLength(1)
  })
})
