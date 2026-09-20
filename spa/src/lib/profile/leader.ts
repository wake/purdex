// spa/src/lib/profile/leader.ts — picks the ONE window on this machine that
// runs the Profile Sync driver (collector + executor), with a lease kept in
// localStorage (plan Task 10). Nothing calls this yet; Task 11 will.
//
//   purdex-profile-leader  →  {"windowId": "<hex>", "expiresAt": <epoch ms>}
//
// WHY NOT WEB LOCKS. `navigator.locks` exists only in secure contexts, and the
// Electron dev window loads plain `http://100.64.0.2:5174`. And the promise of
// `locks.request()` resolves when the lock is RELEASED, not when it is granted,
// so "await it, then lead" can never return. localStorage is synchronous and is
// shared by every same-origin window, secure context or not.
//
// THIS IS A LEASE. IT IS NOT A LOCK, AND IT DOES NOT EXCLUDE ANYTHING.
// localStorage has no compare-and-swap. "Read the lease, see none, write mine"
// is two steps, and another process can run between them. What this module does
// about that is narrow the window, not close it:
//   - claim: read → absent, damaged or expired → write own record → wait a
//     random 50–150 ms → read back → lead only if the record is still ours.
//     Two windows that claim at the same moment overwrite each other, and the
//     read-back lets the one that was overwritten stand aside. That works when
//     both writes land before either read-back. It does NOT work when a write
//     lands after the other window has already read back — a process that was
//     suspended between its read and its write, or a read that was stale for
//     longer than the jitter. Then both windows have read back their own record
//     at some point and BOTH ARE LEADING: a double leader.
//   - what ends a double leader: the window whose record was overwritten finds
//     out at its next renewal (it reads before it writes, sees a lease that is
//     not its own, steps down and does not write), or sooner if the `storage`
//     event reaches it. So the overlap lasts up to about one `renewMs` (2 s),
//     longer if that window's timers are being throttled or the process is
//     frozen — a background tab can be throttled to one timer tick a minute.
//   - during the overlap, `isLeader()` is already right for the window that lost
//     — it reads storage on every call, and the executor calls it before every
//     network write — but that too is a read followed by an act, not an atomic
//     step: a write can still leave after the check and before the lease moved.
//   - release has the same hole, the other way round. `release()` (stop, pagehide)
//     is "read → the record is mine → removeItem": two steps, and localStorage has
//     no compare-and-delete either. A window suspended between them for longer
//     than its lease comes back after another window has taken over, and its
//     `removeItem` DELETES THE NEW LEADER'S LIVE LEASE. For that moment nobody
//     holds a lease: the new leader's `isLeader()` turns false at once (it reads
//     storage), its next renewal finds nothing, steps down and claims again —
//     and any waiting window woken by the `storage` event claims too, so the
//     lease may go to a third window. Cost: one extra hand-over, which for the
//     driver means one extra full reindex, and up to `renewMs` + jitter in which
//     nothing is pushed. Not fixed on purpose: "never delete, let it expire"
//     would leave every ordinary window close with no leader for `ttlMs` (6 s),
//     and that happens all the time while this needs a process frozen across
//     exactly those two lines. Pinned in leader.test.ts ("KNOWN RESIDUAL … release").
// What carries the consequences is therefore not this file. The SOT is guarded
// by the daemon's CAS, and the leader's local state by section-store's
// one-key-per-thing layout; the worst a double leader costs is one false
// conflict (section-store.ts, "RESIDUAL RISK"). This module makes a second
// leader rare and short. It never makes one impossible.
//   The same goes for the clock: the lease compares `expiresAt` with this
// machine's `Date.now()`. A clock set back while the holder is dead would keep a
// dead lease alive, so a lease that claims to outlive `10 × ttlMs` is treated as
// damaged. A clock set forward expires a live lease early and produces exactly
// the double leader described above, with the same ending.
//
// NOT THROUGH `browserStorage`. That backend announces every write with
// `syncManager.notify`, and a renewal every 2 s is nothing any store needs to
// hear about. Followers are woken by the browser's own `storage` event, which
// fires for direct localStorage writes. That event never goes to the window that
// wrote, and it can be missed, so every follower also keeps a fallback timer:
// the lease's `expiresAt`, or `ttlMs` from now, whichever is earlier.
//
// STORAGE THAT DOES NOT WORK. Every access is wrapped. If a claim can neither
// read nor write, this window LEADS ALONE, on the assumption that where no
// window can store anything there is no second window that could be
// coordinated with — and if there is one, it makes the same assumption and both
// lead, with the daemon's CAS as the only guard. The alternative, never syncing
// in a browser that blocks storage, was judged worse. In that mode `isLeader()`
// answers from memory (there is no lease to confirm against) and every
// `renewMs` it probes storage; once a read succeeds it goes back to the normal
// rules. If reads work and writes do not (quota), it does not lead: a lease
// another window wrote earlier may still be readable, so it waits and retries.
//   Once a lease HAS been taken through storage, a read that throws makes
// `isLeader()` false — unknown is treated as "not leader".
//
// `onChange` DOES NOT REPLAY. It fires only when the value changes, never twice
// for the same value, and never at subscribe time: a caller that subscribes
// late asks `isLeader()` itself. `stop()` does not fire it either — the caller
// ended this on purpose, and nothing is heard after `stop()` returns or, if a
// listener calls `stop()`, after that call.
import { STORAGE_KEYS } from '../storage/keys'

