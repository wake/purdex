import { useMemo } from 'react'
import { HostBadge } from '../HostBadge'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { isIconWeight, isPhosphorIconName, resolveHostColors, type HostColorMode } from '../../lib/host-color'
import type { ProbeHostDef } from '../../lib/fleet/types'

/**
 * A probe-only host's badge: the same `HostBadge` a daemon host gets, drawn from the definition's own icon / colours
 * (they live in the prober's host config, not in this App's look store). Sized by the sidebar badge settings.
 */
export function ProbeHostBadge({ look, mode = 'console', testId }: {
  look: Pick<ProbeHostDef, 'icon' | 'icon_weight' | 'colors'>
  mode?: HostColorMode
  testId?: string
}) {
  const box = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const inset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const radius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)
  const lineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const colors = useMemo(() => resolveHostColors({ colors: look.colors, color: undefined }, mode), [look.colors, mode])
  return (
    <HostBadge
      testId={testId}
      colors={colors}
      icon={isPhosphorIconName(look.icon) ? look.icon : undefined}
      iconWeight={isIconWeight(look.icon_weight) ? look.icon_weight : undefined}
      box={box}
      inset={inset}
      radius={radius}
      lineColor={lineColor}
    />
  )
}
