import type { AgentStatus, TabIndicatorStyle } from '../../../stores/useAgentStore'
import { TabStatusIndicator } from '../../../components/TabStatusIndicator'
import { SubagentDots } from '../../../components/SubagentDots'

interface Params {
  IconComponent: React.ComponentType<{ size: number; className?: string }> | undefined
  agentStatus: AgentStatus | undefined
  tabIndicatorStyle: TabIndicatorStyle
  isActive: boolean
  subagentCount: number
  isUnread?: boolean
}

// Left-mode variant of the top-tab renderer in SortableTab.tsx. Slot/icon
// sizes mirror TabIcon (w-4 h-4 = 16px slot, 14px icon) so post-icon text
// positions and overlay-dot offsets stay pixel-identical between the
// activity bar and the top TabBar.
const ICON_SIZE = 14
const DOT_SLOT = 'w-4 h-4'

const UNREAD_PIP = (
  <span
    data-testid="inline-tab-unread-pip"
    className="absolute rounded-full z-20"
    style={{ width: 5, height: 5, top: -1, right: -2, backgroundColor: '#ef4444' }}
  />
)

export function renderInlineTabIcon({
  IconComponent,
  agentStatus,
  tabIndicatorStyle,
  isActive,
  subagentCount,
  isUnread = false,
}: Params) {
  // icon-only OR no agent event → plain icon slot
  if (tabIndicatorStyle === 'icon' || !agentStatus) {
    return (
      <span className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0 ml-[1.5px] lowdpi:ml-px`}>
        {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      </span>
    )
  }

  // error already louder than unread — don't also stack a pip.
  const showDotUnreadPip = isUnread && !isActive && agentStatus !== 'error'

  if (tabIndicatorStyle === 'dot') {
    return (
      <span
        data-testid="inline-tab-dot"
        className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0 ml-[1.5px] lowdpi:ml-px`}
      >
        <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
        {showDotUnreadPip && UNREAD_PIP}
        {subagentCount > 0 && <SubagentDots count={subagentCount} />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'iconDot') {
    return (
      <span className="relative inline-flex items-center flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <span
          data-testid="inline-tab-dot"
          className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0`}
        >
          <TabStatusIndicator status={agentStatus} mode="replace" isActive={isActive} />
          {showDotUnreadPip && UNREAD_PIP}
          {subagentCount > 0 && <SubagentDots count={subagentCount} />}
        </span>
        {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      </span>
    )
  }

  // badge: icon + small overlay dot. Unread tints the overlay dot red
  // instead of stacking a separate pip (parity with TabIcon badge mode).
  return (
    <span
      data-testid="inline-tab-dot-overlay"
      className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0 ml-px mr-[0.5px] lowdpi:mr-0`}
    >
      {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
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
