// The leader lease, driven with fake timers over the real (jsdom) localStorage.
//
// Two "windows" are two module graphs (`vi.resetModules()` + a fresh import —
// the precedent is usePlaceholderFilesStore.cross-window.test.ts) over ONE
// `localStorage` and one clock. Each graph generates its own window id, which is
// the only per-realm state this module has. jsdom does not deliver `storage`
// events across module graphs, so a test that wants one dispatches it by hand.
//
// What a single JS thread CANNOT reproduce is the cross-process interleaving
// that makes a lease on localStorage a lease and not a lock: window B reading
// "no lease" while window A's write is already on its way. Those tests fake it
// with a one-shot stale `getItem` (`staleReadOnce`), and say so in their names.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { STORAGE_KEYS } from '../storage/keys'
import type { LeaderOptions, Leadership } from './leader'

const KEY = STORAGE_KEYS.PROFILE_LEADER
const TTL = 6000
const RENEW = 2000

type Win = { lead: Leadership; changes: boolean[] }

const open: Leadership[] = []

/** A new window: its own module graph, the shared storage, the shared (fake) clock. */
async function openWindow(opts: LeaderOptions = {}): Promise<Win> {
  vi.resetModules()
  const mod = await import('./leader')
  const lead = mod.contendForLeadership({ now: () => Date.now(), random: () => 0.5, ...opts })
  open.push(lead)
  const changes: boolean[] = []
  lead.onChange((leader) => changes.push(leader))
  return { lead, changes }
}

function lease(): { windowId: string; expiresAt: number } | null {
  const raw = localStorage.getItem(KEY)
  return raw === null ? null : JSON.parse(raw)
}

function fireStorageEvent(): void {
  window.dispatchEvent(new StorageEvent('storage', { key: KEY, newValue: localStorage.getItem(KEY) }))
}

/** The next `getItem` answers "nothing there" whatever storage holds — a read that raced a write. */
function staleReadOnce(): void {
  vi.spyOn(Storage.prototype, 'getItem').mockReturnValueOnce(null)
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(1_000_000)
  localStorage.clear()
})

afterEach(() => {
  for (const lead of open.splice(0)) lead.stop()
  vi.restoreAllMocks()
  vi.useRealTimers()
  vi.resetModules()
})

