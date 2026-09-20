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
      status: vi.fn(() => ({ profile: 'synced', schemaLock: null, sections: {} })),
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

import { useHostStore } from '../../stores/useHostStore'
import { useProfileStore } from '../../stores/useProfileStore'
import { useDeviceStateStore } from '../../stores/useDeviceStateStore'
import { STORAGE_KEYS } from '../storage/keys'
import { deleteAttachment, listProfiles, putAttachment } from './api'
import { startCollector, watchUnsyncedStores } from './collector'
import { createExecutor } from './executor'
import { contendForLeadership } from './leader'
import { subscribeProfileEvents } from './profile-ws-dispatch'
import { clearSectionStore } from './section-store'
import {
  PROBLEM_BUFFER_SIZE,
  __resetProfileSyncForTest,
  attachMaster,
  detachMaster,
  profileSyncState,
  startProfileSync,
} from './start'

const P1 = 'p_000000000001'
const P2 = 'p_000000000002'
const host = (id: string) => ({ id, name: id, ip: '100.64.0.9', port: 7860, token: null, order: 0 })
const connect = (id: string) => useHostStore.getState().setRuntime(id, { status: 'connected' })
const disconnect = (id: string) => useHostStore.getState().setRuntime(id, { status: 'disconnected' })
const flush = async (): Promise<void> => {
  for (let i = 0; i < 5; i++) await Promise.resolve()
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
  vi.clearAllMocks()
  vi.mocked(putAttachment).mockReset().mockResolvedValue(okAttach)
  vi.mocked(deleteAttachment).mockReset().mockResolvedValue(okDetach)
  vi.mocked(clearSectionStore).mockReset().mockReturnValue('ok')
  localStorage.clear()
  useProfileStore.setState({ masterHostId: null, masterProfileId: null, autoSync: true, pendingDirection: null })
  useHostStore.setState({ hosts: { h1: host('h1'), h2: host('h2') }, hostOrder: ['h1', 'h2'], runtime: {} })
  useDeviceStateStore.setState({ deviceName: 'Test device' })
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
    expect(profileSyncState()).toEqual({ master: null, leader: false, status: null, problems: [] })
  })
})

describe('master set → contend → lead', () => {
  it('builds executor, WS subscription, collector in that order, awaits primeAll, then announces a connected host once', async () => {
    connect('h1')
    stop = startProfileSync()
    expect(contendForLeadership).not.toHaveBeenCalled()

    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    const e = h.executors[0]
    expect(e.deps.hostId).toBe('h1')
    expect(e.deps.profileId).toBe(P1)
    expect(e.deps.isReachable()).toBe(false)
    connect('h1')
    expect(e.deps.isReachable()).toBe(true)
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    stop = startProfileSync()
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(h.collectors).toHaveLength(1)
  })

  it('host not connected yet: nothing announced; the FIRST connect announces; every reconnect announces again', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    const e = h.executors[0]
    expect(e.onReconnected).not.toHaveBeenCalled()
    expect(putAttachment).not.toHaveBeenCalled()

    connect('h2') // another host is nobody's business
    expect(e.onReconnected).not.toHaveBeenCalled()

    connect('h1')
    expect(e.onReconnected).toHaveBeenCalledTimes(1)
    expect(putAttachment).toHaveBeenCalledTimes(1)

    useHostStore.getState().setRuntime('h1', { latency: 5 }) // still connected: not a reconnect
    expect(e.onReconnected).toHaveBeenCalledTimes(1)

    disconnect('h1')
    connect('h1')
    expect(e.onReconnected).toHaveBeenCalledTimes(2)
    expect(putAttachment).toHaveBeenCalledTimes(2)
  })

  it('a failed attachment refresh is a problem, not a stop', async () => {
    vi.mocked(putAttachment).mockResolvedValue(failed('network'))
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['attachment-refresh-failed'])
  })

  it('never writes a client id that does not survive a reload into an attachment', async () => {
    h.persisted = false
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    expect(putAttachment).not.toHaveBeenCalled()
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['client-id-not-persisted'])
  })

  it('the master host leaving the store is a problem and nothing more', async () => {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    useHostStore.setState({ hosts: { h2: host('h2') }, hostOrder: ['h2'] })
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['master-host-removed'])
    expect(useProfileStore.getState().masterHostId).toBe('h1')
    expect(h.executors[0].dispose).not.toHaveBeenCalled()
  })

  it('autoSync off → on makes the executor decide again; on → off and no-ops do not', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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

describe('leader and follower', () => {
  it('a follower runs watchUnsyncedStores only', async () => {
    h.initialLeader = false
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    h.leaderships[0].set(true)
    await flush()
    expect(h.executors).toHaveLength(1)
  })
})

describe('teardown', () => {
  it('clearMaster takes everything down and leaves no timer or subscription', async () => {
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()

    useProfileStore.getState().setMaster('h2', P2, 'pull')
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
    for (const fn of h.wsListeners) fn({ profileId: P2 })
    expect(old.onReconnected).toHaveBeenCalledTimes(oldCalls)
    expect(old.onRemoteEvent).not.toHaveBeenCalled()
    expect(fresh.onReconnected).toHaveBeenCalledTimes(2)
    expect(fresh.onRemoteEvent).toHaveBeenCalledTimes(1)
  })

  it('the same host with another profile is a changed master too', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    useProfileStore.getState().setMaster('h1', P2, 'pull')
    await flush()
    expect(h.executors).toHaveLength(2)
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.executors[1].deps.profileId).toBe(P2)
  })

  it('an unrelated change of the profile store does not rebuild anything', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1 })
    await flush()
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)
  })

  it('interleaving: clearMaster before primeAll resolves → no announcement, no live collector, no host watcher', async () => {
    h.holdPrime = true
    connect('h1')
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['prime-failed'])
    expect(h.executors[0].onReconnected).toHaveBeenCalledTimes(1)
  })

  it('stop() takes everything down and unsubscribes from the profile store', async () => {
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    stop()
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
    expect(h.collectors[0].stop).toHaveBeenCalledTimes(1)
    expect(h.leaderships[0].stop).toHaveBeenCalledTimes(1)
    expect(h.unsyncedStops[0]).toHaveBeenCalledTimes(1)

    useProfileStore.getState().setMaster('h2', P2, 'pull')
    await flush()
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)
    stop() // idempotent
    expect(h.executors[0].dispose).toHaveBeenCalledTimes(1)
  })

  it('another window attaching (a rehydrate of the synced store) puts this window into master mode, and detaching takes it out', async () => {
    stop = startProfileSync()
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1 })
    await flush()
    expect(contendForLeadership).toHaveBeenCalledTimes(1)
    expect(h.executors).toHaveLength(1)

    useProfileStore.setState({ masterHostId: null, masterProfileId: null })
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
    expect(clearSectionStore).not.toHaveBeenCalled()
  })

  it('switching master: the old attachment is deleted (best effort) and the section store cleared BEFORE the new master is set', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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

  it('re-attaching the same master keeps the section store', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    expect(await attachMaster('h1', P1, 'pull')).toEqual({ ok: true })
    expect(putAttachment).toHaveBeenCalledTimes(1)
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(clearSectionStore).not.toHaveBeenCalled()
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
    useProfileStore.setState({ masterHostId: 'h1', masterProfileId: P1, pendingDirection: 'pull' })
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

  it('re-attaching the same master with a direction starts a new first reconciliation without rebuilding the driver', async () => {
    stop = startProfileSync()
    await attachMaster('h1', P1, 'pull')
    await flush()
    h.executors[0].deps.onInitialSettled()
    expect(await attachMaster('h1', P1, 'push')).toEqual({ ok: true })
    await flush()
    expect(h.executors).toHaveLength(1)
    expect(h.executors[0].deps.initialDirection()).toBe('push')
    expect(h.executors[0].syncNow).toHaveBeenCalled() // pumped: nothing else would make it look
  })
})

