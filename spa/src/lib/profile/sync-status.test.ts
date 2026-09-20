// The status channel, over the real (jsdom) localStorage and fake timers.
//
// A "window" is its own module graph (`vi.resetModules()` + a fresh import, as in
// leader.test.ts): the cached snapshot is per realm, and that is the one piece of
// module state sync-status.ts has. What a window believes about itself — master,
// leader, blocked, status, problems, the locks the executor holds — is a plain object the
// test edits, followed by `refresh()`: exactly what start.ts does.
//
// jsdom does not deliver `storage` events, so a test that wants them calls
// `deliver()`: one event per status / command key that exists, to EVERY window
// (the writer included, which a browser would spare — a harsher test, not a
// weaker one). Writes and deliveries are separate steps on purpose: "two
// followers wrote before the leader looked" is the case the key layout exists for.
//
// Every channel is opened FOR a master — `masterTag` — and a window of another
// master (one that has not heard of the switch yet, or has heard first) is simply
// a window opened with another tag: `openWindow(id, init, TAG2)`.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import type { SectionLock } from './executor'
import type { SectionConflict } from './sync-state'
import type { ProfileSyncState } from './start'
import type { StatusChannel } from './sync-status'

const STATUS = STORAGE_KEYS.PROFILE_STATUS
const CMD = STORAGE_KEYS.PROFILE_COMMAND_PREFIX
const MASTER = { hostId: 'h1', profileId: 'p_000000000001' }
const MASTER2 = { hostId: 'h2', profileId: 'p_000000000002' }
/** `hostId|profileId|attachGeneration` — written out, not built with the module's helper: the format is pinned here. */
const TAG1 = 'h1|p_000000000001|1'
const TAG2 = 'h2|p_000000000002|2'
/** The command keys of a master: the tag, URI-encoded (no `:` and no `|` left in it), then `:`. */
const cmdOf = (tag: string): string => `${CMD}${encodeURIComponent(tag)}:`
const CMD1 = cmdOf(TAG1)
const CMD2 = cmdOf(TAG2)
const SYNCED = { profile: 'synced', schemaLock: null, sections: { hosts: 'synced' }, locks: {} } as const
const PENDING = { profile: 'pending', schemaLock: null, sections: { hosts: 'pending' }, locks: {} } as const
const PAIR: SectionConflict = { localHash: 'L1', sot: { rev: 5, hash: 'S5' } }
/** A `locked:conflict` section as the executor publishes it: the pair, the live hash, the SOT (which moves with the pair's). */
const conflictLock = (pair: SectionConflict, currentHash: string | null = pair.localHash): SectionLock => ({ status: 'locked:conflict', currentHash, sot: { ...pair.sot }, conflict: pair })
const LOCK = conflictLock(PAIR)
const INVALID: SectionLock = { status: 'locked:invalid', currentHash: 'L1', sot: { rev: 5, hash: 'S5' }, conflict: null }
const syncNowCmd = (tag: string, at: number): string => JSON.stringify({ kind: 'syncNow', master: tag, at })

type Mod = typeof import('./sync-status')

interface Win {
  mod: Mod
  channel: StatusChannel
  local: ProfileSyncState
  locks: Record<string, SectionLock | null>
  syncNow: ReturnType<typeof vi.fn>
  resolve: ReturnType<typeof vi.fn>
  /** Edit what this window believes, then tell the channel — as start.ts does. */
  set(patch: Partial<ProfileSyncState>): void
  snapshot(): ReturnType<Mod['profileSyncSnapshot']>
}

const open: StatusChannel[] = []
/** Is anybody holding the lease? Shared by every window of a test, like the storage it stands for. */
let leaseLive = true

