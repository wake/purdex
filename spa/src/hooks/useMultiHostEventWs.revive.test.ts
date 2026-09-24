// spa/src/hooks/useMultiHostEventWs.revive.test.ts — the two revive triggers
// (spec §3.2): a reconciled `sessions` payload revives the host's
// `tmux-restarted` panes by name, and an operation-lock release re-runs the
// pass for every host (from a fresh list for a versioned host, #1309 — the
// frames here are unversioned unless a test says otherwise). Real stores and the real engine / batch throughout —
// the scenarios are about how the pass interleaves with them.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useHostStore } from '../stores/useHostStore'
import { useShownHostsStore } from '../stores/useShownHostsStore'
import { useSessionStore } from '../stores/useSessionStore'
import { useTabStore } from '../stores/useTabStore'
import { useRebuildStore } from '../stores/useRebuildStore'
import { rebuildPane, type RebuildReport } from '../lib/rebuild/engine'
import { runBatchRebuild, type BatchReport } from '../lib/rebuild/batch'
import { emptyHostConfigEntry, useHostConfigStore } from '../stores/useHostConfigStore'
import type { FreshSessions, Session } from '../lib/host-api'
import type { PaneRebuildRecord, Tab, TmuxSessionContent } from '../types/tab'
import { useWorkspaceStore } from '../features/workspace/store'
import { MASTER_PROFILE_ID, useLocalProfilesStore, type ParkedWorld } from '../stores/useLocalProfilesStore'
import { __resetMasterWorldForTest } from '../lib/profile/master-world'

vi.mock('../lib/host-connection', () => ({
  checkHealth: vi.fn(async () => ({ daemon: 'connected', latency: 3, ticket: 'tk' })),
}))
vi.mock('../lib/rebuild/cwd-probe', () => ({
  probeMissingCwds: vi.fn(),
  probeSessionCwd: vi.fn(),
  resetCwdProbes: vi.fn(),
}))
vi.mock('../lib/rebuild/provenance-probe', () => ({
  probeSessionProvenance: vi.fn(),
  resetProvenanceProbes: vi.fn(),
}))
// Only `listSessions` is faked: S17 needs the REAL `fetchHost` — the one that
// overwrites the session store whenever its HTTP response lands.
const { listSessions, listSessionsFresh } = vi.hoisted(() => ({
  listSessions: vi.fn<() => Promise<Session[]>>(),
  listSessionsFresh: vi.fn<(hostId: string) => Promise<FreshSessions>>(),
}))
vi.mock('../lib/host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../lib/host-api')>()),
  listSessions: (...args: unknown[]) => (listSessions as (...a: unknown[]) => Promise<Session[]>)(...args),
  listSessionsFresh: (hostId: string) => listSessionsFresh(hostId),
}))

const { useMultiHostEventWs } = await import('./useMultiHostEventWs')
const { PROFILE_SWITCH_LOCK_OWNER, switchActiveProfile } = await import('../lib/profile/switch-active')

const HOST = 'h1'

class FakeSocket {
  static OPEN = 1
  readyState = 0
  binaryType = ''
  url: string
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  send = vi.fn()
  close = vi.fn(() => { this.readyState = 3 })
  constructor(url: string) { this.url = url; sockets.push(this) }
  emit(data: string) { this.onmessage?.({ data }) }
}

let sockets: FakeSocket[] = []

// --- fixtures (shapes copied from batch.test.ts / engine.test.ts) ---------

function session(over: Partial<Session>): Session {
  return { code: 'c', name: 'n', cwd: '', mode: 'terminal', ...over }
}

const plan = { createSession: true, applyCwd: true, runResume: true }
const NEW1 = session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' })
const DEAD = { hostId: HOST, sessionCode: 'old111', tmuxInstance: '111:1000' }

function content(over: Partial<TmuxSessionContent> = {}): TmuxSessionContent {
  return {
    kind: 'tmux-session', hostId: HOST, sessionCode: 'old111', mode: 'terminal',
    cachedName: 'dev', tmuxInstance: '111:1000', terminated: 'tmux-restarted',
    rebuild: { sessionName: 'dev', tmuxInstance: '111:1000', cwd: '/w', capturedAt: 1,
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } },
    ...over,
  }
}

