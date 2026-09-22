// spa/src/lib/rebuild/refresh-after-switch.test.ts — the post-switch session
// refresh (#1255 SPA spec §3.2). `listSessionsFresh` is faked; the
// reconciliation is the real one behind a spy, so "applied" / "dropped" can be
// asserted both on the call and on what it wrote.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useHostStore } from '../../stores/useHostStore'
import { useSessionStore } from '../../stores/useSessionStore'
import { useTabStore } from '../../stores/useTabStore'
import { useRebuildStore } from '../../stores/useRebuildStore'
import type { FreshSessions, Session } from '../host-api'
import { STORAGE_KEYS } from '../storage/keys'
import type { Tab, TmuxSessionContent } from '../../types/tab'
import { closeAttachGate, openAttachGate } from './attach-gate'
import { __resetForTests, connectionClosed, connectionOpened, heldVersion, note } from './session-version'
import { handleSessionsFrame } from './ws-sessions'

vi.mock('./cwd-probe', () => ({ probeMissingCwds: vi.fn(), probeSessionCwd: vi.fn(), resetCwdProbes: vi.fn() }))
vi.mock('./provenance-probe', () => ({ probeSessionProvenance: vi.fn(), resetProvenanceProbes: vi.fn() }))

const { listSessionsFresh } = vi.hoisted(() => ({ listSessionsFresh: vi.fn<(hostId: string) => Promise<FreshSessions>>() }))
vi.mock('../host-api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../host-api')>()),
  listSessionsFresh: (hostId: string) => listSessionsFresh(hostId),
}))

vi.mock('./reconcile-host', async (importOriginal) => {
  const real = await importOriginal<typeof import('./reconcile-host')>()
  return { ...real, reconcileHostSessions: vi.fn(real.reconcileHostSessions) }
})

const { reconcileHostSessions } = await import('./reconcile-host')
const { refreshSessionsAfterSwitch, cancelSessionRefresh, __resetRefreshForTests } = await import('./refresh-after-switch')
const reconcile = vi.mocked(reconcileHostSessions)

const H = 'h1'
const H2 = 'h2'
const E1 = '9f3c1a0b7d2e4c61'
const E2 = '0123456789abcdef'
const S: Session = { code: 'sss111', name: 'dev', cwd: '', mode: 'terminal', tmux_instance: '111:1000' }

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

const versioned = (seq: number, sessions: Session[] = [], epoch = E1): FreshSessions => ({ kind: 'versioned', epoch, seq, sessions })

function seedLivePane(hostId = H): void {
  const content: TmuxSessionContent = {
    kind: 'tmux-session', hostId, sessionCode: 'sss111', mode: 'terminal', cachedName: 'dev', tmuxInstance: '111:1000',
  }
  const id = `t-${hostId}`
  const tab: Tab = { id, pinned: false, locked: false, createdAt: 0, layout: { type: 'leaf', pane: { id: `p-${hostId}`, content } } }
  const prev = useTabStore.getState()
  useTabStore.setState({ tabs: { ...prev.tabs, [id]: tab }, tabOrder: [...prev.tabOrder, id], activeTabId: id })
}

function terminated(hostId = H): TmuxSessionContent['terminated'] {
  const layout = useTabStore.getState().tabs[`t-${hostId}`].layout
  if (layout.type !== 'leaf' || layout.pane.content.kind !== 'tmux-session') throw new Error('fixture')
  return layout.pane.content.terminated
}

const setFence = (n: number) => localStorage.setItem(STORAGE_KEYS.WORLD_EPOCH, String(n))

beforeEach(() => {
  // Retries wait on timers (1 s, 2 s, 4 s): every test drives them itself.
  vi.useFakeTimers()
  localStorage.clear()
  __resetForTests()
  __resetRefreshForTests()
  listSessionsFresh.mockReset()
  reconcile.mockClear()
  useHostStore.setState({
    hosts: {
      [H]: { id: H, name: 'Host', ip: '1.2.3.4', port: 7860, order: 0 },
      [H2]: { id: H2, name: 'Host 2', ip: '5.6.7.8', port: 7860, order: 1 },
    },
    hostOrder: [H], runtime: {}, activeHostId: H,
  })
  useSessionStore.setState({ sessions: {} })
  useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
  useRebuildStore.setState({ operations: {}, lockedBy: null, lockGrant: null })
  seedLivePane()
  openAttachGate(H)
})

