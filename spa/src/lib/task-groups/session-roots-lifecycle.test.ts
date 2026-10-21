// spa/src/lib/task-groups/session-roots-lifecycle.test.ts — the roots read, outside React (TG-2r). It reads only while the task-group view is on, once per host epoch plus one debounced re-read per session the last read missed,
// keeps what the hosts listed in the device-local cache (an offline host still answers), and drops a host's entries when the host goes or answers as another daemon, and entries no world's tab refers to.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

const readFirstPage = vi.fn()
vi.mock('../team/unattended-reader', () => ({ readFirstPage: (...a: unknown[]) => readFirstPage(...a), refreshHost: (...a: unknown[]) => readFirstPage(...a) }))

const settled = { value: true } // whether the three world stores agree (profile/master-world.ts `readMasterWorld`)
vi.mock('../profile/master-world', () => ({ readMasterWorld: () => (settled.value ? { settled: true } : { settled: false, reason: 'epoch-mismatch' }) }))

import { startSessionRootsLifecycle, setRootsInterest, ROOT_REREAD_DEBOUNCE_MS, ROOT_PRUNE_SETTLE_MS } from './session-roots-lifecycle'
import { useRootCacheStore, rootOf } from './root-cache'
import { useRelayQuotaStore } from '../team/relay-quota'
import { useUnattendedStore } from '../../stores/useUnattendedStore'
import { useHostStore } from '../../stores/useHostStore'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkspaceStore } from '../../features/workspace/store'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import type { SessionQuota } from '../team/types'
import type { Tab } from '../../types/tab'

const q = (session_id: string, root_session_id: string): SessionQuota => ({ session_id, root_session_id, title: '', address: `mlab/${session_id}`, is_lead: false, self_left: 0, member_pool_left: 0, rev: 1 })
const setRead = (hostId: string, rows: SessionQuota[]) => useRelayQuotaStore.setState((s) => ({ reads: { ...s.reads, [hostId]: { ok: true, quotas: rows } } }))
const want = (hostId: string, agentSessionId: string) => ({ hostId, agentSessionId })
const supportQuota = (hostId: string) => useUnattendedStore.setState((s) => ({ byHost: { ...s.byHost, [hostId]: { ...s.byHost[hostId], quotaSupport: 'yes' } } }) as never)
const hostOf = (id: string, ip = '10.0.0.1') => ({ id, name: id, ip, port: 7860, token: 't' }) as never
const tabOf = (id: string, hostId: string, agentSessionId: string): Tab => ({
  id, createdAt: 1, layout: { type: 'leaf', pane: { id: `p-${id}`, content: { kind: 'tmux-session', hostId, sessionCode: id, mode: 'terminal', cachedName: id, tmuxInstance: '', rebuild: { agent: { sessionId: agentSessionId } } } } },
}) as never

let stop: () => void
beforeEach(() => {
  vi.useFakeTimers()
  settled.value = true
  readFirstPage.mockReset()
  readFirstPage.mockResolvedValue({})
  useRelayQuotaStore.getState().reset()
  useRootCacheStore.setState({ roots: {} })
  useUnattendedStore.setState({ byHost: {} })
  useHostStore.setState({ hosts: { h1: hostOf('h1') }, runtime: {} })
  useTabStore.setState({ tabs: {} })
  supportQuota('h1')
  stop = startSessionRootsLifecycle()
})
afterEach(() => { stop(); setRootsInterest(false, []); setRootsInterest(false, [], 'personal'); setRootsInterest(false, [], 'notify'); vi.useRealTimers() })

