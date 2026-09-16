import type { IconWeight, Tab } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'
import { getTabHostId, isIconWeight, isValidHostColor } from '../lib/host-color'

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
 * Uses three *primitive* selectors on purpose: a single selector returning a fresh
 * `{ color, icon, iconWeight }` object would compare unequal on every read and
 * re-render every tab row on unrelated store writes.
 */
export function useTabHostBadge(tab: Tab): TabHostBadge | null {
  const hostId = getTabHostId(tab)
  // Hooks run unconditionally (Rules of Hooks); the `hostId` bail-out happens after.
  const color = useHostStore((s) => (hostId ? s.hosts[hostId]?.color : undefined))
  const icon = useHostStore((s) => (hostId ? s.hosts[hostId]?.icon : undefined))
  const iconWeight = useHostStore((s) => (hostId ? s.hosts[hostId]?.iconWeight : undefined))

  if (!hostId) return null
  return {
    color: isValidHostColor(color) ? color : null,
    icon: typeof icon === 'string' && icon.trim() !== '' ? icon : undefined,
    iconWeight: isIconWeight(iconWeight) ? iconWeight : undefined,
  }
}
