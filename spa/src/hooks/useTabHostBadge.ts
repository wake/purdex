import { useMemo } from 'react'
import type { IconWeight, Tab } from '../types/tab'
import { useHostLook } from '../lib/host-look'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import {
  getTabBadgePane,
  isIconWeight,
  isPhosphorIconName,
  resolveHostColors,
  type HostColorMode,
  type ResolvedHostColors,
} from '../lib/host-color'

/** Resolved badge identity for a tab. `colors` null = host has no color; `icon` undefined = default icon. */
export interface TabHostBadge {
  colors: ResolvedHostColors | null
  icon: string | undefined
  iconWeight: IconWeight | undefined
}

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
 * re-render on unrelated store writes.
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
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const mode: HostColorMode = agentType ? 'terminal' : 'console'

  const resolved = useMemo(() => resolveHostColors({ colors: look.colors, color: look.color }, mode), [look, mode])

  if (!hostId) return null
  return {
    colors: resolved,
    // Last line of defence: a value stored before the guard existed (or written by
    // a stranger) must never reach `WorkspaceIcon`, which renders an unknown name
    // as literal text inside the badge.
    icon: isPhosphorIconName(look.icon) ? look.icon : undefined,
    iconWeight: isIconWeight(look.iconWeight) ? look.iconWeight : undefined,
  }
}