describe('reading', () => {
  it('reads nothing while the view is off', () => {
    setRootsInterest(false, [want('h1', 'S1')])
    vi.advanceTimersByTime(60_000)
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('two askers do not clobber each other: the sidebar going off leaves the personal panel\'s session wanted (TG-5b)', () => {
    setRootsInterest(true, [want('h1', 'S1')], 'personal')
    setRootsInterest(true, [want('h1', 'S9')])
    setRootsInterest(false, [])
    readFirstPage.mockClear()
    setRead('h1', [q('S2', 'S2')]) // lists neither: S1 is still wanted, so one debounced re-read follows
    vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS)
    expect(readFirstPage).toHaveBeenCalledTimes(1)
    setRootsInterest(false, [], 'personal')
    readFirstPage.mockClear()
    setRead('h1', [])
    vi.advanceTimersByTime(60_000)
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('the notify asker is a third source: the other two going off leave its session wanted, and it going off leaves theirs', () => {
    setRootsInterest(true, [want('h1', 'S1')], 'notify')
    setRootsInterest(true, [want('h1', 'S2')])
    setRootsInterest(true, [want('h1', 'S3')], 'personal')
    setRootsInterest(false, [])
    setRootsInterest(false, [], 'personal')
    readFirstPage.mockClear()
    setRead('h1', [q('S9', 'S9')])
    vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS)
    expect(readFirstPage).toHaveBeenCalledTimes(1) // S1 is still wanted by notify
    setRootsInterest(true, [want('h1', 'S2')])
    setRootsInterest(false, [], 'notify')
    readFirstPage.mockClear()
    setRead('h1', [q('S8', 'S8')])
    vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS)
    expect(readFirstPage).toHaveBeenCalledTimes(1) // S2 (sidebar) remains
  })

  it('reads a supporting host once when the view opens, and never a host without team.relay_quota.v1', () => {
    setRootsInterest(true, [want('h1', 'S1'), want('h2', 'S2')])
    expect(readFirstPage.mock.calls.map((c) => c[0])).toEqual(['h1'])
  })

  it('re-reads once, debounced, for a session the last read did not list — and not again for the same session', () => {
    setRootsInterest(true, [want('h1', 'S1')])
    readFirstPage.mockClear()
    setRead('h1', [q('S2', 'S2')])
    vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS - 1)
    expect(readFirstPage).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(readFirstPage).toHaveBeenCalledTimes(1)
    setRead('h1', [q('S2', 'S2')])
    vi.advanceTimersByTime(10_000)
    expect(readFirstPage).toHaveBeenCalledTimes(1)
  })

  it('does not re-read for a session the cache already knows (offline host, nothing to ask)', () => {
    useRootCacheStore.getState().ingest('h1', [q('S1', 'ROOT')])
    setRootsInterest(true, [want('h1', 'S1')])
    readFirstPage.mockClear()
    setRead('h1', [])
    vi.advanceTimersByTime(10_000)
    expect(readFirstPage).not.toHaveBeenCalled()
  })

  it('asks again after a reconnect fenced the host (its read is gone)', () => {
    setRootsInterest(true, [want('h1', 'S1')])
    setRead('h1', [])
    vi.advanceTimersByTime(ROOT_REREAD_DEBOUNCE_MS)
    readFirstPage.mockClear()
    useRelayQuotaStore.setState({ reads: {} })
    expect(readFirstPage.mock.calls.map((c) => c[0])).toEqual(['h1'])
  })
})

describe('the cache', () => {
  it('keeps what a read listed, and an offline host still answers from it', () => {
    setRead('h1', [q('S1', 'ROOT')])
    expect(rootOf('h1', 'S1')).toBe('ROOT')
    useHostStore.setState({ runtime: { h1: { status: 'disconnected' } } as never })
    useRelayQuotaStore.setState({ reads: {} })
    expect(rootOf('h1', 'S1')).toBe('ROOT')
  })

  it('does not keep a failed or rowless read', () => {
    useRelayQuotaStore.setState({ reads: { h1: { ok: false, code: 'timeout' } } })
    useRelayQuotaStore.setState({ reads: { h1: { ok: true } } })
    expect(useRootCacheStore.getState().roots).toEqual({})
  })

  it('drops a removed host, and a host moved to another address, but not a host that is unchanged', () => {
    useRootCacheStore.getState().ingest('h1', [q('S1', 'R')])
    useRootCacheStore.getState().ingest('h2', [q('S2', 'R')])
    useHostStore.setState({ hosts: { h1: hostOf('h1'), h2: hostOf('h2') } })
    expect(rootOf('h1', 'S1')).toBe('R')
    useHostStore.setState({ hosts: { h1: hostOf('h1', '10.0.0.9'), h2: hostOf('h2') } })
    expect(rootOf('h1', 'S1')).toBeNull()
    useHostStore.setState({ hosts: { h1: hostOf('h1', '10.0.0.9') } })
    expect(rootOf('h2', 'S2')).toBeNull()
  })

  it('drops a host whose endpoint now answers as another daemon', () => {
    useRootCacheStore.getState().ingest('h1', [q('S1', 'R')])
    useHostStore.setState({ runtime: { h1: { daemonIdMismatch: { observed: 'other' } } } as never })
    expect(rootOf('h1', 'S1')).toBeNull()
  })
})

