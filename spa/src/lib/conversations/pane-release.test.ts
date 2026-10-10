// #2457: the module-level memories of a session pane are freed when the pane is gone for good - and only then.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createTab } from '../../types/tab'
import type { Tab } from '../../types/tab'
import { useTabStore } from '../../stores/useTabStore'
import { readScrollMemo, writeScrollMemo } from '../nex/transcript-scroll-memory'
import { draftKey, readDraft, writeDraft, clearAllDrafts } from './draft-memory'
import { addAttachment, clearAllAttachments, readAttachments } from './attachment-memory'
import { deckPanes, isFolded, noteDeckPane, setOpen } from './fold-memory'
import { chatScrollKey, clearAllPanels, conversationBinding, openPanel, readPanel } from './panel-memory'
import { commitTabWorld } from '../profile/master-world'
import { useHistoryStore } from '../../stores/useHistoryStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { installPaneRelease, releasePane, retireStaleSessions } from './pane-release'
import { clearAllSendQueues, hasSendQueue, retiringQueueCount, sendQueueCount, sendQueueFor } from './send-queue'
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
  addAttachment(dk, { id: 'a1', name: 'a.png', path: '/up/a.png', text: '[Image: source: /up/a.png]' })
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
  attachments: readAttachments(draftKey(pane, H, s)).length > 0,
  queue: hasSendQueue(draftKey(pane, H, s)),
  panel: readPanel(pane) !== undefined,
  deckFold: isFolded(`${pane}\0${s}`, 'deck-fold'),
  chatFold: isFolded(`${pane}\0${conversationBinding(H, s)}`, 'chat-fold'),
  deckScroll: readScrollMemo(`${pane}\0${s}`) !== undefined,
  chatScroll: readScrollMemo(chatScrollKey(pane, conversationBinding(H, s))) !== undefined,
  deckPane: deckPanes().includes(pane),
})
const ALL = { draft: true, attachments: true, queue: true, panel: true, deckFold: true, chatFold: true, deckScroll: true, chatScroll: true, deckPane: true }
const NONE = Object.fromEntries(Object.keys(ALL).map((k) => [k, false]))

/** The release is evaluated once the synchronous write that caused it has finished (a microtask later). */
const settle = () => Promise.resolve()