/** Add one single-pane tab, keeping the tabs already seeded. */
function seedPane(tabId: string, paneId: string, over: Partial<TmuxSessionContent> = {},
                  record: Partial<PaneRebuildRecord> = {}) {
  const base = content(over)
  const tab: Tab = {
    id: tabId, pinned: false, locked: false, createdAt: 0,
    layout: { type: 'leaf', pane: { id: paneId, content: {
      ...base,
      rebuild: base.rebuild ? { ...base.rebuild, ...record } : undefined,
    } } },
  }
  const prev = useTabStore.getState()
  useTabStore.setState({
    tabs: { ...prev.tabs, [tabId]: tab },
    tabOrder: [...prev.tabOrder.filter((id) => id !== tabId), tabId],
    activeTabId: tabId,
  })
}

function paneContent(tabId: string, paneId: string): TmuxSessionContent {
  const layout = useTabStore.getState().tabs[tabId].layout
  if (layout.type !== 'leaf' || layout.pane.id !== paneId) throw new Error('fixture is a leaf')
  const c = layout.pane.content
  if (c.kind !== 'tmux-session') throw new Error('fixture is a tmux pane')
  return c
}

/** A finished operation on the pane's dead binding, with or without a created session. */
function seedDoneOperation(tabId: string, paneId: string, createdSession?: Session) {
  const store = useRebuildStore.getState()
  const report: RebuildReport = {
    hostId: HOST,
    steps: { create: { status: 'ok' }, resume: { status: 'failed', error: 'boom' }, repoint: { status: 'skipped' } },
    repointed: false,
  }
  store.beginOperation({ paneId, tabId, hostId: HOST, plan, binding: DEAD, resumeCommand: '', report })
  store.finishOperation(paneId, { report, createdSession })
}

function deferred<T = void>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

function payload(sessions: Session[]): string {
  return JSON.stringify({ type: 'sessions', session: '', value: JSON.stringify(sessions) })
}

const attachReady = () => useHostStore.getState().runtime[HOST]?.attachReady
const lockedBy = () => useRebuildStore.getState().lockedBy
const dead = { sessionCode: 'old111', tmuxInstance: '111:1000', terminated: 'tmux-restarted' }
const revived = { sessionCode: 'new1', tmuxInstance: '222:2000', cachedName: 'dev' }

/** Mount the hook and wait for every host's socket. */
async function mount(hostCount = 1) {
  const view = renderHook(() => useMultiHostEventWs())
  await waitFor(() => expect(sockets).toHaveLength(hostCount))
  return view
}

/** The socket the hook opened for `ip` — connection order across hosts is not promised. */
function socketFor(ip: string): FakeSocket {
  const socket = sockets.find((s) => s.url.includes(`//${ip}:`))
  if (!socket) throw new Error(`no socket for ${ip}`)
  return socket
}

function emit(sessions: Session[], socket: FakeSocket = sockets[0]) {
  act(() => { socket.emit(payload(sessions)) })
}

beforeEach(() => {
  sockets = []
  vi.stubGlobal('WebSocket', FakeSocket)
  useHostStore.setState({
    hosts: { [HOST]: { id: HOST, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 } },
    hostOrder: [HOST],
    runtime: {},
    activeHostId: HOST,
  })
  useShownHostsStore.setState({ ids: [HOST, 'h2'] }) // shown in this workbench: the revive pass skips a hidden host (H2d-4)
  // The real session store, `fetchHost` stubbed out; S17 restores the real one.
  useSessionStore.setState({ sessions: {}, fetchHost: vi.fn(async () => {}) } as never)
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  useHostConfigStore.setState({ byHost: { [HOST]: emptyHostConfigEntry('ready'), h2: emptyHostConfigEntry('ready') } })
})

afterEach(() => {
  vi.unstubAllGlobals()
  useHostStore.getState().reset()
})

