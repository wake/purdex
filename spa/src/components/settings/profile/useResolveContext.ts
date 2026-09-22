// spa/src/components/settings/profile/useResolveContext.ts — the ONE place a Resolve confirmation is frozen and
// checked (P3d-4 plan; review A1 / A3 / A4). ResolveRow only displays what this hook answers.
//
// WHAT IS FROZEN when a confirmation opens — the RESOLVE CONTEXT:
//   - the master's TAG, `masterTagOf(master, attachGeneration)` — the very tag the status channel is opened with.
//     A lock is equal in every field on a copied or recreated profile, so the lock alone does not say WHICH profile
//     the user looked at; the tag does, down to the attach generation (review A1);
//   - the master's ENDPOINT, `masterEndpoint`: the daemon the attachment is on. The api layer resolves a host's
//     address on every request, so a host whose address was edited meanwhile would answer from ANOTHER daemon with
//     the same id (review A3). The request itself is pinned (`expectEndpoint`: api.ts compares it where it resolves
//     the address, so an address that moves away and back cannot slip a request to another daemon), and the answer
//     is checked again when it arrives;
//   - the section's LOCK (`sameLock`, sync-status.ts).
// WHAT GOES THROUGH IT: the counts (the host's read, above) and the send. The send hands `requestResolve` the frozen
// lock AND the frozen tag, and the channel refuses a tag that is not its own — so even a handler that runs after the
// master moved, before React took the dialog away, sends nothing.
// WHEN IT STOPS BEING CURRENT (any of the three), the confirmation closes itself and the row says "this changed while
// you were deciding"; an answer that arrives for it sets nothing.
//
// "SENT" ENDS (R2): a handed-over command is "sent" until the lock for this key — or the master — changes, or until
// the command's own TTL has passed ("no answer"). That timer is the only one on the page.
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { endpointOfHost, selectMaster, useProfileStore } from '../../../stores/useProfileStore'
import { useHostStore } from '../../../stores/useHostStore'
import type { SectionLock } from '../../../lib/profile/executor'
import { requestResolve } from '../../../lib/profile/start'
import { COMMAND_TTL_MS, masterTagOf, sameLock } from '../../../lib/profile/sync-status'
import { readHostSide, readLocalSide, type HostSide, type LocalSide } from './resolve-counts'

export type Keep = 'local' | 'sot'

export interface ResolveContext {
  tag: string
  hostId: string
  profileId: string
  /** `masterEndpoint` when the confirmation opened: the daemon the attachment is on. */
  endpoint: string
  lock: SectionLock
}

export type Outcome = { state: 'sent' | 'no-answer' | 'not-sent'; lock: SectionLock; tag: string }

type ProfileState = ReturnType<typeof useProfileStore.getState>

/** The tag the status channel is on right now — null without a master. */
function liveTagOf(s: ProfileState): string | null {
  const master = selectMaster(s)
  return master === null ? null : masterTagOf(master, s.attachGeneration)
}

/** Where the master host is RIGHT NOW (`"<ip>:<port>"`), or null when it is not in the store. */
function hostEndpointOf(hostId: string | null): string | null {
  const host = hostId === null ? undefined : useHostStore.getState().hosts[hostId]
  return host === undefined ? null : endpointOfHost(host)
}

/** The context to freeze now, for `lock` — null without a master. */
function contextNow(lock: SectionLock): ResolveContext | null {
  const s = useProfileStore.getState()
  const master = selectMaster(s)
  const tag = liveTagOf(s)
  if (master === null || tag === null || s.masterEndpoint === null) return null
  return { tag, hostId: master.hostId, profileId: master.profileId, endpoint: s.masterEndpoint, lock }
}

/** Is the frozen context still the one on screen: same master tag, same attachment endpoint, the host at it, same lock. */
export function stillCurrent(ctx: ResolveContext, liveLock: SectionLock | undefined): boolean {
  const s = useProfileStore.getState()
  if (liveTagOf(s) !== ctx.tag || s.masterEndpoint !== ctx.endpoint) return false
  if (hostEndpointOf(ctx.hostId) !== ctx.endpoint) return false
  return liveLock !== undefined && sameLock(ctx.lock, liveLock)
}

