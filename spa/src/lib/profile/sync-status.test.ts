// The status channel, over the real (jsdom) localStorage and fake timers.
//
// A "window" is its own module graph (`vi.resetModules()` + a fresh import, as in
// leader.test.ts): the cached snapshot is per realm, and that is the one piece of
// module state sync-status.ts has. What a window believes about itself — master,
// leader, blocked, status, problems, the conflict pairs — is a plain object the
// test edits, followed by `refresh()`: exactly what start.ts does.
//
// jsdom does not deliver `storage` events, so a test that wants them calls
// `deliver()`: one event per status / command key that exists, to EVERY window
// (the writer included, which a browser would spare — a harsher test, not a
// weaker one). Writes and deliveries are separate steps on purpose: "two
// followers wrote before the leader looked" is the case the key layout exists for.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import type { SectionConflict } from './sync-state'
import type { ProfileSyncState } from './start'
import type { StatusChannel } from './sync-status'

const STATUS = STORAGE_KEYS.PROFILE_STATUS
const CMD = STORAGE_KEYS.PROFILE_COMMAND_PREFIX
const MASTER = { hostId: 'h1', profileId: 'p_000000000001' }
const SYNCED = { profile: 'synced', schemaLock: null, sections: { hosts: 'synced' }, conflicts: {} } as const
const PENDING = { profile: 'pending', schemaLock: null, sections: { hosts: 'pending' }, conflicts: {} } as const
const PAIR: SectionConflict = { localHash: 'L1', sot: { rev: 5, hash: 'S5' } }

type Mod = typeof import('./sync-status')

interface Win {
  mod: Mod
  channel: StatusChannel
  local: ProfileSyncState
  conflicts: Record<string, SectionConflict | null>
  syncNow: ReturnType<typeof vi.fn>
  resolve: ReturnType<typeof vi.fn>
  /** Edit what this window believes, then tell the channel — as start.ts does. */
  set(patch: Partial<ProfileSyncState>): void
  snapshot(): ReturnType<Mod['profileSyncSnapshot']>
}

const open: StatusChannel[] = []
/** Is anybody holding the lease? Shared by every window of a test, like the storage it stands for. */
let leaseLive = true

async function openWindow(windowId: string, init: Partial<ProfileSyncState> = {}): Promise<Win> {
  vi.resetModules()
  const mod = await import('./sync-status')
  const win = {
    mod,
    local: { master: MASTER, leader: false, blocked: null, status: null, problems: [], ...init } as ProfileSyncState,
    conflicts: {} as Record<string, SectionConflict | null>,
    syncNow: vi.fn(),
    resolve: vi.fn(),
  }
  const channel = mod.openStatusChannel({
    now: () => Date.now(),
    windowId,
    local: () => win.local,
    leaseLive: () => leaseLive,
    syncNow: win.syncNow,
    resolve: win.resolve,
    conflictOf: (section) => win.conflicts[section] ?? null,
  })
  open.push(channel)
  channel.refresh()
  return {
    ...win,
    channel,
    get local() {
      return win.local
    },
    set(patch) {
      win.local = { ...win.local, ...patch }
      channel.refresh()
    },
    snapshot: () => mod.profileSyncSnapshot(),
  }
}

function commandKeys(): string[] {
  return Object.keys(localStorage).filter((k) => k.startsWith(CMD))
}

function fire(key: string): void {
  window.dispatchEvent(new StorageEvent('storage', { key, newValue: localStorage.getItem(key) }))
}

function deliver(): void {
  fire(STATUS)
  for (const key of commandKeys()) fire(key)
}

function published(): Record<string, unknown> | null {
  const raw = localStorage.getItem(STATUS)
  return raw === null ? null : JSON.parse(raw)
}

function statusWrites(setItem: ReturnType<typeof vi.spyOn>): unknown[] {
  return setItem.mock.calls.filter(([k]: [string]) => k === STATUS).map(([, v]: [string, string]) => JSON.parse(v))
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  localStorage.clear()
  leaseLive = true
})

