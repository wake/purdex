// spa/src/hooks/useSidebarHostBadgeGeometry.ts — the sidebar host badge's size, inset, corner and line colour, as the user
// set them (Settings → Terminal → host badge, sidebar). The team seats' badges and the fleet cells read it here, so a
// change to the setting moves them together.
import { useUISettingsStore, type HostBadgeLineColor } from '../stores/useUISettingsStore'

export interface HostBadgeGeometry {
  box: number
  inset: number
  radius: number
  lineColor: HostBadgeLineColor
}

export function useSidebarHostBadgeGeometry(): HostBadgeGeometry {
  const box = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const inset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const radius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)
  const lineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  return { box, inset, radius, lineColor }
}
