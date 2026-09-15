// spa/src/hooks/useExecutionSubscription.ts — one pane's observe path onto a
// Nexen execution (spec §4.3.2): summary → attach(observe) → history pages in
// ascending seq order → THEN the SSE with Last-Event-ID = store.lastSeq.
// That order is a contract with the reducer's single high-water mark (a live
// frame applied first would make every older history event look like a
// duplicate). The summary is authoritative: lifecycle events only mark it
// stale and this hook refetches, debounced; it also refetches once after a
// reconnect. Host removal in keep-tabs mode tears everything down here.
import { useEffect, useRef, useState } from 'react'
import { attachObserve, fetchExecutionEvents, getExecution } from '../lib/nex/nex-api'
import { openNexSse, type NexSseHandle } from '../lib/nex/nex-sse'
import { frameToEvent } from '../lib/nex/event-reducer'
import { subscriptionSlots } from '../lib/nex/subscription-slots'
import { NexApiError } from '../lib/nex/types'
import { useExecutionStore, executionKey } from '../stores/useExecutionStore'
import { useHostStore } from '../stores/useHostStore'

export type SubscriptionProblem = null | 'not_found' | 'host_removed' | 'nex_unavailable' | 'nex_disabled'
export const HISTORY_PAGE_LIMIT = 500
export const SUMMARY_REFETCH_DEBOUNCE_MS = 300
// A stale/failed summary refetch reschedules itself (fix round 1, Important
// 2): `summaryStale` only notifies subscribers on a boolean transition, so a
// second lifecycle event landing while a refetch is in flight would
// otherwise leave the store stuck stale forever. Cap consecutive attempts so
// a persistently failing daemon doesn't retry every debounce indefinitely.
const MAX_CONSECUTIVE_STALE_REFETCHES = 5

export function useExecutionSubscription(hostId: string, executionId: string, active: boolean): { problem: SubscriptionProblem; paused: boolean } {
  const [problem, setProblem] = useState<SubscriptionProblem>(null)
  const [paused, setPaused] = useState(false)
  const key = executionKey(hostId, executionId)
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
      useExecutionStore.getState().setSse(hostId, executionId, 'connecting')
      openStreamRef.current()
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
    if (!useHostStore.getState().hosts[hostId]) {
      setProblem('host_removed')
      store().setSse(hostId, executionId, 'closed', 'host_removed')
      return
    }

    let staleRefetchAttempts = 0
    const refetchSummary = async () => {
      const asOf = store().executions[key]?.lastSeq ?? 0
      try {
        const s = await getExecution(hostId, executionId)
        if (cancelled) return
        store().setSummary(hostId, executionId, s, asOf)
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
    const teardown = (reason?: SubscriptionProblem, detail?: string) => {
      sseRef.current?.close()
      sseRef.current = null
      if (refetchTimer) { clearTimeout(refetchTimer); refetchTimer = null }
      if (reason) {
        setProblem(reason)
        store().setSse(hostId, executionId, 'closed', detail ?? reason)
        subscriptionSlots.release(hostId, key)
      }
    }

    // useHostStore has no subscribeWithSelector: compare prev/next by hand.
    const unsubHost = useHostStore.subscribe((state, prev) => {
      if (prev.hosts[hostId] && !state.hosts[hostId] && !cancelled) {
        cancelled = true
        teardown('host_removed')
      }
    })

    let unsubEvict: (() => void) | null = null

    ;(async () => {
      store().setSse(hostId, executionId, 'connecting')
      try {
        const asOf = store().executions[key]?.lastSeq ?? 0
        const s = await getExecution(hostId, executionId)
        if (cancelled) return
        store().setSummary(hostId, executionId, s, asOf)
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
          sseRef.current = openNexSse({
            hostId,
            url: obs.stream_url,
            getLastEventId: () => store().executions[key]?.lastSeq ?? null,
            onFrame: (frame) => {
              const ev = frameToEvent(frame)
              if (!ev) {
                // Transient frames are expected (P-B2 renders them); a durable
                // frame that does not parse is dropped without moving the cursor
                // and warned about once per connection (spec §4.5).
                if (frame.id != null && !warnedMalformed) {
                  warnedMalformed = true
                  console.warn(`nex sse: dropped malformed durable frame id=${frame.id} kind=${frame.event}`)
                }
                return
              }
              store().applyEvents(hostId, executionId, [ev])
            },
            onStatus: (status, err) => {
              if (cancelled) return
              store().setSse(hostId, executionId, status, err?.message ?? null)
              if (status === 'reconnecting') wasReconnecting = true
              if (status === 'open') { warnedMalformed = false; if (wasReconnecting) { wasReconnecting = false; void refetchSummary() } }
              if (status === 'closed' && err) {
                // Terminal from openNexSse itself (401/403, or a
                // non-retryable structured error code) — the handle is
                // already dead and will never reconnect on its own. Drop it
                // and free the slot so a later activation can claim a fresh
                // one instead of the pane being stuck "live" forever with a
                // broken stream and nobody else able to use its slot.
                sseRef.current = null
                subscriptionSlots.release(hostId, key)
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
        unsubEvict = subscriptionSlots.onEvict(key, () => {
          sseRef.current?.close(); sseRef.current = null
          setPaused(true)
          store().setSse(hostId, executionId, 'paused')
        })
        openStreamRef.current = openStream
        if (subscriptionSlots.isLive(hostId, key) || subscriptionSlots.claimIfFree(hostId, key)) openStream()
        else { setPaused(true); store().setSse(hostId, executionId, 'paused') }
      } catch (e) {
        if (cancelled) return
        if (e instanceof NexApiError && e.code === 'execution_not_found') teardown('not_found', e.message)
        else if (e instanceof NexApiError && e.code === 'nex_unavailable') teardown('nex_unavailable', e.message)
        else if (e instanceof NexApiError && e.code === 'http_404') teardown('nex_disabled', e.message)
        else {
          // Unexpected error before the stream ever opened (a claimed slot
          // would otherwise leak forever on a subscription that can never
          // succeed on its own).
          sseRef.current = null
          subscriptionSlots.release(hostId, key)
          store().setSse(hostId, executionId, 'closed', e instanceof Error ? e.message : String(e))
        }
      }
    })()

    return () => {
      cancelled = true
      unsubStale()
      unsubHost()
      unsubEvict?.()
      openStreamRef.current = null
      subscriptionSlots.release(hostId, key)
      teardown()
    }
  }, [hostId, executionId, key])

  return { problem, paused }
}