describe('useMultiHostEventWs revive — the sessions-payload trigger', () => {
  it('S1: revives a tmux-restarted pane onto the live session of the same name', async () => {
    const view = await mount()
    seedPane('t1', 'p1')

    emit([NEW1])

    const c = paneContent('t1', 'p1')
    expect(c).toMatchObject(revived)
    expect(c.terminated).toBeUndefined()
    expect(c.rebuild).toMatchObject({ sessionName: 'dev', tmuxInstance: '222:2000', cwd: '/w',
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } })
    view.unmount()
  })

  it('S1b: the payload that marks a live pane tmux-restarted also revives it (code reused)', async () => {
    const view = await mount()
    const { terminated: _t, ...live } = content()
    seedPane('t1', 'p1', { ...live, terminated: undefined })
    expect(paneContent('t1', 'p1').terminated).toBeUndefined()

    emit([session({ code: 'old111', name: 'dev', tmux_instance: '222:2000' })])

    const c = paneContent('t1', 'p1')
    expect(c.terminated).toBeUndefined()
    expect(c.tmuxInstance).toBe('222:2000')
    expect(c.sessionCode).toBe('old111')
    view.unmount()
  })

  it.each(['session-closed', 'host-removed'] as const)('S2/S3: leaves a %s pane alone', async (reason) => {
    const view = await mount()
    seedPane('t1', 'p1', { terminated: reason })

    emit([NEW1])

    expect(paneContent('t1', 'p1')).toMatchObject({ sessionCode: 'old111', terminated: reason })
    view.unmount()
  })

  it('S4: a live session with no generation is no evidence; the next one with a generation revives', async () => {
    const view = await mount()
    seedPane('t1', 'p1')

    emit([session({ code: 'new1', name: 'dev', tmux_instance: '' })])
    expect(paneContent('t1', 'p1')).toMatchObject(dead)

    emit([NEW1])
    expect(paneContent('t1', 'p1')).toMatchObject(revived)
    view.unmount()
  })

  it('S8: leaves a pane whose finished operation created a session', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    seedDoneOperation('t1', 'p1', NEW1)
    expect(lockedBy()).toBeNull()

    emit([NEW1])

    expect(paneContent('t1', 'p1')).toMatchObject(dead)
    view.unmount()
  })

  it('S15: replaying the payload after a revive rewrites nothing', async () => {
    const view = await mount()
    seedPane('t1', 'p1')

    emit([NEW1])
    const c = paneContent('t1', 'p1')
    expect(c).toMatchObject(revived)
    const record = c.rebuild

    emit([NEW1])
    expect(paneContent('t1', 'p1')).toBe(c)
    expect(paneContent('t1', 'p1').rebuild).toBe(record)
    view.unmount()
  })
})

