// spa/src/hooks/useExecutionSubscription.ts — one pane's observe path onto a
// Nexen execution (spec §4.3.2): summary → attach(observe) → history pages in
// ascending seq order → THEN the SSE with Last-Event-ID = store.lastSeq.
// That order is a contract with the reducer's single high-water mark (a live
// frame applied first would make every older history event look like a
// duplicate). The summary is authoritative: lifecycle events only mark it
// stale and this hook refetches, debounced; it also refetches once after a
// reconnect. Host removal in keep-tabs mode tears everything down here.
import { useEffect, useRef, useState } from 'react'
import { attachObserve, fetchExecutionEvents, fetchExecutionTasks, getExecution } from '../lib/nex/nex-api'
import { openNexSse, type NexSseHandle } from '../lib/nex/nex-sse'
import { frameToEvent } from '../lib/nex/event-reducer'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import { createTransientFrameQueue, type TransientFrameQueue } from '../lib/nex/transient-frame-queue'
import { NexApiError } from '../lib/nex/types'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'
import { selectWorkerRollup, useNexHostStore } from '../stores/useNexHostStore'

export type SubscriptionProblem = null | 'not_found' | 'host_removed' | 'nex_unavailable' | 'nex_disabled'
export const HISTORY_PAGE_LIMIT = 500
export const SUMMARY_REFETCH_DEBOUNCE_MS = 300
// A stale/failed summary refetch reschedules itself: `summaryStale` only
// notifies subscribers on a boolean transition, so a second lifecycle
// event landing while a refetch is in flight would
// otherwise leave the store stuck stale forever. Cap consecutive attempts so
// a persistently failing daemon doesn't retry every debounce indefinitely.
const MAX_CONSECUTIVE_STALE_REFETCHES = 5

/** One instance's own stream while it holds one: dialling in, delivering, or between two attempts. */
interface HeldStream { status: 'connecting' | 'open' | 'reconnecting'; err: string | null }
const HELD_RANK: Record<HeldStream['status'], number> = { connecting: 0, reconnecting: 1, open: 2 }

/**
 * The streams held per execution (its store key), one per main-effect run of this hook whose own stream is
 * connecting, open or reconnecting. The same execution shown in two panes (a split, or two tabs) runs two instances
 * that share the store entry and the subscription-slot key (the slot registry holds one slot per key, however many
 * instances touch it), so neither the slot nor the entry's stream status is any one instance's to give up:
 *  - the entry shows the best stream any instance holds (open, then reconnecting, then connecting), so a pane dialling
 *    in or recovering never hides another pane's delivering stream;
 *  - an instance whose own stream ends — for any reason: unmount, a terminal problem, a terminal close, a failed
 *    attempt, an eviction — leaves the set. Only the last one out frees the slot and writes the entry's streamless
 *    status (`closed` with its reason, `paused`, `idle`); before that the leaver keeps its problem / retry to itself.
 * Membership, not mounting, is what counts: a mounted pane with a dead stream holds nothing for the others.
 */
const streamsByKey = new Map<string, Map<object, HeldStream>>()

/** The stream the entry for `key` shows: the best one held; null when none is. */
function bestStream(key: string): HeldStream | null {
  let best: HeldStream | null = null
  for (const s of streamsByKey.get(key)?.values() ?? []) if (!best || HELD_RANK[s.status] > HELD_RANK[best.status]) best = s
  return best
}

