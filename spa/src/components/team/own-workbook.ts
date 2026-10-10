// spa/src/components/team/own-workbook.ts — the workbook of the active tab's OWN conversation (WA-2b-1b, round 3): a tab of
// no team whose first live `cc` tmux-session pane has a recorded session id (`rebuild.agent`, the same id the daemon's
// workbook is keyed by) and whose host lists `workbook.v1` with a workbook for it. Other agents and sessions with no
// workbook yet have none (the area is then absent too). There is no per-tab toggle.
import { useEffect, useMemo } from 'react'
import { useTabStore } from '../../stores/useTabStore'
import { useWorkbookStore } from '../../stores/useWorkbookStore'
import { collectLeaves } from '../../lib/pane-tree'
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
  useEffect(() => {
    if (hostId !== '' && v1) void useWorkbookStore.getState().loadSeat(hostId, sessionId)
  }, [hostId, sessionId, gen, v1, seated])
  return target !== null && wb.has ? target : null
}
