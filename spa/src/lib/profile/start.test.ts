// spa/src/lib/profile/start.test.ts — the wiring of Profile Sync (P2b plan Task 11).
// The parts are fakes that record what was asked of them; the zustand stores
// are the real ones. The iron rule has a second, unmocked proof in
// start.ironrule.test.ts.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

interface FakeExecutor {
  deps: {
    hostId: string
    profileId: string
    isLeader: () => boolean
    isReachable: () => boolean
    autoSync: () => boolean
    onProblem: (p: { kind: string; section?: string; detail: string }) => void
    onStatus: (s: unknown) => void
    initialDirection: () => 'push' | 'pull' | null
    onInitialSettled: () => void
  }
  onSection: ReturnType<typeof vi.fn>
  onRemoteEvent: ReturnType<typeof vi.fn>
  onReconnected: ReturnType<typeof vi.fn>
  syncNow: ReturnType<typeof vi.fn>
  resolve: ReturnType<typeof vi.fn>
  status: ReturnType<typeof vi.fn>
  dispose: ReturnType<typeof vi.fn>
}
interface FakeCollector {
  opts: { onSection: (r: unknown) => void; onProblem: (p: { kind: string; detail: string }) => void }
  primeAll: ReturnType<typeof vi.fn>
  stop: ReturnType<typeof vi.fn>
  releasePrime: () => void
}
interface FakeLeadership {
  isLeader: ReturnType<typeof vi.fn>
  onChange: (cb: (l: boolean) => void) => () => void
  stop: ReturnType<typeof vi.fn>
  listeners: Set<(l: boolean) => void>
  set: (leader: boolean) => void
}

const h = vi.hoisted(() => ({
  order: [] as string[],
  executors: [] as FakeExecutor[],
  collectors: [] as FakeCollector[],
  leaderships: [] as FakeLeadership[],
  wsListeners: new Set<(e: unknown) => void>(),
  unsyncedStops: [] as Array<ReturnType<typeof vi.fn>>,
  initialLeader: true,
  holdPrime: false,
  persisted: true,
  lease: null as { windowId: string; expiresAt: number } | null,
}))

vi.mock('./executor', () => ({
  createExecutor: vi.fn((deps: FakeExecutor['deps']) => {
    h.order.push('executor')
    const e: FakeExecutor = {
      deps,
      onSection: vi.fn(),
      onRemoteEvent: vi.fn(),
      onReconnected: vi.fn(() => h.order.push('onReconnected')),
      syncNow: vi.fn(),
      resolve: vi.fn(),
      status: vi.fn(() => ({ profile: 'synced', schemaLock: null, sections: {}, locks: {} })),
      dispose: vi.fn(),
    }
    h.executors.push(e)
    return e
  }),
}))

vi.mock('./collector', () => ({
  startCollector: vi.fn((opts: FakeCollector['opts']) => {
    h.order.push('collector')
    let release: () => void = () => {}
    const gate = h.holdPrime ? new Promise<void>((r) => (release = r)) : Promise.resolve()
    const c: FakeCollector = {
      opts,
      primeAll: vi.fn(() => {
        h.order.push('primeAll')
        return gate.then(() => void h.order.push('primed'))
      }),
      stop: vi.fn(),
      releasePrime: () => release(),
    }
    h.collectors.push(c)
    return c
  }),
  watchUnsyncedStores: vi.fn(() => {
    const stop = vi.fn()
    h.unsyncedStops.push(stop)
    return stop
  }),
}))

vi.mock('./leader', () => ({
  contendForLeadership: vi.fn(() => {
    let leader = h.initialLeader
    const listeners = new Set<(l: boolean) => void>()
    const l: FakeLeadership = {
      isLeader: vi.fn(() => leader),
      onChange: (cb) => {
        listeners.add(cb)
        return () => listeners.delete(cb)
      },
      stop: vi.fn(),
      listeners,
      set: (v) => {
        leader = v
        for (const cb of [...listeners]) cb(v)
      },
    }
    h.leaderships.push(l)
    return l
  }),
  leaderWindowId: () => 'w-test',
  readLeaderLease: () => h.lease,
}))

vi.mock('./profile-ws-dispatch', () => ({
  subscribeProfileEvents: vi.fn((fn: (e: unknown) => void) => {
    h.order.push('ws')
    h.wsListeners.add(fn)
    return () => h.wsListeners.delete(fn)
  }),
}))

vi.mock('./api', () => ({
  putAttachment: vi.fn(),
  deleteAttachment: vi.fn(),
  listProfiles: vi.fn(),
}))

vi.mock('./section-store', () => ({ clearSectionStore: vi.fn(() => 'ok') }))

vi.mock('../client-identity', () => ({
  getClientId: () => 'client-1',
  isClientIdPersisted: () => h.persisted,
}))

import { useWorkspaceStore } from '../../features/workspace/store'
import { useHostStore } from '../../stores/useHostStore'
import { useLocalProfilesStore } from '../../stores/useLocalProfilesStore'
import { useTabStore } from '../../stores/useTabStore'
import { pendingDetachKey, selectMaster, useProfileStore } from '../../stores/useProfileStore'
import { __resetDefaultDeviceNameForTest, useDeviceNameStore } from '../../stores/useDeviceNameStore'
import { STORAGE_KEYS } from '../storage/keys'
import { deleteAttachment, listProfiles, putAttachment } from './api'
import { startCollector, watchUnsyncedStores } from './collector'
import { createExecutor } from './executor'
import { contendForLeadership } from './leader'
import { subscribeProfileEvents } from './profile-ws-dispatch'
import { clearSectionStore } from './section-store'
import {
  ATTACH_SUSPEND_MS,
  PROBLEM_BUFFER_SIZE,
  __resetProfileSyncForTest,
  attachMaster,
  detachMaster,
  retryPendingDetach,
  profileSyncSnapshot,
  profileSyncState,
  requestResolve,
  requestSyncNow,
  startProfileSync,
  subscribeProfileSync,
} from './start'

const P1 = 'p_000000000001'
const P2 = 'p_000000000002'
/** `ip:port` of every host of this file (see `host`). */
const EP = '100.64.0.9:7860'
const host = (id: string) => ({ id, name: id, ip: '100.64.0.9', port: 7860, token: null, order: 0 })
const connect = (id: string) => useHostStore.getState().setRuntime(id, { status: 'connected' })
const disconnect = (id: string) => useHostStore.getState().setRuntime(id, { status: 'disconnected' })
/** Enough turns for the longest chain: primeAll → the device name's default (its first resolution is a few awaits deep) → the PUT → onReconnected. */
const flush = async (): Promise<void> => {
  for (let i = 0; i < 12; i++) await Promise.resolve()
}
const okAttach = { kind: 'ok', value: { attached: true } } as const
const okDetach = { kind: 'ok', value: { detached: true } } as const
const failed = (reason: string) => ({ kind: 'failed', reason, status: 0, message: reason }) as never

let stop: () => void = () => {}

beforeEach(() => {
  vi.useFakeTimers()
  h.order.length = 0
  h.executors.length = 0
  h.collectors.length = 0
  h.leaderships.length = 0
  h.unsyncedStops.length = 0
  h.wsListeners.clear()
  h.initialLeader = true
  h.holdPrime = false
  h.persisted = true
  h.lease = null
  vi.clearAllMocks()
  vi.mocked(putAttachment).mockReset().mockResolvedValue(okAttach)
  vi.mocked(deleteAttachment).mockReset().mockResolvedValue(okDetach)
  vi.mocked(clearSectionStore).mockReset().mockReturnValue('ok')
  localStorage.clear()
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null, attachGeneration: 0, masterEndpoint: null, suspension: null, pendingDetaches: [] })
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: {} })
  useDeviceNameStore.setState({ deviceName: 'Test device' })
  __resetProfileSyncForTest()
  vi.spyOn(console, 'warn').mockImplementation(() => {})
})