afterEach(() => {
  for (const channel of open.splice(0)) channel.close(false)
  vi.restoreAllMocks()
  vi.useRealTimers()
  vi.resetModules()
})

/* ─── the snapshot ─── */

describe('profileSyncSnapshot — an identity that means something', () => {
  it('without a channel: the same object until something changed, then a new one', async () => {
    vi.resetModules()
    const mod = await import('./sync-status')
    const first = mod.profileSyncSnapshot()
    expect(first).toEqual({ master: null, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false })
    expect(mod.profileSyncSnapshot()).toBe(first)

    mod.setLocalSnapshot({ master: null, leader: false, blocked: null, status: null, problems: [] })
    expect(mod.profileSyncSnapshot()).toBe(first) // fresh objects, equal content: not a change

    mod.setLocalSnapshot({ master: null, leader: false, blocked: null, status: null, problems: [{ kind: 'detach-failed', detail: 'x', at: 1 }] })
    const second = mod.profileSyncSnapshot()
    expect(second).not.toBe(first)
    expect(second.problems).toEqual([{ kind: 'detach-failed', detail: 'x', at: 1 }])
  })

  it('with a channel: a refresh that found nothing new keeps the object; status, leader, blocked and problems each replace it', async () => {
    const w = await openWindow('A', { leader: true, status: SYNCED })
    const s0 = w.snapshot()
    expect(s0).toMatchObject({ master: MASTER, leader: true, status: SYNCED, remote: false, stale: false })
    w.set({ status: { ...SYNCED, sections: { hosts: 'synced' } }, problems: [] }) // equal, freshly built
    w.channel.refresh()
    expect(w.snapshot()).toBe(s0)

    w.set({ status: PENDING })
    const s1 = w.snapshot()
    expect(s1).not.toBe(s0)
    w.set({ problems: [{ kind: 'k', detail: 'd', at: 2 }] })
    const s2 = w.snapshot()
    expect(s2).not.toBe(s1)
    w.set({ blocked: 'suspended' })
    const s3 = w.snapshot()
    expect(s3).not.toBe(s2)
    w.set({ leader: false, status: null })
    expect(w.snapshot()).not.toBe(s3)
  })

  it('subscribers hear changes only, and not after they left', async () => {
    const w = await openWindow('A', { leader: true, status: SYNCED })
    const heard = vi.fn()
    const leave = w.mod.subscribeProfileSync(heard)
    w.channel.refresh()
    expect(heard).not.toHaveBeenCalled()
    w.set({ status: PENDING })
    expect(heard).toHaveBeenCalledTimes(1)
    leave()
    w.set({ status: SYNCED })
    expect(heard).toHaveBeenCalledTimes(1)
  })

  it('a subscriber that throws does not keep the others from hearing', async () => {
    const w = await openWindow('A', { leader: true, status: SYNCED })
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const heard = vi.fn()
    w.mod.subscribeProfileSync(() => {
      throw new Error('boom')
    })
    w.mod.subscribeProfileSync(heard)
    w.set({ status: PENDING })
    expect(heard).toHaveBeenCalledTimes(1)
  })
})

/* ─── the leader publishes ─── */