afterEach(() => {
  __resetRefreshForTests()
  vi.useRealTimers()
  useHostStore.getState().reset()
  localStorage.clear()
})

/** Let every retry wait there could be (1 + 2 + 4 s) elapse, then settle `p`. */
async function drain(p: Promise<void>): Promise<void> {
  await vi.advanceTimersByTimeAsync(7_000)
  await p
}

describe('refreshSessionsAfterSwitch', () => {
  it('gate closed: no fetch', async () => {
    closeAttachGate(H)
    await refreshSessionsAfterSwitch()
    expect(listSessionsFresh).not.toHaveBeenCalled()
  })

  it('an unversioned answer (old daemon) is not reconciled', async () => {
    listSessionsFresh.mockResolvedValue({ kind: 'unversioned' })
    await refreshSessionsAfterSwitch()
    expect(listSessionsFresh).toHaveBeenCalledWith(H)
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('a newer versioned list is reconciled with the fetched sessions, then held', async () => {
    note(H, { epoch: E1, seq: 3 })
    listSessionsFresh.mockResolvedValue(versioned(4, []))
    await refreshSessionsAfterSwitch()
    expect(reconcile).toHaveBeenCalledWith(H, [])
    expect(terminated()).toBe('session-closed')
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 4 })
  })

  it('an older or equal versioned list is dropped', async () => {
    note(H, { epoch: E1, seq: 9 })
    listSessionsFresh.mockResolvedValue(versioned(9, []))
    await refreshSessionsAfterSwitch()
    expect(reconcile).not.toHaveBeenCalled()
    expect(terminated()).toBeUndefined()
  })

  // Claim before apply (codex adversarial F2): a half-applied list must never
  // be followed by an older one.
  it('a reconcile that throws still holds the fetched version: an older WS frame after it is not reconciled', async () => {
    note(H, { epoch: E1, seq: 3 })
    reconcile.mockImplementationOnce(() => { throw new Error('quota') })
    listSessionsFresh.mockResolvedValueOnce(versioned(5, [S]))
    listSessionsFresh.mockResolvedValue({ kind: 'unversioned' }) // the retry: not evidence
    const p = refreshSessionsAfterSwitch()
    await expect(drain(p)).resolves.toBeUndefined()
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 5 })

    reconcile.mockClear()
    handleSessionsFrame(H, { type: 'sessions', session: '', value: '[]', epoch: E1, seq: 4 })
    expect(reconcile).not.toHaveBeenCalled()
    expect(terminated()).toBeUndefined()
  })

  it('a WS frame whose reconcile threw is recovered by a fresh refresh on its connection (gate closed)', async () => {
    closeAttachGate(H)
    reconcile.mockImplementationOnce(() => { throw new Error('quota') })
    listSessionsFresh.mockResolvedValue(versioned(7, []))
    handleSessionsFrame(H, { type: 'sessions', session: '', value: JSON.stringify([S]), epoch: E1, seq: 6 })
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 6 })
    await vi.advanceTimersByTimeAsync(0)
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(reconcile).toHaveBeenCalledTimes(2)
    expect(reconcile).toHaveBeenLastCalledWith(H, [])
    expect(terminated()).toBe('session-closed')
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 7 })
    expect(useHostStore.getState().runtime[H]?.attachReady).toBe(true)
  })

  it('a recovery refresh is dropped when its connection moved during the fetch', async () => {
    closeAttachGate(H)
    reconcile.mockImplementationOnce(() => { throw new Error('quota') })
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    handleSessionsFrame(H, { type: 'sessions', session: '', value: JSON.stringify([S]), epoch: E1, seq: 6 })
    connectionClosed(H)
    connectionOpened(H)
    d.resolve(versioned(7, []))
    await drain(Promise.resolve())
    expect(reconcile).toHaveBeenCalledTimes(1) // the frame's own, which threw
    expect(terminated()).toBeUndefined()
  })

  it('reads the gate and captures conn in ONE step: a connection closing right after the start never gets its result applied', async () => {
    note(H, { epoch: E1, seq: 3 })
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    // The hook's onClose, then a new connection whose first frame reopens the gate.
    connectionClosed(H)
    closeAttachGate(H)
    connectionOpened(H)
    openAttachGate(H)
    d.resolve(versioned(1, [], E2)) // another daemon process, fetched on the dead connection
    await pending
    expect(reconcile).not.toHaveBeenCalled()
    expect(terminated()).toBeUndefined()
  })

  it('the world changes during the fetch (a switch in any window): dropped', async () => {
    setFence(100)
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    setFence(200)
    d.resolve(versioned(4, []))
    await pending
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('the world generation of a device that never switched (no fence key) compares equal to itself', async () => {
    expect(localStorage.getItem(STORAGE_KEYS.WORLD_EPOCH)).toBeNull()
    listSessionsFresh.mockResolvedValue(versioned(4, []))
    await refreshSessionsAfterSwitch()
    expect(reconcile).toHaveBeenCalledTimes(1)
  })

  it('…and the first switch ever (no fence → a fence) during the fetch drops it', async () => {
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    setFence(1_700_000_000_000_000)
    d.resolve(versioned(4, []))
    await pending
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('the gate closes during the fetch: dropped', async () => {
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    closeAttachGate(H)
    d.resolve(versioned(4, []))
    await pending
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('the host is removed during the fetch: dropped', async () => {
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    useHostStore.setState({ hostOrder: [] })
    d.resolve(versioned(4, []))
    await pending
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('the host\'s endpoint changes during the fetch: dropped', async () => {
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    const h = useHostStore.getState().hosts[H]
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, [H]: { ...h, port: 7861 } } })
    d.resolve(versioned(4, []))
    await pending
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('conn moves during the fetch: a different epoch is dropped', async () => {
    note(H, { epoch: E1, seq: 3 })
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    connectionClosed(H)
    connectionOpened(H)
    d.resolve(versioned(1, [], E2))
    await pending
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('conn moves during the fetch: the same epoch with a newer seq is applied', async () => {
    note(H, { epoch: E1, seq: 3 })
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    connectionClosed(H)
    connectionOpened(H)
    d.resolve(versioned(4, []))
    await pending
    expect(reconcile).toHaveBeenCalledTimes(1)
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 4 })
  })

  it('a WS frame with a higher seq lands during the fetch: the fetch result is stale', async () => {
    note(H, { epoch: E1, seq: 3 })
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValue(d.promise)
    const pending = refreshSessionsAfterSwitch()
    handleSessionsFrame(H, { type: 'sessions', session: '', value: JSON.stringify([S]), epoch: E1, seq: 6 })
    reconcile.mockClear()
    d.resolve(versioned(5, [])) // read before the frame's list: would close S
    await pending
    expect(reconcile).not.toHaveBeenCalled()
    expect(terminated()).toBeUndefined()
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 6 })
  })

  it('two hosts: one fetch fails, the other still reconciles', async () => {
    useHostStore.setState({ hostOrder: [H, H2] })
    seedLivePane(H2)
    openAttachGate(H2)
    listSessionsFresh.mockImplementation(async (hostId) => {
      if (hostId === H) throw new Error('offline')
      return versioned(4, [])
    })
    await drain(refreshSessionsAfterSwitch())
    expect(reconcile).toHaveBeenCalledTimes(1)
    expect(reconcile).toHaveBeenCalledWith(H2, [])
    expect(terminated(H)).toBeUndefined()
    expect(terminated(H2)).toBe('session-closed')
  })

  it('idempotent: a list that still has the session changes no binding', async () => {
    listSessionsFresh.mockResolvedValue(versioned(4, [S]))
    const before = useTabStore.getState().tabs
    await refreshSessionsAfterSwitch()
    expect(reconcile).toHaveBeenCalledTimes(1)
    expect(useTabStore.getState().tabs).toBe(before)
  })
})

// A host with no further session change never pushes again, so one failed
// fetch / reconcile must not leave the world un-reconciled for good (codex
// adversarial F3): a bounded number of retries, each re-checking its fences.
describe('refreshSessionsAfterSwitch — retries', () => {
  const offline = () => Promise.reject(new Error('offline'))

  it('a fetch that fails twice and then succeeds is reconciled once', async () => {
    listSessionsFresh.mockImplementationOnce(offline).mockImplementationOnce(offline)
      .mockResolvedValueOnce(versioned(4, []))
    const p = refreshSessionsAfterSwitch()
    await vi.advanceTimersByTimeAsync(0)
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1_000)
    expect(listSessionsFresh).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(2_000)
    expect(listSessionsFresh).toHaveBeenCalledTimes(3)
    await p
    expect(reconcile).toHaveBeenCalledTimes(1)
    expect(terminated()).toBe('session-closed')
  })

  it('every attempt fails: it stops after the last retry and schedules nothing more', async () => {
    listSessionsFresh.mockImplementation(offline)
    await drain(refreshSessionsAfterSwitch())
    expect(listSessionsFresh).toHaveBeenCalledTimes(4) // the first try + 3 retries (1 s, 2 s, 4 s)
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(listSessionsFresh).toHaveBeenCalledTimes(4)
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('a reconcile that throws is retried with a fresh fetch', async () => {
    reconcile.mockImplementationOnce(() => { throw new Error('quota') })
    listSessionsFresh.mockResolvedValueOnce(versioned(4, [])).mockResolvedValueOnce(versioned(5, []))
    await drain(refreshSessionsAfterSwitch())
    expect(listSessionsFresh).toHaveBeenCalledTimes(2)
    expect(reconcile).toHaveBeenCalledTimes(2)
    expect(terminated()).toBe('session-closed')
    expect(heldVersion(H)).toEqual({ epoch: E1, seq: 5 })
  })

  it.each([
    ['the world moves', () => setFence(200)],
    ['the connection moves', () => { connectionClosed(H); connectionOpened(H) }],
    ['the gate closes', () => closeAttachGate(H)],
    ['the host leaves hostOrder', () => useHostStore.setState({ hostOrder: [] })],
    ['the endpoint changes', () => {
      const h = useHostStore.getState().hosts[H]
      useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, [H]: { ...h, port: 7861 } } })
    }],
  ])('%s during the retry wait: no further attempt', async (_label, move) => {
    listSessionsFresh.mockImplementationOnce(offline).mockResolvedValue(versioned(4, []))
    const p = refreshSessionsAfterSwitch()
    await vi.advanceTimersByTimeAsync(0)
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    move()
    await drain(p)
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('an unversioned answer is not retried', async () => {
    listSessionsFresh.mockResolvedValue({ kind: 'unversioned' })
    await drain(refreshSessionsAfterSwitch())
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
  })

  it('a stale answer is not retried', async () => {
    note(H, { epoch: E1, seq: 9 })
    listSessionsFresh.mockResolvedValue(versioned(9, []))
    await drain(refreshSessionsAfterSwitch())
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
    expect(reconcile).not.toHaveBeenCalled()
  })

  it('teardown (cancelSessionRefresh) ends a pending retry: no further attempt, no timer left', async () => {
    listSessionsFresh.mockImplementationOnce(offline).mockResolvedValue(versioned(4, []))
    const p = refreshSessionsAfterSwitch()
    await vi.advanceTimersByTimeAsync(0)
    cancelSessionRefresh(H)
    await p // settles without any timer firing
    expect(vi.getTimerCount()).toBe(0)
    await vi.advanceTimersByTimeAsync(60_000)
    expect(listSessionsFresh).toHaveBeenCalledTimes(1)
  })

  it('one refresh per host at a time: a newer one replaces the one waiting to retry', async () => {
    listSessionsFresh.mockImplementationOnce(offline)
    const first = refreshSessionsAfterSwitch()
    await vi.advanceTimersByTimeAsync(0)
    listSessionsFresh.mockResolvedValueOnce(versioned(4, []))
    await refreshSessionsAfterSwitch()
    await drain(first)
    expect(listSessionsFresh).toHaveBeenCalledTimes(2) // the replaced one never retried
    expect(reconcile).toHaveBeenCalledTimes(1)
  })

  it('one refresh per host at a time: a replaced one\'s in-flight answer is dropped', async () => {
    const d = deferred<FreshSessions>()
    listSessionsFresh.mockReturnValueOnce(d.promise)
    const first = refreshSessionsAfterSwitch()
    listSessionsFresh.mockResolvedValueOnce(versioned(4, [S]))
    await refreshSessionsAfterSwitch()
    expect(reconcile).toHaveBeenCalledTimes(1)
    d.resolve(versioned(5, [])) // would close S if it were still the host's refresh
    await drain(first)
    expect(reconcile).toHaveBeenCalledTimes(1)
    expect(terminated()).toBeUndefined()
  })
})
