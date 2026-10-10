// #2457: the module-level memories of a session pane are freed when the pane is gone for good - and only then.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { readScrollMemo, writeScrollMemo } from '../nex/transcript-scroll-memory'
import { draftKey, readDraft, writeDraft, clearAllDrafts } from './draft-memory'
import { deckPanes, isFolded, noteDeckPane, setOpen } from './fold-memory'
import { chatScrollKey, clearAllPanels, conversationBinding, openPanel, readPanel } from './panel-memory'
import { installPaneRelease, releasePane, retireStaleSessions } from './pane-release'
import { clearAllSendQueues, hasSendQueue, sendQueueCount, sendQueueFor } from './send-queue'
import type { SendOutcome, SendPort } from './send'

const H = 'host-1'
const mkTab = (id: string, code: string): Tab => ({ ...createTab({ kind: 'tmux-session', hostId: H, sessionCode: code, mode: 'terminal', cachedName: code, tmuxInstance: 'i' }), id })
const paneOf = (tab: Tab) => (tab.layout as { pane: { id: string } }).pane.id
const tabA = mkTab('ta', 'aaa001')
const tabB = mkTab('tb', 'bbb002')
const A = paneOf(tabA)
const B = paneOf(tabB)

interface Call { text: string; id: string; resolve: (o: SendOutcome) => void }
function fakePort() {
  const calls: Call[] = []
  const interrupt = vi.fn(() => Promise.resolve<SendOutcome>({ kind: 'accepted' }))
  const port: SendPort = { submit: (text, id) => new Promise<SendOutcome>((resolve) => { calls.push({ text, id, resolve }) }), interrupt }
  return { calls, port, interrupt }
}

/** Fill every memory a pane keeps, under session `s`. */
function fill(pane: string, s: string, port: SendPort = fakePort().port) {
  const dk = draftKey(pane, H, s)
  writeDraft(dk, 'typing')
  sendQueueFor(dk, () => port)
  openPanel(pane, conversationBinding(H, s), { kind: 'chain', turnId: 't0', firstStepId: 'x' })
  setOpen(`${pane}\0${s}`, 'deck-fold', true) // deck
  setOpen(`${pane}\0${conversationBinding(H, s)}`, 'chat-fold', true) // chat
  const memo = { scrollTop: 5, atBottom: false, view: 'deck' as const, firstTurn: 0 }
  writeScrollMemo(`${pane}\0${s}`, memo)
  writeScrollMemo(chatScrollKey(pane, conversationBinding(H, s)), { ...memo, view: 'chat' })
  noteDeckPane(pane)
  return dk
}
const has = (pane: string, s: string) => ({
  draft: readDraft(draftKey(pane, H, s)) !== undefined,
  queue: hasSendQueue(draftKey(pane, H, s)),
  panel: readPanel(pane) !== undefined,
  deckFold: isFolded(`${pane}\0${s}`, 'deck-fold'),
  chatFold: isFolded(`${pane}\0${conversationBinding(H, s)}`, 'chat-fold'),
  deckScroll: readScrollMemo(`${pane}\0${s}`) !== undefined,
  chatScroll: readScrollMemo(chatScrollKey(pane, conversationBinding(H, s))) !== undefined,
  deckPane: deckPanes().includes(pane),
})
const ALL = { draft: true, queue: true, panel: true, deckFold: true, chatFold: true, deckScroll: true, chatScroll: true, deckPane: true }
const NONE = Object.fromEntries(Object.keys(ALL).map((k) => [k, false]))

let off: () => void
beforeEach(() => {
  vi.useFakeTimers()
  clearAllSendQueues(); clearAllDrafts(); clearAllPanels()
  useTabStore.setState({ tabs: { [tabA.id]: tabA, [tabB.id]: tabB }, tabOrder: [tabA.id, tabB.id], activeTabId: tabA.id, visitHistory: [] })
  off = installPaneRelease()
})
afterEach(() => { off(); vi.useRealTimers(); clearAllSendQueues(); clearAllDrafts(); clearAllPanels() })

