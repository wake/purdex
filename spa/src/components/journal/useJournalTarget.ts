// spa/src/components/journal/useJournalTarget.ts — what the journal surfaces (title-bar buttons, the card stack) show for the
// active tab: its host's sticky journal.v1 answer, the team, the unit the loader resolved, and where it is read from.
// Same origin as the loader (journal-loader.ts): the tab's own `cc` session; a team tab with no `cc` leaf falls back to the
// lead's host for the support check only (no unit is loaded for it).
import { useMemo } from 'react'
import { ownSessionOf } from '../team/own-workbook'
import { useTitleBarTeam } from '../team/useTitleBarTeam'
import type { TeamPanelTeam } from '../team/team-display'
import { useTabStore } from '../../stores/useTabStore'
import { selectUnit, sessionKey, useJournalStore, type UnitState } from '../../stores/useJournalStore'
import { unitKeyOf } from '../../lib/journal/types'

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
}

/** The host's sticky journal.v1 answer (the last yes / no, kept through a disconnect; absent = never answered = old UI). */
export const useIsJournalHost = (hostId: string): boolean => useJournalStore((s) => hostId !== '' && s.sticky[hostId] === true)

export function useJournalTarget(): JournalTarget {
  const team = useTitleBarTeam()
  const layout = useTabStore((s) => (s.activeTabId ? s.tabs[s.activeTabId]?.layout : undefined))
  const origin = useMemo(() => (layout ? ownSessionOf(layout) : null), [layout])
  const hostId = origin?.hostId ?? team?.lead.hostId ?? ''
  const journalHost = useIsJournalHost(hostId)
  const route = useJournalStore((s) => (origin ? s.routes[sessionKey(origin.hostId, origin.sessionId)] : undefined))
  const read = origin === null ? null : route?.kind === 'follow' ? { hostId: route.hostId, sessionId: route.sessionId } : route?.kind === 'unreachable' || route?.kind === 'none' ? null : origin
  const unit = useJournalStore((s) => (read ? selectUnit(s, read.hostId, read.sessionId) : undefined))
  return {
    hostId, journalHost, team, origin, read, unit,
    unitKey: unit ? unitKeyOf(unit.unit) : null,
    unreachableHost: route?.kind === 'unreachable' ? route.leadHostId : null,
  }
}