afterEach(() => {
  stop()
  stop = () => {}
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('the iron rule: no master, no Profile Sync', () => {
  it('starts nothing, calls nothing, schedules nothing', async () => {
    stop = startProfileSync()
    connect('h1')
    disconnect('h1')
    connect('h1')
    useProfileStore.getState().setAutoSync(false)
    useProfileStore.getState().setAutoSync(true)
    await flush()
    vi.advanceTimersByTime(1)

    expect(contendForLeadership).not.toHaveBeenCalled()
    expect(startCollector).not.toHaveBeenCalled()
    expect(createExecutor).not.toHaveBeenCalled()
    expect(subscribeProfileEvents).not.toHaveBeenCalled()
    expect(watchUnsyncedStores).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(listProfiles).not.toHaveBeenCalled()
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(vi.getTimerCount()).toBe(0)
    expect(localStorage.getItem(STORAGE_KEYS.PROFILE_LEADER)).toBeNull()
    expect(profileSyncState()).toEqual({ master: null, leader: false, blocked: null, status: null, problems: [] })
  })
})

describe('master set → contend → lead', () => {
  it('builds executor, WS subscription, collector in that order, awaits primeAll, then announces a connected host once', async () => {
    connect('h1')
    stop = startProfileSync()
    expect(contendForLeadership).not.toHaveBeenCalled()

    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(watchUnsyncedStores).toHaveBeenCalledTimes(1)
    // Nothing is announced before primeAll has resolved.
    expect(h.executors[0].onReconnected).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
    await flush()

    expect(h.order).toEqual(['executor', 'ws', 'collector', 'primeAll', 'primed', 'onReconnected'])
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledWith('h1', P1, { clientId: 'client-1', deviceName: 'Test device' })
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('hands the executor live deps, and wires collector and WS into it', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const e = h.executors[0]
    expect(e.deps.hostId).toBe('h1')
    expect(e.deps.profileId).toBe(P1)
    expect(e.deps.isReachable()).toBe(false)
    connect('h1')
    expect(e.deps.isReachable()).toBe(false) // connected, but the attachment is not confirmed yet
    await flush()
    expect(e.deps.isReachable()).toBe(true)
    disconnect('h1')
    expect(e.deps.isReachable()).toBe(false)
    connect('h1')
    await flush()
    expect(e.deps.autoSync()).toBe(true)
    useProfileStore.getState().setAutoSync(false)
    expect(e.deps.autoSync()).toBe(false)
    expect(e.deps.isLeader()).toBe(true)
    h.leaderships[0].isLeader.mockReturnValue(false)
    expect(e.deps.isLeader()).toBe(false)

    const report = { key: 'hosts', hash: 'x', payload: {} }
    h.collectors[0].opts.onSection(report)
    expect(e.onSection).toHaveBeenCalledWith(report)
    const event = { profileId: P1 }
    for (const fn of h.wsListeners) fn(event)
    expect(e.onRemoteEvent).toHaveBeenCalledWith(event)
  })

  it('a master already in the store at start is entered at once', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    stop = startProfileSync()
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(h.collectors).toHaveLength(1)
  })

  it('host not connected yet: nothing announced; the FIRST connect announces; every reconnect announces again', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const e = h.executors[0]
    expect(e.onReconnected).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()

    connect('h2') // another host is nobody's business
    await flush()
    expect(e.onReconnected).not.toHaveBeenCalled()

    connect('h1')
    await flush()
    expect(e.onReconnected).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledTimes(1)

    useHostStore.getState().setRuntime('h1', { latency: 5 }) // still connected: not a reconnect
    await flush()
    expect(e.onReconnected).toHaveBeenCalledTimes(1)

    disconnect('h1')
    connect('h1')
    await flush()
    expect(e.onReconnected).toHaveBeenCalledTimes(2)
    expect(putAttachment).toHaveBeenCalledTimes(2)
  })

  it('NOTHING syncs before the attachment is confirmed: the executor is unreachable and unannounced while the PUT is out', async () => {
    let release: (v: typeof okAttach) => void = () => {}
    vi.mocked(putAttachment).mockReturnValue(new Promise((r) => (release = r)))
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const e = h.executors[0]
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(e.onReconnected).not.toHaveBeenCalled()
    expect(e.deps.isReachable()).toBe(false)

    release(okAttach)
    await flush()
    expect(e.deps.isReachable()).toBe(true)
    expect(e.onReconnected).toHaveBeenCalledTimes(1)
  })

  it('a failed attachment does not start the sync; it is retried 2 s → 4 s → … and the sync starts when it succeeds', async () => {
    vi.mocked(putAttachment).mockResolvedValue(failed('network'))
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const e = h.executors[0]
    expect(e.onReconnected).not.toHaveBeenCalled()
    expect(e.deps.isReachable()).toBe(false)
    expect(e.dispose).not.toHaveBeenCalled()
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['attachment-failed'])

    await vi.advanceTimersByTimeAsync(1_999)
    expect(putAttachment).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(putAttachment).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(3_999)
    expect(putAttachment).toHaveBeenCalledTimes(2)
    vi.mocked(putAttachment).mockResolvedValue(okAttach)
    await vi.advanceTimersByTimeAsync(1)
    expect(putAttachment).toHaveBeenCalledTimes(3)
    expect(e.onReconnected).toHaveBeenCalledTimes(1)
    expect(e.deps.isReachable()).toBe(true)
    await vi.advanceTimersByTimeAsync(120_000)
    expect(putAttachment).toHaveBeenCalledTimes(3)
  })

  it('the retry backoff is capped at 30 s and starts over with a reconnect', async () => {
    vi.mocked(putAttachment).mockResolvedValue(failed('server'))
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    await vi.advanceTimersByTimeAsync(2_000 + 4_000 + 8_000 + 16_000)
    expect(putAttachment).toHaveBeenCalledTimes(5)
    await vi.advanceTimersByTimeAsync(29_999)
    expect(putAttachment).toHaveBeenCalledTimes(5)
    await vi.advanceTimersByTimeAsync(1)
    expect(putAttachment).toHaveBeenCalledTimes(6)

    disconnect('h1')
    await vi.advanceTimersByTimeAsync(120_000)
    expect(putAttachment).toHaveBeenCalledTimes(6) // nobody to tell
    connect('h1')
    await flush()
    expect(putAttachment).toHaveBeenCalledTimes(7)
    await vi.advanceTimersByTimeAsync(2_000)
    expect(putAttachment).toHaveBeenCalledTimes(8)
  })

  it('a lost lease, a detach or a dropped connection while the PUT is out: its answer starts nothing, and no retry is left behind', async () => {
    for (const interrupt of ['lease', 'detach', 'drop'] as const) {
      let release: (v: typeof okAttach) => void = () => {}
      vi.mocked(putAttachment).mockReturnValue(new Promise((r) => (release = r)))
      connect('h1')
      const end = startProfileSync()
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      await flush()
      const e = h.executors[h.executors.length - 1]
      if (interrupt === 'lease') h.leaderships[h.leaderships.length - 1].set(false)
      if (interrupt === 'detach') useProfileStore.getState().clearMaster()
      if (interrupt === 'drop') disconnect('h1')
      release(okAttach)
      await flush()
      expect(e.onReconnected, interrupt).not.toHaveBeenCalled()
      expect(e.deps.isReachable(), interrupt).toBe(false)
      end()
      useProfileStore.getState().clearMaster()
      disconnect('h1')
      vi.advanceTimersByTime(1)
      expect(vi.getTimerCount(), interrupt).toBe(0)
    }
  })

  it('a client id that does not survive a reload: no attachment is written — and so nothing syncs', async () => {
    h.persisted = false
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(putAttachment).not.toHaveBeenCalled()
    expect(h.executors[0].onReconnected).not.toHaveBeenCalled()
    expect(h.executors[0].deps.isReachable()).toBe(false)
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['client-id-not-persisted'])
  })

  it('the master host leaving the store is a problem and nothing more', async () => {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['master-host-removed'])
    expect(useProfileStore.getState().masterHostId).toBe('h1')
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
  })

  it('autoSync off → on makes the executor decide again; on → off and no-ops do not', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const e = h.executors[0]
    useProfileStore.getState().setAutoSync(false)
    expect(e.syncNow).not.toHaveBeenCalled()
    useProfileStore.getState().setAutoSync(true)
    expect(e.syncNow).toHaveBeenCalledTimes(1)
    useProfileStore.getState().setAutoSync(true)
    expect(e.syncNow).toHaveBeenCalledTimes(1)
  })
})

describe('the attachment answers 404: the profile is not there any more', () => {
  const notFound = { kind: 'failed', reason: 'not-found', status: 404, message: 'profile not found' } as never

  async function gone(): Promise<void> {
    vi.mocked(putAttachment).mockResolvedValue(notFound)
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
  }

  it('no retry, no driver, `blocked: profile-gone`, a status that reads locked:reset — and nothing local is touched', async () => {
    await gone()
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.executors[0].onReconnected).not.toHaveBeenCalled()
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.wsListeners.size).toBe(0)
    expect(profileSyncState()).toMatchObject({
      master: { hostId: 'h1', profileId: P1 },
      blocked: 'profile-gone',
      status: { profile: 'locked:reset', schemaLock: null, sections: {}, locks: {} },
    })
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['profile-gone'])

    // five minutes, a reconnect, a lease change: still one request, still one problem
    await vi.advanceTimersByTimeAsync(300_000)
    disconnect('h1')
    connect('h1')
    h.leaderships[0].set(false)
    h.leaderships[0].set(true)
    await flush()
    await vi.advanceTimersByTimeAsync(300_000)
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['profile-gone'])
    vi.advanceTimersByTime(1)
    expect(vi.getTimerCount()).toBe(0)

    // not a detach, not a wipe
    expect(useProfileStore.getState().masterProfileId).toBe(P1)
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(Object.keys(useHostStore.getState().hosts)).toEqual(['h1', 'h2'])
  })

  it('attachMaster to another profile is a way out: a normal start', async () => {
    await gone()
    vi.mocked(putAttachment).mockResolvedValue(okAttach)
    expect(await attachMaster('h1', P2, 'pull')).toEqual({ ok: true })
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].deps.profileId).toBe(P2)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
    expect(profileSyncState().status).toEqual({ profile: 'synced', schemaLock: null, sections: {}, locks: {} })
  })

  it('detachMaster is the other', async () => {
    await gone()
    await detachMaster()
    expect(profileSyncState()).toMatchObject({ master: null, blocked: null, status: null })
  })

  it.each(['unknown-host', 'unauthorized', 'network', 'server'])('%s is NOT that: the host may come back, the token may be fixed — retried as before', async (reason) => {
    vi.mocked(putAttachment).mockResolvedValue(failed(reason))
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    await vi.advanceTimersByTimeAsync(2_000)
    expect(putAttachment).toHaveBeenCalledTimes(2)
    expect(profileSyncState().blocked).toBeNull()
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
  })
})

