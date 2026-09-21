// spa/src/lib/profile/start.pending-detach.windows.test.ts — two renderers of one client, each with its own module
// graph (`vi.resetModules()`), sharing `localStorage` and ONE Web Locks manager — and hearing NOTHING of each
// other otherwise (no BroadcastChannel, no storage event): the worst case for the list of pending detaches,
// whose every change is "read storage → change → write the whole store back" (review F3, second round).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import { FakeLockManager, navigatorWithLocks } from '../storage/__tests__/fake-web-locks'

vi.mock('../client-identity', () => ({ getClientId: () => 'client-1', isClientIdPersisted: () => true }))

const P1 = 'p_000000000001'
const PX = 'p_00000000000a'
const EP = '100.64.0.9:7860'
const X = { hostId: 'h1', profileId: PX, endpoint: EP, detail: 'network', at: 1 }
const failed = (reason: string) => ({ kind: 'failed', reason, status: 0, message: reason }) as never
const told = { kind: 'ok', value: { detached: true } } as never

async function openWindow() {
  vi.resetModules()
  // `doMock`, per window: a hoisted `vi.mock` of './api' reached the FIRST module graph only — the second window's
  // start.ts got the real api (measured: its DELETE went out and hung for the 15 s timeout).
  const deleteAttachment = vi.fn()
  vi.doMock('./api', () => ({ putAttachment: vi.fn(), deleteAttachment }))
  const [start, store, hosts] = await Promise.all([import('./start'), import('../../stores/useProfileStore'), import('../../stores/useHostStore')])
  hosts.useHostStore.setState({ hosts: { h1: { id: 'h1', name: 'h1', ip: '100.64.0.9', port: 7860, order: 0 } }, hostOrder: ['h1'], runtime: {} })
  return { ...start, useProfileStore: store.useProfileStore, keyOf: store.pendingDetachKey, deleteAttachment }
}

const onDisk = (): Array<{ profileId: string; detail: string }> =>
  (JSON.parse(localStorage.getItem(STORAGE_KEYS.PROFILE) ?? '{"state":{}}') as { state: { pendingDetaches?: Array<{ profileId: string; detail: string }> } }).state.pendingDetaches ?? []

/** Storage as a previous session left it: attached to P1, and X still to be told. */
function seed(): void {
  localStorage.setItem(STORAGE_KEYS.PROFILE, JSON.stringify({ version: 1, state: { masterHostId: 'h1', masterProfileId: P1, masterEndpoint: EP, pendingDirection: null, suspension: null, attachGeneration: 1, autoSync: true, pendingDetaches: [X] } }))
}

class SilentChannel {
  onmessage: unknown = null
  postMessage(): void {}
  addEventListener(): void {}
  removeEventListener(): void {}
  close(): void {}
}

beforeEach(() => {
  localStorage.clear()
  vi.stubGlobal('BroadcastChannel', SilentChannel)
  vi.spyOn(console, 'warn').mockImplementation(() => {})
  seed()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function twoWindows() {
  const a = await openWindow()
  await a.useProfileStore.persist.rehydrate()
  const b = await openWindow()
  await b.useProfileStore.persist.rehydrate()
  return { a, b }
}

describe('with Web Locks: read → merge → write is ONE step across renderers', () => {
  let locks: FakeLockManager
  beforeEach(() => {
    locks = new FakeLockManager()
    vi.stubGlobal('navigator', navigatorWithLocks(locks))
  })

  it('two windows fail at the same moment — A a detach, B a retry: BOTH are on record afterwards', async () => {
    const { a, b } = await twoWindows()
    a.deleteAttachment.mockResolvedValue(failed('server'))
    b.deleteAttachment.mockResolvedValue(failed('timeout'))
    const results = await Promise.all([a.detachMaster(), b.retryPendingDetach(b.keyOf(X))])
    expect(results.map((r) => r.ok)).toEqual([false, false])
    expect(onDisk().map((l) => [l.profileId, l.detail]).sort()).toEqual([[P1, 'server'], [PX, 'timeout']].sort())
    expect(locks.grants.map((g) => g.name)).toEqual(['purdex-pending-detach', 'purdex-pending-detach'])
    expect(locks.grants.every((g) => !(g.returned instanceof Promise))).toBe(true) // the block is synchronous: nothing yields inside the lock
  })

  it('A\'s retry gets through (its record goes) while B fails a detach (one is added): the removal is under the lock too — neither undoes the other', async () => {
    const { a, b } = await twoWindows()
    a.deleteAttachment.mockResolvedValue(told)
    b.deleteAttachment.mockResolvedValue(failed('timeout'))
    await Promise.all([a.retryPendingDetach(a.keyOf(X)), b.detachMaster()])
    expect(onDisk().map((l) => l.profileId)).toEqual([P1])
  })

  it('giving up (`dismissPendingDetach`) likewise: on top of what storage holds, not of what this window remembers', async () => {
    const { a, b } = await twoWindows()
    b.deleteAttachment.mockResolvedValue(failed('timeout'))
    await b.detachMaster() // A has not heard of B's record
    await a.dismissPendingDetach(a.keyOf(X))
    expect(onDisk().map((l) => l.profileId)).toEqual([P1])
  })

  it('the lock is not granted in time (a renderer frozen inside it): the record is written all the same — late and unlocked beats lost', async () => {
    vi.useFakeTimers()
    try {
      const { a } = await twoWindows()
      void locks.request('purdex-pending-detach', {}, () => new Promise(() => {})) // held for ever
      a.deleteAttachment.mockResolvedValue(failed('server'))
      const detaching = a.detachMaster()
      await vi.advanceTimersByTimeAsync(5_000)
      await detaching
      expect(onDisk().map((l) => l.profileId).sort()).toEqual([P1, PX].sort())
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('WITHOUT Web Locks (plain http: no secure context)', () => {
  it('nothing throws and a record is still written — as before this lock existed', async () => {
    expect((navigator as unknown as { locks?: unknown }).locks).toBeUndefined()
    const a = await openWindow()
    await a.useProfileStore.persist.rehydrate()
    a.deleteAttachment.mockResolvedValue(failed('server'))
    await expect(a.detachMaster()).resolves.toMatchObject({ ok: false })
    expect(onDisk().map((l) => l.profileId).sort()).toEqual([P1, PX].sort())
    await expect(a.dismissPendingDetach(a.keyOf(X))).resolves.toBeUndefined()
    expect(onDisk().map((l) => l.profileId)).toEqual([P1])
  })
})
