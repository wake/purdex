// spa/src/components/team/own-workbook.ts — the workbook of the active tab's OWN conversation (WA-2b-1b, round 3): a tab of
// no team whose first live `cc` tmux-session pane has a recorded session id (`rebuild.agent`, the same id the daemon's
// workbook is keyed by) and whose host lists `workbook.v1` with a workbook for it. Other agents and sessions with no
// workbook yet have none (the area is then absent too). There is no per-tab toggle.
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { collectLeaves } from '../../lib/pane-tree'
import { WORKBOOK_MAX_RETRIES, workbookRetryDelay } from '../../lib/workbook/retry'
import type { PaneLayout } from '../../types/tab'
import { useI18nStore } from '../../stores/useI18nStore'
import { firstSentence, useSeatWorkbook } from './seat-workbook'
import type { OwnWorkbookTarget } from './panel-view'


/** The conversation a layout's first live `cc` tmux-session pane (pre-order) belongs to; null when there is none. */
export function ownSessionOf(layout: PaneLayout): OwnWorkbookTarget | null {
  for (const pane of collectLeaves(layout)) {
    const c = pane.content
    if (c.kind !== 'tmux-session' || c.terminated) continue
    const agent = c.rebuild?.agent
    if (agent?.type === 'cc' && agent.sessionId) return { hostId: c.hostId, sessionId: agent.sessionId }
  }
  return null
}

/** The one line both the pane's line and the title-bar strip draw: the first sentence of the status, or the "no status yet" word. */
export function useOwnStatusLine(hostId: string, sessionId: string): { text: string; full: string } {
  const t = useI18nStore((s) => s.t)
  const status = useSeatWorkbook(hostId, sessionId).conv?.status.trim() ?? ''
  return { text: status === '' ? t('team.workbook.no_status') : firstSentence(status), full: status }
}

/**
 * The active tab's own conversation, only when it HAS a workbook (null otherwise). Asking the host is the person looking at
 * this tab, not an event: `loadSeat` (limit 1, once per connection generation; the store dedupes) runs when the target, the
 * connection generation or the host's support answer changes, and again if a roster sync let go of the seat.
 */
export function useOwnWorkbook(): OwnWorkbookTarget | null {
  const layout = useTabStore((s) => (s.activeTabId ? s.tabs[s.activeTabId]?.layout : undefined))
  const target = useMemo(() => (layout ? ownSessionOf(layout) : null), [layout])
  const hostId = target?.hostId ?? ''
  const sessionId = target?.sessionId ?? ''
  const wb = useSeatWorkbook(hostId, sessionId)
  const gen = useWorkbookStore((s) => s.gens[hostId])
  const v1 = useWorkbookStore((s) => s.support[hostId]?.v1 === true)
  const seated = useWorkbookStore((s) => s.seatGen[hostId]?.[sessionId])
  // A probe that got no answer (network / 5xx) is retried with a bounded backoff: the store hands the seat back (it is not
  // marked loaded), and `retry` holds the count and the earliest next ask so that hand-back does not turn into a hot loop.
  const retry = useRef({ key: '', tries: 0, notBefore: 0, busy: '' })
  const [tick, setTick] = useState(0)
  useEffect(() => {
    if (hostId === '' || !v1) return
    const key = `${hostId}\u0000${sessionId}\u0000${gen ?? 0}`
    const r = retry.current
    if (r.key !== key) { r.key = key; r.tries = 0; r.notBefore = 0 }
    if (r.busy === key) return // the answer of the ask in flight decides what happens next
    if (Date.now() < r.notBefore) return
    r.busy = key
    void Promise.resolve(useWorkbookStore.getState().loadSeat(hostId, sessionId)).then((res) => {
      if (retry.current.busy === key) retry.current.busy = ''
      if (res !== 'failed' || retry.current.key !== key || retry.current.tries >= WORKBOOK_MAX_RETRIES) return
      const delay = workbookRetryDelay(retry.current.tries++)
      retry.current.notBefore = Date.now() + delay
      setTimeout(() => setTick((n) => n + 1), delay)
    })
  }, [hostId, sessionId, gen, v1, seated, tick])
  return target !== null && wb.has ? target : null
}