describe('suspended: an attach is being made somewhere', () => {
  async function leading(): Promise<void> {
    vi.setSystemTime(1_000_000)
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    vi.mocked(putAttachment).mockClear()
    vi.mocked(clearSectionStore).mockClear()
  }

  it('ANOTHER window suspends: this window\'s leader takes its driver down — master, bases and lease stay — and sends nothing', async () => {
    await leading()
    useProfileStore.setState({ suspension: { token: 'other-window', until: 1_030_000 } }) // the rehydrate
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.wsListeners.size).toBe(0)
    expect(profileSyncState()).toMatchObject({ master: { hostId: 'h1', profileId: P1 }, blocked: 'suspended', status: null })
    expect(h.leaderships[0].stop).not.toHaveBeenCalled()
    expect(clearSectionStore).not.toHaveBeenCalled()
    disconnect('h1')
    connect('h1')
    h.leaderships[0].set(false)
    h.leaderships[0].set(true)
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(putAttachment).not.toHaveBeenCalled()

    useProfileStore.setState({ suspension: null }) // that attach failed: as you were
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('EXPIRY: the window that suspended died — nobody lifts it, and at `suspendedUntil` the driver comes back by itself', async () => {
    await leading()
    useProfileStore.setState({ suspension: { token: 'other-window', until: 1_030_000 } })
    await vi.advanceTimersByTimeAsync(29_999)
    expect(h.executors).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    expect(h.executors).toHaveLength(2)
  })

  it('a suspension already expired when the window opens is none; one still running holds the driver back until it ends', async () => {
    vi.setSystemTime(2_000_000)
    connect('h1')
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP, suspension: { token: 'dead-window', until: 1_999_999 } })
    stop = startProfileSync()
    await flush()
    expect(h.executors).toHaveLength(1)
    stop()

    useProfileStore.setState({ suspension: { token: 'dead-window', until: 2_010_000 } })
    stop = startProfileSync()
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(profileSyncState().blocked).toBe('suspended')
    await vi.advanceTimersByTimeAsync(10_000)
    await flush()
    expect(h.executors).toHaveLength(2)
  })

  it('clearMaster while suspended leaves no timer behind', async () => {
    await leading()
    useProfileStore.setState({ suspension: { token: 'other-window', until: 1_030_000 } })
    useProfileStore.getState().clearMaster()
    vi.advanceTimersByTime(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('attachMaster suspends BEFORE its first await: the old driver is down while the attachment PUT is out', async () => {
    await leading()
    let release: (v: typeof okAttach) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const attaching = attachMaster('h1', P1, 'pull')
    await flush() // attach and detach run one at a time: the queue hop, no request in it
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(useProfileStore.getState().suspension).toEqual({ token: expect.stringMatching(/^[0-9a-f]{32}$/), until: 1_000_000 + ATTACH_SUSPEND_MS })
    await flush()
    disconnect('h1')
    connect('h1')
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(putAttachment).toHaveBeenCalledTimes(1) // the attach's own; no driver refreshed anything

    release(okAttach)
    expect(await attaching).toEqual({ ok: true })
    await flush()
    expect(useProfileStore.getState().suspension).toBeNull()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['the PUT fails', () => vi.mocked(putAttachment).mockResolvedValueOnce(failed('network')), 'network'],
    ['the PUT throws', () => vi.mocked(putAttachment).mockRejectedValueOnce(new Error('boom')), 'boom'],
    ['the host leaves the store meanwhile', () => vi.mocked(putAttachment).mockImplementationOnce(async () => {
      useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'] })
      return okAttach
    }), 'unknown-host'],
  ])('attachMaster fails (%s): the suspension is lifted and the OLD mode is back, bases and attachment untouched', async (_label, arrange, reason) => {
    await leading()
    arrange()
    expect(await attachMaster('h2', P2, 'push')).toEqual({ ok: false, reason })
    await flush()
    expect(useProfileStore.getState()).toMatchObject({ masterHostId: 'h1', masterProfileId: P1, suspension: null })
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].deps.profileId).toBe(P1)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('TWO attaches overlap (another window\'s is newer): this one failing does not wake the drivers under it', async () => {
    await leading()
    let release: (v: never) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const mine = attachMaster('h1', P1, 'pull')
    await flush()
    const other = { token: 'window-b', until: 1_000_000 + 25_000 }
    useProfileStore.setState({ suspension: other }) // window B suspended after us (the rehydrate)

    release(failed('network'))
    expect(await mine).toEqual({ ok: false, reason: 'network' })
    await flush()
    expect(useProfileStore.getState().suspension).toEqual(other)
    expect(profileSyncState().blocked).toBe('suspended')
    expect(h.executors).toHaveLength(1) // still standing still

    useProfileStore.getState().resume('window-b') // B failed too
    await flush()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].deps.profileId).toBe(P1)
  })

  it('…and this one SUCCEEDING does not either: the master is ours, the suspension still theirs, no driver until they are done', async () => {
    await leading()
    let release: (v: typeof okAttach) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const mine = attachMaster('h2', P2, 'push')
    await flush()
    const other = { token: 'window-b', until: 1_000_000 + 25_000 }
    useProfileStore.setState({ suspension: other })

    release(okAttach)
    expect(await mine).toEqual({ ok: true })
    await flush()
    expect(useProfileStore.getState()).toMatchObject({ masterHostId: 'h2', masterProfileId: P2, suspension: other })
    expect(profileSyncState().blocked).toBe('suspended')
    expect(h.executors).toHaveLength(1)

    connect('h2')
    useProfileStore.getState().resume('window-b')
    await flush()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].deps.profileId).toBe(P2)
  })

  it('the NEWER attach finishes first and lifts its suspension: ours is still in progress, so the drivers are put back to sleep at once', async () => {
    await leading()
    let release: (v: typeof okAttach) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const mine = attachMaster('h1', P1, 'pull')
    await flush()
    useProfileStore.setState({ suspension: { token: 'window-b', until: 1_000_000 + 25_000 } })
    useProfileStore.getState().resume('window-b') // B is done; nobody holds a suspension — but we are not done
    expect(useProfileStore.getState().suspension).toMatchObject({ until: 1_000_000 + ATTACH_SUSPEND_MS })
    expect(profileSyncState().blocked).toBe('suspended')
    await flush()
    for (const e of h.executors) expect(e.dispose).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledTimes(1) // ours; no driver got as far as its own

    release(okAttach)
    expect(await mine).toEqual({ ok: true })
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('attachMaster suspends IN THE CALL — not one microtask later: the driver is down before the caller gets its promise back', async () => {
    await leading()
    const attaching = attachMaster('h1', P1, 'pull')
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(useProfileStore.getState().suspension).toMatchObject({ until: 1_000_000 + ATTACH_SUSPEND_MS })
    expect(putAttachment).not.toHaveBeenCalled() // the request itself still waits its turn in the queue
    expect(await attaching).toEqual({ ok: true })
  })

  it('a refusal found only when its turn comes (the host left while it was queued) lifts what the call had suspended', async () => {
    await leading()
    const attaching = attachMaster('h2', P2, 'push')
    useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'] })
    expect(await attaching).toEqual({ ok: false, reason: 'unknown-host' })
    await flush()
    expect(useProfileStore.getState().suspension).toBeNull()
    expect(vi.mocked(putAttachment).mock.calls.map((c) => c[1])).toEqual([P1]) // nothing for P2; the old mode, back, refreshes its own
    expect(h.executors).toHaveLength(2) // as you were
  })

  it('a queued attach\'s suspension cannot run out while the one in front of it works (two requests, 20 s each): it is refreshed under ITS token', async () => {
    await leading()
    let releasePut: (v: typeof okAttach) => void = () => {}
    let releaseDelete: (v: typeof okDetach) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (releasePut = r)))
    vi.mocked(deleteAttachment).mockReturnValueOnce(new Promise((r) => (releaseDelete = r)))
    const first = attachMaster('h2', P2, 'push') // another master: a PUT, then the DELETE of the old attachment
    const second = attachMaster('h1', P1, 'pull') // suspends in its call: the newer owner, from t0
    const owner = useProfileStore.getState().suspension?.token
    await vi.advanceTimersByTimeAsync(20_000)
    releasePut(okAttach)
    await flush()
    expect(useProfileStore.getState().suspension).toEqual({ token: owner, until: 1_020_000 + ATTACH_SUSPEND_MS })
    await vi.advanceTimersByTimeAsync(15_000) // t0 + 35 s: the second's own 30 s are over
    expect(profileSyncState().blocked).toBe('suspended')
    expect(h.executors).toHaveLength(1)
    releaseDelete(okDetach)
    expect(await first).toEqual({ ok: true })
    expect(await second).toEqual({ ok: true })
    expect(useProfileStore.getState()).toMatchObject({ masterHostId: 'h1', masterProfileId: P1, suspension: null })
  })

  it('attaches queued in ONE window: the one that runs keeps the suspension alive for the one that waits (it is the newer owner)', async () => {
    await leading()
    let releaseFirst: (v: never) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (releaseFirst = r)))
    const first = attachMaster('h2', P2, 'push')
    const second = attachMaster('h1', P1, 'pull')
    await flush()
    await vi.advanceTimersByTimeAsync(14_000) // the first PUT is slow…
    releaseFirst(failed('timeout')) // …and fails: it must not lift the second's suspension
    expect(await first).toEqual({ ok: false, reason: 'timeout' })
    expect(await second).toEqual({ ok: true })
    await flush()
    expect(useProfileStore.getState()).toMatchObject({ masterHostId: 'h1', masterProfileId: P1, pendingDirection: 'pull', suspension: null })
    // between the two, no driver ever ran
    expect(h.executors.slice(1, -1).every((e) => e.onReconnected.mock.calls.length === 0)).toBe(true)
  })

  it('THE LEADER\'S isReachable READS THE SUSPENSION FROM localStorage — another window\'s attach is seen before its broadcast arrives', async () => {
    await leading()
    await flush()
    const e = h.executors[0]
    expect(e.deps.isReachable()).toBe(true)

    // window B persisted its suspension; this window's store has not been told yet
    const envelope = JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? '{}') as { state: Record<string, unknown> }
    const write = (suspension: unknown): void => localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ ...envelope, state: { ...envelope.state, suspension } }))
    write({ token: 'window-b', until: 1_000_000 + 30_000 })
    expect(useProfileStore.getState().suspension).toBeNull()
    expect(e.deps.isReachable()).toBe(false)

    write({ token: 'window-b', until: 999_999 }) // expired: none
    expect(e.deps.isReachable()).toBe(true)
    write(null)
    expect(e.deps.isReachable()).toBe(true)
    localStorage.setItem(STORAGE_KEYS.PROFILE, '{not json') // unreadable is "not suspended": the store's own sanitiser decides what it is
    expect(e.deps.isReachable()).toBe(true)
  })

  it('a refusal BEFORE any request suspends nothing', async () => {
    await leading()
    expect(await attachMaster('nope', P1, 'pull')).toEqual({ ok: false, reason: 'unknown-host' })
    expect(await attachMaster('h1', P1, 'sideways' as never)).toEqual({ ok: false, reason: 'invalid-direction' })
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
    expect(useProfileStore.getState().suspension).toBeNull()
  })

  it('a first attach (no master yet) has nobody to suspend', async () => {
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    expect(useProfileStore.getState().suspension).toBeNull()
  })
})

describe('an attach overtaken by ANOTHER window (its store has not heard yet: only localStorage has)', () => {
  type Envelope = { state: Record<string, unknown>; version: number }
  const stored = (): Envelope => JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? '{}') as Envelope
  /** What window B persisted. This window's `useProfileStore` is NOT told: the broadcast is still on its way. */
  const otherWindowWrote = (over: Record<string, unknown>): void => {
    const envelope = stored()
    const generation = (envelope.state.attachGeneration as number) + 1
    localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ ...envelope, state: { ...envelope.state, suspension: null, ...over, attachGeneration: generation } }))
  }
  const DETACHED = { masterHostId: null, masterProfileId: null, masterEndpoint: null, pendingDirection: null }

  async function attachingWithThePutOut(hostId: string, profileId: string): Promise<{ result: Promise<unknown>; release: (v: never) => void }> {
    connect('h1')
    connect('h2')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    vi.mocked(putAttachment).mockClear()
    vi.mocked(clearSectionStore).mockClear()
    let release: (v: never) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const result = attachMaster(hostId, profileId, 'pull')
    await flush()
    return { result, release }
  }

  it('DETACH in window B while our PUT is out: no master comes back — nothing cleared, nothing set, our attachment taken down again, `superseded`', async () => {
    const { result, release } = await attachingWithThePutOut('h1', P1)
    otherWindowWrote(DETACHED)
    const generation = stored().state.attachGeneration

    release(okAttach as never)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(deleteAttachment).toHaveBeenCalledTimes(1)
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    // storage still says what window B said — our late writes did not put the master back
    expect(stored().state).toMatchObject({ ...DETACHED, suspension: null, attachGeneration: generation })
    expect(selectMaster(useProfileStore.getState())).toBeNull()
    await flush()
    expect(profileSyncState()).toMatchObject({ master: null, blocked: null })
    expect(h.executors.every((e) => e.dispose.mock.calls.length === 1)).toBe(true)
  })

  it('a newer ATTACH to the SAME profile in window B: the attachment is the one they want — not deleted; ours steps back', async () => {
    const { result, release } = await attachingWithThePutOut('h1', P1)
    otherWindowWrote({ pendingDirection: 'push' })
    release(okAttach as never)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(deleteAttachment).not.toHaveBeenCalled()
    // not OUR commit's clear (that one takes no argument) — only the leader's duty towards THEIR re-attach, once it has heard of it
    expect(vi.mocked(clearSectionStore).mock.calls).toEqual([[P1]])
    expect(useProfileStore.getState().pendingDirection).toBe('push') // theirs
  })

  it('a newer ATTACH to ANOTHER profile in window B: the attachment we have just written is nobody\'s — deleted; their master stands', async () => {
    const { result, release } = await attachingWithThePutOut('h2', P2)
    otherWindowWrote({ masterHostId: 'h1', masterProfileId: 'p_00000000000b', masterEndpoint: EP, pendingDirection: 'push' })
    release(okAttach as never)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(deleteAttachment).toHaveBeenCalledTimes(1)
    expect(deleteAttachment).toHaveBeenCalledWith('h2', P2, 'client-1')
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(useProfileStore.getState().masterProfileId).toBe('p_00000000000b')
  })

  it('a failed take-down of our attachment is a problem, not a different answer', async () => {
    const { result, release } = await attachingWithThePutOut('h1', P1)
    otherWindowWrote(DETACHED)
    vi.mocked(deleteAttachment).mockResolvedValue(failed('network'))
    release(okAttach as never)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(profileSyncState().problems.map((p) => p.kind)).toContain('detach-failed')
  })

  it('overtaken while the OLD attachment was being deleted (the second await): caught there too', async () => {
    const { result, release } = await attachingWithThePutOut('h2', P2)
    let releaseDelete: (v: typeof okDetach) => void = () => {}
    vi.mocked(deleteAttachment).mockReturnValueOnce(new Promise((r) => (releaseDelete = r)))
    release(okAttach as never)
    await flush()
    otherWindowWrote(DETACHED)
    releaseDelete(okDetach)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(selectMaster(useProfileStore.getState())).toBeNull()
    expect(vi.mocked(deleteAttachment).mock.calls.map((c) => c[1])).toEqual([P1, P2])
  })

  it('NOT overtaken by this window\'s own queue: an attach queued behind another one still attaches, and behind a detach too', async () => {
    connect('h1')
    stop = startProfileSync()
    const first = attachMaster('h1', P1, 'pull')
    const second = attachMaster('h1', P2, 'push')
    expect(await first).toEqual({ ok: true })
    expect(await second).toEqual({ ok: true })
    expect(useProfileStore.getState().masterProfileId).toBe(P2)

    const detaching = detachMaster()
    const third = attachMaster('h1', P1, 'pull')
    await detaching
    expect(await third).toEqual({ ok: true })
    expect(useProfileStore.getState().masterProfileId).toBe(P1)
  })

  it('SAME window: detach called while an attach is in progress → detached in the end', async () => {
    const { result, release } = await attachingWithThePutOut('h1', P1)
    const detaching = detachMaster()
    release(okAttach as never)
    await result
    await detaching
    await flush()
    expect(selectMaster(useProfileStore.getState())).toBeNull()
    expect(profileSyncState().master).toBeNull()
    expect(h.executors.every((e) => e.dispose.mock.calls.length === 1)).toBe(true)
  })
})

