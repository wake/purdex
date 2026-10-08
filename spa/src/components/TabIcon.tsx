// spa/src/components/TabIcon.tsx
import type { AgentStatus, BackgroundKind, SubagentRef } from '../stores/useAgentStore'
import type { TabIndicatorStyle } from '../stores/useUISettingsStore'
import { TabStatusIndicator } from './TabStatusIndicator'
import { SubagentDots } from './SubagentDots'
import { BackgroundSymbol } from './BackgroundSymbol'

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
  /**
   * useTabDisplay's `isAwaitingApproval` — a waiting light with the 「等待核准」 hand (TabStatusIndicator), shown even
   * before `agentStatus` is known.
   */
  awaitingApproval?: boolean
  /** The tab's highest background-work kind (N6): a small symbol at the top-left of the agent icon (of the dot in `dot`). */
  background?: BackgroundKind
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
  background,
}: Props) {
  const iconBox = (
    <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
      {IconComponent && <IconComponent size={iconSize} className="flex-shrink-0" />}
    </span>
  )

  // 「等待核准」 is a waiting light whatever `agentStatus` says: on the first paint after a cold load the summary
  // already carries the pending request while useWorkerAgentProjection (an App-level effect) has not written it.
  const status: AgentStatus | undefined = awaitingApproval ? 'waiting' : agentStatus

  // Lights off ('icon'): only an awaiting worker still shows its light (user decision 2026-10-08 — a pending approval
  // is a must-show exception); it falls through to the overlay layout below, with no unread tint / subagent dots.
  const lightsOff = tabIndicatorStyle === 'icon'
  if (!status || (lightsOff && !awaitingApproval)) return iconBox

  // error warning diamond suppresses the overlayed unread pip on dot wrappers —
  // error itself is already a louder signal than unread. The 「等待核准」 hand
  // does not: it is a waiting light, and useAgentStore marks waiting unread.
  const showDotUnreadPip = isUnread && !isActive && status !== 'error'

  if (tabIndicatorStyle === 'dot') {
    return (
      <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <TabStatusIndicator status={status} mode="replace" isActive={isActive} awaitingApproval={awaitingApproval} />
        {showDotUnreadPip && <UnreadPip />}
        {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} />}
        {background && <BackgroundSymbol kind={background} top={0} left={0} />}
      </span>
    )
  }

  if (tabIndicatorStyle === 'iconDot') {
    return (
      <span className="relative inline-flex items-center flex-shrink-0 ml-[1.5px] lowdpi:ml-px">
        <span className="relative inline-flex items-center justify-center w-4 h-4 flex-shrink-0">
          <TabStatusIndicator status={status} mode="replace" isActive={isActive} awaitingApproval={awaitingApproval} />
          {showDotUnreadPip && <UnreadPip />}
          {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} />}
        </span>
        {IconComponent && (background ? (
          <span className="relative inline-flex">
            <IconComponent size={iconSize} className="flex-shrink-0" />
            <BackgroundSymbol kind={background} />
          </span>
        ) : (
          <IconComponent size={iconSize} className="flex-shrink-0" />
        ))}
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
        status={status}
        mode="overlay"
        isActive={isActive}
        isUnread={isUnread && !isActive && !lightsOff}
        awaitingApproval={awaitingApproval}
      />
      {!lightsOff && subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} left={-4} />}
      {!lightsOff && background && <BackgroundSymbol kind={background} />}
    </span>
  )
}