describe('the leader publishes', () => {
  it('a burst of changes is ONE write, 250 ms after the first, holding the LAST value', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    const w = await openWindow('A', { leader: true, status: SYNCED })
    for (let i = 1; i <= 5; i++) {
      vi.advanceTimersByTime(10)
      w.set({ problems: [{ kind: 'k', detail: String(i), at: i }] })
    }
    vi.advanceTimersByTime(199)
    expect(statusWrites(setItem)).toEqual([])
    vi.advanceTimersByTime(1)
    expect(statusWrites(setItem)).toEqual([
      { at: 1_000_250, leader: 'A', status: SYNCED, blocked: null, problems: [{ kind: 'k', detail: '5', at: 5 }] },
    ])

    vi.advanceTimersByTime(5_000)
    w.channel.refresh() // nothing new: no write, and no timer either
    expect(vi.getTimerCount()).toBe(0)
    w.set({ status: PENDING })
    vi.advanceTimersByTime(250)
    expect(statusWrites(setItem)).toHaveLength(2)
    expect(published()).toMatchObject({ at: 1_005_500, status: PENDING })
  })

  it('a leader that stopped leading inside the 250 ms writes nothing', async () => {
    const w = await openWindow('A', { leader: true, status: SYNCED })
    w.set({ leader: false, status: null })
    vi.advanceTimersByTime(1_000)
    expect(published()).toBeNull()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('a new leader publishes even when the content is what the old one said: the record names its writer', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    expect(published()).toMatchObject({ leader: 'A' })
    const b = await openWindow('B')
    a.set({ leader: false, status: null })
    b.set({ leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    expect(published()).toMatchObject({ leader: 'B', at: 1_000_500 })
  })

  it('the record vanished under a leader (another window cleared it late) → it is published again', async () => {
    const w = await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    localStorage.removeItem(STATUS)
    fire(STATUS)
    vi.advanceTimersByTime(250)
    expect(published()).toMatchObject({ leader: 'A', status: SYNCED })
    expect(w.snapshot().remote).toBe(false)
  })

  it('storage that refuses the write: nothing thrown, and the next change tries again', async () => {
    const w = await openWindow('A', { leader: true, status: SYNCED })
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementationOnce(() => {
      throw new Error('quota')
    })
    expect(() => vi.advanceTimersByTime(250)).not.toThrow()
    expect(published()).toBeNull()
    setItem.mockRestore()
    w.set({ status: PENDING })
    vi.advanceTimersByTime(250)
    expect(published()).toMatchObject({ status: PENDING })
  })
})

/* ─── followers read ─── */

describe('a follower reads what the leader published', () => {
  it('its snapshot is the published one, marked remote — master and `leader: false` are its own', async () => {
    const a = await openWindow('A', { leader: true, status: PENDING, blocked: null, problems: [{ kind: 'k', detail: 'd', at: 7 }] })
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ status: null, remote: false, stale: false }) // nothing published yet
    const heard = vi.fn()
    b.mod.subscribeProfileSync(heard)
    vi.advanceTimersByTime(250)
    deliver()
    expect(b.snapshot()).toEqual({
      master: MASTER, leader: false, blocked: null, status: PENDING, problems: [{ kind: 'k', detail: 'd', at: 7 }], remote: true, stale: false,
    })
    expect(heard).toHaveBeenCalledTimes(1)
    const seen = b.snapshot()
    deliver() // the same record again
    expect(b.snapshot()).toBe(seen)
    expect(a.snapshot()).toMatchObject({ leader: true, remote: false }) // the leader never reads its own record back
  })

  it('the published `blocked` wins over nothing of its own: profile-gone is something only the leader knows', async () => {
    await openWindow('A', { leader: true, blocked: 'profile-gone', status: { profile: 'locked:reset', schemaLock: null, sections: {}, conflicts: {} } })
    const b = await openWindow('B')
    vi.advanceTimersByTime(250)
    deliver()
    expect(b.snapshot()).toMatchObject({ blocked: 'profile-gone', status: { profile: 'locked:reset' }, remote: true })
  })

  it('stale ⇔ older than 10 s AND nobody holds the lease', async () => {
    await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250) // published at 1_000_250
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ remote: true, stale: false })

    // old, but somebody leads: a leader with nothing to say writes nothing — that is not staleness
    vi.setSystemTime(1_000_250 + 60_000)
    b.channel.refresh()
    expect(b.snapshot().stale).toBe(false)
  })

  it('no lease: fresh at exactly 10 s, stale after — and the follower finds out by itself', async () => {
    await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    leaseLive = false
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ remote: true, stale: false })
    const heard = vi.fn()
    b.mod.subscribeProfileSync(heard)
    vi.advanceTimersByTime(10_000 - 1) // the record is now 9_999 ms old … (B opened at its age 0)
    expect(b.snapshot().stale).toBe(false)
    vi.advanceTimersByTime(1)
    expect(b.snapshot().stale).toBe(false) // exactly 10 s: not OLDER than 10 s
    vi.advanceTimersByTime(1)
    expect(b.snapshot().stale).toBe(true)
    expect(heard).toHaveBeenCalledTimes(1)

    leaseLive = true // somebody took over; its first publish is what wakes the follower
    localStorage.setItem(STATUS, JSON.stringify({ at: Date.now(), leader: 'C', status: SYNCED, blocked: null, problems: [] }))
    deliver()
    expect(b.snapshot()).toMatchObject({ remote: true, stale: false })
  })

  it.each([
    ['not JSON', 'nope'],
    ['not an object', '3'],
    ['no `at`', JSON.stringify({ leader: 'A', status: null, blocked: null, problems: [] })],
    ['no `leader`', JSON.stringify({ at: 1, status: null, blocked: null, problems: [] })],
    ['`problems` not an array', JSON.stringify({ at: 1, leader: 'A', status: null, blocked: null, problems: {} })],
    ['`status` not an object', JSON.stringify({ at: 1, leader: 'A', status: 'synced', blocked: null, problems: [] })],
    ['an unknown `blocked`', JSON.stringify({ at: 1, leader: 'A', status: null, blocked: 'whatever', problems: [] })],
  ])('a damaged record (%s) is no record', async (_name, raw) => {
    localStorage.setItem(STATUS, raw)
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ status: null, remote: false, stale: false })
  })

  it('storage that throws on read is no record', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ remote: false })
  })
})

