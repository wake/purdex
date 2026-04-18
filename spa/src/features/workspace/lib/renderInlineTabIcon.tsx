import type { AgentStatus, TabIndicatorStyle } from '../../../stores/useAgentStore'
import { TabStatusIndicator } from '../../../components/TabStatusIndicator'
import { SubagentDots } from '../../../components/SubagentDots'

interface Params {
  IconComponent: React.ComponentType<{ size: number; className?: string }> | undefined
  agentStatus: AgentStatus | undefined
  tabIndicatorStyle: TabIndicatorStyle
  isActive: boolean
  subagentCount: number
}

// Left-mode variant of the top-tab renderer in SortableTab.tsx. Icon size
// matches the top TabBar (14px) so both surfaces feel the same weight.
const ICON_SIZE = 14
const DOT_SLOT = 'w-3.5 h-3.5'

export function renderInlineTabIcon({
  IconComponent,
  agentStatus,
  tabIndicatorStyle,
  isActive,
  subagentCount,
}: Params) {
  // icon-only OR no agent event → plain icon slot
  if (tabIndicatorStyle === 'icon' || !agentStatus) {
    return (
      <span className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0`}>
        {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'dot') {
    return (
      <span
        data-testid="inline-tab-dot"
        className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0`}
      >
        <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
        {subagentCount > 0 && <SubagentDots count={subagentCount} />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'iconDot') {
    return (
      <span className="relative inline-flex items-center flex-shrink-0 gap-1">
        <span
          data-testid="inline-tab-dot"
          className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0`}
        >
          <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
          {subagentCount > 0 && <SubagentDots count={subagentCount} />}
        </span>
        {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      </span>
    )
  }

  // badge: icon + small overlay dot
  // Nudge the whole icon+badge group 1px right and park subagent dots at
  // left:-4 so they sit just outside the box edge, clear of the icon.
  return (
    <span
      data-testid="inline-tab-dot-overlay"
      className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0 ml-px`}
    >
      {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      <TabStatusIndicator status={agentStatus} mode="overlay" isActive={isActive} />
      {subagentCount > 0 && <SubagentDots count={subagentCount} left={-4} />}
    </span>
  )
}
