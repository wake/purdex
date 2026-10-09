// spa/src/components/team/TeamSeatIcon.tsx — a seat's agent icon with its light, and its host icon (plan TI-3).
//
// The same pieces the tab rows use (TabIcon + HostBadge), keyed by (hostId, sessionCode) instead of a tab, so a member
// without a tab still shows its agent icon and light. A seat on a host this Mac has not configured (hostId '') has no key
// at all: no light, and a neutral host glyph in place of the host icon.
import { useMemo } from 'react'
import { Desktop, Robot } from '@phosphor-icons/react'
import { TabIcon } from '../TabIcon'
import { HostBadge } from '../HostBadge'
import { useSessionAgentIndicator } from '../../hooks/useSessionAgentIndicator'
import { useHostLook } from '../../lib/host-look'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { useAgentStore } from '../../stores/useAgentStore'
import { compositeKey } from '../../lib/composite-key'
import { hasHostBadge, isIconWeight, isPhosphorIconName, resolveHostColors } from '../../lib/host-color'

interface IconProps {
  hostId: string
  sessionCode: string
  isActive?: boolean
  size?: number
  /** Draw the seat's subagent dots to the icon's left (the panel's full rows); the beads leave them off. */
  subagents?: boolean
}

/** The icon of a seat whose agent type is not known (no store key, or none yet): a bead is never visually empty. */
function DefaultBot({ size, className }: { size: number; className?: string }) {
  return <Robot size={size} className={className} data-testid="team-bead-bot" />
}

export function TeamSeatIcon({ hostId, sessionCode, isActive = false, size = 14, subagents = false }: IconProps) {
  const { agentIcon, agentStatus, isUnread, tabIndicatorStyle, subagentRefs } = useSessionAgentIndicator(hostId, sessionCode)
  return (
    <TabIcon
      IconComponent={agentIcon ?? DefaultBot}
      agentStatus={agentStatus}
      tabIndicatorStyle={tabIndicatorStyle}
      isActive={isActive}
      iconSize={size}
      subagentRefs={subagents ? subagentRefs : []}
      isUnread={isUnread}
    />
  )
}

function KnownHostBadge({ hostId, sessionCode }: { hostId: string; sessionCode: string }) {
  const look = useHostLook(hostId)
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
  if (!hasHostBadge(badge)) return null
  return <HostBadge colors={badge.colors} icon={badge.icon} iconWeight={badge.iconWeight} box={box} inset={inset} radius={radius} lineColor={lineColor} />
}

/** The host icon of a seat; a neutral glyph while its host is not configured on this Mac. */
export function TeamSeatHostBadge({ hostId, sessionCode }: { hostId: string; sessionCode: string }) {
  if (hostId === '') {
    return <span data-testid="team-bead-host-unknown" aria-hidden="true" className="inline-flex flex-shrink-0 text-text-muted"><Desktop size={12} /></span>
  }
  return <KnownHostBadge hostId={hostId} sessionCode={sessionCode} />
}