describe('the master host is edited in place', () => {
  const edit = (over: Record<string, unknown>): void => {
    const { hosts } = useHostStore.getState()
    useHostStore.setState({ hosts: { ...hosts, h1: { ...hosts.h1, ...over } } })
  }

  async function leading(): Promise<void> {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    vi.mocked(putAttachment).mockClear()
    vi.mocked(clearSectionStore).mockClear()
  }

  it.each([
    ['ip', { ip: '100.64.0.77' }],
    ['port', { port: 7999 }],
  ])('%s changed while connected: is this the same daemon? Nobody knows — the driver stops, the master and the bases stay', async (_what, over) => {
    await leading()
    edit(over)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.wsListeners.size).toBe(0)
    expect(profileSyncState().blocked).toBe('master-endpoint-changed')
    expect(profileSyncState().status).toBeNull()
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['master-endpoint-changed'])
    // not a detach, not a wipe
    expect(useProfileStore.getState().masterHostId).toBe('h1')
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(h.leaderships[0].stop).not.toHaveBeenCalled()

    // and it stays stopped: reconnects, more edits, a lease change
    disconnect('h1')
    connect('h1')
    edit({ name: 'renamed' })
    h.leaderships[0].set(false)
    h.leaderships[0].set(true)
    await flush()
    await vi.advanceTimersByTimeAsync(120_000)
    expect(h.executors).toHaveLength(1)
    expect(putAttachment).not.toHaveBeenCalled()
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['master-endpoint-changed'])
  })

  it('changed BACK: a typo corrected — unblocked, a new driver, the bases kept', async () => {
    await leading()
    edit({ ip: '100.64.0.77' })
    edit({ ip: '100.64.0.9' })
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(clearSectionStore).not.toHaveBeenCalled()
  })

  it('only the token changed (a rotation): the same daemon — the driver is rebuilt like a reconnect, the bases kept, the attachment refreshed', async () => {
    await leading()
    edit({ token: 'rotated' })
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(clearSectionStore).not.toHaveBeenCalled()
  })

  it('an edit that touches neither (name, colour, order) is nobody\'s business', async () => {
    await leading()
    edit({ name: 'renamed', order: 3 })
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
    expect(profileSyncState().blocked).toBeNull()
  })

  it('attachMaster — the same master — is the way out: THIS endpoint is the master now, from cleared bases', async () => {
    await leading()
    edit({ ip: '100.64.0.77' })
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    expect(clearSectionStore).toHaveBeenCalled()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('detachMaster is the other way out', async () => {
    await leading()
    edit({ ip: '100.64.0.77' })
    await detachMaster()
    expect(profileSyncState()).toMatchObject({ master: null, blocked: null })
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
  })

  it('attachMaster records the endpoint of THAT moment — the way out of a block moves the home', async () => {
    await leading()
    edit({ ip: '100.64.0.77' })
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    expect(useProfileStore.getState().masterEndpoint).toBe('100.64.0.77:7860')
    await flush()
    expect(profileSyncState().blocked).toBeNull()
    edit({ ip: '100.64.0.9' }) // the OLD address is now the foreign one
    expect(profileSyncState().blocked).toBe('master-endpoint-changed')
  })

  it('a master without an endpoint is no master: nothing is built (only a hand-built state can hold one; storage cannot)', async () => {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: null })
    await flush()
    expect(contendForLeadership).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
    expect(profileSyncState().master).toBeNull()
  })

  it('a follower is blocked too: the lease falling to it builds nothing', async () => {
    h.initialLeader = false
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    edit({ port: 7999 })
    h.leaderships[0].set(true)
    await flush()
    expect(h.executors).toHaveLength(0)
    expect(profileSyncState().blocked).toBe('master-endpoint-changed')
  })
})

describe('leader and follower', () => {
  it('a follower runs watchUnsyncedStores only', async () => {
    h.initialLeader = false
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(watchUnsyncedStores).toHaveBeenCalledTimes(1)
    expect(createExecutor).not.toHaveBeenCalled()
    expect(startCollector).not.toHaveBeenCalled()
    expect(subscribeProfileEvents).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
    expect(profileSyncState().leader).toBe(false)
  })

  it('follower → leader builds; leader → follower disposes everything; leading again builds afresh', async () => {
    h.initialLeader = false
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()

    h.leaderships[0].set(true)
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(h.collectors).toHaveLength(1)
    expect(h.wsListeners.size).toBe(1)
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)

    h.leaderships[0].set(false)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.wsListeners.size).toBe(0)
    // the host watcher is gone too
    disconnect('h1')
    connect('h1')
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledTimes(1)
    // still a follower of the same master: the lease is still contended, the unsynced watcher still runs
    expect(h.leaderships[0].stop).not.toHaveBeenCalled()
    expect(h.unsyncedStops[0]).not.toHaveBeenCalled()

    h.leaderships[0].set(true)
    await flush()
    expect(h.executors).toHaveLength(2)
    expect(h.collectors).toHaveLength(2)
    expect(h.wsListeners.size).toBe(1)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('a repeated onChange(true) does not build a second driver', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    h.leaderships[0].set(true)
    await flush()
    expect(h.executors).toHaveLength(1)
  })
})

describe('teardown', () => {
  it('clearMaster takes everything down and leaves no timer or subscription', async () => {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()

    useProfileStore.getState().clearMaster()
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].listeners.size).toBe(0)
    expect(h.unsyncedStops[0]).toHaveBeenCalledTimes(1)
    expect(h.wsListeners.size).toBe(0)

    disconnect('h1')
    connect('h1')
    useProfileStore.getState().setAutoSync(false)
    useProfileStore.getState().setAutoSync(true)
    await flush()
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
    expect(h.executors[0].syncNow).not.toHaveBeenCalled()
    expect(putAttachment).toHaveBeenCalledTimes(1)
    vi.advanceTimersByTime(1)
    expect(vi.getTimerCount()).toBe(0)
    expect(profileSyncState().master).toBeNull()
  })

  it('a changed master tears the old driver down and builds a new one; the old executor hears nothing more', async () => {
    connect('h1')
    connect('h2')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()

    useProfileStore.getState().setMaster('h2', P2, 'pull', EP)
    await flush()
    const [old, fresh] = h.executors
    expect(old.dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
    expect(fresh.deps.hostId).toBe('h2')
    expect(fresh.deps.profileId).toBe(P2)
    expect(h.wsListeners.size).toBe(1)

    const oldCalls = old.onReconnected.mock.calls.length
    disconnect('h1')
    connect('h1')
    disconnect('h2')
    connect('h2')
    await flush()
    for (const fn of h.wsListeners) fn({ profileId: P2 })
    expect(old.onReconnected).toHaveBeenCalledTimes(oldCalls)
    expect(old.onRemoteEvent).not.toHaveBeenCalled()
    expect(fresh.onReconnected).toHaveBeenCalledTimes(2)
    expect(fresh.onRemoteEvent).toHaveBeenCalledTimes(1)
  })

  it('the same host with another profile is a changed master too', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    useProfileStore.getState().setMaster('h1', P2, 'pull', EP)
    await flush()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.executors[1].deps.profileId).toBe(P2)
  })

  it('an unrelated change of the profile store does not rebuild anything', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    useProfileStore.getState().setAutoSync(false)
    useProfileStore.getState().clearPendingDirection()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP })
    await flush()
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)
  })

  it('interleaving: clearMaster before primeAll resolves → no announcement, no live collector, no host watcher', async () => {
    h.holdPrime = true
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(h.collectors[0].primeAll).toHaveBeenCalledTimes(1)

    useProfileStore.getState().clearMaster()
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    h.collectors[0].releasePrime()
    await flush()

    expect(h.executors[0].onReconnected).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
    disconnect('h1')
    connect('h1')
    expect(h.executors[0].onReconnected).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
  })

  it('interleaving: a lost lease before primeAll resolves is the same', async () => {
    h.holdPrime = true
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    h.leaderships[0].set(false)
    h.collectors[0].releasePrime()
    await flush()
    expect(h.executors[0].onReconnected).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()
  })

  it('a primeAll that rejects is a problem; the driver still announces', async () => {
    connect('h1')
    stop = startProfileSync()
    vi.mocked(startCollector).mockImplementationOnce((opts) => {
      const c = { opts, primeAll: vi.fn(() => Promise.reject(new Error('boom'))), stop: vi.fn(), releasePrime: () => {} }
      h.collectors.push(c as never)
      return c
    })
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['prime-failed'])
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('stop() takes everything down and unsubscribes from the profile store', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    stop()
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
    expect(h.unsyncedStops[0]).toHaveBeenCalledTimes(1)

    useProfileStore.getState().setMaster('h2', P2, 'pull', EP)
    await flush()
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)
    stop() // idempotent
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
  })

  it('another window attaching (a rehydrate of the synced store) puts this window into master mode, and detaching takes it out', async () => {
    stop = startProfileSync()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP })
    await flush()
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)

    useProfileStore.setState({ masterHostId: null, masterProfileId: null, masterEndpoint: null })
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
  })
})