let off: () => void
beforeEach(() => {
  vi.useFakeTimers()
  clearAllSendQueues(); clearAllDrafts(); clearAllAttachments(); clearAllPanels()
  useTabStore.setState({ tabs: { [tabA.id]: tabA, [tabB.id]: tabB }, tabOrder: [tabA.id, tabB.id], activeTabId: tabA.id, visitHistory: [] })
  useHistoryStore.setState({ closedTabs: [] })
  vi.spyOn(useHistoryStore.persist, 'hasHydrated').mockReturnValue(true) // the test storage hydrates asynchronously
  useLocalProfilesStore.setState({ activeProfileId: 'master', parkedMaster: null, slaves: {}, slaveOrder: [] })
  off = installPaneRelease()
})
afterEach(() => { off(); vi.restoreAllMocks(); vi.useRealTimers(); clearAllSendQueues(); clearAllDrafts(); clearAllAttachments(); clearAllPanels() })

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
  it('closing a tab releases its panes only', async () => {
    fill(A, 's1'); fill(B, 's1')
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(B, 's1')).toEqual(ALL)
  })

  it('closing the LAST tab releases too (an empty world is real once the store has hydrated)', async () => {
    fill(A, 's1')
    vi.spyOn(useTabStore.persist, 'hasHydrated').mockReturnValue(true)
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('an empty world before hydration releases nothing', async () => {
    fill(A, 's1')
    vi.spyOn(useTabStore.persist, 'hasHydrated').mockReturnValue(false)
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('switching the active tab, reordering and renaming release nothing (the pane merely unmounts)', async () => {
    fill(A, 's1')
    useTabStore.setState({ activeTabId: tabB.id })
    useTabStore.setState({ tabOrder: [tabB.id, tabA.id] })
    useTabStore.setState({ tabs: { [tabA.id]: { ...tabA, pinned: true }, [tabB.id]: tabB } })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('a pane that moves to another tab (same id) is not released', async () => {
    fill(A, 's1')
    const moved = { ...tabB, layout: tabA.layout }
    useTabStore.setState({ tabs: { [tabB.id]: moved }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('a wholesale replacement of the tabs (rehydrate, Profile Sync) releases the panes that are not in it', async () => {
    fill(A, 's1'); fill(B, 's1')
    const other = mkTab('tc', 'ccc003')
    useTabStore.setState({ tabs: { [tabB.id]: tabB, [other.id]: other }, tabOrder: [tabB.id, other.id] })
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(B, 's1')).toEqual(ALL)
  })

  it('installing again replaces the subscription (one release, not two)', async () => {
    const second = installPaneRelease()
    const { port } = fakePort()
    fill(A, 's1', port)
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
    second()
  })
})


describe('a profile switch is not a deletion (the old world is parked, and may come back)', () => {
  const world = (...tabs: Tab[]) => ({ tabs: Object.fromEntries(tabs.map((t) => [t.id, t])), workspaces: [], activeWorkspaceId: null, activeTabId: tabs[0]?.id ?? null })

  it('to a slave world and back: the master\'s panes keep their draft, queue, folds, scroll and panel', async () => {
    fill(A, 's1')
    // switch: the live tabs become the slave\'s, the master is parked
    useLocalProfilesStore.setState({ activeProfileId: 'slave-1', parkedMaster: world(tabA) })
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
    // and back
    useTabStore.setState({ tabs: { [tabA.id]: tabA }, tabOrder: [tabA.id], activeTabId: tabA.id })
    useLocalProfilesStore.setState({ activeProfileId: 'master', parkedMaster: null })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('the order of the two writes does not matter (the tab store may change before the profile store parks the world)', async () => {
    fill(A, 's1')
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    useLocalProfilesStore.setState({ activeProfileId: 'slave-1', parkedMaster: world(tabA) })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('a pane parked in a SLAVE world is kept as well', async () => {
    fill(A, 's1')
    useLocalProfilesStore.setState({ slaves: { x: { id: 'x', name: 'x', createdAt: 0, shownHostIds: [], world: world(tabA) } }, slaveOrder: ['x'] })
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('a commitTabWorld that fails is rolled back and releases nothing', async () => {
    fill(A, 's1')
    expect(() => commitTabWorld({ tabs: { [tabB.id]: tabB }, workspaces: [], activeWorkspaceId: null, activeTabId: tabB.id }, () => { throw new Error('write failed') })).toThrow('write failed')
    expect(Object.keys(useTabStore.getState().tabs)).toContain(tabA.id)
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })

  it('closing a tab inside ONE world still releases it', async () => {
    fill(A, 's1')
    useLocalProfilesStore.setState({ parkedMaster: null, slaves: {}, slaveOrder: [] })
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('a parked world that is thrown away finally releases its panes (no permanent leak)', async () => {
    fill(A, 's1')
    useLocalProfilesStore.setState({ activeProfileId: 'slave-1', parkedMaster: world(tabA) })
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
    useLocalProfilesStore.setState({ parkedMaster: null }) // e.g. the slave was promoted over it / deleted
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('a profile store that has not hydrated yet releases nothing (parked worlds are not visible yet)', async () => {
    fill(A, 's1')
    vi.spyOn(useLocalProfilesStore.persist, 'hasHydrated').mockReturnValue(false)
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
  })
})

describe('a closed tab can be reopened with the same panes, so its state is kept while the record is', () => {
  const close = (tab: Tab) => useWorkspaceStore.getState().closeTabInWorkspace(tab.id)

  it('close (the real action) then reopen: draft, queue, folds, scroll and panel are all still there and live', async () => {
    fill(A, 's1')
    close(tabA)
    expect(useTabStore.getState().tabs[tabA.id]).toBeUndefined()
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
    const reopened = useHistoryStore.getState().reopenLast()!
    expect(paneOf(reopened)).toBe(A)
    useTabStore.getState().addTab(reopened)
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
    expect(readDraft(draftKey(A, H, 's1'))).toBe('typing')
  })

  it('once reopened and then closed WITHOUT a new record (the old record is spent), the pane is released', async () => {
    fill(A, 's1')
    close(tabA)
    useTabStore.getState().addTab(useHistoryStore.getState().reopenLast()!)
    await settle()
    useTabStore.setState({ tabs: { [tabB.id]: tabB }, tabOrder: [tabB.id], activeTabId: tabB.id })
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('a record that was reopened but whose tab is not live any more does not keep the pane', async () => {
    fill(A, 's1')
    close(tabA)
    useHistoryStore.getState().reopenLast() // marked reopened, the caller dropped the tab
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('clearing the closed tabs releases what only they were keeping', async () => {
    fill(A, 's1'); fill(B, 's1')
    close(tabA)
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
    useHistoryStore.getState().clearClosedTabs()
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
    expect(has(B, 's1')).toEqual(ALL)
  })

  it('a record pushed out by the cap releases its panes (no permanent leak)', async () => {
    fill(A, 's1')
    close(tabA)
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
    for (let i = 0; i < 100; i++) useHistoryStore.getState().recordClose(mkTab('x' + i, 'xx' + String(i).padStart(4, '0')))
    await settle()
    expect(has(A, 's1')).toEqual(NONE)
  })

  it('a closed tab\'s pane that is also parked in a world is kept by the world, not only the record', async () => {
    fill(A, 's1')
    close(tabA)
    useLocalProfilesStore.setState({ activeProfileId: 'slave-1', parkedMaster: { tabs: { [tabA.id]: tabA }, workspaces: [], activeWorkspaceId: null, activeTabId: null } })
    useHistoryStore.getState().clearClosedTabs()
    await settle()
    expect(has(A, 's1')).toEqual(ALL)
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

  it('an old session\'s undo / waiting messages are dropped and never sent; one in flight finishes and its queue leaves the registry', async () => {
    const { calls, port } = fakePort()
    const dk = fill(A, 's1', port)
    const q = sendQueueFor(dk, () => port)
    q.enqueue('in flight')
    await vi.advanceTimersByTimeAsync(3000)
    q.enqueue('in the undo window')
    retireStaleSessions(A, H, 's2')
    expect(hasSendQueue(dk)).toBe(false) // detached at once; only the old queue waits for its request
    expect(retiringQueueCount()).toBe(1)
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(10_000)
    expect(hasSendQueue(dk)).toBe(false)
    expect(retiringQueueCount()).toBe(0)
    expect(calls.map((c) => c.text)).toEqual(['in flight'])
    expect(vi.getTimerCount()).toBe(0)
  })

  it('back to s1 while its old request is still out: a FRESH queue, the new message goes out, and the old one finishing does not touch it', async () => {
    const { calls, port } = fakePort()
    const dk = fill(A, 's1', port)
    const old = sendQueueFor(dk, () => port)
    old.enqueue('old')
    await vi.advanceTimersByTimeAsync(3000)
    retireStaleSessions(A, H, 's2')
    retireStaleSessions(A, H, 's1') // and back
    const fresh = sendQueueFor(dk, () => port)
    expect(fresh).not.toBe(old)
    fresh.enqueue('new')
    await vi.advanceTimersByTimeAsync(3000)
    expect(calls.map((c) => c.text)).toEqual(['old', 'new'])
    calls[0].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(0)
    expect(retiringQueueCount()).toBe(0)
    expect(hasSendQueue(dk)).toBe(true)
    expect(fresh.entries().map((e) => e.state)).toEqual(['sending'])
    calls[1].resolve({ kind: 'accepted' })
    await vi.advanceTimersByTimeAsync(0)
    expect(fresh.entries().map((e) => e.state)).toEqual(['sent'])
  })

  it('a waiting (busy) message of the old session is dropped, not stranded', async () => {
    const { calls, port } = fakePort()
    const dk = fill(A, 's1', port)
    const q = sendQueueFor(dk, () => port)
    q.enqueue('busy one')
    await vi.advanceTimersByTimeAsync(3000)
    calls[0].resolve({ kind: 'busy' } as SendOutcome)
    await vi.advanceTimersByTimeAsync(0)
    expect(q.entries()[0].state).toBe('waiting')
    retireStaleSessions(A, H, 's2')
    expect(hasSendQueue(dk)).toBe(false)
    expect(calls).toHaveLength(1)
  })

  it('switching session over and over leaves the registry bounded, and going back finds a fresh start', async () => {
    const { calls, port } = fakePort()
    for (let i = 0; i < 20; i++) {
      const dk = fill(A, 's' + i, port)
      sendQueueFor(dk, () => port).enqueue('m' + i)
      retireStaleSessions(A, H, 's' + i)
    }
    retireStaleSessions(A, H, 's20')
    await vi.advanceTimersByTimeAsync(10_000)
    expect(sendQueueCount()).toBe(0)
    expect(calls).toHaveLength(0)
    const q = sendQueueFor(draftKey(A, H, 's0'), () => port)
    expect(q.entries()).toEqual([])
    expect(readDraft(draftKey(A, H, 's0'))).toBeUndefined()
  })

  it('the same session again is no change (a view switch, a remount)', () => {
    fill(A, 's1')
    retireStaleSessions(A, H, 's1')
    expect(has(A, 's1')).toEqual(ALL)
  })
})
