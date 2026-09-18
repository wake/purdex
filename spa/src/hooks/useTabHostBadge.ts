import type { IconWeight, Tab } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'
import { getTabHostId, isIconWeight, isPhosphorIconName, resolveHostColorSet } from '../lib/host-color'

/** Validated host identity for a tab's badge. `null` / `undefined` mean "fall back to the neutral default". */
export interface TabHostBadge {
  color: string | null
  icon: string | undefined
  iconWeight: IconWeight | undefined
}

/**
 * Identity of the tab's host (first tmux pane) for the host badge, or `null` when
 * the tab has no tmux pane.
 *
 * Uses primitive selectors plus the `colors` object, whose identity only changes on
 * a write: a single selector returning a fresh `{ color, icon, iconWeight }` object
 * would compare unequal on every read and re-render every tab row on unrelated
 * store writes.
 */
export function useTabHostBadge(tab: Tab): TabHostBadge | null {
  const hostId = getTabHostId(tab)
  // Hooks run unconditionally (Rules of Hooks); the `hostId` bail-out happens after.
  const colors = useHostStore((s) => (hostId ? s.hosts[hostId]?.colors : undefined))
  const legacyColor = useHostStore((s) => (hostId ? s.hosts[hostId]?.color : undefined))
  const icon = useHostStore((s) => (hostId ? s.hosts[hostId]?.icon : undefined))
  const iconWeight = useHostStore((s) => (hostId ? s.hosts[hostId]?.iconWeight : undefined))

  if (!hostId) return null
  // P1 adapter: the badge still takes one hex; per-mode rgba arrives with P2.
  const resolved = resolveHostColorSet({ colors, color: legacyColor }, 'console')
  return {
    color: resolved?.main.color ?? null,
    // Last line of defence: a value stored before the guard existed (or written by
    // a stranger) must never reach `WorkspaceIcon`, which renders an unknown name
    // as literal text inside the badge.
    icon: isPhosphorIconName(icon) ? icon : undefined,
    iconWeight: isIconWeight(iconWeight) ? iconWeight : undefined,
  }
}
