import type { AgentStatus, TabIndicatorStyle } from '../../../stores/useAgentStore'
import { TabStatusDot } from '../../../components/TabStatusDot'
import { SubagentDots } from '../../../components/SubagentDots'

interface Params {
  IconComponent: React.ComponentType<{ size: number; className?: string }> | undefined
  agentStatus: AgentStatus | undefined
  tabIndicatorStyle: TabIndicatorStyle
  isActive: boolean
  subagentCount: number
}

// Left-mode variant of the top-tab renderer in SortableTab.tsx. Sizes are tuned
// for the 12px (xs) row; branching mirrors SortableTab exactly so the two
// surfaces stay visually coherent.
const ICON_SIZE = 12
const DOT_SLOT = 'w-3 h-3'

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
        <TabStatusDot status={agentStatus} style="replace" isActive={isActive} />
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
          <TabStatusDot status={agentStatus} style="replace" isActive={isActive} />
          {subagentCount > 0 && <SubagentDots count={subagentCount} />}
        </span>
        {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      </span>
    )
  }

  // badge: icon + small overlay dot
  return (
    <span
      data-testid="inline-tab-dot-overlay"
      className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0`}
    >
      {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      <TabStatusDot status={agentStatus} style="overlay" isActive={isActive} />
      {subagentCount > 0 && <SubagentDots count={subagentCount} />}
    </span>
  )
}