/* ─── commands ─── */

describe('commands reach the leader, one key each', () => {
  it('in the leader window: executed at once, nothing written', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.channel.requestSyncNow()
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('in a follower: one key with {kind, at}; the leader executes it and removes the key', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    const b = await openWindow('B')
    b.channel.requestSyncNow()
    expect(b.syncNow).not.toHaveBeenCalled()
    expect(commandKeys()).toHaveLength(1)
    expect(JSON.parse(localStorage.getItem(commandKeys()[0]) ?? 'null')).toEqual({ kind: 'syncNow', at: 1_000_000 })
    deliver()
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(b.syncNow).not.toHaveBeenCalled() // a follower never executes
    expect(commandKeys()).toEqual([])
  })

  it('TWO followers send before the leader looks → BOTH are executed (why it is a key per command and not an array)', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.conflicts.hosts = PAIR
    const b = await openWindow('B')
    const c = await openWindow('C')
    b.channel.requestSyncNow()
    c.channel.requestResolve('hosts', 'sot', PAIR)
    expect(commandKeys()).toHaveLength(2)
    deliver()
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(a.resolve.mock.calls).toEqual([['hosts', 'sot']])
    expect(commandKeys()).toEqual([])
  })

  it('a command older than 30 s is removed unexecuted; at exactly 30 s it still runs', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    localStorage.setItem(`${CMD}old`, JSON.stringify({ kind: 'syncNow', at: Date.now() - 30_001 }))
    localStorage.setItem(`${CMD}edge`, JSON.stringify({ kind: 'syncNow', at: Date.now() - 30_000 }))
    fire(`${CMD}old`)
    expect(a.syncNow).not.toHaveBeenCalled()
    fire(`${CMD}edge`)
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('a window that has just taken the lease scans what is already there — including what nobody will announce again', async () => {
    const b = await openWindow('B')
    b.conflicts.hosts = PAIR
    const c = await openWindow('C')
    c.channel.requestSyncNow()
    c.channel.requestResolve('hosts', 'local', PAIR)
    localStorage.setItem(`${CMD}old`, JSON.stringify({ kind: 'syncNow', at: Date.now() - 30_001 }))
    expect(commandKeys()).toHaveLength(3)
    b.set({ leader: true, status: SYNCED }) // no storage event is delivered in this test
    expect(b.syncNow).toHaveBeenCalledTimes(1)
    expect(b.resolve.mock.calls).toEqual([['hosts', 'local']])
    expect(commandKeys()).toEqual([])
  })

  it('a window that opens as the leader scans too', async () => {
    localStorage.setItem(`${CMD}x`, JSON.stringify({ kind: 'syncNow', at: Date.now() }))
    const a = await openWindow('A', { leader: true, status: SYNCED })
    expect(a.syncNow).toHaveBeenCalledTimes(1)
  })

  it('the same command delivered twice — and a delivery about a key that is gone — is executed once', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    const b = await openWindow('B')
    b.channel.requestSyncNow()
    const [key] = commandKeys()
    const raw = localStorage.getItem(key)
    fire(key)
    fire(key)
    window.dispatchEvent(new StorageEvent('storage', { key, newValue: raw })) // a late event: the key is gone by now
    expect(a.syncNow).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['not JSON', 'nope'],
    ['an unknown kind', JSON.stringify({ kind: 'wipe', at: 1_000_000 })],
    ['no `at`', JSON.stringify({ kind: 'syncNow' })],
    ['a resolve without a section', JSON.stringify({ kind: 'resolve', keep: 'sot', conflict: null, at: 1_000_000 })],
    ['a resolve with an unknown keep', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'both', conflict: null, at: 1_000_000 })],
    ['a resolve with a damaged conflict', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', conflict: { localHash: 'L1' }, at: 1_000_000 })],
    ['a resolve with NO conflict field', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', at: 1_000_000 })],
  ])('a damaged command (%s) is removed unexecuted', async (_name, raw) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    localStorage.setItem(`${CMD}bad`, raw)
    fire(`${CMD}bad`)
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it('a follower leaves every command alone, expired ones included: the next leader decides', async () => {
    const b = await openWindow('B')
    localStorage.setItem(`${CMD}old`, JSON.stringify({ kind: 'syncNow', at: Date.now() - 60_000 }))
    fire(`${CMD}old`)
    expect(commandKeys()).toEqual([`${CMD}old`])
    expect(b.syncNow).not.toHaveBeenCalled()
  })

  it('an executor that throws does not leave the command behind', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.syncNow.mockImplementation(() => {
      throw new Error('boom')
    })
    vi.spyOn(console, 'error').mockImplementation(() => {})
    localStorage.setItem(`${CMD}x`, JSON.stringify({ kind: 'syncNow', at: Date.now() }))
    expect(() => fire(`${CMD}x`)).not.toThrow()
    expect(commandKeys()).toEqual([])
  })
})

