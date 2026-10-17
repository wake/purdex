// spa/src/lib/watch/queued-sessions.ts — which agent sessions are waiting in a host's command queue (watcher-area plan §8),
// read from the fleet snapshot. A subagent's command carries its main session's id (spec §2.3), so a session id is all it takes.
import type { FleetWaiter } from '../fleet/types'
import type { FleetSnapshot } from '../fleet/store'

/** Every waiter of every host in the snapshot, by lowercase session id (empty ids dropped). */
export function queuedBySession(snapshot: FleetSnapshot | null): Map<string, FleetWaiter[]> {
  const out = new Map<string, FleetWaiter[]>()
  if (!snapshot) return out
  for (const id of snapshot.order) {
    for (const w of snapshot.hosts[id]?.slow?.queue?.waiters ?? []) {
      if (!w.session) continue
      const k = w.session.toLowerCase()
      const list = out.get(k)
      if (list) list.push(w)
      else out.set(k, [w])
    }
  }
  return out
}

type T = (key: string, params?: Record<string, string | number>) => string

/** 「排隊中：第 {place} 位・已等 {m} 分鐘・{command}（重指令｜輕指令）」 for the longest wait, plus 「・另有 n 個」. */
export function queuedTooltip(waiters: FleetWaiter[], now: number, t: T): string {
  if (waiters.length === 0) return ''
  // an unknown start (null) sorts as the shortest wait
  const since = (w: FleetWaiter) => w.since ?? Number.POSITIVE_INFINITY
  const top = waiters.reduce((a, b) => (since(b) < since(a) ? b : a))
  const m = top.since === null ? 0 : Math.max(0, Math.floor((now - top.since) / 60_000))
  const head = t('tab.queued.tooltip', {
    place: top.place, m, command: top.command, kind: t(top.class === 'light' ? 'tab.queued.light' : 'tab.queued.heavy'),
  })
  return waiters.length > 1 ? head + t('tab.queued.more', { n: waiters.length - 1 }) : head
}