describe('attachMaster', () => {
  it('refuses a client id that is not persisted — before any request', async () => {
    h.persisted = false
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: false, reason: 'client-id-not-persisted' })
    expect(putAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().masterHostId).toBeNull()
  })

  it('refuses an unknown host', async () => {
    expect(await attachMaster('nope', P1, 'pull')).toEqual({ ok: false, reason: 'unknown-host' })
    expect(putAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().masterHostId).toBeNull()
  })

  it('refuses a profile id the store would refuse — before any request', async () => {
    expect(await attachMaster('h1', 'not-a-profile', 'pull')).toEqual({ ok: false, reason: 'invalid-profile-id' })
    expect(putAttachment).not.toHaveBeenCalled()
  })

  it('the host leaves the store while the PUT is out: no master, and the previous master\'s bases are not cleared', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    vi.mocked(putAttachment).mockImplementation(async () => {
      useHostStore.setState({ hosts: { h1: host('h1') }, hostOrder: ['h1'] })
      return okAttach
    })
    expect(await attachMaster('h2', P2, 'pull')).toEqual({ ok: false, reason: 'unknown-host' })
    expect(useProfileStore.getState().masterProfileId).toBe(P1)
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(deleteAttachment).not.toHaveBeenCalled()
  })

  it('a failed putAttachment sets no master and reports the reason', async () => {
    vi.mocked(putAttachment).mockResolvedValue(failed('unauthorized'))
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: false, reason: 'unauthorized' })
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(useProfileStore.getState().masterProfileId).toBeNull()
    expect(clearSectionStore).not.toHaveBeenCalled()
  })

  it('success: the attachment is written first, then the master', async () => {
    vi.mocked(putAttachment).mockImplementation(async () => {
      expect(useProfileStore.getState().masterHostId).toBeNull()
      return okAttach
    })
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    expect(putAttachment).toHaveBeenCalledWith('h1', P1, { clientId: 'client-1', deviceName: 'Test device' })
    expect(useProfileStore.getState().masterHostId).toBe('h1')
    expect(useProfileStore.getState().masterProfileId).toBe(P1)
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
  })

  it('switching master: the old attachment is deleted (best effort) and the section store cleared BEFORE the new master is set', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    vi.mocked(deleteAttachment).mockResolvedValue(failed('network'))
    vi.mocked(clearSectionStore).mockImplementation(() => {
      expect(useProfileStore.getState().masterProfileId).toBe(P1)
      return 'ok'
    })
    expect(await attachMaster('h2', P2, 'pull')).toEqual({ ok: true })
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
    expect(useProfileStore.getState().masterHostId).toBe('h2')
    expect(useProfileStore.getState().masterProfileId).toBe(P2)
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['detach-failed'])
  })

  it('EVERY attach is a new reconciliation — the same master too: the bases are cleared before the master is set, the attachment is not deleted', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    const generation = useProfileStore.getState().attachGeneration
    vi.mocked(clearSectionStore).mockImplementation(() => {
      expect(useProfileStore.getState().attachGeneration).toBe(generation)
      return 'ok'
    })
    expect(await attachMaster('h1', P1, 'push')).toEqual({ ok: true })
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
    expect(useProfileStore.getState().attachGeneration).toBe(generation + 1)
    expect(useProfileStore.getState().pendingDirection).toBe('push')
  })

  it('a first attach clears whatever bases an earlier attachment left behind', async () => {
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
  })

  it('attach → detach issued back to back run in order and leave nothing alive', async () => {
    connect('h1')
    stop = startProfileSync()
    const a = attachMaster('h1', P1, 'pull')
    const d = detachMaster()
    await a
    await d
    await flush()
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    for (const e of h.executors) expect(e.dispose).toHaveBeenCalledTimes(1)
    for (const c of h.collectors) expect(c.stop).toHaveBeenCalledTimes(1)
    for (const l of h.leaderships) expect(l.stop).toHaveBeenCalledTimes(1)
    expect(h.wsListeners.size).toBe(0)
  })
})

describe('the direction of an attach', () => {
  it.each([undefined, null, '', 'both', 'PUSH'])('attachMaster refuses the direction %j — before any request', async (direction) => {
    expect(await attachMaster('h1', P1, direction as never)).toEqual({ ok: false, reason: 'invalid-direction' })
    expect(putAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().masterHostId).toBeNull()
  })

  it.each(['push', 'pull'] as const)('attachMaster(%s) stores it with the master', async (direction) => {
    expect(await attachMaster('h1', P1, direction)).toEqual({ ok: true })
    expect(useProfileStore.getState().pendingDirection).toBe(direction)
  })

  it('the executor reads it live, and settling clears it — the master stays', async () => {
    stop = startProfileSync()
    expect(await attachMaster('h1', P1, 'push')).toEqual({ ok: true })
    await flush()
    const { deps } = h.executors[0]
    expect(deps.initialDirection()).toBe('push')

    deps.onInitialSettled()
    expect(useProfileStore.getState().pendingDirection).toBeNull()
    expect(deps.initialDirection()).toBeNull()
    expect(useProfileStore.getState().masterProfileId).toBe(P1)
    expect(h.executors).toHaveLength(1) // clearing the direction is not a changed master
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
  })

  it('a direction set by ANOTHER window is the one this window\'s leader uses', async () => {
    stop = startProfileSync()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP, pendingDirection: 'pull' })
    await flush()
    expect(h.executors[0].deps.initialDirection()).toBe('pull')
  })

  it('a settle that arrives after the driver was torn down clears nothing', async () => {
    stop = startProfileSync()
    await attachMaster('h1', P1, 'pull')
    await flush()
    const { deps } = h.executors[0]
    await attachMaster('h2', P2, 'push')
    await flush()
    deps.onInitialSettled() // the OLD executor's
    expect(useProfileStore.getState().pendingDirection).toBe('push')
  })

  it('re-attaching the SAME master rebuilds the driver: the old executor is disposed, a new one primes and announces, and it carries the new direction', async () => {
    connect('h1')
    stop = startProfileSync()
    await attachMaster('h1', P1, 'pull')
    await flush()
    h.executors[0].deps.onInitialSettled()
    expect(useProfileStore.getState().pendingDirection).toBeNull()

    expect(await attachMaster('h1', P1, 'push')).toEqual({ ok: true })
    await flush()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
    expect(h.executors[0].syncNow).not.toHaveBeenCalled()
    expect(h.collectors[1].primeAll).toHaveBeenCalledTimes(1)
    expect(h.executors[1].onReconnected).toHaveBeenCalledTimes(1)
    expect(h.executors[1].deps.initialDirection()).toBe('push')
    expect(h.executors[0].deps.initialDirection()).toBeNull() // the old one is out of it

    h.executors[1].deps.onInitialSettled()
    expect(useProfileStore.getState().pendingDirection).toBeNull()
  })

  it('a re-attach made in ANOTHER window: this window\'s LEADER clears the bases it alone writes, then rebuilds', async () => {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    vi.mocked(clearSectionStore).mockClear()
    const { attachGeneration } = useProfileStore.getState()
    vi.mocked(clearSectionStore).mockImplementation(() => {
      expect(h.executors[0].dispose).toHaveBeenCalledTimes(1) // the writer is down before its bases go
      expect(h.executors).toHaveLength(1)
      return 'ok'
    })
    useProfileStore.setState({ pendingDirection: 'push', attachGeneration: attachGeneration + 1 }) // the rehydrate
    await flush()
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
    expect(clearSectionStore).toHaveBeenCalledWith(P1)
    expect(h.executors).toHaveLength(2)
    expect(h.executors[1].deps.initialDirection()).toBe('push')
  })

  it('…while a FOLLOWER re-enters without touching the bases (the leader may already have written new ones)', async () => {
    h.initialLeader = false
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const { attachGeneration } = useProfileStore.getState()
    useProfileStore.setState({ pendingDirection: 'push', attachGeneration: attachGeneration + 1 })
    await flush()
    expect(clearSectionStore).not.toHaveBeenCalled()
    expect(h.leaderships).toHaveLength(2)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
  })

  it('a changed direction ALONE (no new generation) rebuilds nothing and pumps nothing: the executor calls it stale by itself', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    useProfileStore.setState({ pendingDirection: 'push' })
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
  })
})

describe('detachMaster', () => {
  it('stops syncing FIRST: the master and its bases are gone before the daemon is told, however long that takes', async () => {
    connect('h1')
    stop = startProfileSync()
    await attachMaster('h1', P1, 'pull')
    await flush()
    vi.mocked(putAttachment).mockClear()
    let release: (v: typeof okDetach) => void = () => {}
    vi.mocked(deleteAttachment).mockReturnValue(new Promise((r) => (release = r)))

    const detaching = detachMaster()
    await flush()
    // the DELETE is still out — and everything is already down
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(clearSectionStore).toHaveBeenLastCalledWith(P1)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
    disconnect('h1')
    connect('h1')
    await flush()
    expect(putAttachment).not.toHaveBeenCalled()
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)

    release(okDetach)
    await detaching
    expect(useProfileStore.getState().autoSync).toBe(true)
  })

  it('a master another window set while the DELETE was out is not touched by the late continuation', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    let release: (v: typeof okDetach) => void = () => {}
    vi.mocked(deleteAttachment).mockReturnValue(new Promise((r) => (release = r)))
    const detaching = detachMaster()
    await flush()
    useProfileStore.setState({ masterHostId: 'h2', masterProfileId: P2, masterEndpoint: EP, pendingDirection: 'push' })
    vi.mocked(clearSectionStore).mockClear()
    release(okDetach)
    await detaching
    expect(useProfileStore.getState().masterHostId).toBe('h2')
    expect(useProfileStore.getState().pendingDirection).toBe('push')
    expect(clearSectionStore).not.toHaveBeenCalled()
  })

  it('detaches even when the daemon cannot be told — and says so', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    vi.mocked(deleteAttachment).mockResolvedValue(failed('network'))
    await detachMaster()
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['detach-failed'])
  })

  it('detaches even when deleteAttachment throws', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    vi.mocked(deleteAttachment).mockRejectedValue(new Error('boom'))
    await detachMaster()
    expect(useProfileStore.getState().masterHostId).toBeNull()
  })

  it('without a master it does nothing at all', async () => {
    expect(await detachMaster()).toEqual({ ok: true })
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(clearSectionStore).not.toHaveBeenCalled()
  })

  describe('the answer says whether the daemon was told — and a detach it was not told of is remembered', () => {
    it('told → ok, nothing is remembered', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      expect(await detachMaster()).toEqual({ ok: true })
      expect(useProfileStore.getState().pendingDetaches).toEqual([])
    })

    it('not told (a failure) → says so, and remembers WHICH attachment is left, after the master is gone', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      vi.mocked(deleteAttachment).mockResolvedValue(failed('network'))
      expect(await detachMaster()).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'network' })
      expect(useProfileStore.getState().masterHostId).toBeNull()
      expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP, detail: 'network' }])
    })

    it('not told (it threw) → the same', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      vi.mocked(deleteAttachment).mockRejectedValue(new Error('boom'))
      expect(await detachMaster()).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'Error' })
      expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1 }])
    })

    it('404 — the profile, and its attachments with it, is not on the daemon: nothing is left there', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      vi.mocked(deleteAttachment).mockResolvedValue(failed('not-found'))
      expect(await detachMaster()).toEqual({ ok: true })
      expect(useProfileStore.getState().pendingDetaches).toEqual([])
    })

    it('remembering does not write over a master another window set meanwhile (the store is read again first)', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      let release: (v: never) => void = () => {}
      vi.mocked(deleteAttachment).mockReturnValue(new Promise((r) => (release = r)))
      const detaching = detachMaster()
      await flush()
      // Window B attaches h2/P2: in storage, not (yet) in this window's memory.
      const envelope = JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? 'null')
      localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ ...envelope, state: { ...envelope.state, masterHostId: 'h2', masterProfileId: P2, masterEndpoint: EP, pendingDirection: 'push', attachGeneration: envelope.state.attachGeneration + 1 } }))
      release(failed('timeout'))
      await detaching
      const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? 'null').state
      expect(stored.masterHostId).toBe('h2')
      expect(stored.masterProfileId).toBe(P2)
      expect(stored.pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1 }])
    })

    it('attached to the same profile again while the DELETE was out: the attachment is wanted, nothing is remembered', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      let release: (v: never) => void = () => {}
      vi.mocked(deleteAttachment).mockReturnValue(new Promise((r) => (release = r)))
      const detaching = detachMaster()
      await flush()
      useProfileStore.getState().setMaster('h1', P1, 'push', EP)
      release(failed('timeout'))
      await detaching
      expect(useProfileStore.getState().pendingDetaches).toEqual([])
    })
  })
})

