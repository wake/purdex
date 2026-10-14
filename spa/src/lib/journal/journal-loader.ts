// spa/src/lib/journal/journal-loader.ts — which unit the active tab shows, kept loaded whether or not the journal card is
// open (so the 日誌 button can show its count). One module-level subscription for the app's lifetime, started from main.tsx.
//
// Target: the active tab's own `cc` session (`ownSessionOf`); the daemon resolves a member's session to its team's unit, so a
// member's tab and its lead's tab show the same one. A host answers `409 {lead_host_id, lead_session_id}` when the lead lives on
// another daemon: the route then FOLLOWS the lead (the SPA host whose verified daemon id is `lead_host_id`, `teamHostMap`), all four
// endpoints go there with the lead's session, and the unit key is that host's. A lead host this App does not have is
// `unreachable` (the card says so).
//
// Loading rules (plan T-2a.6, bounded like the workbook loader):
//   * LOAD ONCE per (host, session, connection generation) — a tab switch back to a loaded unit asks nothing;
//   * only on a host whose live support is `yes`; a disconnect (support unknown) loads nothing, the data held stays;
//   * `journal.changed` only marks stale (journal-ws.ts); the ACTIVE unit's stale resources are re-read after a 300 ms debounce
//     (a burst reads once); a hidden unit's stale flag waits until its tab is active, then it is read once;
//   * tasks / roadmap are re-read only if the tab opened them before (`tasks !== null`); until then the stale flag just waits;
//   * a failed read is not marked loaded (the next tab switch / generation / event tries again; nothing here polls).
import { ownSessionOf } from '../../components/team/own-workbook'
import { useHostStore } from '../../stores/useHostStore'
import { isStale, liveSupport, selectUnit, sessionKey, useJournalStore, type LoadResult } from '../../stores/useJournalStore'
import { useTabStore } from '../../stores/useTabStore'
import { teamHostMap } from '../team/team-state'

export const STALE_DEBOUNCE_MS = 300

interface Target { hostId: string; sessionId: string }