export function useExecutionSubscription(hostId: string, executionId: string, active: boolean): { problem: SubscriptionProblem; paused: boolean } {
  const [problem, setProblem] = useState<SubscriptionProblem>(null)
  const [paused, setPaused] = useState(false)
  const key = executionKey(hostId, executionId)
  // In the main effect's deps: a removed host that comes back (undo of a
  // keep-tabs delete) must start a fresh chain, since hostId/executionId
  // themselves never changed.
  const hostPresent = useHostStore((s) => !!s.hosts[hostId])
  const sseRef = useRef<NexSseHandle | null>(null)
  // Set by the main effect once history is loaded (spec order contract): a
  // function that (re)opens the live stream. Read only by the activation
  // effect below to resume after a pause; null while history is still
  // loading guards against a premature reopen.
  const openStreamRef = useRef<(() => void) | null>(null)

  // Activation: claims/refreshes this pane's live slot whenever `active`
  // (or hostId/key) actually changes — NOT on every render, which would
  // self-heal a legitimate eviction the instant this component re-renders
  // as a side effect of that very eviction (setPaused(true) below). A pane
  // that lost its slot to a busier sibling reclaims it only when the
  // caller signals renewed interest by toggling `active`; nothing resumes
  // it spontaneously just because a slot happens to free up elsewhere.
  useEffect(() => {
    if (!active || problem) return
    if (!useHostStore.getState().hosts[hostId]) return // main effect already reports host_removed
    subscriptionSlots.touch(hostId, key)
    if (subscriptionSlots.isLive(hostId, key) && !sseRef.current && openStreamRef.current) {
      setPaused(false)
      openStreamRef.current() // rejoins this execution's streams as `connecting`
    }
  }, [active, hostId, executionId, key, problem])

  useEffect(() => {
    let cancelled = false
    let refetchTimer: ReturnType<typeof setTimeout> | null = null
    let wasReconnecting = false
    setProblem(null)
    // A pane paused under its PREVIOUS executionId must not carry that flag
    // into a fresh chain for a new one — the new key gets its own slot
    // decision below (isLive / claimIfFree) and sets `paused` again only if
    // that fails.
    setPaused(false)
    const store = () => useExecutionStore.getState()

    // Spec §4.3.2 step 5: the pane's stored host is the only host we talk
    // to. Missing → host_removed now, no request, no fallback.
    if (!hostPresent) {
      setProblem('host_removed')
      store().setSse(hostId, executionId, 'closed', 'host_removed')
      return
    }
    // This run's own stream among the ones held for `key` (`streamsByKey`).
    const owner = {}
    // Its stream is connecting / open / reconnecting: the entry shows the best one any instance holds.
    const hold = (status: HeldStream['status'], err: string | null = null) => {
      let held = streamsByKey.get(key)
      if (!held) { held = new Map(); streamsByKey.set(key, held) }
      held.set(owner, { status, err })
      const best = bestStream(key)!
      store().setSse(hostId, executionId, best.status, best.err)
    }
    // Its stream ended (idempotent). True when no instance holds one any more: the caller then frees the slot and
    // writes the entry's streamless status. Otherwise the entry shows the best stream still held (this one may have
    // been it) and the slot stays the others'.
    const letGo = (): boolean => {
      const held = streamsByKey.get(key)
      if (!held) return true
      if (held.delete(owner)) {
        if (held.size === 0) { streamsByKey.delete(key); return true }
        const best = bestStream(key)!
        store().setSse(hostId, executionId, best.status, best.err)
      }
      return false
    }

    let staleRefetchAttempts = 0
    const refetchSummary = async () => {
      const asOf = store().executions[key]?.lastSeq ?? 0
      const gen = store().executions[key]?.summaryGen ?? 0
      try {
        const s = await getExecution(hostId, executionId)
        if (cancelled) return
        store().setSummary(hostId, executionId, s, asOf, gen)
        // subscribeWithSelector only notifies on a boolean transition: a
        // second lifecycle event landing while this fetch was in flight
        // (lastSeq advancing past asOf again) leaves summaryStale true with
        // no new true→true notification to re-trigger us. Check the result
        // directly and reschedule ourselves when still stale, capped so a
        // persistently failing daemon doesn't retry forever.
        if (store().executions[key]?.summaryStale) {
          staleRefetchAttempts += 1
          if (staleRefetchAttempts < MAX_CONSECUTIVE_STALE_REFETCHES) scheduleRefetch()
        } else {
          staleRefetchAttempts = 0
        }
      } catch {
        if (cancelled) return
        // transient — the next stale mark or reconnect tries again, up to the same cap
        staleRefetchAttempts += 1
        if (staleRefetchAttempts < MAX_CONSECUTIVE_STALE_REFETCHES) scheduleRefetch()
      }
    }
    // Belt and braces alongside the `cancelled` checks above: even if some
    // future caller of scheduleRefetch forgets to check `cancelled` first,
    // a debounced getExecution settling after unmount/key-change can never
    // arm a new timer here.
    // nexen #83: the task table is rebuilt by history replay, but a (re)open
    // can miss task_end frames. Every `open` — the first, each reconnect,
    // and a resumed stream after eviction — re-reads the running rows and
    // merges them. Only the newest read may land: `tasksGen` bumps per read
    // (and `cancelled` covers an execution switch), so an older answer
    // arriving late is dropped rather than merged over a newer one.
    let tasksGen = 0
    const refetchTasks = async () => {
      if (!selectWorkerRollup(hostId)(useNexHostStore.getState())) return
      const gen = ++tasksGen
      try {
        const snap = await fetchExecutionTasks(hostId, executionId, 'running')
        if (cancelled || gen !== tasksGen) return
        store().applyTasksSnapshot(hostId, executionId, snap)
      } catch {
        // transient — the next (re)open reads again; live events keep flowing
      }
    }

    const scheduleRefetch = () => {
      if (cancelled || refetchTimer) return
      refetchTimer = setTimeout(() => { refetchTimer = null; void refetchSummary() }, SUMMARY_REFETCH_DEBOUNCE_MS)
    }

    const unsubStale = useExecutionStore.subscribe(
      (s) => s.executions[key]?.summaryStale ?? false,
      (stale) => { if (stale && !cancelled) scheduleRefetch() },
    )

    // release: whether the slot claimed by the activation effect should be
    // given back. True once this subscription reaches a terminal problem —
    // a dead subscription must not keep occupying one of the 4 live slots.
    // `detail` is the server's own message (spec §4.5 wants it surfaced,
    // e.g. "nex: init: assembling engine: …") — falls back to the problem
    // code itself only when the caller has nothing better (a plain reason
    // like 'host_removed' has no server text to carry).
    // Transient frames (spec §4.3) go through a per-stream queue that
    // coalesces them per animation frame and drops them at connection
    // boundaries (transient-frame-queue.ts). One queue per openStream: a
    // closed queue never flushes again, and a pane resumed after eviction
    // opens a fresh stream with a fresh queue.
    let queue: TransientFrameQueue | null = null
    // Closes this run's own stream; returns `letGo()`'s answer (no instance holds one any more).
    const closeStream = (): boolean => {
      tasksGen += 1 // a /tasks read for the closed stream must not land
      sseRef.current?.close()
      sseRef.current = null
      queue?.close()
      queue = null
      return letGo()
    }

    // The problem is this pane's own; the entry and the slot are shared, so only the last stream out closes and frees them.
    const teardown = (reason?: SubscriptionProblem, detail?: string): boolean => {
      const last = closeStream()
      if (refetchTimer) { clearTimeout(refetchTimer); refetchTimer = null }
      if (reason) {
        setProblem(reason)
        if (last) {
          store().setSse(hostId, executionId, 'closed', detail ?? reason)
          subscriptionSlots.release(hostId, key)
        }
      }
      return last
    }

    // useHostStore has no subscribeWithSelector: compare prev/next by hand.
    const unsubHost = useHostStore.subscribe((state, prev) => {
      if (prev.hosts[hostId] && !state.hosts[hostId] && !cancelled) {
        cancelled = true
        teardown('host_removed')
      }
    })

    let unsubEvict: (() => void) | null = null
    let retryTimer: ReturnType<typeof setTimeout> | null = null
    let retryAttempt = 0

    const connect = async () => {
      hold('connecting')
      try {
        const asOf = store().executions[key]?.lastSeq ?? 0
        const gen = store().executions[key]?.summaryGen ?? 0
        const s = await getExecution(hostId, executionId)
        if (cancelled) return
        store().setSummary(hostId, executionId, s, asOf, gen)
        const obs = await attachObserve(hostId, executionId)
        if (cancelled) return
        // History: forward-only paging from 0; stop at the last page or once
        // we have reached the cursor attach reported.
        let after = 0
        for (;;) {
          const page = await fetchExecutionEvents(hostId, executionId, { after, limit: HISTORY_PAGE_LIMIT })
          if (cancelled) return
          store().applyEvents(hostId, executionId, page.items)
          if (page.next_cursor === 0 || page.next_cursor >= obs.cursor) break
          after = page.next_cursor
        }
        store().setHistoryLoaded(hostId, executionId, true)
        let warnedMalformed = false
        const openStream = () => {
          queue?.close()
          const q = createTransientFrameQueue({ flush: (batch) => store().applyTransient(hostId, executionId, batch) })
          queue = q
          sseRef.current = openNexSse({
            hostId,
            url: obs.stream_url,
            getLastEventId: () => store().executions[key]?.lastSeq ?? null,
            onFrame: (frame) => {
              if (frame.id == null) {
                let payload: unknown
                try { payload = JSON.parse(frame.data) } catch { return }
                if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return
                q.enqueue(frame.event, payload as Record<string, unknown>)
                return
              }
              // A delta queued before its finalizing `assistant` must land first.
              q.flushNow()
              const ev = frameToEvent(frame)
              if (!ev) {
                // A durable frame that does not parse is dropped without moving
                // the cursor and warned about once per connection (spec §4.5).
                if (!warnedMalformed) {
                  warnedMalformed = true
                  console.warn(`nex sse: dropped malformed durable frame id=${frame.id} kind=${frame.event}`)
                }
                return
              }
              // Nexen's live `data:` is the bare provider payload with no
              // `{seq, kind, payload, created_at}` wrapper (api/sse.go
              // writeFrame), so frameToEvent yields created_at 0 here.
              // Stamp client arrival time so the tool timers (spec §4.2 A1/
              // A2) run on live turns; history keeps the server's ms. Same
              // split as Nexen's own console. The reducer stays pure.
              if (ev.created_at === 0) ev.created_at = Date.now()
              store().applyEvents(hostId, executionId, [ev])
            },
            onStatus: (status, err) => {
              if (cancelled) return
              if (status === 'connecting' || status === 'reconnecting') q.bumpGeneration()
              if (status === 'closed') q.close()
              // This stream's status reaches the entry only as far as no other pane's stream says better (`hold`);
              // a closed one says `closed` only when it was the last stream held.
              if (status !== 'closed') hold(status, err?.message ?? null)
              const last = status === 'closed' && letGo()
              if (last) store().setSse(hostId, executionId, 'closed', err?.message ?? null)
              if (status === 'reconnecting') wasReconnecting = true
              if (status === 'open') { warnedMalformed = false; void refetchTasks(); if (wasReconnecting) { wasReconnecting = false; void refetchSummary() } }
              if (status === 'closed' && err) {
                // Terminal from openNexSse itself (401/403, or a
                // non-retryable structured error code) — the handle is
                // already dead and will never reconnect on its own. Drop it
                // and free the slot so a later activation can claim a fresh
                // one instead of the pane being stuck "live" forever with a
                // broken stream and nobody else able to use its slot —
                // unless another pane's stream still holds that slot.
                sseRef.current = null
                if (last) {
                  subscriptionSlots.release(hostId, key)
                  // A send already in flight when the stream dies terminally
                  // would otherwise leave pendingSend stuck forever: no SSE
                  // will ever deliver the message_accepted/result that would
                  // clear it, and the input stays disabled with no way out
                  // short of a full remount. Another pane's live stream
                  // still delivers it, so then it stays.
                  store().setPendingSend(hostId, executionId, false)
                }
              }
            },
          })
        }
        // The activation effect above claims the slot synchronously before
        // this async work even starts (it runs first, same commit, on
        // mount) — so by the time history is loaded, isLive already
        // reflects whether we hold a slot from an `active` pane. But an
        // inactive-at-mount pane never runs that effect's touch(), so it
        // would otherwise stay paused forever even with slots free — spec
        // §4.3.2 step 4 says only eviction pauses; an idle cap should not.
        // claimIfFree grabs a free slot without evicting anyone, so a
        // restored (hidden, inactive) tab still goes live up to the cap.
        // An eviction takes the key's one slot from every pane showing it
        // (each gets the notice); the entry says `paused` once the last of
        // their streams is out. A resume (activation effect) rejoins the
        // streams held for the key as `connecting` before it dials.
        unsubEvict = subscriptionSlots.onEvict(key, () => {
          const last = closeStream()
          setPaused(true)
          if (last) store().setSse(hostId, executionId, 'paused')
        })
        openStreamRef.current = () => { hold('connecting'); openStream() }
        if (subscriptionSlots.isLive(hostId, key) || subscriptionSlots.claimIfFree(hostId, key)) openStream()
        else { const last = letGo(); setPaused(true); if (last) store().setSse(hostId, executionId, 'paused') }
        retryAttempt = 0
      } catch (e) {
        if (cancelled) return
        if (e instanceof NexApiError && e.code === 'execution_not_found') teardown('not_found', e.message)
        else if (e instanceof NexApiError && e.code === 'nex_unavailable') teardown('nex_unavailable', e.message)
        else if (e instanceof NexApiError && e.code === 'http_404') teardown('nex_disabled', e.message)
        else {
          // Unexpected, non-terminal error before the stream ever opened (a
          // claimed slot would otherwise leak forever on a subscription
          // that never retries). The common trigger is app start with
          // restored tabs before the host connection is up — retry the
          // whole chain with backoff (2s -> 4 -> 8 -> capped at 30s)
          // instead of getting stuck at "Loading execution..." forever.
          // `problem` is left null so the view keeps showing the loading
          // state (with sseError surfaced) rather than a terminal one. While
          // another pane's stream still holds the slot and feeds the entry,
          // this attempt's failure stays this pane's own (its retry below).
          sseRef.current = null
          if (letGo()) {
            subscriptionSlots.release(hostId, key)
            store().setSse(hostId, executionId, 'closed', e instanceof Error ? e.message : String(e))
          }
          retryAttempt += 1
          const delay = Math.min(2000 * 2 ** (retryAttempt - 1), 30000)
          retryTimer = setTimeout(() => {
            retryTimer = null
            if (!cancelled) void connect()
          }, delay)
        }
      }
    }
    void connect()

    return () => {
      cancelled = true
      unsubStale()
      unsubHost()
      unsubEvict?.()
      if (retryTimer) { clearTimeout(retryTimer); retryTimer = null }
      openStreamRef.current = null
      // This instance's own stream always closes. The shared slot and entry are another pane's while its stream is
      // still held (`streamsByKey`): only the last stream out releases the one and idles the other.
      const last = teardown()
      if (!last) return
      subscriptionSlots.release(hostId, key)
      // The entry outlives the pane (the next mount starts from it), but no stream of this hook feeds it any more, and
      // `cancelled` keeps the closing stream from saying so: mark it streamless (`idle`, what a fresh mount begins
      // from) so it is never taken for a live one. A terminal problem (`closed` with its reason) or an eviction
      // (`paused`) is left as it is. The next mount's chain sets its own status first thing (`connecting`).
      const sse = store().executions[key]?.sse
      if (sse === 'open' || sse === 'connecting' || sse === 'reconnecting') store().setSse(hostId, executionId, 'idle')
    }
  }, [hostId, executionId, key, hostPresent])

  return { problem, paused }
}