describe('retryPendingDetach — the way to get rid of an attachment a failed detach left on the daemon', () => {
  const left = (over: Record<string, unknown> = {}) => useProfileStore.getState().addPendingDetach({ hostId: 'h1', profileId: P1, endpoint: EP, detail: 'network', at: 1, ...over } as never)
  /** The key of `left()`'s record. */
  const KEY = pendingDetachKey({ hostId: 'h1', profileId: P1, endpoint: EP })
  const moveHost = (id: string, ip: string) => useHostStore.setState((s) => ({ hosts: { ...s.hosts, [id]: { ...s.hosts[id], ip } } }))

  it('nothing remembered → nothing asked', async () => {
    expect(await retryPendingDetach(KEY)).toEqual({ ok: true })
    expect(deleteAttachment).not.toHaveBeenCalled()
  })

  it('asks the daemon with what was REMEMBERED — there is no master to ask about any more', async () => {
    left()
    expect(selectMaster(useProfileStore.getState())).toBeNull()
    expect(await retryPendingDetach(KEY)).toEqual({ ok: true })
    expect(deleteAttachment).toHaveBeenCalledTimes(1)
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    expect(useProfileStore.getState().pendingDetaches).toEqual([])
  })

  it('still failing → says so, stays remembered, with the newer reason', async () => {
    left()
    vi.mocked(deleteAttachment).mockResolvedValue(failed('timeout'))
    expect(await retryPendingDetach(KEY)).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'timeout' })
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP, detail: 'timeout' }])
  })

  it('attached to that very profile by now → the attachment is wanted: NOT deleted, and forgotten', async () => {
    left()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP })
    expect(await retryPendingDetach(KEY)).toEqual({ ok: true })
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().pendingDetaches).toEqual([])
  })

  describe('the attachment is on ONE daemon: a retry never follows the host id to another address (review F4)', () => {
    it('the host was re-pointed since → NOTHING is sent, the answer names it, the record stays', async () => {
      left()
      moveHost('h1', '100.64.0.77')
      expect(await retryPendingDetach(KEY)).toEqual({ ok: false, reason: 'endpoint-changed' })
      expect(deleteAttachment).not.toHaveBeenCalled()
      expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP }])
    })

    it('… pointed back → the retry goes out, to the daemon it was always about', async () => {
      left()
      moveHost('h1', '100.64.0.77')
      await retryPendingDetach(KEY)
      moveHost('h1', '100.64.0.9')
      expect(await retryPendingDetach(KEY)).toEqual({ ok: true })
      expect(deleteAttachment).toHaveBeenCalledTimes(1)
      expect(useProfileStore.getState().pendingDetaches).toEqual([])
    })

    it('the host is not in the app any more → its own answer, nothing sent, the record stays', async () => {
      left()
      useHostStore.setState((s) => ({ hosts: Object.fromEntries(Object.entries(s.hosts).filter(([id]) => id !== 'h1')) }))
      expect(await retryPendingDetach(KEY)).toEqual({ ok: false, reason: 'host-gone' })
      expect(deleteAttachment).not.toHaveBeenCalled()
      expect(useProfileStore.getState().pendingDetaches).toHaveLength(1)
    })

    it('a record from before the endpoint was written down (unknown) → never sent on a guess', async () => {
      useProfileStore.setState({ pendingDetaches: [{ hostId: 'h1', profileId: P1, endpoint: null, detail: 'network', at: 1 }] })
      expect(await retryPendingDetach(pendingDetachKey({ hostId: 'h1', profileId: P1, endpoint: null }))).toEqual({ ok: false, reason: 'endpoint-unknown' })
      expect(deleteAttachment).not.toHaveBeenCalled()
      expect(useProfileStore.getState().pendingDetaches).toHaveLength(1)
    })

    it('detachMaster itself: a master whose host was re-pointed (it is `blocked` for that) is not told at the NEW address — remembered with the OLD one', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      moveHost('h1', '100.64.0.77')
      expect(await detachMaster()).toEqual({ ok: false, reason: 'endpoint-changed' })
      expect(deleteAttachment).not.toHaveBeenCalled()
      expect(useProfileStore.getState().masterHostId).toBeNull()
      expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP }])
    })
  })

  describe('what is remembered of a failure is a short reason — never what the transport said', () => {
    const SECRET = 'Authorization: Bearer xyz — DELETE https://100.64.0.9:7860/api/profiles/p_000000000001/attachment?clientId=client-1'
    const nothingLeaked = (r: unknown): void => {
      const everywhere = JSON.stringify(r) + JSON.stringify(useProfileStore.getState().pendingDetaches) + (localStorage.getItem(STORAGE_KEYS.PROFILE) ?? '')
      for (const piece of ['Bearer', 'xyz', 'https://', 'clientId', 'Authorization']) expect(everywhere).not.toContain(piece)
    }

    it('a failure: its class and the HTTP status, not its message', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      vi.mocked(deleteAttachment).mockResolvedValue({ kind: 'failed', reason: 'server', status: 502, message: SECRET } as never)
      const r = await detachMaster()
      expect(r).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'server (HTTP 502)' })
      nothingLeaked(r)
    })

    it('something thrown: the kind of error, not its message', async () => {
      useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
      vi.mocked(deleteAttachment).mockRejectedValue(new TypeError(SECRET))
      const r = await detachMaster()
      expect(r).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'TypeError' })
      nothingLeaked(r)
    })

    it('the retry: the same', async () => {
      left()
      vi.mocked(deleteAttachment).mockResolvedValue({ kind: 'failed', reason: 'unauthorized', status: 401, message: SECRET } as never)
      const r = await retryPendingDetach(KEY)
      expect(r).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'unauthorized (HTTP 401)' })
      nothingLeaked(r)
    })
  })

  it('ONE record at a time (review F3): a retry that gets through clears ITS record and leaves the others; one that does not, updates its own only', async () => {
    left()
    left({ profileId: P2, detail: 'server (HTTP 502)', at: 2 })
    const KEY2 = pendingDetachKey({ hostId: 'h1', profileId: P2, endpoint: EP })
    vi.mocked(deleteAttachment).mockResolvedValueOnce(failed('timeout'))
    expect(await retryPendingDetach(KEY2)).toEqual({ ok: false, reason: 'daemon-not-told', detail: 'timeout' })
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ profileId: P1, detail: 'network' }, { profileId: P2, detail: 'timeout' }])
    expect(await retryPendingDetach(KEY2)).toEqual({ ok: true })
    expect(deleteAttachment).toHaveBeenLastCalledWith('h1', P2, 'client-1')
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ profileId: P1, detail: 'network' }])
  })

  it('a key nothing is remembered under → nothing asked', async () => {
    left()
    expect(await retryPendingDetach('nope')).toEqual({ ok: true })
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().pendingDetaches).toHaveLength(1)
  })

  it('attached to that profile id on ANOTHER daemon by now → the remembered one is still a ghost: deleted where it is, not "wanted"', async () => {
    left({ endpoint: '100.64.0.77:7860' })
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP })
    const r = await retryPendingDetach(pendingDetachKey({ hostId: 'h1', profileId: P1, endpoint: '100.64.0.77:7860' }))
    expect(r).toEqual({ ok: false, reason: 'endpoint-changed' }) // the host is not at that address: not sent — but not forgotten either
    expect(useProfileStore.getState().pendingDetaches).toHaveLength(1)
  })

  it('attached to ANOTHER profile by now → the old one is still deleted', async () => {
    left()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P2, masterEndpoint: EP })
    expect(await retryPendingDetach(KEY)).toEqual({ ok: true })
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    expect(useProfileStore.getState().masterProfileId).toBe(P2)
  })
})

describe('the two best-effort drops inside attachMaster leave no ghost unrecorded (P3d-3)', () => {
  type Envelope = { state: Record<string, unknown>; version: number }
  const stored = (): Envelope => JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? '{}') as Envelope

  it('switching master, the old daemon not told: remembered with the OLD master and the address IT was attached at — after the new master is set', async () => {
    useHostStore.setState({ hosts: { h1: host('h1'), h2: { ...host('h2'), ip: '100.64.0.10' } }, hostOrder: ['h1', 'h2'], runtime: {} })
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    vi.mocked(deleteAttachment).mockResolvedValue({ kind: 'failed', reason: 'server', status: 502, message: 'Bad Gateway: http://100.64.0.9:7860/api/profiles?token=SECRET' } as never)
    expect(await attachMaster('h2', P2, 'pull')).toEqual({ ok: true })
    expect(useProfileStore.getState().masterProfileId).toBe(P2)
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP, detail: 'server (HTTP 502)' }])
    expect(JSON.stringify(useProfileStore.getState().pendingDetaches) + (localStorage.getItem(STORAGE_KEYS.PROFILE) ?? '')).not.toContain('SECRET')
  })

  it('A→B→C, both drops failing (review F3): BOTH ghosts are on record — the second does not write over the first', async () => {
    const P3 = 'p_000000000003'
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    vi.mocked(deleteAttachment).mockResolvedValue(failed('network'))
    expect(await attachMaster('h2', P2, 'pull')).toEqual({ ok: true })
    expect(await attachMaster('h1', P3, 'pull')).toEqual({ ok: true })
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1 }, { hostId: 'h2', profileId: P2 }])
  })

  it('written on top of what STORAGE holds: a record another window added meanwhile is kept', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    let release: (v: never) => void = () => {}
    vi.mocked(deleteAttachment).mockReturnValue(new Promise((r) => (release = r)))
    const detaching = detachMaster()
    await flush()
    const envelope = JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? 'null')
    const theirs = { hostId: 'h2', profileId: P2, endpoint: EP, detail: 'timeout', at: 5 }
    localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ ...envelope, state: { ...envelope.state, pendingDetaches: [theirs] } }))
    release(failed('network'))
    await detaching
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? 'null').state
    expect(stored.pendingDetaches).toMatchObject([theirs, { hostId: 'h1', profileId: P1 }])
  })

  it('switching master, the old daemon told: nothing is remembered', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    expect(await attachMaster('h2', P2, 'pull')).toEqual({ ok: true })
    expect(useProfileStore.getState().pendingDetaches).toEqual([])
  })

  it('switching master away from a host that was re-pointed: the DELETE is NOT sent to the new address, and the old one is remembered', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    useHostStore.setState({ hosts: { h1: { ...host('h1'), ip: '100.64.0.77' }, h2: host('h2') } })
    expect(await attachMaster('h2', P2, 'pull')).toEqual({ ok: true })
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP, detail: 'endpoint-changed' }])
  })

  it('overtaken by a detach elsewhere, and the take-down of what we had just written fails: remembered with the address the PUT went to', async () => {
    connect('h1')
    stop = startProfileSync()
    let release: (v: never) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const result = attachMaster('h1', P1, 'pull')
    await flush()
    const envelope = stored()
    localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ ...envelope, state: { ...envelope.state, suspension: null, attachGeneration: (envelope.state.attachGeneration as number) + 1 } }))
    vi.mocked(deleteAttachment).mockResolvedValue(failed('timeout'))
    release(okAttach as never)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(useProfileStore.getState().pendingDetaches).toMatchObject([{ hostId: 'h1', profileId: P1, endpoint: EP, detail: 'timeout' }])
    expect(selectMaster(useProfileStore.getState())).toBeNull()
  })

  it('overtaken by an attach to the SAME profile elsewhere: the attachment is wanted — nothing sent, nothing remembered', async () => {
    connect('h1')
    stop = startProfileSync()
    let release: (v: never) => void = () => {}
    vi.mocked(putAttachment).mockReturnValueOnce(new Promise((r) => (release = r)))
    const result = attachMaster('h1', P1, 'pull')
    await flush()
    const envelope = stored()
    localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ ...envelope, state: { ...envelope.state, suspension: null, masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP, pendingDirection: 'push', attachGeneration: (envelope.state.attachGeneration as number) + 1 } }))
    release(okAttach as never)
    expect(await result).toEqual({ ok: false, reason: 'superseded' })
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(useProfileStore.getState().pendingDetaches).toEqual([])
  })
})