async function openWindow(windowId: string, init: Partial<ProfileSyncState> = {}, masterTag: string = TAG1): Promise<Win> {
  vi.resetModules()
  const mod = await import('./sync-status')
  const win = {
    mod,
    local: { master: MASTER, leader: false, blocked: null, status: null, problems: [], ...init } as ProfileSyncState,
    locks: {} as Record<string, SectionLock | null>,
    syncNow: vi.fn(),
    resolve: vi.fn(),
  }
  const channel = mod.openStatusChannel({
    now: () => Date.now(),
    windowId,
    masterTag,
    local: () => win.local,
    leaseLive: () => leaseLive,
    syncNow: win.syncNow,
    resolve: win.resolve,
    lockOf: (section) => win.locks[section] ?? null,
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
      { at: 1_000_250, leader: 'A', master: TAG1, status: SYNCED, blocked: null, problems: [{ kind: 'k', detail: '5', at: 5 }] },
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
    await openWindow('A', { leader: true, blocked: 'profile-gone', status: { profile: 'locked:reset', schemaLock: null, sections: {}, locks: {} } })
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
    localStorage.setItem(STATUS, JSON.stringify({ at: Date.now(), leader: 'C', master: TAG1, status: SYNCED, blocked: null, problems: [] }))
    deliver()
    expect(b.snapshot()).toMatchObject({ remote: true, stale: false })
  })

  it.each([
    ['not JSON', 'nope'],
    ['not an object', '3'],
    ['no `at`', JSON.stringify({ leader: 'A', master: TAG1, status: SYNCED, blocked: null, problems: [] })],
    ['no `leader`', JSON.stringify({ at: 1, master: TAG1, status: SYNCED, blocked: null, problems: [] })],
    ['no `master`', JSON.stringify({ at: 1, leader: 'A', status: SYNCED, blocked: null, problems: [] })],
    ['`problems` not an array', JSON.stringify({ at: 1, leader: 'A', master: TAG1, status: SYNCED, blocked: null, problems: {} })],
    ['`status` not an object', JSON.stringify({ at: 1, leader: 'A', master: TAG1, status: 'synced', blocked: null, problems: [] })],
    ['an unknown `blocked`', JSON.stringify({ at: 1, leader: 'A', master: TAG1, status: SYNCED, blocked: 'whatever', problems: [] })],
  ])('a damaged record (%s) is no record', async (_name, raw) => {
    localStorage.setItem(STATUS, raw)
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ status: null, remote: false, stale: false })
  })

  it('the control: the same record, undamaged, IS one', async () => {
    localStorage.setItem(STATUS, JSON.stringify({ at: 1, leader: 'A', master: TAG1, status: SYNCED, blocked: null, problems: [] }))
    const b = await openWindow('B')
    expect(b.snapshot()).toMatchObject({ status: SYNCED, remote: true })
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
    expect(commandKeys()[0].startsWith(CMD1)).toBe(true)
    expect(commandKeys()[0].slice(CMD1.length)).toMatch(/^[0-9a-f]{32}$/)
    expect(JSON.parse(localStorage.getItem(commandKeys()[0]) ?? 'null')).toEqual({ kind: 'syncNow', master: TAG1, at: 1_000_000 })
    deliver()
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(b.syncNow).not.toHaveBeenCalled() // a follower never executes
    expect(commandKeys()).toEqual([])
  })

  it('TWO followers send before the leader looks → BOTH are executed (why it is a key per command and not an array)', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.hosts = LOCK
    const b = await openWindow('B')
    const c = await openWindow('C')
    b.channel.requestSyncNow()
    c.channel.requestResolve('hosts', 'sot', LOCK)
    expect(commandKeys()).toHaveLength(2)
    deliver()
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(a.resolve.mock.calls).toEqual([['hosts', 'sot']])
    expect(commandKeys()).toEqual([])
  })

  it('a command older than 30 s is removed unexecuted; at exactly 30 s it still runs', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    localStorage.setItem(`${CMD1}old`, syncNowCmd(TAG1, Date.now() - 30_001))
    localStorage.setItem(`${CMD1}edge`, syncNowCmd(TAG1, Date.now() - 30_000))
    fire(`${CMD1}old`)
    expect(a.syncNow).not.toHaveBeenCalled()
    fire(`${CMD1}edge`)
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('a command from the FUTURE (a clock that was set back) is removed unexecuted; at exactly +5 s it still runs', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    localStorage.setItem(`${CMD1}future`, syncNowCmd(TAG1, Date.now() + 5_001))
    localStorage.setItem(`${CMD1}edge`, syncNowCmd(TAG1, Date.now() + 5_000))
    fire(`${CMD1}future`)
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([`${CMD1}edge`]) // removed, not kept until its time comes
    fire(`${CMD1}edge`)
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('a command from far in the future does not stay valid for as long as the clock is behind it', async () => {
    localStorage.setItem(`${CMD1}future`, syncNowCmd(TAG1, Date.now() + 3_600_000))
    const a = await openWindow('A', { leader: true, status: SYNCED }) // the scan of a new leader
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it('a window that has just taken the lease scans what is already there — including what nobody will announce again', async () => {
    const b = await openWindow('B')
    b.locks.hosts = LOCK
    const c = await openWindow('C')
    c.channel.requestSyncNow()
    c.channel.requestResolve('hosts', 'local', LOCK)
    localStorage.setItem(`${CMD1}old`, syncNowCmd(TAG1, Date.now() - 30_001))
    expect(commandKeys()).toHaveLength(3)
    b.set({ leader: true, status: SYNCED }) // no storage event is delivered in this test
    expect(b.syncNow).toHaveBeenCalledTimes(1)
    expect(b.resolve.mock.calls).toEqual([['hosts', 'local']])
    expect(commandKeys()).toEqual([])
  })

  it('a window that opens as the leader scans too', async () => {
    localStorage.setItem(`${CMD1}x`, syncNowCmd(TAG1, Date.now()))
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
    ['an unknown kind', JSON.stringify({ kind: 'wipe', master: TAG1, at: 1_000_000 })],
    ['no `at`', JSON.stringify({ kind: 'syncNow', master: TAG1 })],
    ['no `master`', JSON.stringify({ kind: 'syncNow', at: 1_000_000 })],
    ['a resolve without a section', JSON.stringify({ kind: 'resolve', keep: 'sot', lock: LOCK, master: TAG1, at: 1_000_000 })],
    ['a resolve with an unknown keep', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'both', lock: LOCK, master: TAG1, at: 1_000_000 })],
    ['a resolve with a damaged conflict', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: { ...LOCK, conflict: { localHash: 'L1' } }, master: TAG1, at: 1_000_000 })],
    ['a resolve with a status that is no lock', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: { ...LOCK, status: 'pending' }, master: TAG1, at: 1_000_000 })],
    ['a resolve with no currentHash', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: { status: LOCK.status, sot: LOCK.sot, conflict: PAIR }, master: TAG1, at: 1_000_000 })],
    ['a resolve with no sot', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: { status: LOCK.status, currentHash: 'L1', conflict: PAIR }, master: TAG1, at: 1_000_000 })],
    ['a resolve with NO lock field', JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', master: TAG1, at: 1_000_000 })],
  ])('a damaged command (%s) is removed unexecuted', async (_name, raw) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.hosts = LOCK
    localStorage.setItem(`${CMD1}bad`, raw)
    fire(`${CMD1}bad`)
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it('the control: the same resolve, undamaged, IS executed', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.hosts = LOCK
    localStorage.setItem(`${CMD1}good`, JSON.stringify({ kind: 'resolve', section: 'hosts', keep: 'sot', lock: LOCK, master: TAG1, at: 1_000_000 }))
    fire(`${CMD1}good`)
    expect(a.resolve.mock.calls).toEqual([['hosts', 'sot']])
  })

  it('a follower leaves every command alone, expired ones included: the next leader decides', async () => {
    const b = await openWindow('B')
    localStorage.setItem(`${CMD1}old`, syncNowCmd(TAG1, Date.now() - 60_000))
    fire(`${CMD1}old`)
    expect(commandKeys()).toEqual([`${CMD1}old`])
    expect(b.syncNow).not.toHaveBeenCalled()
  })

  it('an executor that throws does not leave the command behind', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.syncNow.mockImplementation(() => {
      throw new Error('boom')
    })
    vi.spyOn(console, 'error').mockImplementation(() => {})
    localStorage.setItem(`${CMD1}x`, syncNowCmd(TAG1, Date.now()))
    expect(() => fire(`${CMD1}x`)).not.toThrow()
    expect(commandKeys()).toEqual([])
  })
})