describe('detachMaster', () => {
  it('deletes the attachment, clears the master, then the section store', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    vi.mocked(clearSectionStore).mockImplementation(() => {
      expect(useProfileStore.getState().masterHostId).toBeNull()
      return 'ok'
    })
    await detachMaster()
    expect(deleteAttachment).toHaveBeenCalledWith('h1', P1, 'client-1')
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
    expect(useProfileStore.getState().autoSync).toBe(true)
  })

  it('detaches even when the daemon cannot be told — and says so', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    vi.mocked(deleteAttachment).mockResolvedValue(failed('network'))
    await detachMaster()
    expect(useProfileStore.getState().masterHostId).toBeNull()
    expect(clearSectionStore).toHaveBeenCalledTimes(1)
    expect(profileSyncState().problems.map((p) => p.kind)).toEqual(['detach-failed'])
  })

  it('detaches even when deleteAttachment throws', async () => {
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    vi.mocked(deleteAttachment).mockRejectedValue(new Error('boom'))
    await detachMaster()
    expect(useProfileStore.getState().masterHostId).toBeNull()
  })

  it('without a master it does nothing at all', async () => {
    await detachMaster()
    expect(deleteAttachment).not.toHaveBeenCalled()
    expect(clearSectionStore).not.toHaveBeenCalled()
  })
})

describe('problems and status', () => {
  it('keeps the latest PROBLEM_BUFFER_SIZE problems, timestamped', async () => {
    vi.setSystemTime(5_000)
    stop = startProfileSync()
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
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
    useProfileStore.getState().setMaster('h1', P1, 'pull')
    await flush()
    expect(profileSyncState()).toMatchObject({ master: { hostId: 'h1', profileId: P1 }, leader: false, status: null })

    h.leaderships[0].set(true)
    await flush()
    expect(profileSyncState().leader).toBe(true)
    expect(profileSyncState().status).toEqual({ profile: 'synced', schemaLock: null, sections: {} })
    const pushed = { profile: 'pending', schemaLock: null, sections: { hosts: 'dirty' } }
    h.executors[0].deps.onStatus(pushed)
    expect(profileSyncState().status).toEqual(pushed)

    h.leaderships[0].set(false)
    expect(profileSyncState().status).toBeNull()
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