export interface LeaderOptions {
  /** Epoch ms. Default `Date.now`. */
  now?: () => number
  /** [0, 1). Default `Math.random`. Only used for the claim jitter. */
  random?: () => number
  /** How long a written lease is valid. Default 6000. */
  ttlMs?: number
  /** How often the leader rewrites `expiresAt`. Default 2000. */
  renewMs?: number
  /** [min, max] of the wait between writing a claim and reading it back. Default [50, 150]. */
  jitterMs?: [number, number]
  /** Default: one random id per realm (module instance). */
  windowId?: string
}

export interface Leadership {
  /** The in-memory flag AND a fresh read of the lease: ours and not expired. Call it before every write. */
  isLeader(): boolean
  /** Fires on change only; does not replay the current value. Returns the unsubscribe. */
  onChange(cb: (leader: boolean) => void): () => void
  /** Releases the lease if it is ours, cancels every timer and listener. Nothing is heard afterwards. */
  stop(): void
}

interface Lease {
  windowId: string
  expiresAt: number
}

type ReadResult = { ok: true; lease: Lease | null } | { ok: false }

const KEY = STORAGE_KEYS.PROFILE_LEADER

let realmWindowId: string | undefined

function getRealmWindowId(): string {
  if (realmWindowId !== undefined) return realmWindowId
  const bytes = new Uint8Array(16)
  try {
    crypto.getRandomValues(bytes)
  } catch {
    for (let i = 0; i < bytes.length; i++) bytes[i] = Math.floor(Math.random() * 256)
  }
  realmWindowId = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return realmWindowId
}

/** Anything that is not exactly `{windowId: non-empty string, expiresAt: finite number}` is "no lease". */
function parseLease(raw: string | null): Lease | null {
  if (raw === null) return null
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    return null
  }
  if (typeof value !== 'object' || value === null) return null
  const { windowId, expiresAt } = value as Record<string, unknown>
  if (typeof windowId !== 'string' || windowId === '') return null
  if (typeof expiresAt !== 'number' || !Number.isFinite(expiresAt)) return null
  return { windowId, expiresAt }
}

