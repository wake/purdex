// spa/src/components/TabIcon.tsx
import type { AgentStatus, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
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
  subagentRefs: SubagentRef[]
  isUnread: boolean
  /** useTabDisplay's `isAwaitingApproval` — the light becomes the 「等待核准」 hand (TabStatusIndicator). */
  awaitingApproval?: boolean
}

export function TabIcon({
  IconComponent,
  agentStatus,
  tabIndicatorStyle,
  isActive,
  iconSize,
  subagentRefs,
  isUnread,
  awaitingApproval = false,
}: Props) {
  const iconBox = (
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
    </span>
  )

  if (tabIndicatorStyle === 'icon' || !agentStatus) return iconBox

  // error warning diamond suppresses the overlayed unread pip on dot wrappers —
  // error itself is already a louder signal than unread. So does the 「等待核准」
  // hand: a tab that needs an answer stays the warning colour, never red.
  const showDotUnreadPip = isUnread && !isActive && agentStatus !== 'error' && !awaitingApproval

  if (tabIndicatorStyle === 'dot') {
    return (
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} awaitingApproval={awaitingApproval} />
        {showDotUnreadPip && <UnreadPip />}
        {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'iconDot') {
    return (
      <span className="relative inline-flex items-center flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
          <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} awaitingApproval={awaitingApproval} />
          {showDotUnreadPip && <UnreadPip />}
          {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} />}
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
        awaitingApproval={awaitingApproval}
      />
      {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} left={-4} />}
    </span>
  )
}