describe('useMultiHostEventWs revive — the lock-release trigger', () => {
  it('S9: a pane refused under the lock is revived when the lock is released, with no new payload', async () => {
    const view = await mount()
    seedPane('tX', 'pX')
    const grant = useRebuildStore.getState().acquireOperationLock('rebuild:pX')

    emit([NEW1])
    expect(paneContent('tX', 'pX')).toMatchObject(dead)

    // The operation ran and created nothing — a refusal, or a failed create.
    seedDoneOperation('tX', 'pX')
    act(() => { useRebuildStore.getState().releaseOperationLock(grant) })

    expect(paneContent('tX', 'pX')).toMatchObject(revived)
    view.unmount()
  })

  it('S7 + S11: a single rebuild owns its pane; its sibling is revived on release', async () => {
    const view = await mount()
    seedPane('tX', 'pX')
    seedPane('tY', 'pY')
    const resume = deferred()
    const sendKeys = vi.fn(() => resume.promise)
    const run = rebuildPane(HOST, 'tX', 'pX', plan, {
      createSession: async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }),
      sendKeys,
    })
    // The lock is taken before the create runs; only the send-keys call
    // proves the payload below lands while the resume is in flight.
    await waitFor(() => expect(sendKeys).toHaveBeenCalledTimes(1))
    expect(lockedBy()).not.toBeNull()

    emit([NEW1]) // the daemon's create broadcast
    expect(paneContent('tX', 'pX')).toMatchObject(dead)
    expect(paneContent('tY', 'pY')).toMatchObject(dead)

    let report!: RebuildReport
    await act(async () => { resume.resolve(); report = await run })

    expect(report.repointed).toBe(true)
    expect(lockedBy()).toBeNull()
    expect(paneContent('tX', 'pX')).toMatchObject(revived)
    const y = paneContent('tY', 'pY')
    expect(y).toMatchObject(revived)
    expect(y.terminated).toBeUndefined()
    expect(y.rebuild).toMatchObject({ sessionName: 'dev', tmuxInstance: '222:2000',
      agent: { type: 'cc', sessionId: 'S1', updatedAt: 1 } })

    // X's record is the one the engine wrote; the pass never touched it.
    const xRecord = paneContent('tX', 'pX').rebuild
    emit([NEW1])
    expect(paneContent('tX', 'pX').rebuild).toBe(xRecord)
    view.unmount()
  })

  it('S20: a pane whose write throws on release stays dead; the rebuild still resolves and the next pane revives', async () => {
    const view = await mount()
    seedPane('tX', 'pX')
    seedPane('tY', 'pY')
    seedPane('tZ', 'pZ')
    const resume = deferred()
    const sendKeys = vi.fn(() => resume.promise)
    const run = rebuildPane(HOST, 'tX', 'pX', plan, {
      createSession: async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }),
      sendKeys,
    })
    await waitFor(() => expect(sendKeys).toHaveBeenCalledTimes(1))
    emit([NEW1])

    // Only Y's write fails — the engine's own write of X, before the release,
    // must go through, so the throw is what the persisted store would do on a
    // quota error, scoped to the release pass's first pane.
    const original = useTabStore.getState().setPaneContent
    let thrown = false
    useTabStore.setState({ setPaneContent: (tabId, paneId, next) => {
      if (paneId === 'pY' && !thrown) { thrown = true; throw new Error('QuotaExceededError') }
      original(tabId, paneId, next)
    } })

    let report!: RebuildReport
    try {
      await act(async () => { resume.resolve(); report = await run })
    } finally {
      useTabStore.setState({ setPaneContent: original })
    }

    expect(thrown).toBe(true)
    expect(report.repointed).toBe(true)
    expect(lockedBy()).toBeNull()
    expect(paneContent('tX', 'pX')).toMatchObject(revived)
    expect(paneContent('tY', 'pY')).toMatchObject(dead)
    expect(paneContent('tZ', 'pZ')).toMatchObject(revived)
    view.unmount()
  })

  it('S12(a): a batch re-points its members itself; nothing revives while it runs', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    seedPane('t2', 'p2')
    const resume = deferred()
    const sendKeys = vi.fn(() => resume.promise)
    const run = runBatchRebuild({
      createSession: async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }),
      sendKeys,
    })
    await waitFor(() => expect(sendKeys).toHaveBeenCalledTimes(1))
    expect(lockedBy()).toBe('rebuild:batch')

    emit([NEW1])
    expect(paneContent('t1', 'p1')).toMatchObject(dead)
    expect(paneContent('t2', 'p2')).toMatchObject(dead)

    let report!: BatchReport
    await act(async () => { resume.resolve(); report = await run })

    expect(report.groups[0].members[0]).toMatchObject({ paneId: 'p2', repointed: true })
    expect(paneContent('t1', 'p1')).toMatchObject(revived)
    expect(paneContent('t2', 'p2')).toMatchObject(revived)
    view.unmount()
  })

  it('S12(b): a group whose resume failed keeps its source on the report; the member is revived on release', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    seedPane('t2', 'p2')
    const resume = deferred()
    const sendKeys = vi.fn(async () => { await resume.promise; throw new Error('boom') })
    const run = runBatchRebuild({
      createSession: async () => session({ code: 'new1', name: 'dev', tmux_instance: '222:2000' }),
      sendKeys,
    })
    await waitFor(() => expect(sendKeys).toHaveBeenCalledTimes(1))

    // The create response reaches the session store only on a successful
    // re-point, so the daemon's broadcast is what puts `new1` in evidence.
    emit([NEW1])
    expect(useSessionStore.getState().sessions[HOST]?.map((s) => s.code)).toEqual(['new1'])
    expect(attachReady()).toBe(true)
    expect(lockedBy()).toBe('rebuild:batch')
    expect(paneContent('t1', 'p1')).toMatchObject(dead)
    expect(paneContent('t2', 'p2')).toMatchObject(dead)

    let report!: BatchReport
    await act(async () => { resume.resolve(); report = await run })

    expect(report.groups[0].members[0]).toMatchObject({ paneId: 'p2', repointed: false })
    expect(paneContent('t1', 'p1')).toMatchObject(dead)
    const op = useRebuildStore.getState().operations['p1']
    expect(op).toMatchObject({ status: 'done', createdSession: { code: 'new1' } })
    expect(op.binding.sessionCode).toBe('old111')
    expect(paneContent('t2', 'p2')).toMatchObject(revived)
    expect(paneContent('t2', 'p2').terminated).toBeUndefined()
    view.unmount()
  })

  it('S17: the release pass reads the reconciled payload, not a session store a late fetchHost overwrote', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    const grant = useRebuildStore.getState().acquireOperationLock('legacy:restore')

    // The connection opens: the hook starts a `fetchHost`, whose response is
    // still in flight when the WS payload below lands.
    const late = deferred<Session[]>()
    listSessions.mockReturnValueOnce(late.promise)
    useSessionStore.setState({ fetchHost: useSessionStore.getInitialState().fetchHost })
    act(() => { sockets[0].onopen?.() })
    expect(listSessions).toHaveBeenCalledTimes(1)

    emit([NEW1])
    expect(useSessionStore.getState().sessions[HOST]?.map((s) => s.code)).toEqual(['new1'])
    expect(paneContent('t1', 'p1')).toMatchObject(dead)

    // The stale HTTP list lands: `dev` is now `old` in the store.
    const STALE = session({ code: 'old', name: 'dev', tmux_instance: '333:3000' })
    await act(async () => { late.resolve([STALE]) })
    expect(useSessionStore.getState().sessions[HOST]?.map((s) => s.code)).toEqual(['old'])
    expect(paneContent('t1', 'p1')).toMatchObject(dead)

    const seen: string[] = []
    const unsubscribe = useTabStore.subscribe(() => { seen.push(paneContent('t1', 'p1').sessionCode) })
    act(() => { useRebuildStore.getState().releaseOperationLock(grant) })
    unsubscribe()

    expect(paneContent('t1', 'p1')).toMatchObject(revived)
    expect(seen).not.toContain('old')
    view.unmount()
  })

  it('S19: the release pass covers every host', async () => {
    const H2 = 'h2'
    useHostStore.setState((state) => ({
      hosts: { ...state.hosts, [H2]: { id: H2, name: 'Host 2', ip: '5.6.7.8', port: 7860, order: 1 } },
      hostOrder: [HOST, H2],
    }))
    const view = await mount(2)
    seedPane('t1', 'p1')
    seedPane('t2', 'p2', { hostId: H2 })
    const grant = useRebuildStore.getState().acquireOperationLock('legacy:restore')

    emit([NEW1], socketFor('1.2.3.4'))
    emit([NEW1], socketFor('5.6.7.8'))
    expect(useHostStore.getState().runtime[H2]?.attachReady).toBe(true)
    expect(paneContent('t1', 'p1')).toMatchObject(dead)
    expect(paneContent('t2', 'p2')).toMatchObject(dead)

    act(() => { useRebuildStore.getState().releaseOperationLock(grant) })

    expect(paneContent('t1', 'p1')).toMatchObject({ ...revived, hostId: HOST })
    expect(paneContent('t2', 'p2')).toMatchObject({ ...revived, hostId: H2 })
    view.unmount()
  })

  it('S16: no pass for a host whose gate is closed when the lock is released; its next payload revives', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    const grant = useRebuildStore.getState().acquireOperationLock('legacy:restore')

    emit([NEW1])
    expect(useSessionStore.getState().sessions[HOST]?.map((s) => s.code)).toEqual(['new1'])
    expect(attachReady()).toBe(true)
    expect(paneContent('t1', 'p1')).toMatchObject(dead)

    act(() => { sockets[0].onclose?.() })
    expect(attachReady()).toBe(false)

    act(() => { useRebuildStore.getState().releaseOperationLock(grant) })
    expect(paneContent('t1', 'p1')).toMatchObject(dead) // the evidence is a dropped connection's

    await waitFor(() => expect(sockets).toHaveLength(2))
    act(() => { sockets[1].onopen?.() })
    expect(paneContent('t1', 'p1')).toMatchObject(dead)

    emit([NEW1], sockets[1])
    expect(paneContent('t1', 'p1')).toMatchObject(revived)
    view.unmount()
  })
})