describe('releasePane', () => {
  it('frees every memory of the pane and leaves another pane\'s alone', () => {
    fill(A, 's1'); fill(B, 's1')
    expect(has(A, 's1')).toEqual(ALL)
    releasePane(A)
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(B, 's1')).toEqual(ALL)
    expect(sendQueueCount()).toBe(1)
  })

  it('frees the memories of EVERY session the pane showed', () => {
    fill(A, 's1'); fill(A, 's2')
    releasePane(A)
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(A, 's2')).toEqual(NONE)
    expect(sendQueueCount()).toBe(0)
  })

  it('a pane whose id starts with another\'s is not touched', () => {
    fill('ab', 's1'); fill('abc', 's1')
    releasePane('ab')
    expect(has('abc', 's1')).toEqual(ALL)
  })

  it('a queue message still in its undo window is dropped: no timer left, no request ever', async () => {
    const { calls, port } = fakePort()
    const dk = fill(A, 's1', port)
    sendQueueFor(dk, () => port).enqueue('never sent')
    await vi.advanceTimersByTimeAsync(1000)
    releasePane(A)
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(0)
  })

  it('a message already handed out keeps its fate: its answer starts nothing, the queued one behind it is not sent', async () => {
    const { calls, port, interrupt } = fakePort()
    const dk = fill(A, 's1', port)
    const q = sendQueueFor(dk, () => port)
    q.enqueue('first')
    await vi.advanceTimersByTimeAsync(3000)
    q.enqueue('second')
    expect(calls).toHaveLength(1)
    releasePane(A)
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(1) // no second request
    expect(vi.getTimerCount()).toBe(0)
    expect(interrupt).not.toHaveBeenCalled()
  })

  it('a `maybe` entry makes no request either (nothing resends on a disposed queue)', async () => {
    const { calls, port } = fakePort()
    const dk = fill(A, 's1', port)
    const q = sendQueueFor(dk, () => port)
    q.enqueue('lost link')
    await vi.advanceTimersByTimeAsync(3000)
    calls[0].resolve({ kind: 'unknown' } as SendOutcome)
    await vi.advanceTimersByTimeAsync(0)
    releasePane(A)
    q.setIdle(true)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(calls).toHaveLength(1)
  })

  it('opening the same pane id again is a fresh start', () => {
    fill(A, 's1')
    releasePane(A)
    const q = sendQueueFor(draftKey(A, H, 's1'), () => fakePort().port)
    expect(q.entries()).toEqual([])
    expect(readDraft(draftKey(A, H, 's1'))).toBeUndefined()
    expect(readPanel(A)).toBeUndefined()
  })
})

describe('what leaves the tab world is released; a tab switch is not', () => {
  it('closing a tab releases its panes only', () => {
    fill(A, 's1'); fill(B, 's1')
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(B, 's1')).toEqual(ALL)
  })

  it('closing the LAST tab releases too (an empty world is real once the store has hydrated)', () => {
    fill(A, 's1')
    vi.spyOn(useTabStore.persist, 'hasHydrated').mockReturnValue(true)
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('an empty world before hydration releases nothing', () => {
    fill(A, 's1')
    vi.spyOn(useTabStore.persist, 'hasHydrated').mockReturnValue(false)
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('switching the active tab, reordering and renaming release nothing (the pane merely unmounts)', () => {
    fill(A, 's1')
    useTabStore.setState({ activeTabId: tabB.id })
    useTabStore.setState({ tabOrder: [tabB.id, tabA.id] })
    useTabStore.setState({ tabs: { [tabA.id]: { ...tabA, pinned: true }, [tabB.id]: tabB } })
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('a pane that moves to another tab (same id) is not released', () => {
    fill(A, 's1')
    const moved = { ...tabB, layout: tabA.layout }
    useTabStore.setState({ tabs: { [tabB.id]: moved }, tabOrder: [tabB.id], activeTabId: tabB.id })
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('a wholesale replacement of the tabs (rehydrate, Profile Sync) releases the panes that are not in it', () => {
    fill(A, 's1'); fill(B, 's1')
    const other = mkTab('tc', 'ccc003')
    useTabStore.setState({ tabs: { [tabB.id]: tabB, [other.id]: other }, tabOrder: [tabB.id, other.id] })
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(B, 's1')).toEqual(ALL)
  })

  it('installing again replaces the subscription (one release, not two)', () => {
    const second = installPaneRelease()
    const { port } = fakePort()
    fill(A, 's1', port)
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    expect(has(A, 's1')).toEqual(NONE)
    second()
  })
})

describe('retireStaleSessions: the pane shows another conversation now', () => {
  it('frees what was kept for the old session, keeps the new one\'s', () => {
    fill(A, 's1'); fill(A, 's2')
    retireStaleSessions(A, H, 's2')
    expect(has(A, 's1')).toMatchObject({ draft: false, queue: false, deckFold: false, chatFold: false, deckScroll: false, chatScroll: false })
    expect(has(A, 's2')).toMatchObject({ draft: true, queue: true, deckFold: true, chatFold: true, deckScroll: true, chatScroll: true })
  })

  it('never touches another pane, and does nothing without a session', () => {
    fill(A, 's1'); fill(B, 's1')
    retireStaleSessions(A, H, '')
    expect(has(A, 's1')).toEqual(ALL)
    retireStaleSessions(A, H, 's2')
    expect(has(B, 's1')).toEqual(ALL)
  })

  it('an old session\'s queue that still has a message waiting is left to finish', async () => {
    const { calls, port } = fakePort()
    const dk = fill(A, 's1', port)
    sendQueueFor(dk, () => port).enqueue('in the undo window')
    retireStaleSessions(A, H, 's2')
    expect(hasSendQueue(dk)).toBe(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(calls.map((c) => c.text)).toEqual(['in the undo window'])
  })

  it('the same session again is no change (a view switch, a remount)', () => {
    fill(A, 's1')
    retireStaleSessions(A, H, 's1')
    expect(has(A, 's1')).toEqual(ALL)
  })
})