/* ─── resolve is bound to a conflict ─── */

describe('a resolve is bound to the conflict the user was looking at', () => {
  const cases: Array<[string, SectionConflict]> = [
    ['the local side changed under an unchanged revision', { localHash: 'L2', sot: { rev: 5, hash: 'S5' } }],
    ['the SOT revision moved', { localHash: 'L1', sot: { rev: 6, hash: 'S5' } }],
    ['the SOT hash moved', { localHash: 'L1', sot: { rev: 5, hash: 'S6' } }],
  ]

  it.each(cases)('refused, from a follower: %s', async (_name, current) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.conflicts.hosts = current
    const b = await openWindow('B')
    b.channel.requestResolve('hosts', 'local', PAIR)
    deliver()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([]) // dropped, not kept for later
  })

  it.each(cases)('refused, in the leader window itself: %s', async (_name, current) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.conflicts.hosts = current
    a.channel.requestResolve('hosts', 'local', PAIR)
    expect(a.resolve).not.toHaveBeenCalled()
  })

  it('executed when the pair is that one, value for value (not by identity)', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.conflicts.hosts = { localHash: 'L1', sot: { rev: 5, hash: 'S5' } }
    const b = await openWindow('B')
    b.channel.requestResolve('hosts', 'local', PAIR)
    a.channel.requestResolve('hosts', 'sot', { ...PAIR, sot: { ...PAIR.sot } })
    deliver()
    expect(a.resolve.mock.calls).toEqual([['hosts', 'sot'], ['hosts', 'local']])
  })

  it('a null localHash and a null sot.hash are values like any other', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.conflicts['tabs.w1'] = { localHash: null, sot: { rev: 3, hash: 'S3' } }
    a.channel.requestResolve('tabs.w1', 'sot', { localHash: null, sot: { rev: 3, hash: 'S3' } })
    a.channel.requestResolve('tabs.w1', 'sot', { localHash: 'L1', sot: { rev: 3, hash: 'S3' } })
    a.channel.requestResolve('tabs.w1', 'sot', { localHash: null, sot: { rev: 3, hash: null } })
    expect(a.resolve).toHaveBeenCalledTimes(1)
  })

  it('a lock without a pair (locked:reset, locked:invalid) is resolved with `null` — and only while there still is no pair', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.channel.requestResolve('settings', 'local', null)
    expect(a.resolve.mock.calls).toEqual([['settings', 'local']])
    a.conflicts.settings = PAIR // it became a conflict meanwhile
    a.channel.requestResolve('settings', 'local', null)
    expect(a.resolve).toHaveBeenCalledTimes(1)
    a.conflicts.settings = null // the conflict the user saw is gone
    a.channel.requestResolve('settings', 'local', PAIR)
    expect(a.resolve).toHaveBeenCalledTimes(1)
  })
})

