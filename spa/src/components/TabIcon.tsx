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
        backgroundColor: '#ef4444',
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
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
    </span>
  )

  if (tabIndicatorStyle === 'icon' || !agentStatus) return iconBox

  // error warning diamond suppresses the overlayed unread pip on dot wrappers —
  // error itself is already a louder signal than unread.
  const showDotUnreadPip = isUnread && !isActive && agentStatus !== 'error'

  if (tabIndicatorStyle === 'dot') {
    return (
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
        {showDotUnreadPip && <UnreadPip />}
        {subagentCount > 0 && <SubagentDots count={subagentCount} />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'iconDot') {
    return (
      <span className="relative inline-flex items-center flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
          <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
          {showDotUnreadPip && <UnreadPip />}
          {subagentCount > 0 && <SubagentDots count={subagentCount} />}
        </span>
        {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
      </span>
    )
  }

  // Unread tints the badge dot red instead of overlaying a separate pip.
  // Margins: badge `ml-px mr-[0.5px]`, non-badge `ml-[1.5px]` — picks up the
  // icon column alignment + a tiny trailing gap on retina. `lowdpi:` snaps
  // sub-pixel values back to integers below @2x. Subagent dots park at left:-4.
  return (
    <span
      className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-px mr-[0.5px] lowdpi:mr-0"
    >
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
      <TabStatusIndicator
        status={agentStatus}
        mode="overlay"
        isActive={isActive}
        isUnread={isUnread && !isActive}
      />
      {subagentCount > 0 && <SubagentDots count={subagentCount} left={-4} />}
    </span>
  )
}