describe('cleanup', () => {
  it('drops entries no tab of any world refers to, and keeps those on screen and in a parked world', () => {
    useRootCacheStore.getState().ingest('h1', [q('ON', 'R'), q('PARKED', 'R'), q('GONE', 'R')])
    stop()
    useTabStore.setState({ tabs: { t1: tabOf('t1', 'h1', 'ON') } })
    useLocalProfilesStore.setState({ parkedMaster: { workspaces: [], tabs: { t2: tabOf('t2', 'h1', 'PARKED') }, activeWorkspaceId: null, activeTabId: null }, slaves: {} })
    stop = startSessionRootsLifecycle()
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS)
    expect(rootOf('h1', 'ON')).toBe('R')
    expect(rootOf('h1', 'PARKED')).toBe('R')
    expect(rootOf('h1', 'GONE')).toBeNull()
    useLocalProfilesStore.setState({ parkedMaster: null })
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS)
    expect(rootOf('h1', 'PARKED')).toBeNull() // the world went: its entries go with it
    expect(rootOf('h1', 'ON')).toBe('R')
  })

  it('a plain tab close (the profiles store untouched) drops the closed session\'s entry once things settle', () => {
    useRootCacheStore.getState().ingest('h1', [q('ON', 'R'), q('CLOSED', 'R')])
    useTabStore.setState({ tabs: { t1: tabOf('t1', 'h1', 'ON'), t2: tabOf('t2', 'h1', 'CLOSED') } })
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS)
    useTabStore.setState({ tabs: { t1: tabOf('t1', 'h1', 'ON') } })
    expect(rootOf('h1', 'CLOSED')).toBe('R') // not at once
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS)
    expect(rootOf('h1', 'CLOSED')).toBeNull()
    expect(rootOf('h1', 'ON')).toBe('R')
  })

  it('another window\'s switch that has reached only some of the stores holds the prune back, however long it takes, until the world is settled', () => {
    useRootCacheStore.getState().ingest('h1', [q('NEXT', 'R'), q('GONE', 'R')])
    const world = { workspaces: [], tabs: { t2: tabOf('t2', 'h1', 'NEXT') }, activeWorkspaceId: null, activeTabId: null }
    settled.value = false // the profiles store has the switch, the tab store still the old epoch
    useLocalProfilesStore.setState({ parkedMaster: null, slaves: {} })
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS * 5)
    expect(rootOf('h1', 'NEXT')).toBe('R')
    expect(rootOf('h1', 'GONE')).toBe('R') // nothing is pruned while unsettled
    useTabStore.setState({ tabs: world.tabs })
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS * 5)
    expect(rootOf('h1', 'GONE')).toBe('R')
    expect(vi.getTimerCount()).toBe(0) // a world that never settles keeps nothing waking
    settled.value = true
    useWorkspaceStore.setState({ worldEpoch: 'e-settled' } as never) // the third store arrives
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS * 2)
    expect(rootOf('h1', 'NEXT')).toBe('R')
    expect(rootOf('h1', 'GONE')).toBeNull()
  })

  it('does not prune in the middle of a world switch: the world about to be shown is neither parked nor on screen for a moment', () => {
    useRootCacheStore.getState().ingest('h1', [q('NEXT', 'R')])
    const world = { workspaces: [], tabs: { t2: tabOf('t2', 'h1', 'NEXT') }, activeWorkspaceId: null, activeTabId: null }
    useLocalProfilesStore.setState({ parkedMaster: world, slaves: {} })
    stop()
    stop = startSessionRootsLifecycle()
    // the switch: the profiles store takes the world out of the parked ones first, the tab store shows it after
    useLocalProfilesStore.setState({ parkedMaster: null })
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS / 2)
    useTabStore.setState({ tabs: world.tabs })
    vi.advanceTimersByTime(ROOT_PRUNE_SETTLE_MS)
    expect(rootOf('h1', 'NEXT')).toBe('R')
  })
})