/* ─── the end ─── */

describe('close', () => {
  it('close(true) — the master is gone: no listener, no timer, no status, no command', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    const a = await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    localStorage.setItem(`${CMD}left`, JSON.stringify({ kind: 'syncNow', at: Date.now() - 60_000 }))
    localStorage.setItem('purdex-tabs', 'untouched')
    a.set({ status: PENDING }) // a publish is pending
    vi.advanceTimersByTime(1) // jsdom's own 0 ms timers (storage, events)
    expect(vi.getTimerCount()).toBe(1)

    a.channel.close(true)
    vi.advanceTimersByTime(1) // jsdom again: every removal schedules one — 1 ms is far from the 250 of a publish
    expect(vi.getTimerCount()).toBe(0)
    expect(Object.keys(localStorage)).toEqual(['purdex-tabs'])
    const storageListeners = add.mock.calls.filter(([type]) => type === 'storage')
    expect(storageListeners).toHaveLength(1)
    expect(remove.mock.calls.filter(([type]) => type === 'storage').map(([, fn]) => fn)).toEqual(storageListeners.map(([, fn]) => fn))

    // and nothing is heard or done afterwards
    const snapshot = a.snapshot()
    localStorage.setItem(`${CMD}late`, JSON.stringify({ kind: 'syncNow', at: Date.now() }))
    fire(`${CMD}late`)
    a.channel.refresh()
    a.channel.requestSyncNow()
    a.channel.requestResolve('hosts', 'sot', null)
    a.channel.close(true) // twice: harmless, and does not clear what came later
    vi.advanceTimersByTime(60_000)
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([`${CMD}late`])
    expect(published()).toBeNull()
    expect(a.snapshot()).toBe(snapshot)
  })

  it('a follower waiting to call its record stale leaves no timer behind either', async () => {
    await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    leaseLive = false
    const b = await openWindow('B')
    vi.advanceTimersByTime(1) // jsdom's own 0 ms timers
    expect(vi.getTimerCount()).toBe(1)
    b.channel.close(true)
    vi.advanceTimersByTime(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('close(false) — this window stops, the master stays: the keys are the other windows’ business', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    localStorage.setItem(`${CMD}x`, JSON.stringify({ kind: 'syncNow', at: Date.now() }))
    a.channel.close(false)
    vi.advanceTimersByTime(1) // jsdom's own 0 ms timers
    expect(published()).not.toBeNull()
    expect(commandKeys()).toEqual([`${CMD}x`])
    expect(vi.getTimerCount()).toBe(0)
  })
})
