import { useMemo } from 'react'
import type { Tab } from '../types/tab'
import { useHostLook } from '../lib/host-look'
import { useTabPoolHostLook } from '../lib/pool/host-looks'
import { getPrimaryPane } from '../lib/pane-tree'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import {
  getTabBadgePane,
  resolveHostBadge,
  type HostColorMode,
  type ResolvedHostBadge,
} from '../lib/host-color'

/** Resolved badge identity for a tab. `colors` null = host has no color; `icon` undefined = default icon. */
export type TabHostBadge = ResolvedHostBadge

/**
 * Badge identity of the tab's host (first tmux pane in pre-order), or `null` when
 * the tab has no tmux pane. Mode (spec D1): `terminal` when THAT SAME pane has a
 * live agentType, otherwise `console`; `execution` is not wired yet.
 *
 * Host and mode must come from the same pane — reading host from
 * `getTabBadgePane` (pre-order) but mode from `getPrimaryPane` let a split tab
 * whose first leaf isn't a tmux pane (e.g. `new-tab` before a live agent tmux
 * pane) pick that pane's host under the wrong mode (P2 Task 1 review finding,
 * PR #1153).
 *
 * The host's look comes from `useHostLook` (one host object subscribed; the look
 * object is memoised per host), resolved under `useMemo` so tab rows do not
 * re-render on unrelated store writes. `resolveHostBadge` is the last line of
 * defence on the icon and weight.
 */
export function useTabHostBadge(tab: Tab): TabHostBadge | null {
  const badgePane = getTabBadgePane(tab)
  const hostId = badgePane?.hostId ?? null
  const ck =
    badgePane && badgePane.sessionCode && !badgePane.terminated
      ? compositeKey(badgePane.hostId, badgePane.sessionCode)
      : undefined
  // Hooks run unconditionally (Rules of Hooks); the `hostId` bail-out happens after.
  const look = useHostLook(hostId)
  // A watch-host tab (MON-6b, M9) wears the look of its pool host, as the sidebar cell does; the hook runs for every tab (`''` is no host).
  const primary = getPrimaryPane(tab.layout)
  const poolHostId = primary.content.kind === 'watch-host' ? primary.content.poolHostId ?? '' : ''
  const poolLook = useTabPoolHostLook(poolHostId)
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const mode: HostColorMode = agentType ? 'terminal' : 'console'

  const badge = useMemo(() => resolveHostBadge(look, mode), [look, mode])
  const poolBadge = useMemo(() => resolveHostBadge(poolLook, 'console'), [poolLook])

  if (poolHostId) return poolBadge
  if (!hostId) return null
  return badge
}