export interface ResolveState {
  /** The open confirmation: what it would keep, and its frozen context. */
  open: { keep: Keep; ctx: ResolveContext } | null
  /** null = still being read. */
  local: LocalSide | null
  host: HostSide | null
  /** A confirmation closed itself: its context stopped being current. */
  changed: boolean
  outcome: Outcome | null
  ask(keep: Keep): void
  cancel(): void
  confirm(): void
}

export function useResolveContext(sectionKey: string, lock: SectionLock): ResolveState {
  // Subscribed so that a change of any of them re-renders — and the check below runs. The values are re-read there.
  const liveTag = useProfileStore(liveTagOf)
  useProfileStore((s) => s.masterEndpoint)
  const masterHostId = useProfileStore((s) => s.masterHostId)
  useHostStore((s) => {
    const h = masterHostId === null ? undefined : s.hosts[masterHostId]
    return h === undefined ? null : endpointOfHost(h)
  })

  const [open, setOpen] = useState<{ keep: Keep; ctx: ResolveContext } | null>(null)
  const [local, setLocal] = useState<LocalSide | null>(null)
  const [host, setHost] = useState<HostSide | null>(null)
  const [changed, setChanged] = useState(false)
  const [outcome, setOutcome] = useState<Outcome | null>(null)
  /** The live lock, for the answers that arrive later. */
  const lockRef = useRef(lock)
  useLayoutEffect(() => {
    lockRef.current = lock
  })

  // Not current any more: the confirmation closes itself — adjusted during render, so no frame shows it.
  if (open !== null && !stillCurrent(open.ctx, lock)) {
    setOpen(null)
    setChanged(true)
  }
  // A "sent" is over when its lock or its master is not the one on screen any more (answered, or overtaken).
  if (outcome !== null && (!sameLock(outcome.lock, lock) || outcome.tag !== liveTag)) setOutcome(null)

  // Each side read ONCE per confirmation, for its frozen context.
  useEffect(() => {
    if (open === null) return
    const { ctx } = open
    let live = true
    const abort = new AbortController()
    const stale = (): boolean => !live || !stillCurrent(ctx, lockRef.current)
    const lost = (): void => {
      if (!live) return
      setOpen((now) => (now === open ? null : now))
      setChanged(true)
    }
    void readLocalSide(ctx.profileId, sectionKey, ctx.lock).then((side) => {
      if (live) setLocal(side)
    })
    if (stale()) lost() // before the host is asked: it must still be at the attachment's endpoint
    else {
      void readHostSide(ctx.hostId, ctx.profileId, sectionKey, ctx.lock, { expectEndpoint: ctx.endpoint, signal: abort.signal }).then((side) => {
        // …and when it answers: an answer from an address that moved meanwhile may be another daemon's
        if (stale()) lost()
        else setHost(side)
      })
    }
    return () => {
      live = false
      abort.abort()
    }
  }, [open, sectionKey])

  // The one timer: a "sent" that nothing answered by the command's TTL.
  useEffect(() => {
    if (outcome === null || outcome.state !== 'sent') return
    const sent = outcome
    const timer = setTimeout(() => setOutcome((now) => (now === sent ? { ...sent, state: 'no-answer' } : now)), COMMAND_TTL_MS)
    return () => clearTimeout(timer)
  }, [outcome])

  return {
    open,
    local,
    host,
    changed,
    outcome,
    ask(keep) {
      const ctx = contextNow(lock)
      if (ctx === null) return
      setChanged(false)
      setLocal(null)
      setHost(null)
      setOpen({ keep, ctx })
    },
    cancel() {
      setOpen(null)
    },
    confirm() {
      if (open === null) return
      const { keep, ctx } = open
      // A handler that runs after the context moved (before React removed the dialog) sends nothing.
      if (!stillCurrent(ctx, lockRef.current)) {
        setOpen(null)
        setChanged(true)
        return
      }
      const handed = requestResolve(sectionKey, keep, ctx.lock, ctx.tag)
      setOutcome({ state: handed ? 'sent' : 'not-sent', lock: ctx.lock, tag: ctx.tag })
      setOpen(null)
    },
  }
}
