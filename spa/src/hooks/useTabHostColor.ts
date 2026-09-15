import type { Tab } from '../types/tab'
import { useHostStore } from '../stores/useHostStore'
import { getTabHostId, isValidHostColor } from '../lib/host-color'

/** Validated color of the tab's host (first tmux pane), or null. Re-renders only on that host's color. */
export function useTabHostColor(tab: Tab): string | null {
  const hostId = getTabHostId(tab)
  const color = useHostStore((s) => (hostId ? s.hosts[hostId]?.color : undefined))
  return isValidHostColor(color) ? color : null
}