/* ─── resolve is bound to a lock ─── */

describe('a resolve is bound to the lock the user was looking at', () => {
  const cases: Array<[string, SectionLock]> = [
    ['the local side of the pair changed under an unchanged revision', conflictLock({ localHash: 'L2', sot: { rev: 5, hash: 'S5' } }, 'L1')],
    ['the SOT revision moved', conflictLock({ localHash: 'L1', sot: { rev: 6, hash: 'S5' } })],
    ['the SOT hash moved', conflictLock({ localHash: 'L1', sot: { rev: 5, hash: 'S6' } })],
    ['the live content changed under an unchanged pair', conflictLock(PAIR, 'L9')],
  ]

  it.each(cases)('refused, from a follower: %s', async (_name, current) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.hosts = current
    const b = await openWindow('B')
    b.channel.requestResolve('hosts', 'local', LOCK)
    deliver()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([]) // dropped, not kept for later
  })

  it.each(cases)('refused, in the leader window itself: %s', async (_name, current) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.hosts = current
    a.channel.requestResolve('hosts', 'local', LOCK)
    expect(a.resolve).not.toHaveBeenCalled()
  })

  it('executed when the lock is that one, value for value (not by identity)', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.hosts = conflictLock({ localHash: 'L1', sot: { rev: 5, hash: 'S5' } })
    const b = await openWindow('B')
    b.channel.requestResolve('hosts', 'local', LOCK)
    a.channel.requestResolve('hosts', 'sot', conflictLock({ ...PAIR, sot: { ...PAIR.sot } }))
    deliver()
    expect(a.resolve.mock.calls).toEqual([['hosts', 'sot'], ['hosts', 'local']])
  })

  it('a null localHash, a null currentHash and a null sot.hash are values like any other', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks['tabs.w1'] = conflictLock({ localHash: null, sot: { rev: 3, hash: 'S3' } })
    a.channel.requestResolve('tabs.w1', 'sot', conflictLock({ localHash: null, sot: { rev: 3, hash: 'S3' } }))
    a.channel.requestResolve('tabs.w1', 'sot', conflictLock({ localHash: 'L1', sot: { rev: 3, hash: 'S3' } }, null))
    a.channel.requestResolve('tabs.w1', 'sot', conflictLock({ localHash: null, sot: { rev: 3, hash: null } }))
    a.channel.requestResolve('tabs.w1', 'sot', conflictLock({ localHash: null, sot: { rev: 3, hash: 'S3' } }, 'L1'))
    expect(a.resolve).toHaveBeenCalledTimes(1)
  })

  // F3: `locked:invalid` and `locked:reset` have no pair. "No pair then, no pair now" is true of every one of these.
  const pairless: Array<[string, SectionLock | null]> = [
    ['the local content changed while the command waited', { ...INVALID, currentHash: 'L2' }],
    ['the KIND of lock changed', { ...INVALID, status: 'locked:reset' }],
    ['the SOT advanced under the same lock', { ...INVALID, sot: { rev: 6, hash: 'S6' } }],
    ['the section is not locked any more', null],
    ['it became a conflict meanwhile', conflictLock(PAIR)],
  ]

  it.each(pairless)('Keep local under locked:invalid, refused from a follower: %s', async (_name, current) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.settings = INVALID
    const b = await openWindow('B')
    b.channel.requestResolve('settings', 'local', INVALID) // what the user was shown
    a.locks.settings = current // … and what the executor holds by the time the leader looks
    deliver()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it.each(pairless)('Keep local under locked:invalid, refused in the leader window itself: %s', async (_name, current) => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.settings = current
    a.channel.requestResolve('settings', 'local', INVALID)
    expect(a.resolve).not.toHaveBeenCalled()
  })

  it('a lock without a pair that has NOT moved is resolved — from a follower and in the leader', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.settings = INVALID
    const b = await openWindow('B')
    b.channel.requestResolve('settings', 'local', { ...INVALID, sot: { ...INVALID.sot } })
    deliver()
    a.channel.requestResolve('settings', 'local', INVALID)
    expect(a.resolve.mock.calls).toEqual([['settings', 'local'], ['settings', 'local']])
  })

  it('the pair the user saw is gone (the lock is a pairless one now) → refused', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    a.locks.settings = { status: 'locked:reset', currentHash: 'L1', sot: { rev: 5, hash: 'S5' }, conflict: null }
    a.channel.requestResolve('settings', 'local', LOCK)
    expect(a.resolve).not.toHaveBeenCalled()
  })
})