describe('problems and status', () => {
  it('keeps the latest PROBLEM_BUFFER_SIZE problems, timestamped', async () => {
    vi.setSystemTime(5_000)
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const { onProblem } = h.executors[0].deps
    for (let i = 0; i < PROBLEM_BUFFER_SIZE + 7; i++) onProblem({ kind: 'k', section: 'hosts', detail: `#${i}` })
    const { problems } = profileSyncState()
    expect(problems).toHaveLength(PROBLEM_BUFFER_SIZE)
    expect(problems[0].detail).toBe('#7')
    expect(problems[problems.length - 1]).toEqual({
      kind: 'k',
      section: 'hosts',
      detail: `#${PROBLEM_BUFFER_SIZE + 6}`,
      at: 5_000,
    })
  })

  it('warns once per kind + section; the collector reports into the same buffer', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const { onProblem } = h.executors[0].deps
    onProblem({ kind: 'k', section: 'hosts', detail: 'a' })
    onProblem({ kind: 'k', section: 'hosts', detail: 'b' })
    onProblem({ kind: 'k', section: 'workspaces', detail: 'c' })
    onProblem({ kind: 'k', detail: 'd' })
    h.collectors[0].opts.onProblem({ kind: 'build-failed', detail: 'e' })
    h.collectors[0].opts.onProblem({ kind: 'build-failed', detail: 'f' })
    expect(console.warn).toHaveBeenCalledTimes(4)
    expect(profileSyncState().problems).toHaveLength(6)
  })

  it('state() reports master, leader and the latest status; null status without a driver', async () => {
    h.initialLeader = false
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(profileSyncState()).toMatchObject({ master: { hostId: 'h1', profileId: P1 }, leader: false, status: null })

    h.leaderships[0].set(true)
    await flush()
    expect(profileSyncState().leader).toBe(true)
    expect(profileSyncState().status).toEqual({ profile: 'synced', schemaLock: null, sections: {}, locks: {} })
    const pushed = { profile: 'pending', schemaLock: null, sections: { hosts: 'dirty' } }
    h.executors[0].deps.onStatus(pushed)
    expect(profileSyncState().status).toEqual(pushed)

    h.leaderships[0].set(false)
    expect(profileSyncState().status).toBeNull()
  })
})

describe('the device name of an attachment', () => {
  it('the default name is resolved BEFORE the PUT, on attach and on every announce — and never for a user without a master', async () => {
    const status = vi.fn(async () => ({ hostname: 'mlab.local' }))
    vi.stubGlobal('electronAPI', { localDaemonStatus: status })
    __resetDefaultDeviceNameForTest()
    useDeviceNameStore.setState({ deviceName: null, defaultDeviceName: 'Browser' })
    stop = startProfileSync()
    connect('h1')
    await flush()
    vi.advanceTimersByTime(60_000)
    expect(status).not.toHaveBeenCalled() // THE IRON RULE

    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    await flush()
    expect(status).toHaveBeenCalledTimes(1) // idempotent: the announce that followed did not ask again
    expect(vi.mocked(putAttachment).mock.calls.map((c) => c[2])).toEqual([
      { clientId: 'client-1', deviceName: 'mlab.local' },
      { clientId: 'client-1', deviceName: 'mlab.local' },
    ])
    vi.unstubAllGlobals()
  })

  it('a round that ended while the name was being resolved sends nothing', async () => {
    let answer: (v: { hostname: string }) => void = () => {}
    vi.stubGlobal('electronAPI', { localDaemonStatus: () => new Promise((r) => (answer = r)) })
    __resetDefaultDeviceNameForTest()
    useDeviceNameStore.setState({ deviceName: null, defaultDeviceName: 'Browser' })
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    connect('h1')
    stop = startProfileSync()
    await flush()
    disconnect('h1')
    answer({ hostname: 'mlab.local' })
    await flush()
    expect(putAttachment).not.toHaveBeenCalled()
    vi.unstubAllGlobals()
  })
})

describe('the dev hook', () => {
  it('is a thin layer over the same functions, present only while started', async () => {
    expect(window.__purdexProfileSync).toBeUndefined()
    stop = startProfileSync()
    const hook = window.__purdexProfileSync
    expect(hook).toBeDefined()
    if (!hook) return

    expect(await hook.attach('h1', P1, 'pull')).toEqual({ ok: true })
    await flush()
    expect(hook.state().master).toEqual({ hostId: 'h1', profileId: P1 })
    hook.syncNow()
    expect(h.executors[0].syncNow).toHaveBeenCalledTimes(1)
    hook.resolve('hosts', 'local')
    expect(h.executors[0].resolve).toHaveBeenCalledWith('hosts', 'local')
    await hook.detach()
    expect(hook.state().master).toBeNull()
    hook.syncNow() // no driver: a no-op, not a throw
    hook.resolve('hosts', 'sot')

    stop()
    expect(window.__purdexProfileSync).toBeUndefined()
  })
})

describe('the dev hook: local profiles (P3b has no UI; the real-machine acceptance drives it from here)', () => {
  const tab = (id: string) => ({ id, pinned: false, locked: false, createdAt: 1, layout: { type: 'leaf' as const, pane: { id: `p-${id}`, content: { kind: 'dashboard' as const } } } })
  const resetWorld = (): void => {
    useLocalProfilesStore.setState({ slaves: {}, slaveOrder: [], activeProfileId: 'master', parkedMaster: null, worldEpoch: 0, master: { name: null } })
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null, visitHistory: [], worldId: 'master', worldEpoch: 0 })
    useWorkspaceStore.setState({ workspaces: [], activeWorkspaceId: null, worldId: 'master', worldEpoch: 0 })
  }
  beforeEach(() => {
    resetWorld()
    useTabStore.setState({ tabs: { t1: tab('t1') }, tabOrder: ['t1'], activeTabId: 't1' })
    useWorkspaceStore.setState({ workspaces: [{ id: 'w1', name: 'Alpha', tabs: ['t1'], activeTabId: 't1' }], activeWorkspaceId: 'w1' })
  })
  afterEach(resetWorld)

  it('setAppearance / appearance: the store\'s setProfileAppearance, for the master and for a slave', () => {
    stop = startProfileSync()
    const profiles = window.__purdexProfileSync?.profiles
    if (!profiles) throw new Error('no dev hook')
    expect(profiles.appearance('master')).toEqual({ name: null })
    expect(profiles.setAppearance('master', { name: 'Work', icon: 'Rocket', color: '#3b82f6' })).toEqual({ ok: true })
    expect(profiles.appearance('master')).toEqual({ name: 'Work', icon: 'Rocket', color: '#3b82f6' })
    expect(profiles.setAppearance('master', { icon: 'NotAnIcon' })).toEqual({ ok: false, reason: 'bad-icon' })

    const copy = profiles.copyMaster('Copy')
    if (!copy.ok) throw new Error(copy.reason)
    expect(profiles.appearance(copy.id)).toEqual({ name: 'Copy' })
    expect(profiles.setAppearance(copy.id, { iconWeight: 'fill', icon: 'Cube' })).toEqual({ ok: true })
    expect(profiles.appearance(copy.id)).toEqual({ name: 'Copy', icon: 'Cube', iconWeight: 'fill' })
    expect(profiles.appearance('nope')).toBeNull()
    expect(profiles.setAppearance('master', { name: null, icon: null, color: null })).toEqual({ ok: true })
  })

  it('list / copyMaster / switch / world / rename / saveScreen / remove / promote are the functions of switch-active.ts', async () => {
    stop = startProfileSync()
    const profiles = window.__purdexProfileSync?.profiles
    expect(profiles).toBeDefined()
    if (!profiles) return

    expect(profiles.list()).toEqual({ active: 'master', slaves: [] })
    expect(profiles.world()).toEqual({ settled: true, onScreen: true, workspaces: ['Alpha'] })

    const copy = profiles.copyMaster('Copy')
    if (!copy.ok) throw new Error(copy.reason)
    expect(profiles.list()).toEqual({ active: 'master', slaves: [{ id: copy.id, name: 'Copy', onScreen: false }] })

    expect(await profiles.switch(copy.id)).toEqual({ ok: true })
    expect(profiles.list()).toEqual({ active: copy.id, slaves: [{ id: copy.id, name: 'Copy', onScreen: true }] })
    expect(profiles.world()).toEqual({ settled: true, onScreen: false, workspaces: ['Alpha'] })
    expect(profiles.rename(copy.id, 'Renamed')).toEqual({ ok: true })
    expect(profiles.remove(copy.id)).toEqual({ ok: false, reason: 'on-screen' })

    const saved = profiles.saveScreen('Saved')
    if (!saved.ok) throw new Error(saved.reason)
    expect(profiles.list().slaves.map((s) => s.name)).toEqual(['Renamed', 'Saved'])
    expect(profiles.remove(saved.id)).toEqual({ ok: true })

    const promoted = await profiles.promote(copy.id, 'Old master')
    expect(promoted.ok).toBe(true)
    expect(profiles.list().active).toBe('master')
    expect(profiles.world()).toEqual({ settled: true, onScreen: true, workspaces: ['Alpha'] })

    useTabStore.setState({ worldEpoch: 99 })
    expect(profiles.world()).toEqual({ settled: false, reason: 'epoch-mismatch' })
  })

  it('outside DEV there is no hook at all', () => {
    vi.stubEnv('DEV', false)
    try {
      stop = startProfileSync()
      expect(window.__purdexProfileSync).toBeUndefined()
    } finally {
      vi.unstubAllEnvs()
    }
  })
})