describe('one window', () => {
  it('leads only after the jitter, and says so once', async () => {
    const a = await openWindow() // random 0.5 → 100 ms
    expect(lease()).not.toBeNull() // the claim is written at once…
    expect(a.lead.isLeader()).toBe(false) // …and is not leadership yet
    vi.advanceTimersByTime(99)
    expect(a.lead.isLeader()).toBe(false)
    vi.advanceTimersByTime(1)
    expect(a.lead.isLeader()).toBe(true)
    expect(a.changes).toEqual([true])
    expect(lease()!.expiresAt).toBe(1_000_000 + TTL)
  })

  it('the jitter comes from the injected random, inside jitterMs', async () => {
    const a = await openWindow({ random: () => 0, jitterMs: [50, 150] })
    vi.advanceTimersByTime(49)
    expect(a.lead.isLeader()).toBe(false)
    vi.advanceTimersByTime(1)
    expect(a.lead.isLeader()).toBe(true)
    a.lead.stop()

    const b = await openWindow({ random: () => 1, jitterMs: [50, 150] })
    vi.advanceTimersByTime(149)
    expect(b.lead.isLeader()).toBe(false)
    vi.advanceTimersByTime(1)
    expect(b.lead.isLeader()).toBe(true)
  })

  it('renewing pushes expiresAt forward, every renewMs', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    expect(lease()!.expiresAt).toBe(1_000_000 + TTL) // written with the claim, at t = 0
    vi.advanceTimersByTime(RENEW) // t = 2100: the first renewal
    expect(lease()!.expiresAt).toBe(1_000_000 + 100 + RENEW + TTL)
    vi.advanceTimersByTime(RENEW * 5)
    expect(lease()!.expiresAt).toBe(1_000_000 + 100 + RENEW * 6 + TTL)
    expect(a.lead.isLeader()).toBe(true)
    expect(a.changes).toEqual([true]) // renewing is not a change
  })

  it('stop() deletes the lease and leaves no timer', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    expect(a.changes).toEqual([true])

    a.lead.stop()

    expect(lease()).toBeNull()
    // jsdom queues a 0 ms timer of its own for every storage write that changes
    // something (its `storage` event dispatch) — measured, and `removeItem` is
    // one. Let that one run; a leaked renewal (2000 ms away) would still be here.
    vi.advanceTimersByTime(1)
    expect(vi.getTimerCount()).toBe(0)
  })

  it('after stop() nothing is heard: not from a timer, not from storage, pagehide or pageshow', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    expect(a.changes).toEqual([true])

    a.lead.stop()

    expect(a.lead.isLeader()).toBe(false)
    fireStorageEvent()
    window.dispatchEvent(new Event('pagehide'))
    window.dispatchEvent(new Event('pageshow'))
    vi.advanceTimersByTime(TTL * 3)
    expect(a.changes).toEqual([true])
    expect(lease()).toBeNull()
  })

  it('stop() during the jitter withdraws the claim and never leads', async () => {
    const a = await openWindow()
    a.lead.stop()
    expect(lease()).toBeNull()
    vi.advanceTimersByTime(TTL)
    expect(a.changes).toEqual([])
    expect(vi.getTimerCount()).toBe(0)
  })

  it('stop() leaves a lease that is somebody else’s alone', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    localStorage.setItem(KEY, JSON.stringify({ windowId: 'someone-else', expiresAt: Date.now() + TTL }))
    a.lead.stop()
    expect(lease()!.windowId).toBe('someone-else')
  })

  it('onChange: not called on subscribe, not called twice for one value, and unsubscribe works', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    const late: boolean[] = []
    const off = a.lead.onChange((v) => late.push(v))
    expect(late).toEqual([]) // already leader — the caller asks isLeader() itself
    vi.advanceTimersByTime(RENEW * 3)
    expect(late).toEqual([])

    localStorage.setItem(KEY, JSON.stringify({ windowId: 'someone-else', expiresAt: Date.now() + TTL }))
    fireStorageEvent()
    fireStorageEvent()
    vi.advanceTimersByTime(RENEW)
    expect(late).toEqual([false])
    expect(a.changes).toEqual([true, false])

    off()
    vi.advanceTimersByTime(TTL * 2) // someone-else never renews → a leads again
    expect(a.changes).toEqual([true, false, true])
    expect(late).toEqual([false])
  })

  it('a listener that throws does not stop the renewals or the other listeners', async () => {
    vi.resetModules()
    const { contendForLeadership } = await import('./leader')
    const lead = contendForLeadership({ random: () => 0.5 })
    open.push(lead)
    const heard: boolean[] = []
    const logged = vi.spyOn(console, 'error').mockImplementation(() => {})
    lead.onChange(() => {
      throw new Error('listener bug')
    })
    lead.onChange((v) => heard.push(v))
    vi.advanceTimersByTime(100)
    expect(heard).toEqual([true])
    expect(logged).toHaveBeenCalledTimes(1) // reported, not swallowed
    const first = lease()!.expiresAt
    vi.advanceTimersByTime(RENEW)
    expect(lease()!.expiresAt).toBeGreaterThan(first)
  })

  it('isLeader() reads storage every time: a lease changed from outside is false at once', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    expect(a.lead.isLeader()).toBe(true)

    localStorage.setItem(KEY, JSON.stringify({ windowId: 'someone-else', expiresAt: Date.now() + TTL }))
    expect(a.lead.isLeader()).toBe(false) // no timer has run; the in-memory flag still says leader
    expect(a.changes).toEqual([true])
  })

  it('isLeader() is false for an own lease that has run out, and for a deleted one', async () => {
    const a = await openWindow({ renewMs: 60_000 }) // sleeps through its renewals
    vi.advanceTimersByTime(100)
    vi.advanceTimersByTime(TTL - 101)
    expect(a.lead.isLeader()).toBe(true)
    vi.advanceTimersByTime(1) // now === expiresAt
    expect(a.lead.isLeader()).toBe(false)

    const b = await openWindow({ windowId: 'b' })
    vi.advanceTimersByTime(100)
    expect(b.lead.isLeader()).toBe(true)
    localStorage.removeItem(KEY)
    expect(b.lead.isLeader()).toBe(false)
  })

  it('isLeader() is false when the read throws', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    expect(a.lead.isLeader()).toBe(true)
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    expect(a.lead.isLeader()).toBe(false)
  })

  it.each([
    ['not JSON', '{not json'],
    ['a JSON string', '"x"'],
    ['null', 'null'],
    ['an array', '[]'],
    ['no windowId', JSON.stringify({ expiresAt: 9_999_999_999 })],
    ['an empty windowId', JSON.stringify({ windowId: '', expiresAt: 9_999_999_999 })],
    ['a non-string windowId', JSON.stringify({ windowId: 7, expiresAt: 9_999_999_999 })],
    ['no expiresAt', JSON.stringify({ windowId: 'x' })],
    ['a string expiresAt', JSON.stringify({ windowId: 'x', expiresAt: '9999999999' })],
    ['a null expiresAt (what NaN/Infinity serialise to)', JSON.stringify({ windowId: 'x', expiresAt: null })],
  ])('a damaged lease (%s) counts as no lease', async (_name, raw) => {
    localStorage.setItem(KEY, raw)
    const a = await openWindow({ windowId: 'a' })
    vi.advanceTimersByTime(100)
    expect(a.lead.isLeader()).toBe(true)
    expect(lease()).toEqual({ windowId: 'a', expiresAt: 1_000_000 + TTL })
  })

  it('an expired lease (expiresAt === now) counts as no lease; a live one does not', async () => {
    localStorage.setItem(KEY, JSON.stringify({ windowId: 'x', expiresAt: 1_000_000 }))
    const a = await openWindow({ windowId: 'a' })
    vi.advanceTimersByTime(100)
    expect(a.lead.isLeader()).toBe(true)
    a.lead.stop()

    localStorage.setItem(KEY, JSON.stringify({ windowId: 'x', expiresAt: Date.now() + 1 }))
    const b = await openWindow({ windowId: 'b' })
    expect(lease()!.windowId).toBe('x') // not overwritten
    expect(b.lead.isLeader()).toBe(false)
  })

  it('a lease that claims to outlive 10 × ttlMs (a clock that was set back) counts as damaged', async () => {
    localStorage.setItem(KEY, JSON.stringify({ windowId: 'x', expiresAt: Date.now() + TTL * 10 + 1 }))
    const a = await openWindow({ windowId: 'a' })
    vi.advanceTimersByTime(100)
    expect(a.lead.isLeader()).toBe(true)
    a.lead.stop()

    localStorage.setItem(KEY, JSON.stringify({ windowId: 'x', expiresAt: Date.now() + TTL * 10 }))
    const b = await openWindow({ windowId: 'b' })
    vi.advanceTimersByTime(100)
    expect(b.lead.isLeader()).toBe(false)
    expect(lease()!.windowId).toBe('x')
  })

  it('storage that reads but cannot write (quota) → does not lead, keeps retrying, leads once it can', async () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    const a = await openWindow()
    vi.advanceTimersByTime(TTL * 2)
    expect(a.changes).toEqual([])
    expect(a.lead.isLeader()).toBe(false)

    setItem.mockRestore()
    vi.advanceTimersByTime(TTL + 100)
    expect(a.changes).toEqual([true])
  })

  it('localStorage that throws on every access → leads alone, and nothing blows up', async () => {
    const boom = (): never => {
      throw new Error('SecurityError')
    }
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(boom)
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(boom)
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(boom)

    const a = await openWindow()
    vi.advanceTimersByTime(100)
    expect(a.changes).toEqual([true])
    // No storage, so there is nothing to confirm against: the flag is the answer.
    expect(a.lead.isLeader()).toBe(true)
    vi.advanceTimersByTime(RENEW * 4)
    expect(a.changes).toEqual([true])
    expect(() => window.dispatchEvent(new Event('pagehide'))).not.toThrow()
    expect(() => a.lead.stop()).not.toThrow()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('pagehide releases the lease and steps down; pageshow contends again', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)

    window.dispatchEvent(new Event('pagehide'))
    expect(lease()).toBeNull()
    expect(a.changes).toEqual([true, false])
    vi.advanceTimersByTime(TTL * 2)
    expect(lease()).toBeNull() // a hidden page does not grab the lease back

    window.dispatchEvent(new Event('pageshow')) // back from the bfcache
    vi.advanceTimersByTime(100)
    expect(a.changes).toEqual([true, false, true])
  })
})

