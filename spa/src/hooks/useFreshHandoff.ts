// spa/src/hooks/useFreshHandoff.ts — does this row's session have a handoff file written and not yet cleared (relay v2
// contract 5, `relay.half.v1`)? Two sources, by what the row knows:
//   - a TEAM row (lead row, panel row, bead) has the roster session, whose `fresh_handoff` the daemon fills in itself;
//   - any other row is a tab: it is matched to the host's list by (hostId, session `code`) and by nothing else. An entry
//     without a `code` (the session is not in tmux) shows on no tab, and a session id, a ref or a name is never used to guess
//     the tab it might be: a wrong mark on another session is worse than a missing one.
// Both read the stores directly, so the mark is gone the moment the list or the roster no longer holds the session.
import { useMemo } from 'react'
import type { Tab } from '../types/tab'
import type { RelayFresh } from '../lib/relay/fresh'
import type { RosterSession } from '../lib/team/roster'
import { getPrimaryPane } from '../lib/pane-tree'
import { useRelayFreshStore } from '../stores/useRelayFreshStore'
import { useRosterSession } from '../components/team/team-readings'
import { useTeamRosterStore } from '../stores/useTeamRosterStore'

export interface FreshHandoffView {
  path: string
  /** unix ms */
  writtenAt: number
}

/** The entry of `list` for the session `code`: the one written last when the daemon lists the code twice. Never matches an empty code. */
export function freshByCode(list: readonly RelayFresh[] | undefined, code: string): RelayFresh | undefined {
  if (!list || code === '') return undefined
  let best: RelayFresh | undefined
  for (const e of list) {
    if (e.code === code && (best === undefined || e.written_at >= best.written_at)) best = e
  }
  return best
}

/** The tab's primary tmux pane as (hostId, code); null for any other pane, or one whose session is gone. */
export function tabSessionOf(tab: Tab): { hostId: string; code: string } | null {
  const c = getPrimaryPane(tab.layout).content
  if (c.kind !== 'tmux-session' || c.terminated || c.hostId === '' || c.sessionCode === '') return null
  return { hostId: c.hostId, code: c.sessionCode }
}

/** The handoff file of the session `code` on `hostId`, matched by code alone. */
export function useFreshHandoffOfCode(hostId: string, code: string): FreshHandoffView | null {
  const entry = useRelayFreshStore((s) => (hostId !== '' ? freshByCode(s.byHost[hostId], code) : undefined))
  return useMemo(() => (entry ? { path: entry.path, writtenAt: entry.written_at } : null), [entry])
}

/**
 * A tab's handoff file. `leadOfTeam` is the team key when the tab is that team's LEAD row: the lead is then known by the roster
 * (which carries the daemon's own `fresh_handoff`), so its mark does not depend on the list entry carrying a `code`; a roster
 * lead without `fresh_handoff` has none, and the list is not asked. The (hostId, code) match is the only source for every tab
 * that is not a roster lead (no `leadOfTeam`, or one that points at no lead in the roster).
 */
export function useFreshHandoffOfTab(tab: Tab, leadOfTeam?: string): FreshHandoffView | null {
  const s = tabSessionOf(tab)
  const byCode = useFreshHandoffOfCode(s?.hostId ?? '', s?.code ?? '')
  const lead = useTeamRosterStore((st) => {
    if (!leadOfTeam) return undefined
    const cut = leadOfTeam.indexOf('\u0000')
    return st.byHost[leadOfTeam.slice(0, cut)]?.find((t) => t.id === leadOfTeam.slice(cut + 1))?.lead
  })
  const fromRoster = useFreshHandoffOfRosterSession(lead)
  // a roster lead is the whole answer, null included (the daemon gives every lead its own `fresh_handoff`, or none)
  return lead ? fromRoster : byCode
}

export function useFreshHandoffOfRosterSession(session: RosterSession | undefined): FreshHandoffView | null {
  const f = session?.fresh_handoff
  return useMemo(() => (f ? { path: f.path, writtenAt: f.written_at } : null), [f])
}

/** A team seat by its team key and session id (the roster store, as the panel's readings). */
export function useFreshHandoffOfSeat(teamKey: string, sessionId: string): FreshHandoffView | null {
  return useFreshHandoffOfRosterSession(useRosterSession(teamKey, sessionId))
}