describe('the status, subscribable and across windows (P3 plan Task 2)', () => {
  const STATUS = STORAGE_KEYS.PROFILE_STATUS
  const CMD = STORAGE_KEYS.PROFILE_COMMAND_PREFIX
  const commandKeys = (): string[] => Object.keys(localStorage).filter((k) => k.startsWith(CMD))
  const PAIR = { localHash: 'L1', sot: { rev: 5, hash: 'S5' } }
  const lockOf = (pair: typeof PAIR) => ({ status: 'locked:conflict' as const, currentHash: pair.localHash, sot: pair.sot, conflict: pair })
  const locked = (pair: typeof PAIR) => ({ profile: 'locked:conflict', schemaLock: null, sections: { hosts: 'locked:conflict' }, locks: { hosts: lockOf(pair) } })
  /** The master this window is on, as sync-status.ts scopes everything: `hostId|profileId|attachGeneration`, from the store. */
  const tag = (): string => {
    const { masterHostId, masterProfileId, attachGeneration } = useProfileStore.getState()
    return `${masterHostId}|${masterProfileId}|${attachGeneration}`
  }
  const cmd = (): string => `${CMD}${encodeURIComponent(tag())}:`

  it('onStatus is re-emitted: subscribers hear it, the snapshot is replaced — and is the SAME object while nothing changes', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const heard = vi.fn()
    const leave = subscribeProfileSync(heard)
    const s0 = profileSyncSnapshot()
    expect(s0).toMatchObject({ master: { hostId: 'h1', profileId: P1 }, leader: true, blocked: null, remote: false, stale: false })
    expect(profileSyncSnapshot()).toBe(s0)
    expect(profileSyncState()).not.toBe(profileSyncState()) // the old accessor is what it was: a fresh object per call

    const pushed = { profile: 'pending', schemaLock: null, sections: { hosts: 'pending' }, locks: {} }
    h.executors[0].deps.onStatus(pushed)
    expect(heard).toHaveBeenCalledTimes(1)
    const s1 = profileSyncSnapshot()
    expect(s1).not.toBe(s0)
    expect(s1.status).toEqual(pushed)
    h.executors[0].deps.onStatus({ ...pushed })
    expect(profileSyncSnapshot()).toBe(s1)
    expect(heard).toHaveBeenCalledTimes(1)

    h.leaderships[0].set(false) // leader
    expect(profileSyncSnapshot()).toMatchObject({ leader: false, status: null })
    h.leaderships[0].set(true)
    h.collectors[1].opts.onProblem({ kind: 'apply-failed', detail: 'x' }) // problems
    expect(profileSyncSnapshot().problems.map((p) => p.kind)).toEqual(['apply-failed'])
    useHostStore.setState({ hosts: { ...useHostStore.getState().hosts, h1: { ...host('h1'), port: 9999 } } }) // blocked
    expect(profileSyncSnapshot()).toMatchObject({ blocked: 'master-endpoint-changed', status: null })
    leave()
  })

  it('the leader publishes {at, leader: windowId, master: <tag>, status, blocked, problems}, throttled', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(localStorage.getItem(STATUS)).toBeNull()
    vi.advanceTimersByTime(250)
    expect(JSON.parse(localStorage.getItem(STATUS) ?? 'null')).toEqual({
      at: expect.any(Number), leader: 'w-test', master: `h1|${P1}|${useProfileStore.getState().attachGeneration}`, status: { profile: 'synced', schemaLock: null, sections: {}, locks: {} }, blocked: null, problems: [],
    })
  })

  it('a follower shows what the leader published, and says when nobody is there to correct it', async () => {
    h.initialLeader = false
    h.lease = { windowId: 'other', expiresAt: Date.now() + 6_000 }
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(profileSyncSnapshot()).toMatchObject({ leader: false, status: null, remote: false })

    const record = { at: Date.now(), leader: 'other', master: tag(), status: locked(PAIR), blocked: null, problems: [{ kind: 'k', detail: 'd', at: 1 }] }
    localStorage.setItem(STATUS, JSON.stringify(record))
    window.dispatchEvent(new StorageEvent('storage', { key: STATUS, newValue: JSON.stringify(record) }))
    expect(profileSyncSnapshot()).toEqual({
      master: { hostId: 'h1', profileId: P1 }, leader: false, blocked: null, status: locked(PAIR), problems: record.problems, remote: true, stale: false,
    })

    h.lease = { windowId: 'other', expiresAt: Date.now() + 60_000 }
    vi.advanceTimersByTime(20_001) // old, but the lease is live (read against the start layer's clock)
    expect(profileSyncSnapshot().stale).toBe(false)
    h.lease = { windowId: 'other', expiresAt: Date.now() - 1 } // an expired lease is no lease
    vi.advanceTimersByTime(10_000)
    expect(profileSyncSnapshot().stale).toBe(true)
  })

  it('requestSyncNow / requestResolve: executed here in the leader, written as ONE KEY EACH in a follower', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    requestSyncNow()
    expect(h.executors[0].syncNow).toHaveBeenCalledTimes(1)
    h.executors[0].status.mockReturnValue(locked(PAIR))
    requestResolve('hosts', 'sot', lockOf({ ...PAIR }))
    expect(h.executors[0].resolve.mock.calls).toEqual([['hosts', 'sot']])
    expect(commandKeys()).toEqual([])

    h.leaderships[0].set(false)
    requestSyncNow()
    requestResolve('hosts', 'local', lockOf(PAIR))
    expect(commandKeys()).toHaveLength(2)
    expect(commandKeys().every((k) => k.startsWith(cmd()))).toBe(true)
    expect(h.executors[0].syncNow).toHaveBeenCalledTimes(1)

    h.leaderships[0].set(true) // takes the lease back: scans what is there
    await flush()
    h.executors[1].status.mockReturnValue(locked(PAIR))
    expect(h.executors[1].syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('a resolve is checked against what the EXECUTOR holds now, not against the last status it announced', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    h.executors[0].deps.onStatus(locked(PAIR)) // what the UI rendered
    h.executors[0].status.mockReturnValue(locked({ ...PAIR, localHash: 'L2' })) // edited since
    requestResolve('hosts', 'local', lockOf(PAIR))
    expect(h.executors[0].resolve).not.toHaveBeenCalled()
    requestResolve('hosts', 'local', lockOf({ ...PAIR, localHash: 'L2' }))
    expect(h.executors[0].resolve).toHaveBeenCalledTimes(1)
  })

  it('a command from another window reaches this leader through the storage event', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const raw = JSON.stringify({ kind: 'syncNow', master: tag(), at: Date.now() })
    localStorage.setItem(`${cmd()}abc`, raw)
    window.dispatchEvent(new StorageEvent('storage', { key: `${cmd()}abc`, newValue: raw }))
    expect(h.executors[0].syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('blocked: the lease holder still answers commands — by removing them', async () => {
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, masterEndpoint: '1.2.3.4:1' })
    stop = startProfileSync()
    await flush()
    expect(profileSyncSnapshot()).toMatchObject({ leader: true, blocked: 'master-endpoint-changed' })
    const raw = JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: lockOf(PAIR), master: tag(), at: Date.now() })
    localStorage.setItem(`${cmd()}abc`, raw)
    expect(() => window.dispatchEvent(new StorageEvent('storage', { key: `${cmd()}abc`, newValue: raw }))).not.toThrow()
    expect(commandKeys()).toEqual([])
    expect(h.executors).toHaveLength(0)
  })

  it('detach: no storage listener, no timer, no status, no command is left — and the snapshot says "no master"', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    stop = startProfileSync()
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    await flush()
    vi.advanceTimersByTime(250)
    expect(localStorage.getItem(STATUS)).not.toBeNull()
    localStorage.setItem(`${cmd()}left`, JSON.stringify({ kind: 'syncNow', master: tag(), at: 0 }))
    h.executors.at(-1)?.deps.onStatus({ profile: 'pending', schemaLock: null, sections: {}, locks: {} }) // a publish is pending
    const added = add.mock.calls.filter(([type]) => type === 'storage').map(([, fn]) => fn)
    expect(added.length).toBeGreaterThan(0)

    await detachMaster()
    vi.advanceTimersByTime(1) // jsdom's own 0 ms timers
    expect(vi.getTimerCount()).toBe(0)
    expect(remove.mock.calls.filter(([type]) => type === 'storage').map(([, fn]) => fn)).toEqual(added)
    expect(Object.keys(localStorage).filter((k) => k === STATUS || k.startsWith(CMD))).toEqual([])
    expect(profileSyncSnapshot()).toEqual({ master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false })
    vi.advanceTimersByTime(60_000)
    expect(localStorage.getItem(STATUS)).toBeNull()
  })

  it('a master replaced by another: the old master’s status and commands do not outlive it — the NEW master’s do', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    vi.advanceTimersByTime(250)
    const oldTag = tag()
    expect(JSON.parse(localStorage.getItem(STATUS) ?? 'null')).toMatchObject({ master: oldTag })
    localStorage.setItem(`${cmd()}left`, JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: lockOf(PAIR), master: oldTag, at: Date.now() }))
    // Another window has switched already and sent this to the new master — before THIS window heard of the switch.
    const nextTag = `h2|${P2}|${useProfileStore.getState().attachGeneration + 1}`
    const theirs = `${CMD}${encodeURIComponent(nextTag)}:theirs`
    localStorage.setItem(theirs, JSON.stringify({ kind: 'syncNow', master: nextTag, at: Date.now() }))
    h.leaderships[0].set(false) // so that nobody here executes it
    h.initialLeader = false
    useProfileStore.getState().setMaster('h2', P2, 'pull', EP)
    await flush()
    expect(tag()).toBe(nextTag) // the channel was reopened for the new (master, generation)
    expect(localStorage.getItem(STATUS)).toBeNull()
    expect(commandKeys()).toEqual([theirs])

    h.leaderships.at(-1)?.set(true) // … and whoever leads the new master executes it
    await flush()
    expect(h.executors.at(-1)?.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('attach again, SAME master: a new generation is a new tag — the old one’s command is not executed by the new driver', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    const oldTag = tag()
    const stale = `${cmd()}stale`
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    expect(tag()).not.toBe(oldTag)
    localStorage.setItem(stale, JSON.stringify({ kind: 'syncNow', master: oldTag, at: Date.now() })) // a window that has not heard
    window.dispatchEvent(new StorageEvent('storage', { key: stale, newValue: localStorage.getItem(stale) }))
    expect(h.executors.at(-1)?.syncNow).not.toHaveBeenCalled()
    vi.advanceTimersByTime(250)
    expect(JSON.parse(localStorage.getItem(STATUS) ?? 'null')).toMatchObject({ master: tag() })
  })

  it('attach again, SAME master: a syncNow this window had sent to the old generation is carried over and executed once — a resolve is not', async () => {
    h.initialLeader = false
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    requestSyncNow() // a follower: written under generation N
    requestResolve('hosts', 'local', lockOf(PAIR))
    const oldPrefix = cmd()
    expect(commandKeys().filter((k) => k.startsWith(oldPrefix))).toHaveLength(2)

    h.initialLeader = true // … and this very window leads generation N+1: no storage event will tell it
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    h.executors.at(-1)?.status.mockReturnValue(locked(PAIR))
    expect(cmd()).not.toBe(oldPrefix)
    expect(h.executors.at(-1)?.syncNow).toHaveBeenCalledTimes(1)
    expect(h.executors.at(-1)?.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it('ANOTHER master: a syncNow sent to the old one is not carried over', async () => {
    h.initialLeader = false
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    requestSyncNow()
    h.initialLeader = true
    useProfileStore.getState().setMaster('h2', P2, 'pull', EP)
    await flush()
    expect(h.executors.at(-1)?.syncNow).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it('stop() with a master: this window lets go, the keys are the other windows’ business', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull', EP)
    await flush()
    vi.advanceTimersByTime(250)
    stop()
    expect(localStorage.getItem(STATUS)).not.toBeNull()
    expect(remove.mock.calls.filter(([type]) => type === 'storage')).toHaveLength(add.mock.calls.filter(([type]) => type === 'storage').length)
    expect(profileSyncSnapshot()).toMatchObject({ leader: false, status: null, remote: false })
  })

  it('no master: asking is a no-op that writes nothing, and a problem still reaches the snapshot', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    stop = startProfileSync()
    const leave = subscribeProfileSync(() => {})
    requestSyncNow()
    requestResolve('hosts', 'sot', lockOf(PAIR))
    vi.advanceTimersByTime(60_000)
    expect(add).not.toHaveBeenCalled()
    expect(setItem.mock.calls.filter(([k]) => String(k).startsWith('purdex-profile-'))).toEqual([])
    expect(vi.getTimerCount()).toBe(0)
    leave()
  })
})
