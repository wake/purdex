// spa/src/components/journal/useJournalTarget.ts — what the journal surfaces (title-bar buttons, the card stack) show for the
// active tab: its host's sticky journal.v1 answer, the team, the unit the loader resolved, and where it is read from.
// Same origin as the loader (journal-loader.ts): the tab's own `cc` session; a team tab with no `cc` leaf falls back to the
// lead's host for the support check only (no unit is loaded for it).
import { useLayoutEffect, useMemo } from 'react'
import { migrateViewMemo } from '../../lib/journal/view-memory'
import { ownSessionOf } from '../team/own-workbook'
import { useTitleBarTeam } from '../team/useTitleBarTeam'
import type { TeamPanelTeam } from '../team/team-display'
import { useTabStore } from '../../stores/useTabStore'
import { selectUnit, sessionKey, useJournalStore, type UnitState } from '../../stores/useJournalStore'
import { unitKeyOf } from '../../lib/journal/types'
import { useIsJournalHost } from '../../lib/journal/use-journal-host'

export interface JournalTarget {
  /** The host whose support decides the UI; '' = the tab has neither a cc session nor a team. */
  hostId: string
  /** The host's sticky journal.v1 answer is yes: the card stack replaces the workbook view. */
  journalHost: boolean
  team: TeamPanelTeam | null
  /** The tab's own cc session. */
  origin: { hostId: string; sessionId: string } | null
  /** Where the journal is read from (a followed remote lead, else the origin). */
  read: { hostId: string; sessionId: string } | null
  unit: UnitState | undefined
  unitKey: string | null
  /** The lead lives on a daemon this App has no host for. */
  unreachableHost: string | null
  /** Where this tab's view memo lives (host, key); null until a unit (or the unreachable stand-in) exists. */
  memoHost: string | null
  memoKey: string | null
}


export function useJournalTarget(): JournalTarget {
  const team = useTitleBarTeam()
  const layout = useTabStore((s) => (s.activeTabId ? s.tabs[s.activeTabId]?.layout : undefined))
  const origin = useMemo(() => (layout ? ownSessionOf(layout) : null), [layout])
  const route = useJournalStore((s) => (origin ? s.routes[sessionKey(origin.hostId, origin.sessionId)] : undefined))
  const read = origin === null ? null : route?.kind === 'follow' ? { hostId: route.hostId, sessionId: route.sessionId } : route?.kind === 'unreachable' || route?.kind === 'none' ? null : origin
  // The host the unit is read FROM decides (spec §11): the followed lead's host, else the tab's own; while the route is unresolved
  // (unreachable / none) the tab's own host decides. A tab with no cc session uses its team's lead host.
  const hostId = read?.hostId ?? origin?.hostId ?? team?.lead.hostId ?? ''
  const journalHost = useIsJournalHost(hostId)
  const unit = useJournalStore((s) => (read ? selectUnit(s, read.hostId, read.sessionId) : undefined))
  const unitKey = unit ? unitKeyOf(unit.unit) : null
  const unreachableHost = route?.kind === 'unreachable' ? route.leadHostId : null
  // The view memo's key: the unit, or — where the lead's daemon is not connected — a stand-in so the card can still be opened to say so.
  const memoKey = unitKey ?? (unreachableHost !== null ? `unreachable:${unreachableHost}` : null)
  // The route resolved from `unreachable`: the stand-in's view state (open, widths, filter, scroll) moves to the resolved unit, once.
  // Stateless: the follow route still names the lead's daemon; every caller of this hook runs it, the move is idempotent.
  const standIn = route?.kind === 'follow' && origin ? `unreachable:${route.leadHostId}` : null
  useLayoutEffect(() => {
    if (standIn !== null && origin && read && unitKey !== null) migrateViewMemo(origin.hostId, standIn, read.hostId, unitKey)
  }, [standIn, origin, read?.hostId, unitKey]) // eslint-disable-line react-hooks/exhaustive-deps
  return { hostId, journalHost, team, origin, read, unit, unitKey, unreachableHost, memoHost: read?.hostId ?? origin?.hostId ?? null, memoKey }
}