/* ─── one master at a time ─── */

describe('everything is scoped to the master it was made for (hostId|profileId|attachGeneration)', () => {
  it('masterTagOf: the three parts, and a new generation of the SAME master is another tag', async () => {
    const { masterTagOf } = await import('./sync-status')
    expect(masterTagOf(MASTER, 1)).toBe(TAG1)
    expect(masterTagOf(MASTER2, 2)).toBe(TAG2)
    expect(masterTagOf(MASTER, 2)).not.toBe(masterTagOf(MASTER, 1))
  })

  it('F1: a window still on the OLD master does not show the new master’s status as its own — and the other way round', async () => {
    const a = await openWindow('A', { master: MASTER2, leader: true, status: PENDING }, TAG2) // has switched, and leads
    const b = await openWindow('B') // has not heard yet: still P1
    vi.advanceTimersByTime(250)
    deliver()
    expect(published()).toMatchObject({ master: TAG2, status: PENDING })
    expect(b.snapshot()).toEqual({ master: MASTER, leader: false, blocked: null, status: null, problems: [], remote: false, stale: false })
    vi.advanceTimersByTime(60_000)
    expect(b.snapshot()).toMatchObject({ status: null, remote: false, stale: false }) // nor does it turn "stale"

    // the other way round: the OLD master's leader is what is in storage, a window of the new one reads it
    a.channel.close(false)
    localStorage.setItem(STATUS, JSON.stringify({ at: Date.now(), leader: 'B', master: TAG1, status: SYNCED, blocked: null, problems: [] }))
    const c = await openWindow('C', { master: MASTER2 }, TAG2)
    deliver()
    expect(c.snapshot()).toMatchObject({ master: MASTER2, status: null, remote: false })
    expect(b.snapshot()).toMatchObject({ master: MASTER, status: SYNCED, remote: true }) // its own master's: read
  })

  it('F1: a command sent by a window still on the OLD master is not executed by the new master’s leader', async () => {
    const a = await openWindow('A', { master: MASTER2, leader: true, status: SYNCED }, TAG2)
    a.locks.hosts = LOCK
    const b = await openWindow('B') // still P1
    b.channel.requestSyncNow()
    b.channel.requestResolve('hosts', 'local', LOCK)
    const keys = commandKeys()
    expect(keys).toHaveLength(2)
    expect(keys.every((k) => k.startsWith(CMD1))).toBe(true)
    deliver()
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual(keys) // not the new leader's to remove either: they are not expired
  })

  it('F1, defence in depth: a payload of another master under THIS master’s key is removed unexecuted', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED })
    localStorage.setItem(`${CMD1}forged`, syncNowCmd(TAG2, Date.now()))
    fire(`${CMD1}forged`)
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([])
  })

  it('a tag is encoded into the key: no tag’s keys are a prefix of another’s, whatever the host id holds', async () => {
    const odd = 'h:1|x|p_000000000001|1' // a host id with both separators in it
    const a = await openWindow('A', { leader: true, status: SYNCED }, 'h')
    const b = await openWindow('B', {}, odd)
    b.channel.requestSyncNow()
    const [key] = commandKeys()
    expect(key.slice(CMD.length)).toMatch(/^[^:|]+:[0-9a-f]{32}$/) // exactly one `:` — the one before the id
    deliver()
    expect(a.syncNow).not.toHaveBeenCalled() // unencoded, `<prefix>h:` would be a prefix of `<prefix>h:1|x|…:<id>`
    expect(commandKeys()).toEqual([key])
    const c = await openWindow('C', { leader: true, status: SYNCED }, odd)
    expect(c.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('F2: a window of the OLD master that closes late leaves the NEW master’s command alone — and the new leader executes it', async () => {
    const old = await openWindow('OLD') // P1, slow to hear
    localStorage.setItem(`${CMD1}mine`, syncNowCmd(TAG1, Date.now()))
    const follower = await openWindow('F', { master: MASTER2 }, TAG2) // has switched; nobody leads P2 yet
    follower.channel.requestSyncNow()
    const [theirs] = commandKeys().filter((k) => k.startsWith(CMD2))
    expect(theirs).toBeDefined()

    old.channel.close(true) // hears of the switch NOW
    expect(commandKeys()).toEqual([theirs]) // its own master's command went, the other one did not

    const leader = await openWindow('L', { master: MASTER2, leader: true, status: SYNCED }, TAG2)
    expect(leader.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('F2: close(true) removes the status only if it is its own master’s', async () => {
    const old = await openWindow('OLD')
    await openWindow('NEW', { master: MASTER2, leader: true, status: SYNCED }, TAG2)
    vi.advanceTimersByTime(250)
    expect(published()).toMatchObject({ master: TAG2 })
    old.channel.close(true)
    expect(published()).toMatchObject({ master: TAG2, leader: 'NEW' })
  })

  it('close(true) removes a status record that is nobody’s (damaged, or from before records named their master)', async () => {
    const a = await openWindow('A')
    localStorage.setItem(STATUS, JSON.stringify({ at: Date.now(), leader: 'X', status: SYNCED, blocked: null, problems: [] }))
    a.channel.close(true)
    expect(published()).toBeNull()
  })

  it('orphans: opening a channel removes OTHER masters’ commands that have expired — and leaves the ones that have not', async () => {
    localStorage.setItem(`${CMD2}expired`, syncNowCmd(TAG2, Date.now() - 30_001))
    localStorage.setItem(`${CMD2}edge`, syncNowCmd(TAG2, Date.now() - 30_000))
    localStorage.setItem(`${CMD2}fresh`, syncNowCmd(TAG2, Date.now()))
    localStorage.setItem(`${CMD2}future`, syncNowCmd(TAG2, Date.now() + 5_001))
    localStorage.setItem(`${CMD2}damaged`, 'nope')
    localStorage.setItem(`${CMD}legacy`, JSON.stringify({ kind: 'syncNow', at: Date.now() - 30_001 })) // no tag at all
    localStorage.setItem(`${CMD1}own-expired`, syncNowCmd(TAG1, Date.now() - 30_001)) // its own master's: the leader's business
    localStorage.setItem('purdex-tabs', 'untouched')
    const b = await openWindow('B') // a follower is enough: any channel does it
    expect(commandKeys().sort()).toEqual([`${CMD1}own-expired`, `${CMD2}edge`, `${CMD2}fresh`].sort())
    expect(localStorage.getItem('purdex-tabs')).toBe('untouched')
    expect(b.syncNow).not.toHaveBeenCalled()
  })
})

/* ─── attach again: the generation hand-over ─── */

describe('the SAME master attached again: a `syncNow` sent to the old generation is carried over, a `resolve` is not', () => {
  /** `TAG1`'s master, one generation on: what `close(true, …)` is told when only the generation moved. */
  const TAG1B = 'h1|p_000000000001|2'
  const CMD1B = cmdOf(TAG1B)

  it('the critic’s case: B (still gen 1) presses Sync now, A leads gen 2 → executed EXACTLY once, after B has moved', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED }, TAG1B)
    const b = await openWindow('B') // has not rehydrated: gen 1
    b.channel.requestSyncNow()
    const [sent] = commandKeys()
    expect(sent.startsWith(CMD1)).toBe(true)
    deliver()
    expect(a.syncNow).not.toHaveBeenCalled() // not A's prefix

    b.channel.close(true, TAG1B) // B rehydrates: start.ts closes gen 1 for gen 2 of the same master
    const id = sent.slice(CMD1.length)
    expect(commandKeys()).toEqual([`${CMD1B}${id}`]) // the same id, under the new prefix; the old key is gone
    expect(JSON.parse(localStorage.getItem(`${CMD1B}${id}`) ?? 'null')).toEqual({ kind: 'syncNow', master: TAG1B, at: 1_000_000 })
    deliver()
    deliver()
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('`at` is the ORIGINAL one: carrying a command over does not make it younger', async () => {
    const b = await openWindow('B')
    localStorage.setItem(`${CMD1}c`, syncNowCmd(TAG1, Date.now() - 29_000))
    b.channel.close(true, TAG1B)
    expect(JSON.parse(localStorage.getItem(`${CMD1B}c`) ?? 'null')).toMatchObject({ at: 1_000_000 - 29_000 })
    vi.advanceTimersByTime(1_001)
    const a = await openWindow('A', { leader: true, status: SYNCED }, TAG1B)
    expect(a.syncNow).not.toHaveBeenCalled() // 30_001 ms after it was pressed
    expect(commandKeys()).toEqual([])
  })

  it('the window that carries it over is ITSELF the new leader: no event will tell it — the scan of a new leader finds it', async () => {
    const b = await openWindow('B')
    b.channel.requestSyncNow()
    b.channel.close(true, TAG1B)
    const again = await openWindow('B', { leader: true, status: SYNCED }, TAG1B) // no `deliver()` anywhere
    expect(again.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })

  it('a `resolve` is NOT carried over: it is dropped, and its key goes', async () => {
    const a = await openWindow('A', { leader: true, status: SYNCED }, TAG1B)
    a.locks.hosts = LOCK // even if the new driver happens to hold that very lock
    const b = await openWindow('B')
    b.channel.requestResolve('hosts', 'local', LOCK)
    expect(commandKeys()).toHaveLength(1)
    b.channel.close(true, TAG1B)
    deliver()
    expect(commandKeys()).toEqual([])
    expect(a.resolve).not.toHaveBeenCalled()
  })

  it.each([
    ['expired', -30_001],
    ['from the future', 5_001],
  ])('a syncNow that is %s is not carried over', async (_name, offset) => {
    const b = await openWindow('B')
    localStorage.setItem(`${CMD1}x`, syncNowCmd(TAG1, Date.now() + offset))
    b.channel.close(true, TAG1B)
    expect(commandKeys()).toEqual([])
  })

  it('damaged, or naming another master in its payload: not carried over', async () => {
    const b = await openWindow('B')
    localStorage.setItem(`${CMD1}bad`, 'nope')
    localStorage.setItem(`${CMD1}forged`, syncNowCmd(TAG2, Date.now()))
    b.channel.close(true, TAG1B)
    expect(commandKeys()).toEqual([])
  })

  it('ANOTHER master (no successor is named): nothing is carried over — as before', async () => {
    const a = await openWindow('A', { master: MASTER2, leader: true, status: SYNCED }, TAG2)
    const b = await openWindow('B')
    b.channel.requestSyncNow()
    b.channel.close(true)
    deliver()
    expect(commandKeys()).toEqual([])
    expect(a.syncNow).not.toHaveBeenCalled()
  })

  it('close(false) carries nothing over, successor or not: the keys are the other windows’ business', async () => {
    const b = await openWindow('B')
    b.channel.requestSyncNow()
    const keys = commandKeys()
    b.channel.close(false, TAG1B)
    expect(commandKeys()).toEqual(keys)
  })

  it('TWO windows carry the same command over before the leader looks → ONE key, executed once (the id is kept)', async () => {
    const b = await openWindow('B')
    const c = await openWindow('C')
    const raw = syncNowCmd(TAG1, Date.now())
    localStorage.setItem(`${CMD1}same`, raw)
    b.channel.close(true, TAG1B)
    localStorage.setItem(`${CMD1}same`, raw) // C had read the key before B removed it: two windows, no lock between them
    c.channel.close(true, TAG1B)
    expect(commandKeys()).toEqual([`${CMD1B}same`])
    const a = await openWindow('A', { leader: true, status: SYNCED }, TAG1B)
    expect(a.syncNow).toHaveBeenCalledTimes(1)
    expect(commandKeys()).toEqual([])
  })
})

/* ─── the end ─── */

describe('close', () => {
  it('close(true) — the master is gone: no listener, no timer, no status, no command', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const remove = vi.spyOn(window, 'removeEventListener')
    const a = await openWindow('A', { leader: true, status: SYNCED })
    vi.advanceTimersByTime(250)
    localStorage.setItem(`${CMD1}left`, syncNowCmd(TAG1, Date.now() - 60_000))
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
    localStorage.setItem(`${CMD1}late`, syncNowCmd(TAG1, Date.now()))
    fire(`${CMD1}late`)
    a.channel.refresh()
    a.channel.requestSyncNow()
    a.channel.requestResolve('hosts', 'sot', LOCK)
    a.channel.close(true) // twice: harmless, and does not clear what came later
    vi.advanceTimersByTime(60_000)
    expect(a.syncNow).not.toHaveBeenCalled()
    expect(a.resolve).not.toHaveBeenCalled()
    expect(commandKeys()).toEqual([`${CMD1}late`])
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
    localStorage.setItem(`${CMD1}x`, syncNowCmd(TAG1, Date.now()))
    a.channel.close(false)
    vi.advanceTimersByTime(1) // jsdom's own 0 ms timers
    expect(published()).not.toBeNull()
    expect(commandKeys()).toEqual([`${CMD1}x`])
    expect(vi.getTimerCount()).toBe(0)
  })
})
