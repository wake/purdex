// spa/src/components/team/TeamSeatIcon.tsx — a seat's agent icon with its light, and its host badge.
//
// Same pieces the tab rows use (TabIcon + HostBadge), keyed by (hostId, sessionCode) instead of a tab,
// so a member without a tab still shows the bot and its light.
import { useMemo } from 'react'
import { TabIcon } from '../TabIcon'
import { HostBadge } from '../HostBadge'
import { useSessionAgentIndicator } from '../../hooks/useSessionAgentIndicator'
import { useHostLook } from '../../lib/host-look'
import { useHostStore } from '../../stores/useHostStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'
import { hasHostBadge, isIconWeight, isPhosphorIconName, resolveHostColors } from '../../lib/host-color'

interface IconProps {
  hostId: string
  sessionCode: string
  isActive?: boolean
  size?: number
  /** Draw the subagent dots (the panel keeps them; the beads leave them out). */
  subagents?: boolean
}

export function TeamSeatIcon({ hostId, sessionCode, isActive = false, size = 14, subagents = true }: IconProps) {
  const { agentIcon, agentStatus, subagentRefs, isUnread, tabIndicatorStyle } = useSessionAgentIndicator(hostId, sessionCode)
  return (
    <TabIcon
      IconComponent={agentIcon}
      agentStatus={agentStatus}
      tabIndicatorStyle={tabIndicatorStyle}
      isActive={isActive}
      iconSize={size}
      subagentRefs={subagents ? subagentRefs : []}
      isUnread={isUnread}
    />
  )
}

interface BadgeProps {
  hostId: string
  sessionCode: string
  /** Also print the host's name after the badge. */
  withName?: boolean
}

export function TeamSeatHostBadge({ hostId, sessionCode, withName = false }: BadgeProps) {
  const look = useHostLook(hostId)
  const name = useHostStore((s) => s.hosts[hostId]?.name ?? hostId)
  const agentType = useAgentStore((s) => s.agentTypes[compositeKey(hostId, sessionCode)])
  const box = useUISettingsStore((s) => s.hostBadgeSidebarBox)
  const inset = useUISettingsStore((s) => s.hostBadgeSidebarInset)
  const radius = useUISettingsStore((s) => s.hostBadgeSidebarRadius)
  const lineColor = useUISettingsStore((s) => s.hostBadgeSidebarLineColor)
  const colors = useMemo(() => resolveHostColors({ colors: look.colors, color: look.color }, agentType ? 'terminal' : 'console'), [look, agentType])
  const badge = {
    colors,
    icon: isPhosphorIconName(look.icon) ? look.icon : undefined,
    iconWeight: isIconWeight(look.iconWeight) ? look.iconWeight : undefined,
  }
  return (
    <>
      {hasHostBadge(badge) && (
        <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} />
      )}
      {withName && <span className="text-text-muted flex-shrink-0">{name}</span>}
    </>
  )
}
