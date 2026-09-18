import { useMemo } from 'react'
import type { IconWeight, Tab } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'
import { useAgentStore } from '../stores/useAgentStore'
import { compositeKey } from '../lib/composite-key'
import { getPrimaryPane } from '../lib/pane-tree'
import {
  getTabHostId,
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
 * Badge identity of the tab's host (first tmux pane), or `null` when the tab has
 * no tmux pane. Mode (spec D1): `terminal` when the primary pane has a live
 * agentType, otherwise `console`; `execution` is not wired yet.
 *
 * Primitive selectors plus the `colors` object (identity changes only on a write),
 * resolved under `useMemo` so tab rows do not re-render on unrelated store writes.
 */
export function useTabHostBadge(tab: Tab): TabHostBadge | null {
  const hostId = getTabHostId(tab)
  const primary = getPrimaryPane(tab.layout).content
  const ck =
    primary.kind === 'tmux-session' && primary.hostId && primary.sessionCode && !primary.terminated
      ? compositeKey(primary.hostId, primary.sessionCode)
      : undefined
  // Hooks run unconditionally (Rules of Hooks); the `hostId` bail-out happens after.
  const colors = useHostStore((s) => (hostId ? s.hosts[hostId]?.colors : undefined))
  const legacyColor = useHostStore((s) => (hostId ? s.hosts[hostId]?.color : undefined))
  const icon = useHostStore((s) => (hostId ? s.hosts[hostId]?.icon : undefined))
  const iconWeight = useHostStore((s) => (hostId ? s.hosts[hostId]?.iconWeight : undefined))
  const agentType = useAgentStore((s) => (ck ? s.agentTypes[ck] : undefined))
  const mode: HostColorMode = agentType ? 'terminal' : 'console'

  const resolved = useMemo(
    () => resolveHostColors({ colors, color: legacyColor }, mode),
    [colors, legacyColor, mode],
  )

  if (!hostId) return null
  return {
    colors: resolved,
    // Last line of defence: a value stored before the guard existed (or written by
    // a stranger) must never reach `WorkspaceIcon`, which renders an unknown name
    // as literal text inside the badge.
    icon: isPhosphorIconName(icon) ? icon : undefined,
    iconWeight: isIconWeight(iconWeight) ? iconWeight : undefined,
  }
}