// #1309 spec §3.1: a lock release reconciles a versioned, live host from a list
// FETCHED after the release — the revive comes from that list, never from the
// one reconciled before the write. An unversioned host keeps the synchronous pass.
describe('useMultiHostEventWs revive — the lock release reconciles from a fresh list', () => {
  const E = '9f3c1a0b7d2e4c61'
  function emitVersioned(sessions: Session[], seq: number, socket: FakeSocket = sockets[0]) {
    act(() => { socket.emit(JSON.stringify({ type: 'sessions', session: '', value: JSON.stringify(sessions), epoch: E, seq })) })
  }
  afterEach(() => listSessionsFresh.mockReset())

  it('a versioned host: the release fetches, and the pane is revived from the FETCHED list', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    emitVersioned([], 1) // gate open, a versioned list held — no `dev` in it
    expect(attachReady()).toBe(true)
    const grant = useRebuildStore.getState().acquireOperationLock('legacy:restore')
    listSessionsFresh.mockResolvedValue({ kind: 'versioned', epoch: E, seq: 2, sessions: [NEW1] })

    act(() => { useRebuildStore.getState().releaseOperationLock(grant) })
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(listSessionsFresh).toHaveBeenCalledWith(HOST)
    await waitFor(() => expect(paneContent('t1', 'p1')).toMatchObject(revived))
    view.unmount()
  })

  // codex plan review #6: the post-release refresh + revive pass is async now;
  // the per-pane guard and the rebuild's own outcome must still hold.
  it('a rebuild that created a session but did not re-point: the post-release refresh does NOT revive that pane', async () => {
    const view = await mount()
    seedPane('tX', 'pX')
    emitVersioned([], 1)
    const run = rebuildPane(HOST, 'tX', 'pX', plan, {
      createSession: async () => NEW1,
      sendKeys: async () => { throw new Error('boom') },
    })
    listSessionsFresh.mockResolvedValue({ kind: 'versioned', epoch: E, seq: 2, sessions: [NEW1] })
    let report!: RebuildReport
    await act(async () => { report = await run })

    expect(report.repointed).toBe(false)
    expect(lockedBy()).toBeNull()
    await waitFor(() => expect(useSessionStore.getState().sessions[HOST]?.map((s) => s.code)).toEqual(['new1']))
    expect(listSessionsFresh).toHaveBeenCalledTimes(1) // the fresh list is in evidence…
    expect(paneContent('tX', 'pX')).toMatchObject(dead) // …and the guard still refuses the revive
    expect(useRebuildStore.getState().operations['pX']).toMatchObject({ status: 'done', createdSession: { code: 'new1' } })
    view.unmount()
  })

  it('a batch: its report, its members and the operation panel are untouched by the post-release reconcile', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    seedPane('t2', 'p2')
    emitVersioned([], 1)
    const resume = deferred()
    const sendKeys = vi.fn(async () => { await resume.promise; throw new Error('boom') })
    const run = runBatchRebuild({ createSession: async () => NEW1, sendKeys })
    await waitFor(() => expect(sendKeys).toHaveBeenCalledTimes(1))
    emitVersioned([NEW1], 2) // the create broadcast, during the hold: held back (barrier)
    expect(paneContent('t2', 'p2')).toMatchObject(dead)

    listSessionsFresh.mockResolvedValue({ kind: 'versioned', epoch: E, seq: 3, sessions: [NEW1] })
    let report!: BatchReport
    await act(async () => { resume.resolve(); report = await run })
    const reportCopy = JSON.parse(JSON.stringify(report))
    const operations = useRebuildStore.getState().operations

    await waitFor(() => expect(paneContent('t2', 'p2')).toMatchObject(revived)) // the member, from the fetched list
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(report).toEqual(reportCopy)
    expect(report.groups[0].members[0]).toMatchObject({ paneId: 'p2', repointed: false })
    expect(useRebuildStore.getState().operations).toBe(operations)
    expect(operations['p1']).toMatchObject({ status: 'done', createdSession: { code: 'new1' } })
    expect(paneContent('t1', 'p1')).toMatchObject(dead) // the source keeps its panel
    view.unmount()
  })

  it('an unversioned host: no fetch, the pass runs synchronously on the release', async () => {
    const view = await mount()
    seedPane('t1', 'p1')
    const grant = useRebuildStore.getState().acquireOperationLock('legacy:restore')
    emit([NEW1])
    expect(paneContent('t1', 'p1')).toMatchObject(dead)

    act(() => { useRebuildStore.getState().releaseOperationLock(grant) })
    expect(paneContent('t1', 'p1')).toMatchObject(revived)
    expect(listSessionsFresh).not.toHaveBeenCalled()
    view.unmount()
  })
})