describe('two windows', () => {
  it('A leads first → B does not, for as long as A renews', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    const b = await openWindow()
    vi.advanceTimersByTime(TTL * 5)
    expect(a.lead.isLeader()).toBe(true)
    expect(b.lead.isLeader()).toBe(false)
    expect(b.changes).toEqual([])
  })

  it('A stops → B takes over on the storage event (still through the jitter)', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    const b = await openWindow()
    vi.advanceTimersByTime(500)

    a.lead.stop()
    fireStorageEvent()
    expect(b.lead.isLeader()).toBe(false)
    vi.advanceTimersByTime(99)
    expect(b.lead.isLeader()).toBe(false)
    vi.advanceTimersByTime(1)
    expect(b.lead.isLeader()).toBe(true)
    expect(b.changes).toEqual([true])
  })

  it('A stops and the storage event is lost → B takes over on its fallback timer, within ttlMs', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    const b = await openWindow()
    vi.advanceTimersByTime(500)

    a.lead.stop()
    vi.advanceTimersByTime(TTL + 100)
    expect(b.lead.isLeader()).toBe(true)
    expect(b.changes).toEqual([true])
  })

  it('a storage event about a lease that is still live does not make B claim it', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    const b = await openWindow()
    const holder = lease()!.windowId
    fireStorageEvent()
    vi.advanceTimersByTime(500)
    expect(lease()!.windowId).toBe(holder)
    expect(b.lead.isLeader()).toBe(false)
    expect(a.lead.isLeader()).toBe(true)
  })

  it('A dies (no renewal, no release) → B takes over once the lease runs out, not before', async () => {
    const a = await openWindow({ renewMs: 10 * 60_000 }) // never renews within this test
    vi.advanceTimersByTime(100) // lease runs out at +6000
    const b = await openWindow()
    vi.advanceTimersByTime(TTL - 100 - 1)
    expect(b.lead.isLeader()).toBe(false)
    vi.advanceTimersByTime(1 + 100) // expiry, then B's jitter
    expect(b.lead.isLeader()).toBe(true)
    expect(a.lead.isLeader()).toBe(false)
  })

  it('B took over, then A wakes up to renew → A steps down and does NOT overwrite B’s lease', async () => {
    const a = await openWindow({ renewMs: 10_000 }) // asleep past its own 6 s lease
    vi.advanceTimersByTime(100)
    const b = await openWindow()
    vi.advanceTimersByTime(TTL + 100) // t = 6200: B leads
    expect(b.lead.isLeader()).toBe(true)
    const bId = lease()!.windowId
    expect(a.changes).toEqual([true])

    vi.advanceTimersByTime(4000) // t = 10_200: A's renewal at 10_100 has run

    expect(a.changes).toEqual([true, false])
    expect(lease()!.windowId).toBe(bId)
    expect(b.lead.isLeader()).toBe(true)
    expect(b.changes).toEqual([true])

    vi.advanceTimersByTime(TTL * 3) // and it stays that way
    expect(lease()!.windowId).toBe(bId)
    expect(a.lead.isLeader()).toBe(false)
    expect(a.changes).toEqual([true, false])
  })

  it('a leader that gets a storage event showing someone else’s lease steps down without waiting for its renewal', async () => {
    const a = await openWindow()
    vi.advanceTimersByTime(100)
    localStorage.setItem(KEY, JSON.stringify({ windowId: 'someone-else', expiresAt: Date.now() + TTL }))
    fireStorageEvent()
    expect(a.changes).toEqual([true, false])
    expect(lease()!.windowId).toBe('someone-else')
  })

  it('started together, different jitter → exactly one leader', async () => {
    const a = await openWindow({ random: () => 0 }) // 50 ms
    const b = await openWindow({ random: () => 1 }) // 150 ms; sees A's claim and waits
    vi.advanceTimersByTime(1000)
    expect([a.lead.isLeader(), b.lead.isLeader()]).toEqual([true, false])
    expect(b.changes).toEqual([])
  })

  it('started together, different jitter, and B’s read raced A’s write (faked) → the read-back settles it: exactly one leader', async () => {
    const a = await openWindow({ random: () => 0 }) // 50 ms
    staleReadOnce()
    const b = await openWindow({ random: () => 1 }) // read "no lease", wrote over A's claim
    vi.advanceTimersByTime(1000)
    // A read back B's record and stood aside.
    expect(a.changes).toEqual([])
    expect(b.changes).toEqual([true])
    expect([a.lead.isLeader(), b.lead.isLeader()]).toEqual([false, true])
  })

  it('started together, SAME jitter, B’s read raced A’s write (faked) → as observed: the last writer leads, the other never does', async () => {
    const a = await openWindow({ random: () => 0.5 })
    staleReadOnce()
    const b = await openWindow({ random: () => 0.5 })
    vi.advanceTimersByTime(1000)
    // Both read back at the same instant and both see B's record: within one
    // thread the write order is total, so equal jitter alone makes no second leader.
    expect(a.changes).toEqual([])
    expect(b.changes).toEqual([true])
  })

  it('KNOWN RESIDUAL, not a guarantee: a claim written over a sitting leader makes two in-memory leaders, and it converges within one renewMs', async () => {
    // What equal jitter cannot do in one thread, a slow process can: B decides
    // "no lease" and its write lands AFTER A has already read back and started
    // leading. There is no compare-and-swap to refuse that write.
    const a = await openWindow()
    vi.advanceTimersByTime(100) // A leads, t = 100
    staleReadOnce()
    const b = await openWindow()
    vi.advanceTimersByTime(100) // t = 200: B read back its own record
    expect(b.changes).toEqual([true])
    expect(a.changes).toEqual([true]) // ← A has not noticed: two windows believe they lead

    // The per-write check is already right, which is what the executor relies on…
    expect(a.lead.isLeader()).toBe(false)
    expect(b.lead.isLeader()).toBe(true)

    // …and A's belief ends at its next renewal, at most renewMs after B's write.
    vi.advanceTimersByTime(RENEW - 100) // t = 2100 = A's renewal
    expect(a.changes).toEqual([true, false])
    expect(b.changes).toEqual([true])
    expect(lease()!.expiresAt).toBeGreaterThan(Date.now())
    expect(b.lead.isLeader()).toBe(true)
  })

  it('KNOWN RESIDUAL, not a guarantee: a release that read its own lease BEFORE a takeover deletes the new leader’s live lease — one extra hand-over, exactly one leader again within renewMs + jitter', async () => {
    // release() is "read, see my record, removeItem" — two steps, no
    // compare-and-delete. A was suspended between them for longer than its lease:
    // B took over in the meantime, and A's removeItem lands on B's record.
    const a = await openWindow({ renewMs: 60_000 }) // never renews: a frozen process
    vi.advanceTimersByTime(100)
    const aRecord = localStorage.getItem(KEY)!
    const b = await openWindow()
    vi.advanceTimersByTime(TTL + 100) // t = 6200: A's lease ran out, B leads
    expect(b.lead.isLeader()).toBe(true)
    expect(b.changes).toEqual([true])
    const bId = lease()!.windowId

    vi.spyOn(Storage.prototype, 'getItem').mockReturnValueOnce(aRecord) // the read A made before it was suspended
    a.lead.stop()

    expect(lease()).toBeNull() // ← B's LIVE lease is gone: this is the defect
    expect(b.lead.isLeader()).toBe(false) // the per-write check notices at once (nobody leads for a moment)…
    expect(b.changes).toEqual([true]) // …while B's memory has not

    // B's next renewal (at most renewMs away) finds no lease: it steps down and claims again through the jitter.
    vi.advanceTimersByTime(RENEW + 100)
    expect(b.changes).toEqual([true, false, true]) // one extra hand-over — for the driver, one extra full reindex
    expect(lease()!.windowId).toBe(bId)
    expect(b.lead.isLeader()).toBe(true)
    expect(a.lead.isLeader()).toBe(false)

    vi.advanceTimersByTime(TTL * 3) // and it stays that way
    expect(b.changes).toEqual([true, false, true])
  })
})