export function contendForLeadership(opts: LeaderOptions = {}): Leadership {
  const now = opts.now ?? (() => Date.now())
  const random = opts.random ?? (() => Math.random())
  const ttlMs = opts.ttlMs ?? 6000
  const renewMs = opts.renewMs ?? 2000
  const [jitterLo, jitterHi] = opts.jitterMs ?? [50, 150]
  const id = opts.windowId ?? getRealmWindowId()

  // waiting: follower, fallback timer armed · claiming: record written, jitter running ·
  // leading · hidden: after pagehide, until pageshow · stopped: for good.
  let phase: 'waiting' | 'claiming' | 'leading' | 'hidden' | 'stopped' = 'waiting'
  let leader = false
  /** Leading with no working storage — see the header. */
  let storageless = false
  let timer: ReturnType<typeof setTimeout> | null = null
  const listeners = new Set<(leader: boolean) => void>()

  function read(): ReadResult {
    try {
      return { ok: true, lease: parseLease(localStorage.getItem(KEY)) }
    } catch {
      return { ok: false }
    }
  }

  function write(): boolean {
    try {
      localStorage.setItem(KEY, JSON.stringify({ windowId: id, expiresAt: now() + ttlMs } satisfies Lease))
      return true
    } catch {
      return false
    }
  }

  /** Delete the lease only if the record in storage is ours. (Read, then remove: not atomic either.) */
  function release(): void {
    const r = read()
    if (!r.ok || r.lease?.windowId !== id) return
    try {
      localStorage.removeItem(KEY)
    } catch {
      /* it will expire */
    }
  }

  /** Held by another window, not expired, and not claiming an impossible lifetime. */
  function heldByOther(lease: Lease | null): lease is Lease {
    if (lease === null || lease.windowId === id) return false
    const left = lease.expiresAt - now()
    return left > 0 && left <= ttlMs * 10
  }

  function schedule(fn: () => void, ms: number): void {
    clearTimer()
    timer = setTimeout(() => {
      timer = null
      fn()
    }, Math.max(0, ms))
  }

  function clearTimer(): void {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }

  function setLeader(next: boolean): void {
    if (leader === next) return
    leader = next
    for (const cb of [...listeners]) {
      if (phase === 'stopped') return
      try {
        cb(next)
      } catch (err) {
        console.error('[profile/leader] onChange listener threw', err)
      }
    }
  }

  function contend(): void {
    if (phase === 'stopped' || phase === 'hidden') return
    const r = read()
    if (r.ok && heldByOther(r.lease)) {
      phase = 'waiting'
      schedule(contend, Math.min(ttlMs, r.lease.expiresAt - now()))
      return
    }
    const wrote = write()
    if (!wrote && r.ok) {
      // Readable but not writable: no claim was made, so there is nothing to read back.
      phase = 'waiting'
      schedule(contend, ttlMs)
      return
    }
    phase = 'claiming'
    const jitter = jitterLo + Math.min(1, Math.max(0, random())) * (jitterHi - jitterLo)
    schedule(() => readBack(wrote), jitter)
  }

  function readBack(wrote: boolean): void {
    const r = read()
    if (!r.ok) {
      if (!wrote) {
        storageless = true
        lead()
      } else {
        phase = 'waiting'
        schedule(contend, ttlMs)
      }
      return
    }
    if (r.lease?.windowId === id) lead()
    else contend() // overwritten (→ waits for that lease) or deleted (→ claims again)
  }

  function lead(): void {
    phase = 'leading'
    schedule(renew, renewMs)
    setLeader(true)
  }

  function stepDown(): void {
    phase = 'waiting'
    storageless = false
    clearTimer()
    setLeader(false)
    contend() // a no-op if a listener stopped us
  }

  /** READ FIRST. A lease that is not ours is never written over — that is the whole takeover protocol. */
  function renew(): void {
    const r = read()
    if (storageless) {
      if (!r.ok) {
        schedule(renew, renewMs)
        return
      }
      storageless = false // storage is back: the normal rules apply from here
    }
    if (!r.ok || r.lease?.windowId !== id || !write()) {
      stepDown()
      return
    }
    schedule(renew, renewMs)
  }

  function onStorage(e: StorageEvent): void {
    if (e.key !== null && e.key !== KEY) return // null = localStorage.clear()
    if (phase === 'waiting') {
      contend() // reads the lease itself; a live one just re-arms the fallback timer
    } else if (phase === 'leading' && !storageless) {
      const r = read()
      if (!r.ok || r.lease?.windowId !== id) stepDown()
    }
    // claiming: the read-back is about to look anyway.
  }

  function onPageHide(): void {
    if (phase === 'stopped') return
    release()
    phase = 'hidden'
    storageless = false
    clearTimer()
    setLeader(false)
  }

  /** Only a page restored from the bfcache gets here in `hidden`; its timers were cancelled above. */
  function onPageShow(): void {
    if (phase !== 'hidden') return
    phase = 'waiting'
    contend()
  }

  const hasWindow = typeof window !== 'undefined'
  if (hasWindow) {
    window.addEventListener('storage', onStorage)
    window.addEventListener('pagehide', onPageHide)
    window.addEventListener('pageshow', onPageShow)
  }
  contend()

  return {
    isLeader() {
      if (!leader || phase !== 'leading') return false
      if (storageless) return true
      const r = read()
      return r.ok && r.lease !== null && r.lease.windowId === id && r.lease.expiresAt > now()
    },
    onChange(cb) {
      if (phase === 'stopped') return () => {}
      listeners.add(cb)
      return () => {
        listeners.delete(cb)
      }
    },
    stop() {
      if (phase === 'stopped') return
      release()
      phase = 'stopped'
      leader = false
      storageless = false
      clearTimer()
      listeners.clear()
      if (hasWindow) {
        window.removeEventListener('storage', onStorage)
        window.removeEventListener('pagehide', onPageHide)
        window.removeEventListener('pageshow', onPageShow)
      }
    },
  }
}
