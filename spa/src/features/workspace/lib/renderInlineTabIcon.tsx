import type { AgentStatus, BackgroundKind, SubagentRef } from '../../../stores/useAgentStore'
import type { TabIndicatorStyle } from '../../../stores/useUISettingsStore'
import { TabStatusIndicator } from '../../../components/TabStatusIndicator'
import { SubagentDots } from '../../../components/SubagentDots'
import { BackgroundSymbol } from '../../../components/BackgroundSymbol'

interface Params {
  IconComponent: React.ComponentType<{ size: number; className?: string }> | undefined
  agentStatus: AgentStatus | undefined
  tabIndicatorStyle: TabIndicatorStyle
  isActive: boolean
  subagentRefs: SubagentRef[]
  isUnread?: boolean
  /**
   * useTabDisplay's `isAwaitingApproval` — a waiting light with the 「等待核准」 hand (TabStatusIndicator), shown even
   * before `agentStatus` is known.
   */
  awaitingApproval?: boolean
  /** The tab's highest background-work kind (N6), drawn as in TabIcon. */
  background?: BackgroundKind
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
  subagentRefs,
  isUnread = false,
  awaitingApproval = false,
  background,
}: Params) {
  // 「等待核准」 is a waiting light whatever `agentStatus` says: on the first paint after a cold load the summary
  // already carries the pending request while useWorkerAgentProjection (an App-level effect) has not written it.
  const status: AgentStatus | undefined = awaitingApproval ? 'waiting' : agentStatus

  // Lights off ('icon'): only an awaiting worker still shows its light (user decision 2026-10-08 — a pending approval
  // is a must-show exception); it falls through to the overlay layout below, with no unread tint / subagent dots.
  const lightsOff = tabIndicatorStyle === 'icon'
  // lights off without an awaiting request OR no agent event → plain icon slot
  if (!status || (lightsOff && !awaitingApproval)) {
    return (
      <span className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0 ml-[1.5px] lowdpi:ml-px`}>
        {IconComponent && <IconComponent size={ICON_SIZE} className="flex-shrink-0" />}
      </span>
    )
  }

  // error already louder than unread — don't also stack a pip. The 「等待核准」
  // hand does not: it is a waiting light, and useAgentStore marks waiting unread.
  const showDotUnreadPip = isUnread && !isActive && status !== 'error'

  if (tabIndicatorStyle === 'dot') {
    return (
      <span
        data-testid="inline-tab-dot"
        className={`relative inline-flex items-center justify-center ${DOT_SLOT} flex-shrink-0 ml-[1.5px] lowdpi:ml-px`}
      >
        <TabStatusIndicator status={status} mode="replace" isActive={isActive} awaitingApproval={awaitingApproval} />
        {showDotUnreadPip && UNREAD_PIP}
        {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} />}
        {background && <BackgroundSymbol kind={background} top={0} left={0} />}
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
          <TabStatusIndicator status={status} mode="replace" isActive={isActive} awaitingApproval={awaitingApproval} />
          {showDotUnreadPip && UNREAD_PIP}
          {subagentRefs.length > 0 && <SubagentDots refs={subagentRefs} />}
        </span>
        {IconComponent && (background ? (
          <span className="relative inline-flex">
            <IconComponent size={ICON_SIZE} className="flex-shrink-0" />
            <BackgroundSymbol kind={background} />
          </span>
        ) : (
          <IconComponent size={ICON_SIZE} className="flex-shrink-0" />
        ))}
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
