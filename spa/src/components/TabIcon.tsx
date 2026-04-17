// spa/src/components/TabIcon.tsx
import type { AgentStatus, TabIndicatorStyle } from '../stores/useAgentStore'
import { TabStatusIndicator } from './TabStatusIndicator'
import { SubagentDots } from './SubagentDots'

function UnreadPip({ size = 5 }: { size?: number }) {
  return (
    <span
      data-testid="tab-unread-pip"
      className="absolute rounded-full z-20"
      style={{
        width: size,
        height: size,
        top: -1,
        right: -2,
        backgroundColor: '#b91c1c',
      }}
    />
  )
}

interface Props {
  IconComponent: React.ComponentType<{ size: number; className?: string }> | undefined
  agentStatus: AgentStatus | undefined
  tabIndicatorStyle: TabIndicatorStyle
  isActive: boolean
  iconSize: number
  subagentCount: number
  isUnread: boolean
}

export function TabIcon({
  IconComponent,
  agentStatus,
  tabIndicatorStyle,
  isActive,
  iconSize,
  subagentCount,
  isUnread,
}: Props) {
  const iconBox = (
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
    </span>
  )

  if (tabIndicatorStyle === 'icon' || !agentStatus) return iconBox

  // error warning diamond suppresses the overlayed unread pip on dot wrappers —
  // error itself is already a louder signal than unread.
  const showDotUnreadPip = isUnread && !isActive && agentStatus !== 'error'

  if (tabIndicatorStyle === 'dot') {
    return (
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
        <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
        {showDotUnreadPip && <UnreadPip />}
        {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'iconDot') {
    return (
      <span className="relative inline-flex items-center flex-shrink-0">
        <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
          <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
          {showDotUnreadPip && <UnreadPip />}
          {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
        </span>
        {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
      </span>
    )
  }

  // Unread tints the badge dot red instead of overlaying a separate pip.
  return (
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
      <TabStatusIndicator
        status={agentStatus}
        mode="overlay"
        isActive={isActive}
        isUnread={isUnread && !isActive}
      />
      {subagentCount > 0 && <SubagentDots count={subagentCount} isActive={isActive} />}
    </span>
  )
}