export function startJournalLoader(): () => void {
  const loaded = new Set<string>() // `${host}\0${session}\0${gen}`
  const inflight = new Set<string>()
  const retryOnSwitch = new Set<string>()
  let timer: ReturnType<typeof setTimeout> | undefined
  let timerFor = ''
  let stopped = false

  const gen = (hostId: string): number => useJournalStore.getState().gens[hostId] ?? 0
  const keyOf = (t: Target): string => `${sessionKey(t.hostId, t.sessionId)}\u0000${gen(t.hostId)}`
  const live = (hostId: string): boolean => liveSupport(hostId) === true

  const activeOrigin = (): Target | null => {
    const { activeTabId, tabs } = useTabStore.getState()
    const layout = activeTabId ? tabs[activeTabId]?.layout : undefined
    return layout ? ownSessionOf(layout) : null
  }

  /** Where the origin's journal is read from now: its followed lead, else the origin itself. */
  const effective = (origin: Target): Target | null => {
    const route = useJournalStore.getState().routes[sessionKey(origin.hostId, origin.sessionId)]
    if (route?.kind === 'follow') return { hostId: route.hostId, sessionId: route.sessionId }
    if (route?.kind === 'unreachable' || route?.kind === 'none') return null
    return origin
  }

  /** The first read of `t` in its connection generation, once. Returns the daemon's answer, or null when nothing was asked. */
  async function loadOnce(t: Target): Promise<LoadResult | null> {
    const key = keyOf(t)
    if (!live(t.hostId) || loaded.has(key) || inflight.has(key)) return null
    inflight.add(key)
    let res: LoadResult
    try {
      res = await useJournalStore.getState().loadJournal(t.hostId, t.sessionId)
    } finally {
      inflight.delete(key)
    }
    // Every answer counts as this generation's attempt, so a store or host-runtime change cannot turn a 404 or a 5xx into a request
    // loop; the ones that may change by themselves (no unit yet, a failure, a read dropped by a reconnect) are retried when the
    // active tab changes (`retryOnSwitch`) or the generation moves on.
    loaded.add(key)
    if (res.kind !== 'ok' && res.kind !== 'remote') retryOnSwitch.add(key)
    return res
  }

  async function ensure(origin: Target): Promise<void> {
    const store = useJournalStore.getState()
    const originKey = keyOf(origin)
    // An ok / none / remote answer always leaves a route; a loaded mark with no route and no pending retry means the route was
    // dropped (its lead host was forgotten): ask the origin again so the lead can be found anew.
    if (!store.routes[sessionKey(origin.hostId, origin.sessionId)] && loaded.has(originKey) && !retryOnSwitch.has(originKey)) loaded.delete(originKey)
    const res = await loadOnce(origin)
    if (stopped) return
    if (res?.kind === 'ok') store.setRoute(origin.hostId, origin.sessionId, { kind: 'direct' })
    else if (res?.kind === 'none') store.setRoute(origin.hostId, origin.sessionId, { kind: 'none' })
    else if (res?.kind === 'remote') store.setRoute(origin.hostId, origin.sessionId, { kind: 'unreachable', leadHostId: res.leadHostId, leadSessionId: res.leadSessionId })
    // Second step, driven by the route: a lead host this App lacked may be configured (and verified) by now, and a followed lead
    // host has its own connection generation.
    const route = useJournalStore.getState().routes[sessionKey(origin.hostId, origin.sessionId)]
    if (route?.kind === 'direct' || route?.kind === 'none' || !route) return
    // The lead's SPA host is looked up again every time (`teamHostMap` holds only hosts whose daemon id is verified now), so a
    // re-pointed or forgotten host is never trusted from the stored route.
    const leadDaemon = route.leadHostId
    const leadSessionId = route.kind === 'follow' ? route.sessionId : route.leadSessionId
    const leadHostId = teamHostMap(useHostStore.getState())[leadDaemon]
    if (!leadHostId) {
      if (route.kind === 'follow') store.setRoute(origin.hostId, origin.sessionId, { kind: 'unreachable', leadHostId: leadDaemon, leadSessionId })
      return // still unreachable
    }
    const lead = { hostId: leadHostId, sessionId: leadSessionId }
    if (route.kind === 'unreachable' || route.hostId !== leadHostId) store.setRoute(origin.hostId, origin.sessionId, { kind: 'follow', hostId: lead.hostId, sessionId: lead.sessionId, leadHostId: leadDaemon })
    const followed = await loadOnce(lead)
    // A lead host that itself answers 409 would loop; one hop only, but the new target is the one kept.
    if (!stopped && followed?.kind === 'remote') store.setRoute(origin.hostId, origin.sessionId, { kind: 'unreachable', leadHostId: followed.leadHostId, leadSessionId: followed.leadSessionId })
  }

  /** A route that was learned in an older generation of the origin may have moved: it is re-asked with the origin (loadOnce). */
  const sync = () => {
    if (stopped) return
    const origin = activeOrigin()
    if (!origin) return
    void ensure(origin).then(() => { if (!stopped) scheduleStale() })
    scheduleStale()
  }

  function scheduleStale() {
    const origin = activeOrigin()
    const t = origin ? effective(origin) : null
    if (!t || !live(t.hostId)) return
    const u = selectUnit(useJournalStore.getState(), t.hostId, t.sessionId)
    if (!u || !(isStale(u, 'entries') || (u.tasks !== null && isStale(u, 'tasks')) || (u.roadmap !== null && isStale(u, 'roadmap')))) return
    const key = sessionKey(t.hostId, t.sessionId)
    if (timer !== undefined && timerFor === key) return // a burst: the pending read covers it
    if (timer !== undefined) clearTimeout(timer)
    timerFor = key
    timer = setTimeout(() => { timer = undefined; timerFor = ''; void refresh() }, STALE_DEBOUNCE_MS)
  }

  async function refresh() {
    if (stopped) return
    const origin = activeOrigin()
    const t = origin ? effective(origin) : null
    if (!t || !live(t.hostId)) return
    const store = useJournalStore.getState()
    const u = selectUnit(store, t.hostId, t.sessionId)
    if (!u) return
    const reads: Promise<LoadResult>[] = []
    if (isStale(u, 'entries')) reads.push(store.loadJournal(t.hostId, t.sessionId))
    if (u.tasks !== null && isStale(u, 'tasks')) reads.push(store.loadTasks(t.hostId, t.sessionId))
    if (u.roadmap !== null && isStale(u, 'roadmap')) reads.push(store.loadRoadmap(t.hostId, t.sessionId))
    const results = await Promise.all(reads)
    if (stopped || !origin) return
    // Any of the three can learn that the unit moved to another daemon since it was routed: take the new lead tuple and follow it
    // (one hop, in ensure).
    const moved = results.find((r): r is Extract<LoadResult, { kind: 'remote' }> => r.kind === 'remote')
    if (moved) {
      store.setRoute(origin.hostId, origin.sessionId, { kind: 'unreachable', leadHostId: moved.leadHostId, leadSessionId: moved.leadSessionId })
      void ensure(origin)
    }
  }

  const stops = [
    useTabStore.subscribe((n, p) => {
      const layoutOf = (s: typeof n) => (s.activeTabId ? s.tabs[s.activeTabId]?.layout : undefined)
      if (n.activeTabId !== p.activeTabId) { for (const k of retryOnSwitch) loaded.delete(k); retryOnSwitch.clear() }
      if (n.activeTabId !== p.activeTabId || layoutOf(n) !== layoutOf(p)) sync()
    }),
    useHostStore.subscribe((n, p) => { if (n.hosts !== p.hosts || n.runtime !== p.runtime) sync() }), // the daemon-id verification lands in runtime
    useJournalStore.subscribe((n, p) => {
      // A forgotten host starts over (its generation restarts at 0): its loaded marks are the old daemon's.
      if (n.epoch !== p.epoch) for (const k of [...loaded]) { const h = k.split('\u0000')[0]; if ((n.epoch[h] ?? 0) !== (p.epoch[h] ?? 0)) { loaded.delete(k); retryOnSwitch.delete(k) } }
      if (n.support !== p.support || n.gens !== p.gens || n.staleRev !== p.staleRev) sync()
    }),
  ]
  sync()
  return () => { stopped = true; stops.forEach((f) => f()); if (timer !== undefined) clearTimeout(timer) }
}