// #1255 SPA spec §3.5 (codex plan review #1): a switch releases the operation
// lock, and the lock-release trigger runs the pass for every host. The list it
// would use was reconciled for the world that was on screen BEFORE the switch —
// no evidence for the world that just came on. Real `switchActiveProfile`.
describe('useMultiHostEventWs revive — a profile switch', () => {
  const SLAVE = 's1'
  function slaveWorld(): ParkedWorld {
    const tab: Tab = { id: 'st', pinned: false, locked: false, createdAt: 0,
      layout: { type: 'leaf', pane: { id: 'sp', content: content() } } }
    return {
      workspaces: [{ id: 'sws', name: 'slave', tabs: ['st'], activeTabId: 'st' }],
      tabs: { st: tab }, activeWorkspaceId: 'sws', activeTabId: 'st',
    }
  }

  beforeEach(() => {
    localStorage.clear()
    __resetMasterWorldForTest()
    listSessionsFresh.mockResolvedValue({ kind: 'unversioned' })
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
    useLocalProfilesStore.setState({
      slaves: { [SLAVE]: { id: SLAVE, name: 'Slave', createdAt: 1, shownHostIds: [HOST], world: slaveWorld() } }, // HOST shown in that workbench too
      slaveOrder: [SLAVE], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, master: { name: null },
    })
  })

  afterEach(() => {
    listSessionsFresh.mockReset()
    useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: MASTER_PROFILE_ID, parkedMaster: null, worldEpoch: 0, master: { name: null } })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
    useTabStore.setState({ worldId: MASTER_PROFILE_ID, worldEpoch: 0 })
    __resetMasterWorldForTest()
    localStorage.clear()
  })

  it('the switch\'s own lock release does not revive the new world from the pre-switch list', async () => {
    const view = await mount()
    emit([NEW1]) // reconciled for the master world; the gate is open
    expect(attachReady()).toBe(true)

    const released: (string | null)[] = []
    const unsub = useRebuildStore.subscribe((s, prev) => { if (prev.lockedBy !== null && s.lockedBy === null) released.push(prev.lockedBy) })
    expect(await switchActiveProfile(SLAVE)).toEqual({ ok: true })
    unsub()
    expect(released).toEqual([PROFILE_SWITCH_LOCK_OWNER]) // the trigger did fire

    await new Promise((r) => setTimeout(r, 0))
    expect(paneContent('st', 'sp')).toMatchObject(dead)
    view.unmount()
  })

  it('after the post-switch reconcile it IS revived, from the new list', async () => {
    const view = await mount()
    // A versioned frame: the lock release refreshes this host from a fresh list (#1309).
    act(() => { sockets[0].emit(JSON.stringify({ type: 'sessions', session: '', value: JSON.stringify([NEW1]), epoch: '9f3c1a0b7d2e4c61', seq: 4 })) })
    listSessionsFresh.mockResolvedValue({ kind: 'versioned', epoch: '9f3c1a0b7d2e4c61', seq: 5, sessions: [NEW1] })

    expect(await switchActiveProfile(SLAVE)).toEqual({ ok: true })
    await waitFor(() => expect(paneContent('st', 'sp')).toMatchObject(revived))
    expect(listSessionsFresh).toHaveBeenCalledWith(HOST)
    view.unmount()
  })
})
